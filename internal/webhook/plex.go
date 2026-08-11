package webhook

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/plex"
)

// PlexHandler handles Plex server webhooks (multipart/form-data with a JSON
// "payload" field). A media.scrobble event for an episode triggers an
// immediate reconcile of that show, so the window advances within seconds of
// the user finishing an episode instead of waiting for the next cron sweep.
// Plex also fires media.scrobble when an episode is marked as watched.
type PlexHandler struct {
	plex          *plex.Client
	shows         *repository.ShowRepository
	settings      *repository.SettingsRepository
	enqueue       EnqueueFunc
	reconcileShow func(tvdbId int) error

	mu        sync.Mutex
	pending   map[int]bool // tvdbIds with a reconcile already queued (scrobble-burst dedupe)
	machineID string       // cached local PMS machineIdentifier ("" = not yet fetched)
}

// NewPlexHandler constructs a PlexHandler.
func NewPlexHandler(
	plexClient *plex.Client,
	shows *repository.ShowRepository,
	settings *repository.SettingsRepository,
	enqueue EnqueueFunc,
	reconcileShow func(tvdbId int) error,
) *PlexHandler {
	return &PlexHandler{
		plex:          plexClient,
		shows:         shows,
		settings:      settings,
		enqueue:       enqueue,
		reconcileShow: reconcileShow,
		pending:       make(map[int]bool),
	}
}

// plexPayload is the subset of the Plex webhook JSON payload we care about.
type plexPayload struct {
	Event string `json:"event"`
	// Owner is true when the webhook fired because the account owns the
	// server the playback happened on; User is true when it fired because the
	// playback was the account's own (which happens on *any* server it can
	// reach, including servers other people own).
	Owner  bool `json:"owner"`
	User   bool `json:"user"`
	Server struct {
		Title string `json:"title"`
		UUID  string `json:"uuid"` // == the server's machineIdentifier
	} `json:"Server"`
	Metadata struct {
		Type                 string `json:"type"` // "episode"
		GrandparentRatingKey string `json:"grandparentRatingKey"`
		GrandparentTitle     string `json:"grandparentTitle"`
		ParentIndex          int    `json:"parentIndex"` // season
		Index                int    `json:"index"`       // episode
	} `json:"Metadata"`
}

// localMachineID returns this PMS's machineIdentifier, fetched once and cached
// for the process lifetime (it never changes for a given server). A failed
// lookup is not cached, so a Plex restart mid-boot doesn't poison the value.
func (h *PlexHandler) localMachineID() (string, error) {
	h.mu.Lock()
	cached := h.machineID
	h.mu.Unlock()
	if cached != "" {
		return cached, nil
	}

	id, err := h.plex.GetMachineIdentifier()
	if err != nil {
		return "", err
	}

	h.mu.Lock()
	h.machineID = id
	h.mu.Unlock()
	return id, nil
}

// isLocalPlayback decides whether a scrobble happened on *this* Plex server and
// should therefore be allowed to drive downloads.
//
// This matters because Plex delivers webhooks per-account: a Plex Home user (or
// the account itself) watching on a friend's server fires the exact same
// media.scrobble at us. Acting on those is actively harmful — grandparentRatingKey
// is a server-local database ID, so a foreign key dereferenced against our PMS
// resolves to an unrelated show, and ServeHTTP will happily reactivate a pruned
// show and search for it.
//
// The check is deliberately server-scoped, not account-scoped: any user playing
// on this server carries our machineIdentifier in Server.uuid, so shared and
// managed users still drive their own windows. Only the *server* is filtered.
//
// localID is this server's machineIdentifier, or "" if it could not be fetched.
// Either side being empty means provenance is unknown, and unknown fails safe:
// a dropped scrobble only delays the window until the next cron sweep, which
// rebuilds it from local history anyway, whereas a wrongly accepted one deletes
// and downloads against the wrong show.
func isLocalPlayback(payload plexPayload, localID string) bool {
	if localID == "" || payload.Server.UUID == "" {
		return false
	}
	return payload.Server.UUID == localID
}

// ServeHTTP handles POST /webhooks/plex.
func (h *PlexHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Plex can't send custom headers, so the optional secret rides in the
	// query string (set the webhook URL to .../webhooks/plex?secret=...).
	if secret := h.settings.Get("plex_webhook_secret"); secret != "" {
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("secret")), []byte(secret)) != 1 {
			log.Printf("[plexhook] rejected: secret mismatch")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload plexPayload
	if err := json.Unmarshal([]byte(r.FormValue("payload")), &payload); err != nil {
		http.Error(w, "invalid payload JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if payload.Event != "media.scrobble" || payload.Metadata.Type != "episode" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if payload.Metadata.GrandparentRatingKey == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Provenance check *before* the ratingKey is dereferenced: keys from a
	// foreign server are meaningless here and can resolve to the wrong show.
	localID, err := h.localMachineID()
	if err != nil {
		log.Printf("[plexhook] cannot resolve local machineIdentifier: %v", err)
	}
	if !isLocalPlayback(payload, localID) {
		log.Printf("[plexhook] ignoring scrobble %q S%02dE%02d from server %q (%s) — not this server",
			payload.Metadata.GrandparentTitle, payload.Metadata.ParentIndex, payload.Metadata.Index,
			payload.Server.Title, payload.Server.UUID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	tvdbId, err := h.plex.GetShowTVDBID(payload.Metadata.GrandparentRatingKey)
	if err != nil {
		log.Printf("[plexhook] scrobble %q S%02dE%02d: %v", payload.Metadata.GrandparentTitle,
			payload.Metadata.ParentIndex, payload.Metadata.Index, err)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	show, err := h.shows.FindByTVDB(tvdbId)
	if err != nil || show == nil {
		w.WriteHeader(http.StatusNoContent) // not a Rollarr-managed show
		return
	}
	if show.Status != "active" {
		// Someone is watching an inactive show — reactivate it so the
		// reconcile rebuilds their window.
		log.Printf("[plexhook] scrobble on inactive show %d (%s) — reactivating", tvdbId, show.Title)
		if err := h.shows.UpdateStatus(tvdbId, "active"); err != nil {
			log.Printf("[plexhook] reactivate %d: %v", tvdbId, err)
		}
	}

	log.Printf("[plexhook] scrobble %q S%02dE%02d → reconcile tvdb=%d",
		payload.Metadata.GrandparentTitle, payload.Metadata.ParentIndex, payload.Metadata.Index, tvdbId)

	// Dedupe: a binge or multi-user burst fires many scrobbles; one queued
	// reconcile covers them all.
	h.mu.Lock()
	already := h.pending[tvdbId]
	if !already {
		h.pending[tvdbId] = true
	}
	h.mu.Unlock()

	if !already {
		h.enqueue(func() {
			defer func() {
				h.mu.Lock()
				delete(h.pending, tvdbId)
				h.mu.Unlock()
			}()
			if err := h.reconcileShow(tvdbId); err != nil {
				log.Printf("[plexhook] reconcile tvdb=%d: %v", tvdbId, err)
			}
		})
	}

	w.WriteHeader(http.StatusAccepted)
}
