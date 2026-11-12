package proxy

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/kiwi3007/rollarr/internal/db"
	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/sonarr"
	"github.com/kiwi3007/rollarr/internal/state"
)

type stubEpisodes struct {
	episodes []sonarr.Episode
}

func (s stubEpisodes) GetSeries() ([]sonarr.Series, error) {
	return []sonarr.Series{{ID: 275, TvdbId: 70851, Title: "Stargate Atlantis"}}, nil
}

func (s stubEpisodes) GetEpisodes(int) ([]sonarr.Episode, error) {
	return s.episodes, nil
}

type stubExpectedState struct {
	expected state.ExpectedState
}

func (s stubExpectedState) ComputeExpectedState(int) (state.ExpectedState, error) {
	return s.expected, nil
}

func newManagedInterceptor(t *testing.T, episodes []sonarr.Episode, expected state.ExpectedState) *Interceptor {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	settings := repository.NewSettingsRepository(database)
	shows := repository.NewShowRepository(database, settings)
	if err := shows.Upsert(repository.Show{
		TVDBId:   70851,
		SonarrId: 275,
		Title:    "Stargate Atlantis",
		Status:   "active",
	}); err != nil {
		t.Fatalf("upsert show: %v", err)
	}

	requests := repository.NewUserRequestRepository(database)
	return NewInterceptor(stubEpisodes{episodes: episodes}, shows, requests, settings, stubExpectedState{expected: expected})
}

func TestMissingEpisodeSearchIsScopedToMissingWindowEpisodes(t *testing.T) {
	interceptor := newManagedInterceptor(t, []sonarr.Episode{
		{ID: 10, SeriesId: 275, SeasonNumber: 1, EpisodeNumber: 1, HasFile: true},
		{ID: 20, SeriesId: 275, SeasonNumber: 2, EpisodeNumber: 1},
		{ID: 21, SeriesId: 275, SeasonNumber: 2, EpisodeNumber: 2},
		{ID: 22, SeriesId: 275, SeasonNumber: 2, EpisodeNumber: 3},
		{ID: 30, SeriesId: 275, SeasonNumber: 3, EpisodeNumber: 1},
	}, state.ExpectedState{
		1: {1},
		2: {1, 2},
	})

	req := httptest.NewRequest("POST", "/api/v3/command", strings.NewReader(`{
		"name":"MissingEpisodeSearch",
		"seriesId":275,
		"monitored":true
	}`))
	rewritten, handled := interceptor.InterceptSearch(httptest.NewRecorder(), req)
	if handled {
		t.Fatal("MissingEpisodeSearch was absorbed; expected a scoped EpisodeSearch")
	}

	var got commandBody
	if err := json.NewDecoder(rewritten.Body).Decode(&got); err != nil {
		t.Fatalf("decode rewritten body: %v", err)
	}
	if got.Name != "EpisodeSearch" {
		t.Fatalf("command name = %q, want EpisodeSearch", got.Name)
	}
	if want := []int{20, 21}; !reflect.DeepEqual(got.EpisodeIds, want) {
		t.Fatalf("episodeIds = %v, want %v", got.EpisodeIds, want)
	}
}

func TestManagedSeriesUpdateDisablesBulkSeasonMonitoring(t *testing.T) {
	interceptor := newManagedInterceptor(t, nil, nil)
	req := httptest.NewRequest("PUT", "/api/v3/series/275", strings.NewReader(`{
		"id":999,
		"title":"Stargate Atlantis",
		"monitored":true,
		"seasons":[
			{"seasonNumber":0,"monitored":false},
			{"seasonNumber":1,"monitored":true},
			{"seasonNumber":2,"monitored":true},
			{"seasonNumber":3,"monitored":true},
			{"seasonNumber":4,"monitored":true},
			{"seasonNumber":5,"monitored":true}
		]
	}`))

	rewritten, handled := interceptor.InterceptSeriesUpdate(httptest.NewRecorder(), req, 275)
	if handled {
		t.Fatal("series update was absorbed; expected a rewritten request")
	}
	var got struct {
		Title     string `json:"title"`
		Monitored bool   `json:"monitored"`
		Seasons   []struct {
			SeasonNumber int  `json:"seasonNumber"`
			Monitored    bool `json:"monitored"`
		} `json:"seasons"`
	}
	if err := json.NewDecoder(rewritten.Body).Decode(&got); err != nil {
		t.Fatalf("decode rewritten body: %v", err)
	}
	if got.Title != "Stargate Atlantis" || !got.Monitored {
		t.Fatalf("unrelated series fields changed: title=%q monitored=%v", got.Title, got.Monitored)
	}
	for _, season := range got.Seasons {
		if season.Monitored {
			t.Errorf("season %d remained monitored", season.SeasonNumber)
		}
	}
}

func TestSeriesUpdateID(t *testing.T) {
	tests := map[string]int{
		"/api/v3/series/275":  275,
		"/api/v3/series/275/": 275,
	}
	for path, want := range tests {
		got, ok := seriesUpdateID(path)
		if !ok || got != want {
			t.Errorf("seriesUpdateID(%q) = (%d, %v), want (%d, true)", path, got, ok, want)
		}
	}
	for _, path := range []string{"/api/v3/series", "/api/v3/series/editor", "/api/v3/episode/275"} {
		if got, ok := seriesUpdateID(path); ok {
			t.Errorf("seriesUpdateID(%q) = (%d, true), want no match", path, got)
		}
	}
}

func TestSeriesAddByExcludedUserPassesThrough(t *testing.T) {
	interceptor := newManagedInterceptor(t, nil, nil)
	if err := interceptor.settings.Set("excluded_user_ids", "111"); err != nil {
		t.Fatal(err)
	}
	if err := interceptor.requests.Upsert(repository.UserRequest{PlexUserID: "111", TVDBId: 81189, RequestedSeason: 1}); err != nil {
		t.Fatal(err)
	}

	body := `{"tvdbId":81189,"addOptions":{"searchForMissingEpisodes":true}}`
	req := httptest.NewRequest("POST", "/api/v3/series", strings.NewReader(body))
	result, handled := interceptor.InterceptSeriesAdd(httptest.NewRecorder(), req)
	if handled {
		t.Fatal("series add was absorbed")
	}
	var got struct {
		AddOptions map[string]bool `json:"addOptions"`
	}
	if err := json.NewDecoder(result.Request.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.AddOptions["searchForMissingEpisodes"] {
		t.Error("search-on-add was disabled for an excluded user's request")
	}
}

func TestSearchOnUnmanagedShowPassesThrough(t *testing.T) {
	interceptor := newManagedInterceptor(t, nil, nil)
	if err := interceptor.shows.SetManaged(70851, false); err != nil {
		t.Fatal(err)
	}

	body := `{"name":"SeriesSearch","seriesId":275}`
	req := httptest.NewRequest("POST", "/api/v3/command", strings.NewReader(body))
	rewritten, handled := interceptor.InterceptSearch(httptest.NewRecorder(), req)
	if handled {
		t.Fatal("search on unmanaged show was absorbed")
	}
	var got commandBody
	if err := json.NewDecoder(rewritten.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "SeriesSearch" || got.SeriesId != 275 {
		t.Errorf("command rewritten to %+v, want unchanged SeriesSearch", got)
	}
}

// bulkOnlyEpisodes resolves episode IDs via the bulk endpoint; the
// all-series scan fails, proving it isn't used.
type bulkOnlyEpisodes struct {
	stubEpisodes
	bulkCalls *int
}

func (s bulkOnlyEpisodes) GetSeries() ([]sonarr.Series, error) {
	return nil, errScanUsed
}

func (s bulkOnlyEpisodes) GetEpisodesByIDs(ids []int) ([]sonarr.Episode, error) {
	*s.bulkCalls++
	want := map[int]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []sonarr.Episode
	for _, ep := range s.episodes {
		if want[ep.ID] {
			out = append(out, ep)
		}
	}
	return out, nil
}

var errScanUsed = errors.New("all-series scan used")

func TestMonitorFilterUsesBulkEpisodeLookup(t *testing.T) {
	episodes := []sonarr.Episode{
		{ID: 20, SeriesId: 275, SeasonNumber: 2, EpisodeNumber: 1},
		{ID: 21, SeriesId: 275, SeasonNumber: 2, EpisodeNumber: 2},
		{ID: 22, SeriesId: 275, SeasonNumber: 2, EpisodeNumber: 3},
	}
	interceptor := newManagedInterceptor(t, nil, state.ExpectedState{2: {1, 2}})
	calls := 0
	interceptor.sonarr = bulkOnlyEpisodes{stubEpisodes: stubEpisodes{episodes: episodes}, bulkCalls: &calls}

	req := httptest.NewRequest("PUT", "/api/v3/episode/monitor", strings.NewReader(`{"episodeIds":[20,21,22],"monitored":true}`))
	rewritten, handled := interceptor.InterceptMonitor(httptest.NewRecorder(), req)
	if handled {
		t.Fatal("monitor request unexpectedly handled")
	}
	var got monitorBody
	if err := json.NewDecoder(rewritten.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if want := []int{20, 21}; !reflect.DeepEqual(got.EpisodeIds, want) {
		t.Fatalf("episodeIds = %v, want %v", got.EpisodeIds, want)
	}
	if calls != 1 {
		t.Fatalf("bulk lookup calls = %d, want 1", calls)
	}
}

// newUntrackedInterceptor has no show row for Sonarr series 275 (tvdb 70851),
// optionally with a pending Seerr request for it.
func newUntrackedInterceptor(t *testing.T, pendingRequestFrom string) *Interceptor {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	settings := repository.NewSettingsRepository(database)
	shows := repository.NewShowRepository(database, settings)
	requests := repository.NewUserRequestRepository(database)
	if pendingRequestFrom != "" {
		if err := requests.Upsert(repository.UserRequest{
			PlexUserID: pendingRequestFrom, TVDBId: 70851, RequestedSeason: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return NewInterceptor(stubEpisodes{}, shows, requests, settings, stubExpectedState{})
}

func seriesSearch(t *testing.T, ic *Interceptor) (passedThrough bool, name string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v3/command", strings.NewReader(`{"name":"SeriesSearch","seriesId":275}`))
	rewritten, handled := ic.InterceptSearch(httptest.NewRecorder(), req)
	if handled {
		return false, ""
	}
	var got commandBody
	if err := json.NewDecoder(rewritten.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	return true, got.Name
}

// A series Rollarr doesn't track (e.g. added straight to Sonarr) is searched
// exactly as asked.
func TestSearchOnUntrackedSeriesPassesThrough(t *testing.T) {
	passed, name := seriesSearch(t, newUntrackedInterceptor(t, ""))
	if !passed || name != "SeriesSearch" {
		t.Fatalf("passed=%v name=%q, want the original SeriesSearch passed through", passed, name)
	}
}

// Between a Seerr request and onboarding there is no show row yet; the search
// must still be held back, or the whole series would download.
func TestSearchDuringOnboardingIsAbsorbed(t *testing.T) {
	if passed, _ := seriesSearch(t, newUntrackedInterceptor(t, "10")); passed {
		t.Fatal("search for a series with a pending managed request passed through")
	}
}
