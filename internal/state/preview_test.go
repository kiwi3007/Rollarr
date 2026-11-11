package state

import (
	"reflect"
	"testing"
	"time"

	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/plex"
)

const testSonarrId = 1
const testBuffer = 3

// Preview must produce the same expected state as Compute when the DB holds
// the auto-discovered requests Preview synthesizes — the guarantee that the
// preview shown before an add matches what reconcile does after it.
func TestPreviewMatchesCompute(t *testing.T) {
	fs := &fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10})}
	fp := &fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
		10: {watched(10, 1, 9, time.Now().Add(-time.Hour))},
		20: {watched(20, 2, 4, time.Now().Add(-time.Hour))},
	}}
	eng, requests := newTestEngine(t, fs, fp)

	preview, err := eng.Preview(testTVDB, testSonarrId, testBuffer, nil)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	// Register the same watchers as auto-discovered rows (requested_season 0).
	for _, userId := range []string{"10", "20"} {
		err := requests.Upsert(repository.UserRequest{
			PlexUserID:       userId,
			TVDBId:           testTVDB,
			RequestTimestamp: time.Now(),
			RequestedSeason:  0,
		})
		if err != nil {
			t.Fatalf("upsert request: %v", err)
		}
	}

	computed, err := eng.Compute(testTVDB)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if !reflect.DeepEqual(preview.Expected, computed.Expected) {
		t.Fatalf("preview/compute mismatch:\npreview: %v\ncompute: %v", preview.Expected, computed.Expected)
	}

	if len(preview.Watchers) != 2 {
		t.Fatalf("want 2 detected watchers, got %+v", preview.Watchers)
	}
	for _, w := range preview.Watchers {
		if !w.Detected {
			t.Errorf("watcher %s: want Detected=true", w.PlexUserID)
		}
	}
}

// A detected watcher mid-season gets the safety episode + buffer window.
func TestPreviewAutoDetect(t *testing.T) {
	fs := &fakeSonarr{episodes: eps(map[int]int{1: 10})}
	fp := &fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
		10: {watched(10, 1, 5, time.Now().Add(-time.Hour))},
	}}
	eng, _ := newTestEngine(t, fs, fp)

	preview, err := eng.Preview(testTVDB, testSonarrId, testBuffer, nil)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	want := ExpectedState{1: {1, 5, 6, 7, 8}}
	if !reflect.DeepEqual(preview.Expected, want) {
		t.Fatalf("expected state mismatch:\n got: %v\nwant: %v", preview.Expected, want)
	}

	w := preview.Watchers[0]
	if w.HighestSeason != 1 || w.HighestEpisode != 5 {
		t.Errorf("want progress S01E05, got S%02dE%02d", w.HighestSeason, w.HighestEpisode)
	}
	wantSegs := []WindowSegment{{Season: 1, Start: 5, End: 8}}
	if !reflect.DeepEqual(w.Segments, wantSegs) {
		t.Errorf("segments mismatch:\n got: %v\nwant: %v", w.Segments, wantSegs)
	}
}

// No history anywhere + manual watcher → seed at the chosen season's premiere,
// with the S01E01 and requested-season anchors.
func TestPreviewManualSeed(t *testing.T) {
	fs := &fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10, 3: 10})}
	fp := &fakePlex{showKey: "555"}
	eng, _ := newTestEngine(t, fs, fp)

	manual := &ManualWatcher{PlexUserID: "10", DisplayName: "kieran", RequestedSeason: 3}
	preview, err := eng.Preview(testTVDB, testSonarrId, testBuffer, manual)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	want := ExpectedState{1: {1}, 3: {1, 2, 3}}
	if !reflect.DeepEqual(preview.Expected, want) {
		t.Fatalf("expected state mismatch:\n got: %v\nwant: %v", preview.Expected, want)
	}

	if len(preview.Watchers) != 1 {
		t.Fatalf("want 1 watcher, got %+v", preview.Watchers)
	}
	w := preview.Watchers[0]
	if w.Detected || w.IsRewatching || w.DisplayName != "kieran" {
		t.Errorf("want manual non-rewatch watcher 'kieran', got %+v", w)
	}
	wantSegs := []WindowSegment{{Season: 3, Start: 1, End: 3}}
	if !reflect.DeepEqual(w.Segments, wantSegs) {
		t.Errorf("segments mismatch:\n got: %v\nwant: %v", w.Segments, wantSegs)
	}
}

// History through S05 + manual season 2 → rewatch: old history ignored, window
// seeds at S02E01, and the watcher is flagged.
func TestPreviewRewatchOverride(t *testing.T) {
	fs := &fakeSonarr{episodes: eps(map[int]int{1: 10, 2: 10, 3: 10, 4: 10, 5: 10})}
	fp := &fakePlex{showKey: "555", history: map[int][]plex.WatchHistoryEntry{
		10: {watched(10, 5, 10, time.Now().Add(-time.Hour))},
	}}
	eng, _ := newTestEngine(t, fs, fp)

	manual := &ManualWatcher{PlexUserID: "10", RequestedSeason: 2}
	preview, err := eng.Preview(testTVDB, testSonarrId, testBuffer, manual)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	want := ExpectedState{1: {1}, 2: {1, 2, 3}}
	if !reflect.DeepEqual(preview.Expected, want) {
		t.Fatalf("expected state mismatch:\n got: %v\nwant: %v", preview.Expected, want)
	}

	if len(preview.Watchers) != 1 {
		t.Fatalf("manual override should replace the detected watcher, got %+v", preview.Watchers)
	}
	if !preview.Watchers[0].IsRewatching {
		t.Errorf("want IsRewatching=true, got %+v", preview.Watchers[0])
	}
}
