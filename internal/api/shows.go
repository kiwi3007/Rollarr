package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/plex"
	"github.com/kiwi3007/rollarr/internal/reconcile"
	"github.com/kiwi3007/rollarr/internal/scheduler"
	"github.com/kiwi3007/rollarr/internal/sonarr"
	"github.com/kiwi3007/rollarr/internal/state"
)

// UserBufferInfo describes a single user's active buffer window for card display.
type UserBufferInfo struct {
	DisplayName string `json:"display_name"`
	Season      int    `json:"season"`
	BufferStart int    `json:"buffer_start"`
	BufferEnd   int    `json:"buffer_end"`
}

// ShowSummary is the JSON shape returned by GET /api/shows.
type ShowSummary struct {
	TVDBId              int              `json:"tvdb_id"`
	SonarrId            int              `json:"sonarr_id"`
	Title               string           `json:"title"`
	Status              string           `json:"status"`
	EffectiveBufferSize int              `json:"effective_buffer_size"`
	ActiveRequestCount  int              `json:"active_request_count"`
	LastActivityAt      *time.Time       `json:"last_activity_at"`
	PosterURL           string           `json:"poster_url"`
	FanartURL           string           `json:"fanart_url"`
	UserBuffers         []UserBufferInfo `json:"user_buffers"`
}

// UserRequestDetail wraps a UserRequest with a resolved human-readable display name.
type UserRequestDetail struct {
	repository.UserRequest
	DisplayName string `json:"display_name"`
}

// ShowDetail is the JSON shape returned by GET /api/shows/{tvdbId}.
type ShowDetail struct {
	ShowSummary
	Requests      []UserRequestDetail          `json:"requests"`
	ExpectedState map[int][]int                `json:"expected_state"`
	AllEpisodes   map[int][]int                `json:"all_episodes"`
	OpenFlags     []repository.DiscrepancyFlag `json:"open_flags"`
	Events        []repository.Event           `json:"events"`
}

type showsHandler struct {
	shows      *repository.ShowRepository
	requests   *repository.UserRequestRepository
	flags      *repository.DiscrepancyFlagRepository
	events     *repository.EventRepository
	engine     *state.Engine
	sonarr     *sonarr.Client
	plex       *plex.Client
	reconciler *reconcile.Reconciler
	queue      *scheduler.JobQueue
}

// resolveDisplayName returns a human-readable name for a user.
// Priority: stored Seerr display name > Plex username lookup > seerr: prefix strip > raw ID.
func resolveDisplayName(storedName string, idToName map[int]string, plexUserID string) string {
	if storedName != "" {
		return storedName
	}
	if name, found := strings.CutPrefix(plexUserID, "seerr:"); found {
		return name
	}
	id, err := strconv.Atoi(plexUserID)
	if err != nil {
		return plexUserID
	}
	if name, ok := idToName[id]; ok {
		return name
	}
	return plexUserID
}

// buildIDToNameMap reverses a username→accountID Plex user map.
func buildIDToNameMap(plexClient *plex.Client) map[int]string {
	idToName := map[int]string{}
	if plexClient == nil {
		return idToName
	}
	userMap, err := plexClient.BuildUserMap()
	if err != nil {
		return idToName
	}
	// Two passes: usernames first, emails as fallback.
	// BuildUserMap indexes both username and email to the same ID; Go map
	// iteration is random so without priority the email can win.
	for name, id := range userMap {
		if !strings.Contains(name, "@") {
			idToName[id] = name
		}
	}
	for name, id := range userMap {
		if strings.Contains(name, "@") {
			if _, exists := idToName[id]; !exists {
				idToName[id] = name
			}
		}
	}
	return idToName
}

// computeUserBuffers derives per-user buffer windows from DB state (no Sonarr call).
func computeUserBuffers(reqs []repository.UserRequest, bufferSize int, idToName map[int]string) []UserBufferInfo {
	var buffers []UserBufferInfo
	for _, req := range reqs {
		season := req.RequestedSeason
		if req.LastWatchedSeason != nil && *req.LastWatchedSeason > 0 {
			season = *req.LastWatchedSeason
		}
		if season < 1 {
			season = 1
		}
		start := 1
		if req.LastWatchedEpisode != nil && *req.LastWatchedEpisode > 0 {
			start = *req.LastWatchedEpisode + 1
		}
		buffers = append(buffers, UserBufferInfo{
			DisplayName: resolveDisplayName(req.DisplayName, idToName, req.PlexUserID),
			Season:      season,
			BufferStart: start,
			BufferEnd:   start + bufferSize - 1,
		})
	}
	return buffers
}

func (h *showsHandler) list(w http.ResponseWriter, r *http.Request) {
	all, err := h.shows.FindAll()
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	idToName := buildIDToNameMap(h.plex)

	summaries := make([]ShowSummary, 0, len(all))
	for _, show := range all {
		reqs, _ := h.requests.FindByShow(show.TVDBId)
		bufferSize := h.shows.EffectiveBufferSize(show.TVDBId)
		summaries = append(summaries, ShowSummary{
			TVDBId:              show.TVDBId,
			SonarrId:            show.SonarrId,
			Title:               show.Title,
			Status:              show.Status,
			EffectiveBufferSize: bufferSize,
			ActiveRequestCount:  len(reqs),
			LastActivityAt:      show.LastActivityAt,
			PosterURL:           show.PosterURL,
			FanartURL:           show.FanartURL,
			UserBuffers:         computeUserBuffers(reqs, bufferSize, idToName),
		})
	}

	writeJSON(w, summaries)
}

func (h *showsHandler) detail(w http.ResponseWriter, r *http.Request) {
	tvdbId, ok := parseTVDBId(w, r)
	if !ok {
		return
	}

	show, err := h.shows.FindByTVDB(tvdbId)
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if show == nil {
		http.Error(w, "show not found", http.StatusNotFound)
		return
	}

	idToName := buildIDToNameMap(h.plex)

	reqs, _ := h.requests.FindByShow(tvdbId)
	reqDetails := make([]UserRequestDetail, 0, len(reqs))
	for _, req := range reqs {
		reqDetails = append(reqDetails, UserRequestDetail{
			UserRequest: req,
			DisplayName: resolveDisplayName(req.DisplayName, idToName, req.PlexUserID),
		})
	}

	openFlags, _ := h.flags.FindByShow(tvdbId)
	filteredFlags := []repository.DiscrepancyFlag{}
	for _, f := range openFlags {
		if f.Status == "open" {
			filteredFlags = append(filteredFlags, f)
		}
	}

	expectedState := map[int][]int{}
	if h.engine != nil {
		if es, err := h.engine.ComputeExpectedState(tvdbId); err == nil {
			expectedState = es
		}
	}

	allEpisodes := map[int][]int{}
	if h.sonarr != nil {
		if eps, err := h.sonarr.GetEpisodes(show.SonarrId); err == nil {
			for _, ep := range eps {
				if ep.SeasonNumber == 0 {
					continue
				}
				allEpisodes[ep.SeasonNumber] = append(allEpisodes[ep.SeasonNumber], ep.EpisodeNumber)
			}
			for s, epNums := range allEpisodes {
				sort.Ints(epNums)
				allEpisodes[s] = epNums
			}
		}
	}

	bufferSize := h.shows.EffectiveBufferSize(tvdbId)
	summary := ShowSummary{
		TVDBId:              show.TVDBId,
		SonarrId:            show.SonarrId,
		Title:               show.Title,
		Status:              show.Status,
		EffectiveBufferSize: bufferSize,
		ActiveRequestCount:  len(reqs),
		LastActivityAt:      show.LastActivityAt,
		PosterURL:           show.PosterURL,
		FanartURL:           show.FanartURL,
		UserBuffers:         computeUserBuffers(reqs, bufferSize, idToName),
	}

	events := []repository.Event{}
	if h.events != nil {
		if evs, err := h.events.FindByShow(tvdbId, 50); err == nil && evs != nil {
			events = evs
		}
	}

	writeJSON(w, ShowDetail{
		ShowSummary:   summary,
		Requests:      reqDetails,
		ExpectedState: expectedState,
		AllEpisodes:   allEpisodes,
		OpenFlags:     filteredFlags,
		Events:        events,
	})
}

func (h *showsHandler) reconcile(w http.ResponseWriter, r *http.Request) {
	tvdbId, ok := parseTVDBId(w, r)
	if !ok {
		return
	}

	h.queue.Enqueue(func() {
		if err := h.reconciler.ReconcileShow(tvdbId); err != nil {
			_ = err
		}
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]bool{"queued": true}) //nolint:errcheck
}

func parseTVDBId(w http.ResponseWriter, r *http.Request) (int, bool) {
	s := chi.URLParam(r, "tvdbId")
	id, err := strconv.Atoi(s)
	if err != nil || id <= 0 {
		http.Error(w, "invalid tvdbId", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
