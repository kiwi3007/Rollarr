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
	eng, requests, _ := newTestEngineWithSettings(t, fs, fp)
	return eng, requests
}

// newTestEngineWithSettings is newTestEngine plus the settings repository, for
// scenarios that need to change a setting (e.g. rewatch_window_days).
func newTestEngineWithSettings(t *testing.T, fs *fakeSonarr, fp *fakePlex) (*Engine, *repository.UserRequestRepository, *repository.SettingsRepository) {
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

	return NewEngine(shows, requests, fp, nil, fs), requests, settings
}

func watched(accountId, season, episode int, at time.Time) plex.WatchHistoryEntry {
	return plex.WatchHistoryEntry{AccountID: accountId, SeasonNum: season, EpisodeNum: episode, ViewedAt: at}
}

// request inserts a Seerr-style request (requested_season >= 1) whose
// timestamp is in the past so newer history passes the timestamp filter.
func request(t *testing.T, requests *repository.UserRequestRepository, userId string, season int) {
	t.Helper()
	requestAt(t, requests, userId, season, time.Now().Add(-24*time.Hour))
}

// requestAt is request with an explicit request timestamp, for scenarios that
// need history older than a day to still clear the timestamp filter.
func requestAt(t *testing.T, requests *repository.UserRequestRepository, userId string, season int, ts time.Time) {
	t.Helper()
	err := requests.Upsert(repository.UserRequest{
		PlexUserID:       userId,
		TVDBId:           testTVDB,
		RequestTimestamp: ts,
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


// ---- rewatch windows --------------------------------------------------------
// Going back to earlier episodes is a second watch-through running alongside the
// first: it buffers ahead on its own and never disturbs the forward window or
// the stored floor. Mirrors the live case — John Kirby, Sugar (2024), floor
// S02E07, started S01E01.

func TestScenarioRewatchOpensSecondWindow(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 1, 1, time.Now().Add(-time.Hour))},
		}},
	)
	request(t, requests, "10", 1)
	if err := requests.UpdateWatchProgress(testTVDB, "10", 2, 7); err != nil {
		t.Fatal(err)
	}

	// Forward window S02E07–E10 kept, rewatch window S01E01–E04 added.
	expectState(t, eng, ExpectedState{1: {1, 2, 3, 4}, 2: {7, 8, 9, 10}})

	// The rewatch must not drag the reported progress backwards: the reconciler
	// writes last_watched_* from UserProgress, so a regression here would erase
	// the Season 2 position the next time it runs.
	result, err := eng.Compute(testTVDB)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.UserProgress["10"][2]; got != 7 {
		t.Fatalf("season 2 progress moved: got E%02d, want E07", got)
	}
}

// The rewatch window follows the rewatch forwards.
func TestScenarioRewatchAdvances(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {
				watched(10, 1, 1, time.Now().Add(-72*time.Hour)),
				watched(10, 1, 5, time.Now().Add(-time.Hour)),
			},
		}},
	)
	request(t, requests, "10", 1)
	if err := requests.UpdateWatchProgress(testTVDB, "10", 2, 7); err != nil {
		t.Fatal(err)
	}

	expectState(t, eng, ExpectedState{1: {1, 5, 6, 7, 8}, 2: {7, 8, 9, 10}})
}

// Restarting the season the user is already partway through counts too — and
// recent forward progress in that same season must not mask it.
func TestScenarioSameSeasonRewatch(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {
				watched(10, 2, 8, time.Now().Add(-12*time.Hour)), // forward progress
				watched(10, 2, 1, time.Now().Add(-time.Hour)),    // back to the start
			},
		}},
	)
	request(t, requests, "10", 1)

	// Forward position S02E08 → S02E08–E10 (nothing behind it, safetyBehind
	// keeps E08); rewatch at S02E01 → S02E01–E04.
	expectState(t, eng, ExpectedState{1: {1}, 2: {1, 2, 3, 4, 8, 9, 10}})
}

// At a season boundary the rewatch rolls into the next season like any
// watch-through.
func TestScenarioRewatchRollsIntoNextSeason(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10, 3: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 1, 10, time.Now().Add(-time.Hour))},
		}},
	)
	request(t, requests, "10", 1)
	if err := requests.UpdateWatchProgress(testTVDB, "10", 3, 7); err != nil {
		t.Fatal(err)
	}

	expectState(t, eng, ExpectedState{1: {1, 10}, 2: {1, 2, 3}, 3: {7, 8, 9, 10}})
}

// A rewatch that catches up merges into the forward window — contiguously, with
// no gap between the two.
func TestScenarioRewatchMergesOnCatchUp(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 2, 5, time.Now().Add(-time.Hour))},
		}},
	)
	request(t, requests, "10", 1)
	if err := requests.UpdateWatchProgress(testTVDB, "10", 2, 7); err != nil {
		t.Fatal(err)
	}

	expectState(t, eng, ExpectedState{1: {1}, 2: {5, 6, 7, 8, 9, 10}})
}

// Old history in an earlier season must NOT open a window — that is the
// download/delete loop this feature has to avoid.
func TestScenarioStaleRewatchIgnored(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 1, 1, time.Now().AddDate(0, 0, -60))},
		}},
	)
	// Request predates the play, so it survives the timestamp filter and only
	// the rewatch-window recency check can exclude it.
	requestAt(t, requests, "10", 1, time.Now().AddDate(-1, 0, 0))
	if err := requests.UpdateWatchProgress(testTVDB, "10", 2, 7); err != nil {
		t.Fatal(err)
	}

	expectState(t, eng, ExpectedState{1: {1}, 2: {7, 8, 9, 10}})
}

// Finished the show, then restarted from S01E01 → no forward window, but the
// rewatch window must still open.
func TestScenarioFinishedUserRestartsSeason1(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 1, 1, time.Now().Add(-time.Hour))},
		}},
	)
	request(t, requests, "10", 1)
	if err := requests.UpdateWatchProgress(testTVDB, "10", 2, 10); err != nil {
		t.Fatal(err)
	}

	expectState(t, eng, ExpectedState{1: {1, 2, 3, 4}})
}

// rewatch_window_days = 0 disables the feature entirely.
func TestScenarioRewatchWindowDisabled(t *testing.T) {
	eng, requests, settings := newTestEngineWithSettings(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 1, 1, time.Now().Add(-time.Hour))},
		}},
	)
	if err := settings.Set("rewatch_window_days", "0"); err != nil {
		t.Fatal(err)
	}
	request(t, requests, "10", 1)
	if err := requests.UpdateWatchProgress(testTVDB, "10", 2, 7); err != nil {
		t.Fatal(err)
	}

	expectState(t, eng, ExpectedState{1: {1}, 2: {7, 8, 9, 10}})
}

// Finished the whole show, then immediately restarted from S01E01: the rewatch
// must buffer ahead at every step, all the way back to the finale.
func TestScenarioFullRewatchAfterFinishing(t *testing.T) {
	fp := &fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
		10: {watched(10, 2, 5, time.Now().Add(-time.Hour))}, // just finished S02E05
	}}
	eng, requests := newTestEngine(t, &fakeSonarr{episodes: eps(map[int]int{1: 5, 2: 5})}, fp)
	request(t, requests, "10", 1)
	if err := requests.UpdateWatchProgress(testTVDB, "10", 2, 5); err != nil {
		t.Fatal(err)
	}

	order := []epRef{
		{1, 1}, {1, 2}, {1, 3}, {1, 4}, {1, 5},
		{2, 1}, {2, 2}, {2, 3}, {2, 4}, {2, 5},
	}
	const bufferSize = 3

	for i, ep := range order {
		// Watching an episode requires it to be on disk in the first place.
		state, err := eng.ComputeExpectedState(testTVDB)
		if err != nil {
			t.Fatal(err)
		}
		if !contains(state[ep.Season], ep.Episode) {
			t.Fatalf("step %d: S%02dE%02d not downloaded — rewatch stalled here (state %v)",
				i, ep.Season, ep.Episode, state)
		}
		fp.history[10] = append(fp.history[10], watched(10, ep.Season, ep.Episode, time.Now()))

		// …and the next few must be buffered ahead of them.
		state, err = eng.ComputeExpectedState(testTVDB)
		if err != nil {
			t.Fatal(err)
		}
		for _, next := range order[i+1:min(i+1+bufferSize, len(order))] {
			if !contains(state[next.Season], next.Episode) {
				t.Fatalf("after S%02dE%02d: S%02dE%02d not buffered ahead (state %v)",
					ep.Season, ep.Episode, next.Season, next.Episode, state)
			}
		}
	}
}

func contains(eps []int, ep int) bool {
	for _, e := range eps {
		if e == ep {
			return true
		}
	}
	return false
}

// Finishing the show and starting it again is a fresh watch-through, not a
// temporary second window: it is reported for persistence, and once persisted
// it survives the rewatch window lapsing.
func TestScenarioFinishedRewatchIsPersisted(t *testing.T) {
	playedAt := time.Now().Add(-time.Hour)
	fp := &fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
		10: {
			watched(10, 2, 5, time.Now().Add(-2*time.Hour)), // finished the show
			watched(10, 1, 1, playedAt),                     // …then started again
		},
	}}
	eng, requests := newTestEngine(t, &fakeSonarr{episodes: eps(map[int]int{1: 5, 2: 5})}, fp)
	request(t, requests, "10", 1)
	if err := requests.UpdateWatchProgress(testTVDB, "10", 2, 5); err != nil {
		t.Fatal(err)
	}

	result, err := eng.Compute(testTVDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RewatchResets) != 1 {
		t.Fatalf("want 1 rewatch reset, got %d", len(result.RewatchResets))
	}
	reset := result.RewatchResets[0]
	if reset.PlexUserID != "10" {
		t.Fatalf("reset for wrong user: %q", reset.PlexUserID)
	}
	// Back-dated before the first play of the rewatch, so that play still counts.
	if !reset.Since.Before(playedAt) {
		t.Fatalf("reset since %v is not before the first rewatch play %v", reset.Since, playedAt)
	}

	// Apply it exactly as the reconciler does.
	if err := requests.SetRewatching(testTVDB, "10", true, reset.Since); err != nil {
		t.Fatal(err)
	}

	// The rewatch is now their only position: S01E01 + buffer, no S02 tail.
	expectState(t, eng, ExpectedState{1: {1, 2, 3, 4}})

	// Age the whole thing well past the rewatch window: the play is now 45 days
	// old, so `recent` is empty and no rewatch window could open. The position
	// holds anyway, which is the point of persisting it — a live-history-only
	// window would have evaporated here and taken the files with it.
	fp.history[10] = []plex.WatchHistoryEntry{watched(10, 1, 1, time.Now().AddDate(0, 0, -45))}
	if err := requests.SetRewatching(testTVDB, "10", true, time.Now().AddDate(0, 0, -46)); err != nil {
		t.Fatal(err)
	}
	expectState(t, eng, ExpectedState{1: {1, 2, 3, 4}})

	// And it must not keep re-firing once persisted.
	result, err = eng.Compute(testTVDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RewatchResets) != 0 {
		t.Fatalf("reset re-fired after being persisted: %+v", result.RewatchResets)
	}
}

// A user still mid-show must never be reset — their forward watch-through is
// exactly what the second window exists to protect.
func TestScenarioMidShowRewatchIsNotPersisted(t *testing.T) {
	eng, requests := newTestEngine(t,
		&fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})},
		&fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
			10: {watched(10, 1, 1, time.Now().Add(-time.Hour))},
		}},
	)
	request(t, requests, "10", 1)
	if err := requests.UpdateWatchProgress(testTVDB, "10", 2, 7); err != nil {
		t.Fatal(err)
	}

	result, err := eng.Compute(testTVDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RewatchResets) != 0 {
		t.Fatalf("mid-show rewatch was reset: %+v", result.RewatchResets)
	}
}
