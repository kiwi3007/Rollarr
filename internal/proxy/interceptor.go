package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/sonarr"
	"github.com/kiwi3007/rollarr/internal/state"
)

// cacheTTL is how long episode metadata is cached before a fresh Sonarr fetch.
const cacheTTL = 5 * time.Minute

// episodeCache is a thread-safe, TTL-based in-memory cache mapping Sonarr
// series IDs to their full episode list.
type episodeCache struct {
	mu      sync.Mutex
	entries map[int]cacheEntry
}

type cacheEntry struct {
	episodes  []sonarr.Episode
	expiresAt time.Time
}

func newEpisodeCache() *episodeCache {
	return &episodeCache{entries: make(map[int]cacheEntry)}
}

func (c *episodeCache) get(seriesId int) ([]sonarr.Episode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[seriesId]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.episodes, true
}

func (c *episodeCache) set(seriesId int, episodes []sonarr.Episode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[seriesId] = cacheEntry{
		episodes:  episodes,
		expiresAt: time.Now().Add(cacheTTL),
	}
}

// Interceptor intercepts Sonarr-bound requests that need to be filtered to the
// computed buffer window.
type Interceptor struct {
	sonarr      episodeSource
	shows       *repository.ShowRepository
	engine      expectedStateEngine
	cache       *episodeCache
	OnSeriesAdd func(tvdbId int, requestedSeasons []int) // called after a successful POST /api/v3/series
}

type episodeSource interface {
	GetSeries() ([]sonarr.Series, error)
	GetEpisodes(seriesId int) ([]sonarr.Episode, error)
}

type expectedStateEngine interface {
	ComputeExpectedState(tvdbId int) (state.ExpectedState, error)
}

// NewInterceptor constructs an Interceptor.
func NewInterceptor(
	sonarrClient episodeSource,
	shows *repository.ShowRepository,
	engine expectedStateEngine,
) *Interceptor {
	return &Interceptor{
		sonarr: sonarrClient,
		shows:  shows,
		engine: engine,
		cache:  newEpisodeCache(),
	}
}

// InterceptSeriesUpdate handles PUT /api/v3/series/{id}. Seerr updates an
// existing series before searching it and sets every requested season to
// monitored=true. For a Rollarr-managed show that would make every episode in
// those seasons eligible for Sonarr's subsequent MissingEpisodeSearch. Keep
// the series itself enabled, but leave season/episode monitoring to reconcile,
// which applies the exact per-episode window immediately afterward.
func (i *Interceptor) InterceptSeriesUpdate(w http.ResponseWriter, r *http.Request, pathSeriesId int) (*http.Request, bool) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return nil, true
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return r, false
	}

	// The URL identifies the resource being changed and is authoritative. Do
	// not allow a mismatched body ID to bypass protection for a managed series.
	seriesId := pathSeriesId
	show, err := i.shows.FindBySonarrId(seriesId)
	if err != nil || show == nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return r, false
	}

	encodedSeasons, ok := body["seasons"]
	if !ok {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return r, false
	}
	var seasons []map[string]json.RawMessage
	if err := json.Unmarshal(encodedSeasons, &seasons); err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return r, false
	}
	for _, season := range seasons {
		season["monitored"] = json.RawMessage(`false`)
	}
	rewrittenSeasons, err := json.Marshal(seasons)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return r, false
	}
	body["seasons"] = rewrittenSeasons

	rewritten, err := rewriteBody(r, body)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return r, false
	}
	log.Printf("[proxy] PUT /api/v3/series/%d intercepted: disabled bulk season monitoring for managed tvdb=%d", seriesId, show.TVDBId)
	return rewritten, false
}

// monitorBody is the shape of PUT /api/v3/episode/monitor.
type monitorBody struct {
	EpisodeIds []int `json:"episodeIds"`
	Monitored  bool  `json:"monitored"`
}

// commandBody is the shape of POST /api/v3/command.
type commandBody struct {
	Name         string `json:"name"`
	EpisodeIds   []int  `json:"episodeIds,omitempty"`
	SeriesId     int    `json:"seriesId,omitempty"`
	SeasonNumber *int   `json:"seasonNumber,omitempty"`
}

// SeriesAddResult holds context from an intercepted POST /api/v3/series so the
// proxy can fire OnSeriesAdd after the reverse proxy completes.
type SeriesAddResult struct {
	ResponseWriter   http.ResponseWriter
	Request          *http.Request
	TvdbId           int
	RequestedSeasons []int // season numbers with monitored=true in the request body
	Status           int   // written by WriteHeader; 0 means 200 OK (default)
}

func (r *SeriesAddResult) WriteHeader(code int) {
	r.Status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *SeriesAddResult) Header() http.Header         { return r.ResponseWriter.Header() }
func (r *SeriesAddResult) Write(b []byte) (int, error) { return r.ResponseWriter.Write(b) }

func (r *SeriesAddResult) succeeded() bool {
	s := r.Status
	if s == 0 {
		s = http.StatusOK
	}
	return s >= 200 && s < 300
}

// InterceptSeriesAdd handles POST /api/v3/series.
// It disables Sonarr's automatic search-on-add and returns a SeriesAddResult
// that wraps the ResponseWriter. The proxy must call rp.ServeHTTP(result, result.Request)
// and then check result.succeeded() to decide whether to fire OnSeriesAdd.
func (i *Interceptor) InterceptSeriesAdd(w http.ResponseWriter, r *http.Request) (*SeriesAddResult, bool) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return nil, true
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return &SeriesAddResult{ResponseWriter: w, Request: r}, false
	}

	var tvdbId int
	if raw, ok := body["tvdbId"]; ok {
		_ = json.Unmarshal(raw, &tvdbId)
	}

	// Extract which seasons Overseerr requested (monitored=true).
	var requestedSeasons []int
	if raw, ok := body["seasons"]; ok {
		var seasons []struct {
			SeasonNumber int  `json:"seasonNumber"`
			Monitored    bool `json:"monitored"`
		}
		if json.Unmarshal(raw, &seasons) == nil {
			for _, s := range seasons {
				if s.Monitored && s.SeasonNumber > 0 {
					requestedSeasons = append(requestedSeasons, s.SeasonNumber)
				}
			}
		}
	}

	addOpts := make(map[string]json.RawMessage)
	if raw, ok := body["addOptions"]; ok {
		_ = json.Unmarshal(raw, &addOpts)
	}
	addOpts["searchForMissingEpisodes"] = json.RawMessage(`false`)
	addOpts["searchForCutoffUnmetEpisodes"] = json.RawMessage(`false`)

	log.Printf("[proxy] POST /api/v3/series tvdbId=%d intercepted: disabled searchForMissingEpisodes, requestedSeasons=%v", tvdbId, requestedSeasons)

	encoded, err := json.Marshal(addOpts)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return &SeriesAddResult{ResponseWriter: w, Request: r, TvdbId: tvdbId, RequestedSeasons: requestedSeasons}, false
	}
	body["addOptions"] = encoded

	rewritten, err := rewriteBody(r, body)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return &SeriesAddResult{ResponseWriter: w, Request: r, TvdbId: tvdbId, RequestedSeasons: requestedSeasons}, false
	}

	return &SeriesAddResult{ResponseWriter: w, Request: rewritten, TvdbId: tvdbId, RequestedSeasons: requestedSeasons}, false
}

// InterceptMonitor handles PUT /api/v3/episode/monitor.
// If monitored=true, it filters episodeIds to only those within the expected
// buffer state. Returns the rewritten request and whether it was modified.
func (i *Interceptor) InterceptMonitor(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return nil, true // handled (with error)
	}

	var body monitorBody
	if err := json.Unmarshal(raw, &body); err != nil {
		// Body is not valid JSON — pass through unmodified.
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return r, false
	}

	// Only filter when monitoring (not when un-monitoring).
	if !body.Monitored || len(body.EpisodeIds) == 0 {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return r, false
	}

	filtered, err := i.filterToExpected(body.EpisodeIds)
	if err != nil {
		// Non-fatal: log and pass through unmodified.
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return r, false
	}

	body.EpisodeIds = filtered
	rewritten, err := rewriteBody(r, body)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return r, false
	}
	return rewritten, false
}

// InterceptSearch handles POST /api/v3/command for search commands.
// EpisodeSearch: filters episodeIds to the expected window.
// SeriesSearch / SeasonSearch / MissingEpisodeSearch: converts to EpisodeSearch
// scoped to the window. Seerr uses MissingEpisodeSearch when re-requesting an
// existing series, so it must not pass through with the whole series ID.
func (i *Interceptor) InterceptSearch(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return nil, true
	}

	var body commandBody
	if err := json.Unmarshal(raw, &body); err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return r, false
	}

	switch body.Name {
	case "EpisodeSearch":
		if len(body.EpisodeIds) == 0 {
			r.Body = io.NopCloser(bytes.NewReader(raw))
			return r, false
		}
		filtered, err := i.filterToExpected(body.EpisodeIds)
		if err != nil {
			r.Body = io.NopCloser(bytes.NewReader(raw))
			return r, false
		}
		body.EpisodeIds = filtered
		rewritten, err := rewriteBody(r, body)
		if err != nil {
			r.Body = io.NopCloser(bytes.NewReader(raw))
			return r, false
		}
		return rewritten, false

	case "SeriesSearch", "SeasonSearch", "MissingEpisodeSearch", "CutoffUnmetEpisodeSearch":
		if body.SeriesId == 0 {
			r.Body = io.NopCloser(bytes.NewReader(raw))
			return r, false
		}
		missingOnly := body.Name == "MissingEpisodeSearch"
		windowIds, err := i.windowEpisodeIds(body.SeriesId, body.SeasonNumber, missingOnly)
		if err != nil || len(windowIds) == 0 {
			log.Printf("[proxy] %s seriesId=%d absorbed: show not managed or window empty (err=%v)", body.Name, body.SeriesId, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"id":0,"status":"queued","name":"EpisodeSearch"}`)) //nolint:errcheck
			return nil, true
		}
		log.Printf("[proxy] %s seriesId=%d → EpisodeSearch %d window episodes", body.Name, body.SeriesId, len(windowIds))
		episode := commandBody{Name: "EpisodeSearch", EpisodeIds: windowIds}
		rewritten, err := rewriteBody(r, episode)
		if err != nil {
			r.Body = io.NopCloser(bytes.NewReader(raw))
			return r, false
		}
		return rewritten, false

	default:
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return r, false
	}
}

// windowEpisodeIds returns episode IDs that are within the expected buffer
// window for the given sonarrSeriesId, optionally restricted to one season.
func (i *Interceptor) windowEpisodeIds(sonarrSeriesId int, season *int, missingOnly bool) ([]int, error) {
	show, err := i.shows.FindBySonarrId(sonarrSeriesId)
	if err != nil || show == nil {
		return nil, fmt.Errorf("show not found for sonarrId=%d", sonarrSeriesId)
	}

	expected, err := i.engine.ComputeExpectedState(show.TVDBId)
	if err != nil {
		return nil, fmt.Errorf("compute expected state: %w", err)
	}

	episodes, err := i.getEpisodes(sonarrSeriesId)
	if err != nil {
		return nil, fmt.Errorf("get episodes: %w", err)
	}

	var ids []int
	for _, ep := range episodes {
		if season != nil && ep.SeasonNumber != *season {
			continue
		}
		if missingOnly && ep.HasFile {
			continue
		}
		if inExpectedState(expected, ep.SeasonNumber, ep.EpisodeNumber) {
			ids = append(ids, ep.ID)
		}
	}
	return ids, nil
}

// filterToExpected takes a list of Sonarr episode IDs and returns only those
// that fall within the computed expected state for their respective shows.
func (i *Interceptor) filterToExpected(episodeIds []int) ([]int, error) {
	// Build a map from episodeId → Episode for all series touched by these IDs.
	// We group by series to minimise Sonarr API calls.
	episodeMap, err := i.resolveEpisodes(episodeIds)
	if err != nil {
		return nil, fmt.Errorf("filterToExpected: resolve episodes: %w", err)
	}

	// Cache computed expected states per series to avoid redundant calls.
	expectedCache := make(map[int]state.ExpectedState)

	var allowed []int
	for _, epId := range episodeIds {
		ep, ok := episodeMap[epId]
		if !ok {
			continue
		}

		expected, ok := expectedCache[ep.SeriesId]
		if !ok {
			show, err := i.shows.FindBySonarrId(ep.SeriesId)
			if err != nil || show == nil {
				// Unknown show — allow through.
				allowed = append(allowed, epId)
				continue
			}
			exp, err := i.engine.ComputeExpectedState(show.TVDBId)
			if err != nil {
				// Computation failure — allow through conservatively.
				allowed = append(allowed, epId)
				continue
			}
			expected = exp
			expectedCache[ep.SeriesId] = expected
		}

		if inExpectedState(expected, ep.SeasonNumber, ep.EpisodeNumber) {
			allowed = append(allowed, epId)
		}
	}

	return allowed, nil
}

// resolveEpisodes fetches episode metadata for all series referenced by the
// given episode IDs, using the in-memory cache.
func (i *Interceptor) resolveEpisodes(episodeIds []int) (map[int]sonarr.Episode, error) {
	// First pass: collect which series IDs we need by checking the cache.
	// Since we don't know the series without fetching, we must fetch all
	// episodes for series that contain these IDs. We need to identify series
	// from existing cache entries or by querying Sonarr.
	//
	// Strategy: build a union of all cached episodes, find uncached episode IDs,
	// then query Sonarr's /episode endpoint by seriesId for the uncovered ones.
	//
	// Since Sonarr episodes don't expose their seriesId without an initial query,
	// we use a single GET /episode?episodeIds=... if supported, otherwise fall
	// back to checking all cached series first.

	result := make(map[int]sonarr.Episode)

	// Check all cached series first.
	i.cache.mu.Lock()
	for _, entry := range i.cache.entries {
		if time.Now().Before(entry.expiresAt) {
			for _, ep := range entry.episodes {
				result[ep.ID] = ep
			}
		}
	}
	i.cache.mu.Unlock()

	// Find which episode IDs are still unresolved.
	var missing []int
	for _, id := range episodeIds {
		if _, ok := result[id]; !ok {
			missing = append(missing, id)
		}
	}

	if len(missing) == 0 {
		return result, nil
	}

	// For missing episodes, attempt to fetch using the episodeIds query param
	// (supported in some Sonarr v3 versions). We'll try the bulk endpoint first.
	episodes, err := i.getEpisodesByIDs(missing)
	if err != nil {
		// Unable to resolve — return what we have.
		return result, nil
	}

	// getEpisodesByIDs already populated the cache with each series' FULL
	// episode list via getEpisodes — do not re-set it here with only the
	// matched subset, or windowEpisodeIds would see partial lists for 5 min.
	for _, ep := range episodes {
		result[ep.ID] = ep
	}

	return result, nil
}

// getEpisodesByIDs fetches episode details for the given episode IDs.
// It uses Sonarr's series episode list if a direct ID query is unavailable.
func (i *Interceptor) getEpisodesByIDs(episodeIds []int) ([]sonarr.Episode, error) {
	// Sonarr v3 doesn't have a reliable bulk "get episodes by IDs" endpoint.
	// The /episode endpoint requires a seriesId. Since we're intercepting a
	// monitor/search request, we can infer the series by querying all series
	// and checking which ones contain these episode IDs in their episode list.
	//
	// This is intentionally best-effort: if we can't resolve, we pass through.
	allSeries, err := i.sonarr.GetSeries()
	if err != nil {
		return nil, fmt.Errorf("getEpisodesByIDs: get all series: %w", err)
	}

	targetSet := make(map[int]struct{}, len(episodeIds))
	for _, id := range episodeIds {
		targetSet[id] = struct{}{}
	}

	var found []sonarr.Episode
	for _, s := range allSeries {
		eps, err := i.getEpisodes(s.ID)
		if err != nil {
			continue
		}
		for _, ep := range eps {
			if _, ok := targetSet[ep.ID]; ok {
				found = append(found, ep)
			}
		}
		if len(found) == len(episodeIds) {
			break
		}
	}
	return found, nil
}

// getEpisodes returns episodes for seriesId, using the cache when available.
func (i *Interceptor) getEpisodes(seriesId int) ([]sonarr.Episode, error) {
	if eps, ok := i.cache.get(seriesId); ok {
		return eps, nil
	}
	eps, err := i.sonarr.GetEpisodes(seriesId)
	if err != nil {
		return nil, err
	}
	i.cache.set(seriesId, eps)
	return eps, nil
}

// inExpectedState reports whether (season, episode) is present in the expected state.
func inExpectedState(expected state.ExpectedState, season, episode int) bool {
	eps, ok := expected[season]
	if !ok {
		return false
	}
	for _, e := range eps {
		if e == episode {
			return true
		}
	}
	return false
}

// rewriteBody encodes v as JSON and replaces r.Body and Content-Length.
func rewriteBody(r *http.Request, v interface{}) (*http.Request, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(data))
	r2.ContentLength = int64(len(data))
	return r2, nil
}
