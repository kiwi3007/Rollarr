package webhook

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/plex"
	"github.com/kiwi3007/rollarr/internal/sonarr"
)

// flexInt unmarshals a JSON number or a quoted string containing a number.
// Seerr's custom webhook template wraps numeric fields in quotes, producing
// `"tvdbId": "12345"` instead of `"tvdbId": 12345`.
type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		*f = flexInt(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("flexInt: cannot parse %s", data)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("flexInt: %q is not an integer", s)
	}
	*f = flexInt(n)
	return nil
}

// EnqueueFunc is the function signature used to enqueue a background job.
type EnqueueFunc func(func())

// Handler handles Seerr/Jellyseerr webhook payloads.
type Handler struct {
	sonarr   *sonarr.Client
	plex     *plex.Client
	shows    *repository.ShowRepository
	requests *repository.UserRequestRepository
	settings *repository.SettingsRepository
	enqueue  EnqueueFunc
	// reconcileShow is called (in the enqueued job) to trigger reconciliation.
	reconcileShow func(tvdbId int) error
}

// SeerrPayload is the JSON body sent by Overseerr and Jellyseerr.
type SeerrPayload struct {
	NotificationType string `json:"notification_type"`
	Media            struct {
		MediaType string  `json:"media_type"`
		TvdbId    flexInt `json:"tvdbId"`
	} `json:"media"`
	// Overseerr flat structure.
	Request struct {
		RequestedByUsername string `json:"requestedBy_username"`
		RequestedByEmail    string `json:"requestedBy_email"`
		Season              int    `json:"season"` // 0 when absent (whole-show request)
	} `json:"request"`
	// Jellyseerr nested structure.
	RequestedBy struct {
		PlexUsername string `json:"plexUsername"`
		DisplayName  string `json:"displayName"`
		Email        string `json:"email"`
	} `json:"requestedBy"`
}

// NewHandler constructs a webhook Handler.
func NewHandler(
	sonarrClient *sonarr.Client,
	plexClient *plex.Client,
	shows *repository.ShowRepository,
	requests *repository.UserRequestRepository,
	settings *repository.SettingsRepository,
	enqueue EnqueueFunc,
	reconcileShow func(tvdbId int) error,
) *Handler {
	return &Handler{
		sonarr:        sonarrClient,
		plex:          plexClient,
		shows:         shows,
		requests:      requests,
		settings:      settings,
		enqueue:       enqueue,
		reconcileShow: reconcileShow,
	}
}

// ServeHTTP handles POST /webhooks/seerr and POST /webhooks/jellyseerr.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("[webhook] %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify webhook secret (timing-safe).
	secret := h.settings.Get("seerr_webhook_secret")
	incoming := r.Header.Get("X-Webhook-Secret")
	log.Printf("[webhook] secret check: configured=%q incoming=%q", secret, incoming)
	if secret != "" {
		if subtle.ConstantTimeCompare([]byte(incoming), []byte(secret)) != 1 {
			log.Printf("[webhook] rejected: secret mismatch")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	var payload SeerrPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("[webhook] received type=%s media_type=%s tvdbId=%d",
		payload.NotificationType, payload.Media.MediaType, payload.Media.TvdbId)

	// Filter: only approved TV media.
	if payload.NotificationType != "MEDIA_APPROVED" && payload.NotificationType != "MEDIA_AUTO_APPROVED" {
		log.Printf("[webhook] ignored: notification type %q not actionable", payload.NotificationType)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if payload.Media.MediaType != "tv" {
		log.Printf("[webhook] ignored: media_type=%q (only tv handled)", payload.Media.MediaType)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	tvdbId := int(payload.Media.TvdbId)
	if tvdbId == 0 {
		http.Error(w, "missing tvdbId", http.StatusBadRequest)
		return
	}

	// Extract plex username + email + display name: try Overseerr flat, then Jellyseerr nested.
	plexUsername := payload.Request.RequestedByUsername
	email := payload.Request.RequestedByEmail
	displayName := payload.Request.RequestedByUsername // Overseerr: username IS the display name
	if plexUsername == "" {
		plexUsername = payload.RequestedBy.PlexUsername
	}
	if email == "" {
		email = payload.RequestedBy.Email
	}
	if payload.RequestedBy.DisplayName != "" {
		displayName = payload.RequestedBy.DisplayName // Jellyseerr has explicit displayName
	}
	if plexUsername == "" {
		log.Printf("[webhook] rejected tvdb=%d: no requestedBy username in payload (request=%+v requestedBy=%+v)",
			payload.Media.TvdbId, payload.Request, payload.RequestedBy)
		http.Error(w, "missing requestedBy username", http.StatusBadRequest)
		return
	}
	log.Printf("[webhook] processing tvdb=%d requestedBy=%q displayName=%q email=%q", payload.Media.TvdbId, plexUsername, displayName, email)

	// Resolve plexUsername → plexUserId via Plex account list, falling back to email.
	plexUserId := h.resolvePlexUser(plexUsername, email)

	// Upsert show row from Sonarr. Seerr fires the webhook before sending
	// POST /api/v3/series to Sonarr, so the series may not exist yet — log and
	// continue. The proxy's OnSeriesAdd will upsert the show once Sonarr confirms.
	if err := h.upsertShow(tvdbId); err != nil {
		log.Printf("[webhook] upsert show %d: %v (will retry via proxy onboard)", tvdbId, err)
	}

	// Treat missing season as 1 (whole-show request implies starting from S01).
	requestedSeason := payload.Request.Season
	if requestedSeason <= 0 {
		requestedSeason = 1
	}

	// Check if user is rewatching.
	isRewatching := h.isRewatching(tvdbId, plexUserId, requestedSeason)

	// Upsert user request. requested_season is written here so re-requests of a
	// series that already exists in Sonarr (where the proxy's series-add hook
	// never fires) still reseed the window at the right season.
	req := repository.UserRequest{
		PlexUserID:       plexUserId,
		DisplayName:      displayName,
		TVDBId:           tvdbId,
		RequestTimestamp: time.Now(),
		IsRewatching:     isRewatching,
		RequestedSeason:  requestedSeason,
	}
	if err := h.requests.Upsert(req); err != nil {
		log.Printf("[webhook] upsert request tvdb=%d user=%s: %v", tvdbId, plexUserId, err)
		http.Error(w, fmt.Sprintf("upsert request: %v", err), http.StatusInternalServerError)
		return
	}

	// A rewatch must drop the stored high-water mark, otherwise the state
	// engine's progress floor pins the window at the old position forever.
	if isRewatching {
		if err := h.requests.ClearWatchProgress(tvdbId, plexUserId); err != nil {
			log.Printf("[webhook] clear progress for rewatch tvdb=%d user=%s: %v", tvdbId, plexUserId, err)
		}
	}

	// Enqueue reconciliation (non-blocking).
	h.enqueue(func() {
		if err := h.reconcileShow(tvdbId); err != nil {
			log.Printf("[webhook] reconcile tvdb=%d: %v", tvdbId, err)
		}
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]bool{"queued": true}) //nolint:errcheck
}

// resolvePlexUser attempts to map a username (or email) to a Plex account ID.
// Tries username first, then email if provided. Falls back to "seerr:<username>"
// when neither matches, deferring resolution to discoverNewWatchers.
func (h *Handler) resolvePlexUser(username, email string) string {
	userMap, err := h.plex.BuildUserMap()
	if err != nil {
		log.Printf("[webhook] build plex user map: %v", err)
		return "seerr:" + username
	}
	if id, ok := userMap[username]; ok {
		log.Printf("[webhook] resolved %q → accountId=%d (username match)", username, id)
		return fmt.Sprintf("%d", id)
	}
	if email != "" {
		if id, ok := userMap[email]; ok {
			log.Printf("[webhook] resolved %q → accountId=%d (email match)", username, id)
			return fmt.Sprintf("%d", id)
		}
	}
	log.Printf("[webhook] could not resolve %q (email=%q) — deferring to seerr:", username, email)
	return "seerr:" + username
}

// upsertShow fetches the show from Sonarr and upserts it into the DB.
func (h *Handler) upsertShow(tvdbId int) error {
	series, err := h.sonarr.GetSeriesByTVDB(tvdbId)
	if err != nil {
		return fmt.Errorf("get series from sonarr: %w", err)
	}

	show := repository.Show{
		TVDBId:    tvdbId,
		SonarrId:  series.ID,
		Title:     series.Title,
		PosterURL: series.PosterURL(),
		FanartURL: series.FanartURL(),
		Status:    "active",
	}
	return h.shows.Upsert(show)
}

// isRewatching returns true when the user is requesting a season they have
// already watched past. Specifically: if requestedSeason is less than the
// user's highest watched season for this show, the new request is a rewatch.
func (h *Handler) isRewatching(tvdbId int, plexUserId string, requestedSeason int) bool {
	showKey, err := h.plex.FindShowByTVDB(tvdbId)
	if err != nil {
		return false // show not in Plex yet
	}

	var history []plex.WatchHistoryEntry
	if strings.HasPrefix(plexUserId, "seerr:") {
		history, err = h.plex.GetShowHistory(showKey)
	} else {
		accountId, _ := strconv.Atoi(plexUserId)
		history, err = h.plex.GetAccountHistory(showKey, accountId)
	}
	if err != nil || len(history) == 0 {
		return false
	}

	highestSeason := 0
	for _, entry := range history {
		if entry.SeasonNum > highestSeason {
			highestSeason = entry.SeasonNum
		}
	}

	return requestedSeason < highestSeason
}
