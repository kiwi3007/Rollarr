package state

import (
	"testing"

	"github.com/kiwi3007/rollarr/internal/sonarr"
)

// eps builds a Sonarr episode list: seasons[i] episodes numbered 1..counts[i].
func eps(counts map[int]int) []sonarr.Episode {
	var out []sonarr.Episode
	for season, n := range counts {
		for e := 1; e <= n; e++ {
			out = append(out, sonarr.Episode{SeasonNumber: season, EpisodeNumber: e})
		}
	}
	return out
}

func TestBuildLayoutLinearOrder(t *testing.T) {
	lay := buildLayout(eps(map[int]int{2: 2, 1: 3}))

	want := []epRef{{1, 1}, {1, 2}, {1, 3}, {2, 1}, {2, 2}}
	if len(lay.linear) != len(want) {
		t.Fatalf("linear length = %d, want %d", len(lay.linear), len(want))
	}
	for i, ref := range want {
		if lay.linear[i] != ref {
			t.Errorf("linear[%d] = %v, want %v", i, lay.linear[i], ref)
		}
		if lay.index[ref] != i {
			t.Errorf("index[%v] = %d, want %d", ref, lay.index[ref], i)
		}
	}
}

func TestBuildLayoutSkipsSpecialsAndDuplicates(t *testing.T) {
	episodes := []sonarr.Episode{
		{SeasonNumber: 0, EpisodeNumber: 1},
		{SeasonNumber: 1, EpisodeNumber: 1},
		{SeasonNumber: 1, EpisodeNumber: 1}, // duplicate row
		{SeasonNumber: 1, EpisodeNumber: 2},
	}
	lay := buildLayout(episodes)
	if len(lay.linear) != 2 {
		t.Fatalf("linear length = %d, want 2 (specials and dupes excluded)", len(lay.linear))
	}
}

func TestPositionIndex(t *testing.T) {
	lay := buildLayout(eps(map[int]int{1: 10, 2: 8}))

	cases := []struct {
		season, episode, want int
		name                  string
	}{
		{1, 1, 0, "first episode"},
		{1, 10, 9, "last of season 1"},
		{2, 1, 10, "first of season 2"},
		{2, 8, 17, "last episode"},
		{1, 12, 9, "watched ep unknown to Sonarr snaps to S01E10"},
		{1, 0, -1, "before first episode"},
		{0, 5, -1, "season zero precedes everything"},
		{3, 1, 17, "season beyond layout snaps to end"},
	}
	for _, c := range cases {
		if got := lay.positionIndex(c.season, c.episode); got != c.want {
			t.Errorf("%s: positionIndex(%d,%d) = %d, want %d", c.name, c.season, c.episode, got, c.want)
		}
	}
}

// TestWindowCrossesSeasonBoundary verifies the core fix: a position near the
// end of a season produces a window that spills into the next season.
func TestWindowCrossesSeasonBoundary(t *testing.T) {
	lay := buildLayout(eps(map[int]int{1: 10, 2: 8}))
	bufferSize := 3

	// User watched S01E09 → expect S01E09 (safety) + S01E10 + S02E01 + S02E02.
	idx := lay.positionIndex(1, 9)
	from, to := idx-safetyBehind+1, idx+bufferSize

	var window []epRef
	for i := from; i <= to && i < len(lay.linear); i++ {
		window = append(window, lay.linear[i])
	}

	want := []epRef{{1, 9}, {1, 10}, {2, 1}, {2, 2}}
	if len(window) != len(want) {
		t.Fatalf("window = %v, want %v", window, want)
	}
	for i := range want {
		if window[i] != want[i] {
			t.Fatalf("window = %v, want %v", window, want)
		}
	}
}

// TestFinishedWatcherHasNoWindow verifies a position at the final known episode
// is detected as finished (idx+1 >= len).
func TestFinishedWatcherHasNoWindow(t *testing.T) {
	lay := buildLayout(eps(map[int]int{1: 10}))
	idx := lay.positionIndex(1, 10)
	if idx+1 < len(lay.linear) {
		t.Fatalf("S01E10 of a 10-episode show should be the final index; idx=%d len=%d", idx, len(lay.linear))
	}
}
