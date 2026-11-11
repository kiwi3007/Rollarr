package reconcile

import (
	"testing"
	"time"

	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/sonarr"
)

var now = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func ep(season, episode int, airedDaysAgo int) sonarr.Episode {
	e := sonarr.Episode{SeasonNumber: season, EpisodeNumber: episode}
	if airedDaysAgo >= 0 {
		t := now.Add(-time.Duration(airedDaysAgo) * 24 * time.Hour)
		e.AirDateUtc = &t
	}
	return e
}

func watcher(season, episode int) repository.UserRequest {
	return repository.UserRequest{LastWatchedSeason: &season, LastWatchedEpisode: &episode}
}

// S01 aired long ago; S02E01 is announced but unaired (-1).
var season1Only = []sonarr.Episode{ep(1, 1, 400), ep(1, 2, 393), ep(1, 3, 386), ep(2, 1, -1)}

func TestCaughtUpMarker(t *testing.T) {
	tests := []struct {
		name   string
		reqs   []repository.UserRequest
		wantOK bool
	}{
		{"finished watcher arms revival", []repository.UserRequest{watcher(1, 3)}, true},
		{"abandoned watcher does not", []repository.UserRequest{watcher(1, 1)}, false},
		{"one finished watcher is enough", []repository.UserRequest{watcher(1, 1), watcher(1, 3)}, true},
		{"no progress recorded", []repository.UserRequest{{}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, e, ok := caughtUpMarker(season1Only, tt.reqs, now)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && (s != 1 || e != 3) {
				t.Errorf("marker = S%02dE%02d, want S01E03 (unaired S02E01 ignored)", s, e)
			}
		})
	}
}

func TestAiredAfter(t *testing.T) {
	if airedAfter(season1Only, 1, 3, now) {
		t.Error("unaired S02E01 triggered revival")
	}
	withS2 := append(append([]sonarr.Episode{}, season1Only[:3]...), ep(2, 1, 2))
	if !airedAfter(withS2, 1, 3, now) {
		t.Error("aired S02E01 did not trigger revival")
	}
}

func TestNextEpisodeAiredAt(t *testing.T) {
	withS2 := []sonarr.Episode{ep(1, 1, 400), ep(1, 2, 393), ep(1, 3, 386), ep(2, 1, 10), ep(2, 2, 3)}

	// Caught-up watcher: clock restarts at S02E01's air date, not S02E02's.
	got := nextEpisodeAiredAt(withS2, []repository.UserRequest{watcher(1, 3)}, now)
	if want := now.Add(-10 * 24 * time.Hour); got == nil || !got.Equal(want) {
		t.Errorf("caught-up = %v, want %v", got, want)
	}

	// Abandoned watcher's next episode aired long ago — no fresh signal.
	got = nextEpisodeAiredAt(withS2, []repository.UserRequest{watcher(1, 1)}, now)
	if want := now.Add(-393 * 24 * time.Hour); got == nil || !got.Equal(want) {
		t.Errorf("abandoned = %v, want %v", got, want)
	}

	// Fully caught up with nothing new aired.
	if got := nextEpisodeAiredAt(withS2, []repository.UserRequest{watcher(2, 2)}, now); got != nil {
		t.Errorf("fully caught up = %v, want nil", got)
	}
}
