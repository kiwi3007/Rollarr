package reconcile

import (
	"reflect"
	"testing"

	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/sonarr"
)

func fileEp(id, season, episode, fileId int, monitored bool) sonarr.Episode {
	return sonarr.Episode{
		ID: id, SeasonNumber: season, EpisodeNumber: episode,
		HasFile: fileId > 0, EpisodeFileId: fileId, Monitored: monitored,
	}
}

func expectedOf(keys ...epKey) map[epKey]struct{} {
	m := make(map[epKey]struct{}, len(keys))
	for _, k := range keys {
		m[k] = struct{}{}
	}
	return m
}

func orphanIDs(o []orphanFile) []int {
	ids := []int{}
	for _, f := range o {
		ids = append(ids, f.fileId)
	}
	return ids
}

func TestPlanOrphansKeepsSpecials(t *testing.T) {
	episodes := []sonarr.Episode{
		fileEp(1, 0, 1, 100, true), // special with a file
		fileEp(2, 1, 1, 101, true),
		fileEp(3, 1, 2, 102, true),
	}
	got := orphanIDs(planOrphans(episodes, expectedOf(epKey{1, 1})))
	if want := []int{102}; !reflect.DeepEqual(got, want) {
		t.Fatalf("orphans = %v, want %v (special must never be deleted)", got, want)
	}
}

func TestPlanOrphansMultiEpisodeFile(t *testing.T) {
	episodes := []sonarr.Episode{
		fileEp(1, 1, 1, 200, true), // S01E01-E02 share file 200
		fileEp(2, 1, 2, 200, true),
		fileEp(3, 1, 3, 201, true), // S01E03-E04 share file 201
		fileEp(4, 1, 4, 201, true),
	}
	// Window covers E02 only: file 200 must survive, file 201 is orphaned.
	orphans := planOrphans(episodes, expectedOf(epKey{1, 2}))
	if got, want := orphanIDs(orphans), []int{201}; !reflect.DeepEqual(got, want) {
		t.Fatalf("orphans = %v, want %v", got, want)
	}
	if got := orphans[0].eps; !reflect.DeepEqual(got, []epKey{{1, 3}, {1, 4}}) {
		t.Fatalf("orphan episodes = %v", got)
	}
}

func TestPlanUnmonitor(t *testing.T) {
	episodes := []sonarr.Episode{
		fileEp(1, 0, 1, 0, true),    // special: left alone
		fileEp(2, 1, 1, 300, true),  // shares kept file with expected E02
		fileEp(3, 1, 2, 300, true),  // expected
		fileEp(4, 1, 3, 0, false),   // already unmonitored, no file: skip
		fileEp(5, 1, 4, 301, false), // file just deleted → Sonarr re-monitors it
		fileEp(6, 1, 5, 0, true),    // out of window, monitored
	}
	got := planUnmonitor(episodes, expectedOf(epKey{1, 2}), map[int]bool{301: true})
	if want := []int{5, 6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unmonitor = %v, want %v", got, want)
	}
}

func TestPlanSeerrResolution(t *testing.T) {
	reqs := []repository.UserRequest{
		{PlexUserID: "seerr:alice", RequestedSeason: 3}, // resolves; no numeric row yet
		{PlexUserID: "seerr:bob", RequestedSeason: 2},   // resolves; auto-discovered row exists
		{PlexUserID: "seerr:carol", RequestedSeason: 1}, // resolves; Seerr row already exists
		{PlexUserID: "seerr:dave", RequestedSeason: 4},  // does not resolve: must be kept
		{PlexUserID: "20", RequestedSeason: 0},          // bob, auto-discovered
		{PlexUserID: "30", RequestedSeason: 2},          // carol, Seerr-initiated
	}
	userMap := map[string]int{"alice": 10, "bob": 20, "carol": 30}

	upserts, deletes := planSeerrResolution(reqs, userMap)

	gotUp := map[string]int{}
	for _, u := range upserts {
		gotUp[u.PlexUserID] = u.RequestedSeason
	}
	if want := map[string]int{"10": 3, "20": 2}; !reflect.DeepEqual(gotUp, want) {
		t.Fatalf("upserts = %v, want %v", gotUp, want)
	}
	if want := []string{"seerr:alice", "seerr:bob", "seerr:carol"}; !reflect.DeepEqual(deletes, want) {
		t.Fatalf("deletes = %v, want %v (dave must be kept)", deletes, want)
	}
}
