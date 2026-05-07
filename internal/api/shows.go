package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/reconcile"
	"github.com/kiwi3007/rollarr/internal/scheduler"
	"github.com/kiwi3007/rollarr/internal/state"
)

// ShowSummary is the JSON shape returned by GET /api/shows.
type ShowSummary struct {
	TVDBId              int        `json:"tvdb_id"`
	SonarrId            int        `json:"sonarr_id"`
	Title               string     `json:"title"`
	Status              string     `json:"status"`
	EffectiveBufferSize int        `json:"effective_buffer_size"`
	ActiveRequestCount  int        `json:"active_request_count"`
	LastActivityAt      *time.Time `json:"last_activity_at"`
}

// ShowDetail is the JSON shape returned by GET /api/shows/{tvdbId}.
type ShowDetail struct {
	ShowSummary
	Requests      []repository.UserRequest      `json:"requests"`
	ExpectedState map[int][]int                 `json:"expected_state"`
	OpenFlags     []repository.DiscrepancyFlag  `json:"open_flags"`
}

type showsHandler struct {
	shows      *repository.ShowRepository
	requests   *repository.UserRequestRepository
	flags      *repository.DiscrepancyFlagRepository
	engine     *state.Engine // may be nil; set by wire in main
	reconciler *reconcile.Reconciler
	queue      *scheduler.JobQueue
}

// SetEngine allows injecting the state engine after construction (avoids import
// cycle if the engine needs to reference repositories).
func (h *showsHandler) SetEngine(engine *state.Engine) {
	h.engine = engine
}

func (h *showsHandler) list(w http.ResponseWriter, r *http.Request) {
	all, err := h.shows.FindAll()
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	summaries := make([]ShowSummary, 0, len(all))
	for _, show := range all {
		reqs, _ := h.requests.FindByShow(show.TVDBId)
		summaries = append(summaries, ShowSummary{
			TVDBId:              show.TVDBId,
			SonarrId:            show.SonarrId,
			Title:               show.Title,
			Status:              show.Status,
			EffectiveBufferSize: h.shows.EffectiveBufferSize(show.TVDBId),
			ActiveRequestCount:  len(reqs),
			LastActivityAt:      show.LastActivityAt,
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

	reqs, _ := h.requests.FindByShow(tvdbId)
	openFlags, _ := h.flags.FindByShow(tvdbId)

	// Filter open flags only.
	var filteredFlags []repository.DiscrepancyFlag
	for _, f := range openFlags {
		if f.Status == "open" {
			filteredFlags = append(filteredFlags, f)
		}
	}

	var expectedState map[int][]int
	if h.engine != nil {
		if es, err := h.engine.ComputeExpectedState(tvdbId); err == nil {
			expectedState = es
		}
	}

	summary := ShowSummary{
		TVDBId:              show.TVDBId,
		SonarrId:            show.SonarrId,
		Title:               show.Title,
		Status:              show.Status,
		EffectiveBufferSize: h.shows.EffectiveBufferSize(tvdbId),
		ActiveRequestCount:  len(reqs),
		LastActivityAt:      show.LastActivityAt,
	}

	detail := ShowDetail{
		ShowSummary:   summary,
		Requests:      reqs,
		ExpectedState: expectedState,
		OpenFlags:     filteredFlags,
	}

	writeJSON(w, detail)
}

func (h *showsHandler) reconcile(w http.ResponseWriter, r *http.Request) {
	tvdbId, ok := parseTVDBId(w, r)
	if !ok {
		return
	}

	h.queue.Enqueue(func() {
		if err := h.reconciler.ReconcileShow(tvdbId); err != nil {
			// Log only; the queue job can't return an error to the HTTP caller.
			_ = err
		}
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]bool{"queued": true}) //nolint:errcheck
}

// parseTVDBId extracts and validates the {tvdbId} path parameter.
func parseTVDBId(w http.ResponseWriter, r *http.Request) (int, bool) {
	s := chi.URLParam(r, "tvdbId")
	id, err := strconv.Atoi(s)
	if err != nil || id <= 0 {
		http.Error(w, "invalid tvdbId", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// writeJSON encodes v as JSON and writes it with Content-Type: application/json.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
