package webhook

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
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
	} `json:"request"`
	// Jellyseerr nested structure.
	RequestedBy struct {
		PlexUsername string `json:"plexUsername"`
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

	// Extract plex username: try Overseerr flat, then Jellyseerr nested.
	plexUsername := payload.Request.RequestedByUsername
	if plexUsername == "" {
		plexUsername = payload.RequestedBy.PlexUsername
	}
	if plexUsername == "" {
		log.Printf("[webhook] rejected tvdb=%d: no requestedBy username in payload (request=%+v requestedBy=%+v)",
			payload.Media.TvdbId, payload.Request, payload.RequestedBy)
		http.Error(w, "missing requestedBy username", http.StatusBadRequest)
		return
	}
	log.Printf("[webhook] processing tvdb=%d requestedBy=%q", payload.Media.TvdbId, plexUsername)

	// Resolve plexUsername → plexUserId via Plex account list.
	plexUserId := h.resolvePlexUser(plexUsername)

	// Upsert show row from Sonarr. Seerr fires the webhook before sending
	// POST /api/v3/series to Sonarr, so the series may not exist yet — log and
	// continue. The proxy's OnSeriesAdd will upsert the show once Sonarr confirms.
	if err := h.upsertShow(tvdbId); err != nil {
		log.Printf("[webhook] upsert show %d: %v (will retry via proxy onboard)", tvdbId, err)
	}

	// Check if user is rewatching.
	isRewatching := h.isRewatching(tvdbId, plexUserId)

	// Upsert user request.
	req := repository.UserRequest{
		PlexUserID:       plexUserId,
		TVDBId:           tvdbId,
		RequestTimestamp: time.Now(),
		IsRewatching:     isRewatching,
	}
	if err := h.requests.Upsert(req); err != nil {
		log.Printf("[webhook] upsert request tvdb=%d user=%s: %v", tvdbId, plexUserId, err)
		http.Error(w, fmt.Sprintf("upsert request: %v", err), http.StatusInternalServerError)
		return
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

// resolvePlexUser attempts to map a username to a Plex account ID string.
// If the Plex user map cannot be built or the user is not found, the username
// is returned as-is (prefixed with "seerr:").
func (h *Handler) resolvePlexUser(username string) string {
	userMap, err := h.plex.BuildUserMap()
	if err != nil {
		log.Printf("[webhook] build plex user map: %v", err)
		return "seerr:" + username
	}
	id, ok := userMap[username]
	if !ok {
		return "seerr:" + username
	}
	return fmt.Sprintf("%d", id)
}

// upsertShow fetches the show from Sonarr and upserts it into the DB.
func (h *Handler) upsertShow(tvdbId int) error {
	series, err := h.sonarr.GetSeriesByTVDB(tvdbId)
	if err != nil {
		return fmt.Errorf("get series from sonarr: %w", err)
	}

	show := repository.Show{
		TVDBId:   tvdbId,
		SonarrId: series.ID,
		Title:    series.Title,
		Status:   "active",
	}
	return h.shows.Upsert(show)
}

// isRewatching returns true if the user has watched all episodes of the show's
// last season (indicating they are starting a rewatch cycle).
func (h *Handler) isRewatching(tvdbId int, plexUserId string) bool {
	showKey, err := h.plex.FindShowByTVDB(tvdbId)
	if err != nil {
		return false // show not in Plex yet
	}

	history, err := h.plex.GetShowHistory(showKey)
	if err != nil || len(history) == 0 {
		return false
	}

	// Build highest watched episode per season.
	highestPerSeason := make(map[int]int)
	for _, entry := range history {
		if entry.SeasonNum <= 0 {
			continue
		}
		if entry.EpisodeNum > highestPerSeason[entry.SeasonNum] {
			highestPerSeason[entry.SeasonNum] = entry.EpisodeNum
		}
	}

	if len(highestPerSeason) == 0 {
		return false
	}

	// Find the highest season number.
	lastSeason := 0
	for s := range highestPerSeason {
		if s > lastSeason {
			lastSeason = s
		}
	}

	// Fetch the total episode count for the last season from Sonarr.
	show, err := h.shows.FindByTVDB(tvdbId)
	if err != nil || show == nil {
		return false
	}
	episodes, err := h.sonarr.GetEpisodes(show.SonarrId)
	if err != nil {
		return false
	}

	lastSeasonTotal := 0
	for _, ep := range episodes {
		if ep.SeasonNumber == lastSeason && ep.EpisodeNumber > lastSeasonTotal {
			lastSeasonTotal = ep.EpisodeNumber
		}
	}

	if lastSeasonTotal == 0 {
		return false
	}

	return highestPerSeason[lastSeason] >= lastSeasonTotal
}
