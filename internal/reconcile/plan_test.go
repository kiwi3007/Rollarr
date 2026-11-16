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

const matchedByIDMsg = "Found matching series via grab history, but release was matched to series by ID. Automatic import is not possible. See the FAQ for details."

func queueRec(id, seriesID, episodeID int, downloadID, state, msg string) sonarr.QueueRecord {
	rec := sonarr.QueueRecord{
		ID: id, SeriesID: seriesID, EpisodeID: episodeID,
		DownloadID: downloadID, Title: downloadID, TrackedDownloadState: state,
	}
	if msg != "" {
		rec.StatusMessages = []sonarr.QueueStatusMessage{{Title: downloadID, Messages: []string{msg}}}
	}
	return rec
}

func TestPlanWrongGrabsSelectsBlockedInWindowOnly(t *testing.T) {
	queue := []sonarr.QueueRecord{
		// US Ghosts grabbed for BBC Ghosts (series 299): in window → removed.
		queueRec(1, 299, 18109, "us-s04e01", "importBlocked", matchedByIDMsg),
		// Same series, blocked for a different reason (executable) → left.
		queueRec(2, 299, 18110, "exe", "importBlocked", "Caution: Found executable file with extension: '.exe'"),
		// Same series, downloading normally → left.
		queueRec(3, 299, 18111, "ok", "downloading", ""),
		// Blocked wrong grab but outside every window → left.
		queueRec(4, 299, 18120, "out-of-window", "importBlocked", matchedByIDMsg),
		// Another series' wrong grab (Archie for Archer) → not this show's.
		queueRec(5, 290, 17169, "archie", "importBlocked", matchedByIDMsg),
	}
	inWindow := map[int]bool{18109: true, 18110: true, 18111: true, 17169: true}

	got := planWrongGrabs(queue, 299, inWindow)
	if len(got) != 1 || got[0].queueID != 1 || !reflect.DeepEqual(got[0].episodeIDs, []int{18109}) {
		t.Fatalf("got %+v, want only queue record 1 for episode 18109", got)
	}
}

func TestPlanWrongGrabsCollapsesMultiEpisodeDownload(t *testing.T) {
	// A season pack appears once per episode; it must be removed once and
	// free every episode it was holding, even those outside the window.
	queue := []sonarr.QueueRecord{
		queueRec(10, 299, 3, "pack", "importBlocked", matchedByIDMsg),
		queueRec(11, 299, 1, "pack", "importBlocked", matchedByIDMsg),
		queueRec(12, 299, 2, "pack", "importBlocked", matchedByIDMsg),
	}
	got := planWrongGrabs(queue, 299, map[int]bool{2: true})
	if len(got) != 1 {
		t.Fatalf("got %d grabs, want 1", len(got))
	}
	if got[0].queueID != 10 || !reflect.DeepEqual(got[0].episodeIDs, []int{1, 2, 3}) {
		t.Fatalf("got %+v, want queue 10 covering episodes [1 2 3]", got[0])
	}
}

func TestPlanWrongGrabsEmptyQueue(t *testing.T) {
	if got := planWrongGrabs(nil, 299, map[int]bool{1: true}); len(got) != 0 {
		t.Fatalf("empty queue (e.g. Sonarr queue error) must plan nothing, got %+v", got)
	}
}
