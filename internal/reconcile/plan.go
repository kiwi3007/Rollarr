package reconcile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kiwi3007/rollarr/internal/sonarr"
	"github.com/kiwi3007/rollarr/internal/state"
)

// epKey identifies one episode as (season, episode).
type epKey struct{ season, episode int }

// managedEpisode reports whether an episode is in Rollarr's scope. Specials
// (season 0) and unnumbered episodes are outside the state engine's linear
// layout, so they are never expected; treating them as orphans would delete
// every special on disk. They are left exactly as the user configured them.
func managedEpisode(ep sonarr.Episode) bool {
	return ep.SeasonNumber > 0 && ep.EpisodeNumber > 0
}

// orphanFile is an episode file none of whose episodes are in any window.
type orphanFile struct {
	fileId int
	eps    []epKey // every managed episode the file contains, sorted
}

// planOrphans returns the files to delete. Sonarr deletes per file, and one
// file can hold several episodes (S01E01-E02). A file is an orphan only when
// none of its episodes are expected: deleting a file that still covers an
// in-window episode would just be re-searched and re-downloaded next cycle.
// Files that also contain a special are kept.
func planOrphans(episodes []sonarr.Episode, expected map[epKey]struct{}) []orphanFile {
	type fileState struct {
		eps  []epKey
		keep bool
	}
	files := make(map[int]*fileState)
	for _, ep := range episodes {
		if !ep.HasFile || ep.EpisodeFileId <= 0 {
			continue
		}
		f := files[ep.EpisodeFileId]
		if f == nil {
			f = &fileState{}
			files[ep.EpisodeFileId] = f
		}
		if !managedEpisode(ep) {
			f.keep = true
			continue
		}
		k := epKey{ep.SeasonNumber, ep.EpisodeNumber}
		f.eps = append(f.eps, k)
		if _, ok := expected[k]; ok {
			f.keep = true
		}
	}

	var out []orphanFile
	for id, f := range files {
		if f.keep || len(f.eps) == 0 {
			continue
		}
		sort.Slice(f.eps, func(i, j int) bool {
			a, b := f.eps[i], f.eps[j]
			return a.season < b.season || (a.season == b.season && a.episode < b.episode)
		})
		out = append(out, orphanFile{fileId: id, eps: f.eps})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].fileId < out[j].fileId })
	return out
}

// OrphanFileIDs returns the episode file IDs a reconcile would delete for the
// given expected state. Exposed so previews report exactly what reconcile
// does (specials kept, a multi-episode file counted once).
func OrphanFileIDs(episodes []sonarr.Episode, expected state.ExpectedState) []int {
	set := make(map[epKey]struct{})
	for season, eps := range expected {
		for _, ep := range eps {
			set[epKey{season, ep}] = struct{}{}
		}
	}
	orphans := planOrphans(episodes, set)
	ids := make([]int, len(orphans))
	for i, o := range orphans {
		ids[i] = o.fileId
	}
	return ids
}

// planUnmonitor returns managed episodes that are outside every window and
// still monitored. Episodes sharing a file with an in-window episode stay
// monitored too, so Sonarr doesn't treat a kept multi-episode file as partly
// unwanted.
func planUnmonitor(episodes []sonarr.Episode, expected map[epKey]struct{}, deleted map[int]bool) []int {
	keptFiles := make(map[int]bool)
	for _, ep := range episodes {
		if !managedEpisode(ep) || !ep.HasFile || ep.EpisodeFileId <= 0 {
			continue
		}
		if _, ok := expected[epKey{ep.SeasonNumber, ep.EpisodeNumber}]; ok {
			keptFiles[ep.EpisodeFileId] = true
		}
	}

	var ids []int
	for _, ep := range episodes {
		if !managedEpisode(ep) {
			continue
		}
		if _, ok := expected[epKey{ep.SeasonNumber, ep.EpisodeNumber}]; ok {
			continue
		}
		if ep.HasFile && keptFiles[ep.EpisodeFileId] {
			continue
		}
		// Sonarr re-monitors an episode when its file is deleted, so anything
		// just deleted must be unmonitored even if it read as unmonitored.
		if ep.Monitored || (ep.HasFile && deleted[ep.EpisodeFileId]) {
			ids = append(ids, ep.ID)
		}
	}
	sort.Ints(ids)
	return ids
}

// wrongGrabMarker is the text Sonarr puts on a queue record it refuses to
// import because the release name doesn't match the series: the grab only
// happened because the indexer tagged it with this series' ID. In practice
// that means a different show (US vs UK remake, "Archie" grabbed for
// "Archer"), so the content must not be imported.
const wrongGrabMarker = "matched to series by id"

// isWrongGrab reports whether a queue record is a download Sonarr blocked as
// matched-by-ID-only.
func isWrongGrab(rec sonarr.QueueRecord) bool {
	if rec.TrackedDownloadState != "importBlocked" {
		return false
	}
	for _, sm := range rec.StatusMessages {
		for _, m := range sm.Messages {
			if strings.Contains(strings.ToLower(m), wrongGrabMarker) {
				return true
			}
		}
	}
	return false
}

// wrongGrab is one download to remove and blocklist, and the episodes it was
// holding in the queue (freed for a fresh search once it is removed).
type wrongGrab struct {
	queueID    int
	downloadID string
	title      string
	episodeIDs []int
}

// planWrongGrabs returns the blocked wrong-show downloads of a series that
// hold at least one in-window episode. Downloads for episodes outside every
// window are left alone: Rollarr would not search them again anyway. A
// download appears once per episode in the queue; it is returned once, with
// every episode it covers, so it is removed once.
func planWrongGrabs(records []sonarr.QueueRecord, seriesID int, inWindow map[int]bool) []wrongGrab {
	byDownload := make(map[string]*wrongGrab)
	var order []string
	for _, rec := range records {
		if rec.SeriesID != seriesID || !isWrongGrab(rec) {
			continue
		}
		key := rec.DownloadID
		if key == "" {
			key = fmt.Sprintf("queue:%d", rec.ID)
		}
		g, ok := byDownload[key]
		if !ok {
			g = &wrongGrab{queueID: rec.ID, downloadID: rec.DownloadID, title: rec.Title}
			byDownload[key] = g
			order = append(order, key)
		}
		if rec.EpisodeID > 0 {
			g.episodeIDs = append(g.episodeIDs, rec.EpisodeID)
		}
	}
	var out []wrongGrab
	for _, key := range order {
		g := byDownload[key]
		for _, id := range g.episodeIDs {
			if inWindow[id] {
				sort.Ints(g.episodeIDs)
				out = append(out, *g)
				break
			}
		}
	}
	return out
}
