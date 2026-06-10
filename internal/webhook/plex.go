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

	mu      sync.Mutex
	pending map[int]bool // tvdbIds with a reconcile already queued (scrobble-burst dedupe)
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
	Event    string `json:"event"`
	Metadata struct {
		Type                 string `json:"type"` // "episode"
		GrandparentRatingKey string `json:"grandparentRatingKey"`
		GrandparentTitle     string `json:"grandparentTitle"`
		ParentIndex          int    `json:"parentIndex"` // season
		Index                int    `json:"index"`       // episode
	} `json:"Metadata"`
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
