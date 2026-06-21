package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/plex"
	"github.com/kiwi3007/rollarr/internal/reconcile"
	"github.com/kiwi3007/rollarr/internal/scheduler"
	"github.com/kiwi3007/rollarr/internal/sonarr"
	"github.com/kiwi3007/rollarr/internal/state"
)

// PlexLibraryShow is one row of GET /api/plex/library: a Plex show
// cross-referenced against Sonarr and Rollarr tracking state.
type PlexLibraryShow struct {
	TVDBId    int    `json:"tvdb_id"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	PlexKey   string `json:"plex_key"`
	PosterURL string `json:"poster_url"`
	InSonarr  bool   `json:"in_sonarr"`
	SonarrId  int    `json:"sonarr_id"`
	IsTracked bool   `json:"is_tracked"`
}

// PreviewResponse is the JSON shape of GET /api/plex/library/{tvdbId}/preview.
type PreviewResponse struct {
	TVDBId        int                    `json:"tvdb_id"`
	Title         string                 `json:"title"`
	BufferSize    int                    `json:"buffer_size"`
	ExpectedState map[int][]int          `json:"expected_state"`
	AllEpisodes   map[int][]int          `json:"all_episodes"`
	Watchers      []state.PreviewWatcher `json:"watchers"`
	FilesDeleted  int                    `json:"files_deleted"`
	BytesFreed    int64                  `json:"bytes_freed"`
}

// AddShowRequest is the body of POST /api/shows.
type AddShowRequest struct {
	TVDBId     int                `json:"tvdb_id"`
	ManualUser *AddShowManualUser `json:"manual_user,omitempty"`
}

// AddShowManualUser is an explicit watcher choice for a manual add.
type AddShowManualUser struct {
	PlexUserID      string `json:"plex_user_id"`
	DisplayName     string `json:"display_name"`
	RequestedSeason int    `json:"requested_season"`
}

type libraryHandler struct {
	shows      *repository.ShowRepository
	requests   *repository.UserRequestRepository
	events     *repository.EventRepository
	engine     *state.Engine
	sonarr     *sonarr.Client
	plex       *plex.Client
	reconciler *reconcile.Reconciler
	queue      *scheduler.JobQueue
}

// list handles GET /api/plex/library.
func (h *libraryHandler) list(w http.ResponseWriter, _ *http.Request) {
	plexShows, err := h.plex.ListLibraryShows()
	if err != nil {
		http.Error(w, "plex error: "+err.Error(), http.StatusBadGateway)
		return
	}

	bySonarrTVDB := map[int]sonarr.Series{}
	if series, err := h.sonarr.GetSeries(); err == nil {
		for _, s := range series {
			bySonarrTVDB[s.TvdbId] = s
		}
	} else {
		http.Error(w, "sonarr error: "+err.Error(), http.StatusBadGateway)
		return
	}

	trackedTVDB := map[int]bool{}
	if tracked, err := h.shows.FindAll(); err == nil {
		for _, s := range tracked {
			if s.Status == "active" {
				trackedTVDB[s.TVDBId] = true
			}
		}
	}

	result := make([]PlexLibraryShow, 0, len(plexShows))
	for _, ps := range plexShows {
		entry := PlexLibraryShow{
			TVDBId:  ps.TVDBId,
			Title:   ps.Title,
			Year:    ps.Year,
			PlexKey: ps.RatingKey,
		}
		if s, ok := bySonarrTVDB[ps.TVDBId]; ok && ps.TVDBId > 0 {
			entry.InSonarr = true
			entry.SonarrId = s.ID
			entry.PosterURL = s.PosterURL()
			entry.IsTracked = trackedTVDB[ps.TVDBId]
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Title < result[j].Title })

	writeJSON(w, result)
}

// preview handles GET /api/plex/library/{tvdbId}/preview?season=N&user=ID&name=X.
// Read-only: runs outside the job queue.
func (h *libraryHandler) preview(w http.ResponseWriter, r *http.Request) {
	tvdbId, ok := parseTVDBId(w, r)
	if !ok {
		return
	}

	series, err := h.sonarr.GetSeriesByTVDB(tvdbId)
	if err != nil {
		http.Error(w, "show not in Sonarr", http.StatusNotFound)
		return
	}

	var manual *state.ManualWatcher
	if user := r.URL.Query().Get("user"); user != "" {
		season, _ := strconv.Atoi(r.URL.Query().Get("season"))
		manual = &state.ManualWatcher{
			PlexUserID:      user,
			DisplayName:     r.URL.Query().Get("name"),
			RequestedSeason: season,
		}
	}

	// Falls back to the global default when the show isn't tracked yet.
	bufferSize := h.shows.EffectiveBufferSize(tvdbId)

	// The library list already holds each show's Plex ratingKey; passing it back
	// as ?key= lets per-card previews skip FindShowByTVDB's full library scan.
	var result state.PreviewResult
	if key := r.URL.Query().Get("key"); key != "" {
		result, err = h.engine.PreviewWithKey(tvdbId, series.ID, key, bufferSize, manual)
	} else {
		result, err = h.engine.Preview(tvdbId, series.ID, bufferSize, manual)
	}
	if err != nil {
		http.Error(w, "preview failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	filesDeleted, bytesFreed := h.previewSavings(series.ID, result.Expected)

	writeJSON(w, PreviewResponse{
		TVDBId:        tvdbId,
		Title:         series.Title,
		BufferSize:    bufferSize,
		ExpectedState: result.Expected,
		AllEpisodes:   result.AllEpisodes,
		Watchers:      result.Watchers,
		FilesDeleted:  filesDeleted,
		BytesFreed:    bytesFreed,
	})
}

// previewSavings counts the files the first reconcile would delete: every
// episode with a file whose (season, episode) is outside the expected state —
// the same orphan rule ReconcileShow applies (specials included).
func (h *libraryHandler) previewSavings(sonarrId int, expected state.ExpectedState) (int, int64) {
	episodes, err := h.sonarr.GetEpisodes(sonarrId)
	if err != nil {
		return 0, 0
	}

	// Sizes joined by file ID — the bulk episodefile resource doesn't reliably
	// carry episode numbers.
	sizeByFileID := map[int]int64{}
	if files, err := h.sonarr.GetEpisodeFiles(sonarrId); err == nil {
		for _, f := range files {
			sizeByFileID[f.ID] = f.Size
		}
	}

	expectedSet := map[[2]int]bool{}
	for season, eps := range expected {
		for _, ep := range eps {
			expectedSet[[2]int{season, ep}] = true
		}
	}

	files, bytes := 0, int64(0)
	for _, ep := range episodes {
		if !ep.HasFile || expectedSet[[2]int{ep.SeasonNumber, ep.EpisodeNumber}] {
			continue
		}
		files++
		bytes += sizeByFileID[ep.EpisodeFileId]
	}
	return files, bytes
}

// users handles GET /api/plex/users — the override dropdown source.
func (h *libraryHandler) users(w http.ResponseWriter, _ *http.Request) {
	type plexUserOption struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	idToName := buildIDToNameMap(h.plex)
	result := make([]plexUserOption, 0, len(idToName))
	for id, name := range idToName {
		result = append(result, plexUserOption{ID: id, Name: name})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	writeJSON(w, result)
}

// addShow handles POST /api/shows: manual onboarding from the library browser.
// Mirrors the Seerr webhook flow — upsert show, register the watcher (manual
// override or reconcile's auto-discovery), then reconcile through the queue.
func (h *libraryHandler) addShow(w http.ResponseWriter, r *http.Request) {
	var body AddShowRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TVDBId <= 0 {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	tvdbId := body.TVDBId

	series, err := h.sonarr.GetSeriesByTVDB(tvdbId)
	if err != nil {
		http.Error(w, "show not in Sonarr", http.StatusBadRequest)
		return
	}

	if existing, err := h.shows.FindByTVDB(tvdbId); err == nil && existing != nil && existing.Status == "active" {
		http.Error(w, "show already tracked", http.StatusConflict)
		return
	}

	// Without a manual watcher the add relies on reconcile's auto-discovery; if
	// Plex history has nobody, the show would sit inert behind the
	// zero-requests safety guard. Reject so the UI asks for a user + season.
	if body.ManualUser == nil {
		preview, err := h.engine.Preview(tvdbId, series.ID, h.shows.EffectiveBufferSize(tvdbId), nil)
		if err != nil || len(preview.Watchers) == 0 {
			http.Error(w, "no watch history found — pick a user and season", http.StatusBadRequest)
			return
		}
	}

	manual := body.ManualUser
	h.queue.Enqueue(func() {
		show := repository.Show{
			TVDBId:    tvdbId,
			SonarrId:  series.ID,
			Title:     series.Title,
			PosterURL: series.PosterURL(),
			FanartURL: series.FanartURL(),
			Status:    "active",
		}
		if err := h.shows.Upsert(show); err != nil {
			log.Printf("[api] addShow tvdb=%d: upsert show: %v", tvdbId, err)
			return
		}

		if manual != nil {
			season := manual.RequestedSeason
			if season < 1 {
				season = 1
			}
			showKey, err := h.plex.FindShowByTVDB(tvdbId)
			if err != nil {
				showKey = ""
			}
			rewatching := h.engine.IsRewatch(tvdbId, showKey, manual.PlexUserID, season)
			req := repository.UserRequest{
				PlexUserID:       manual.PlexUserID,
				DisplayName:      manual.DisplayName,
				TVDBId:           tvdbId,
				RequestTimestamp: time.Now(),
				IsRewatching:     rewatching,
				RequestedSeason:  season,
			}
			if err := h.requests.Upsert(req); err != nil {
				log.Printf("[api] addShow tvdb=%d: upsert request: %v", tvdbId, err)
				return
			}
			if rewatching {
				if err := h.requests.ClearWatchProgress(tvdbId, manual.PlexUserID); err != nil {
					log.Printf("[api] addShow tvdb=%d: clear progress: %v", tvdbId, err)
				}
			}
		}

		if err := h.events.Insert(tvdbId, "onboarded", "added via library browser"); err != nil {
			log.Printf("[api] addShow tvdb=%d: log event: %v", tvdbId, err)
		}

		if err := h.reconciler.ReconcileShow(tvdbId); err != nil {
			log.Printf("[api] addShow tvdb=%d: reconcile: %v", tvdbId, err)
		}
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]bool{"queued": true}) //nolint:errcheck
}
