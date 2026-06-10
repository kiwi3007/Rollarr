package state

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/kiwi3007/rollarr/internal/db"
	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/plex"
	"github.com/kiwi3007/rollarr/internal/sonarr"
)

// ---- fakes ------------------------------------------------------------------

type fakePlex struct {
	showKey string // "" = show not in Plex
	findErr error
	history map[int][]plex.WatchHistoryEntry // accountID → entries
	userMap map[string]int
}

func (f *fakePlex) FindShowByTVDB(int) (string, error) {
	if f.findErr != nil {
		return "", f.findErr
	}
	if f.showKey == "" {
		return "", fmt.Errorf("not in plex")
	}
	return f.showKey, nil
}

func (f *fakePlex) GetAccountHistory(_ string, accountId int) ([]plex.WatchHistoryEntry, error) {
	return f.history[accountId], nil
}

func (f *fakePlex) GetShowHistory(string) ([]plex.WatchHistoryEntry, error) {
	var all []plex.WatchHistoryEntry
	for _, entries := range f.history {
		all = append(all, entries...)
	}
	return all, nil
}

func (f *fakePlex) BuildUserMap() (map[string]int, error) {
	if f.userMap == nil {
		return map[string]int{}, nil
	}
	return f.userMap, nil
}

type fakeSonarr struct {
	episodes []sonarr.Episode
	err      error
}

func (f *fakeSonarr) GetEpisodes(int) ([]sonarr.Episode, error) {
	return f.episodes, f.err
}

// ---- harness ----------------------------------------------------------------

const testTVDB = 100

func newTestEngine(t *testing.T, fs *fakeSonarr, fp *fakePlex) (*Engine, *repository.UserRequestRepository) {
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

	if err := shows.Upsert(repository.Show{TVDBId: testTVDB, SonarrId: 1, Title: "Test Show", Status: "active"}); err != nil {
		t.Fatalf("upsert show: %v", err)
	}

	return NewEngine(shows, requests, fp, nil, fs), requests
}

func watched(accountId, season, episode int, at time.Time) plex.WatchHistoryEntry {
	return plex.WatchHistoryEntry{AccountID: accountId, SeasonNum: season, EpisodeNum: episode, ViewedAt: at}
}

// request inserts a Seerr-style request (requested_season >= 1) whose
// timestamp is in the past so newer history passes the timestamp filter.
func request(t *testing.T, requests *repository.UserRequestRepository, userId string, season int) {
	t.Helper()
	err := requests.Upsert(repository.UserRequest{
		PlexUserID:       userId,
		TVDBId:           testTVDB,
		RequestTimestamp: time.Now().Add(-24 * time.Hour),
		RequestedSeason:  season,
	})
	if err != nil {
		t.Fatalf("upsert request: %v", err)
	}
}

func expectState(t *testing.T, eng *Engine, want ExpectedState) {
	t.Helper()
	got, err := eng.ComputeExpectedState(testTVDB)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected state mismatch:\n got: %v\nwant: %v", got, want)
	}
}

// ---- scenarios ----------------------------------------------------------------

// New request, user has never watched → seed at requested season premiere.
func TestScenarioFreshRequestSeeds(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})},
		&fakePlex{showKey: "555"},
	)
	request(t, requests, "10", 1)

	expectState(t, eng, ExpectedState{1: {1, 2, 3}})
}

// Request for a later season seeds there; S01E01 anchor stays.
func TestScenarioLaterSeasonRequest(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10, 3: 10})},
		&fakePlex{showKey: "555"},
	)
	request(t, requests, "10", 3)

	expectState(t, eng, ExpectedState{1: {1}, 3: {1, 2, 3}})
}

// Steady watching mid-season: safety episode + buffer ahead + anchor.
func TestScenarioMidSeason(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 1, 5, time.Now())},
		}},
	)
	request(t, requests, "10", 1)

	expectState(t, eng, ExpectedState{1: {1, 5, 6, 7, 8}})
}

// The core fix: finishing near a season end slides the window into the next season.
func TestScenarioWindowCrossesSeasons(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 1, 9, time.Now())},
		}},
	)
	request(t, requests, "10", 1)

	expectState(t, eng, ExpectedState{1: {1, 9, 10}, 2: {1, 2}})
}

// Finished watcher holds no window — anchors only.
func TestScenarioFinishedWatcher(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 1, 10, time.Now())},
		}},
	)
	request(t, requests, "10", 1)

	expectState(t, eng, ExpectedState{1: {1}})
}

// Finished watcher's window revives when Sonarr learns a new season.
func TestScenarioFinishedWatcherRevives(t *testing.T) {
	fs := &fakeSonarr{episodes: eps(map[int]int{1: 10})}
	eng, requests := newTestEngine(t, fs,
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 1, 10, time.Now())},
		}},
	)
	request(t, requests, "10", 1)
	expectState(t, eng, ExpectedState{1: {1}})

	fs.episodes = eps(map[int]int{1: 10, 2: 8})
	expectState(t, eng, ExpectedState{1: {1, 10}, 2: {1, 2, 3}})
}

// Rewatch: old history is filtered by the rewatch timestamp, stored progress
// was cleared, so the window reseeds at the requested season.
func TestScenarioRewatchReseeds(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 2, 10, time.Now().Add(-30 * 24 * time.Hour))}, // finished long ago
		}},
	)
	request(t, requests, "10", 1)
	// Simulate progress recorded from the first watch-through, then a rewatch.
	if err := requests.UpdateWatchProgress(testTVDB, "10", 2, 10); err != nil {
		t.Fatal(err)
	}
	if err := requests.SetRewatching(testTVDB, "10", true, time.Now()); err != nil {
		t.Fatal(err)
	}

	expectState(t, eng, ExpectedState{1: {1, 2, 3}})
}

// Two users at different positions → union of both windows.
func TestScenarioTwoUsers(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 1, 2, time.Now())},
			20: {watched(20, 2, 4, time.Now())},
		}},
	)
	request(t, requests, "10", 1)
	request(t, requests, "20", 1)

	expectState(t, eng, ExpectedState{1: {1, 2, 3, 4, 5}, 2: {4, 5, 6, 7}})
}

// Plex unreachable but stored progress exists → window from the stored floor,
// never a destructive reseed.
func TestScenarioPlexDownUsesStoredFloor(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10})},
		&fakePlex{findErr: fmt.Errorf("plex down")},
	)
	request(t, requests, "10", 1)
	if err := requests.UpdateWatchProgress(testTVDB, "10", 1, 5); err != nil {
		t.Fatal(err)
	}

	expectState(t, eng, ExpectedState{1: {1, 5, 6, 7, 8}})
}

// Sonarr unreachable → Compute fails (reconciler must abort, never wipe).
func TestScenarioSonarrErrorFailsSafe(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{err: fmt.Errorf("sonarr down")},
		&fakePlex{showKey: "555"},
	)
	request(t, requests, "10", 1)

	if _, err := eng.ComputeExpectedState(testTVDB); err == nil {
		t.Fatal("expected error when Sonarr is unreachable, got nil")
	}
}

// Auto-discovered watcher (requested_season=0): pre-existing history counts
// even though it predates the registration timestamp.
func TestScenarioAutoDiscoveredKeepsHistory(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10, 3: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 3, 2, time.Now().Add(-365 * 24 * time.Hour))}, // watched a year ago
		}},
	)
	err := requests.Upsert(repository.UserRequest{
		PlexUserID:       "10",
		TVDBId:           testTVDB,
		RequestTimestamp: time.Now(), // registered just now
		RequestedSeason:  0,          // auto-discovered
	})
	if err != nil {
		t.Fatal(err)
	}

	expectState(t, eng, ExpectedState{1: {1}, 3: {2, 3, 4, 5}})
}

// No requests → empty expected state, no error.
func TestScenarioNoRequests(t *testing.T) {
	eng, _ := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10})},
		&fakePlex{showKey: "555"},
	)
	expectState(t, eng, ExpectedState{})
}
