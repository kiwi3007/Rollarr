package proxy

import (
	"encoding/json"
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

	return NewInterceptor(stubEpisodes{episodes: episodes}, shows, stubExpectedState{expected: expected})
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
