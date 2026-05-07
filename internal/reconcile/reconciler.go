package reconcile

import (
	"fmt"
	"log"
	"time"

	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/plex"
	"github.com/kiwi3007/rollarr/internal/sonarr"
	"github.com/kiwi3007/rollarr/internal/state"
)

// Reconciler computes expected vs actual episode state and drives Sonarr ops
// to converge them.
type Reconciler struct {
	shows    *repository.ShowRepository
	requests *repository.UserRequestRepository
	flags    *repository.DiscrepancyFlagRepository
	sonarr   *sonarr.Client
	plex     *plex.Client
	engine   *state.Engine
}

// NewReconciler constructs a Reconciler.
func NewReconciler(
	shows *repository.ShowRepository,
	requests *repository.UserRequestRepository,
	flags *repository.DiscrepancyFlagRepository,
	sonarrClient *sonarr.Client,
	plexClient *plex.Client,
	engine *state.Engine,
) *Reconciler {
	return &Reconciler{
		shows:    shows,
		requests: requests,
		flags:    flags,
		sonarr:   sonarrClient,
		plex:     plexClient,
		engine:   engine,
	}
}

// ReconcileShow computes expected vs actual state for the given show and issues
// Sonarr ops to converge them.
func (r *Reconciler) ReconcileShow(tvdbId int) error {
	start := time.Now()
	log.Printf("[reconcile] tvdb=%d starting", tvdbId)

	show, err := r.shows.FindByTVDB(tvdbId)
	if err != nil {
		return fmt.Errorf("reconcile(%d): find show: %w", tvdbId, err)
	}
	if show == nil {
		return fmt.Errorf("reconcile(%d): show not found", tvdbId)
	}

	// 1. Compute expected state.
	expected, err := r.engine.ComputeExpectedState(tvdbId)
	if err != nil {
		return fmt.Errorf("reconcile(%d): compute expected: %w", tvdbId, err)
	}

	// 2. Fetch actual episodes from Sonarr.
	episodes, err := r.sonarr.GetEpisodes(show.SonarrId)
	if err != nil {
		return fmt.Errorf("reconcile(%d): get episodes: %w", tvdbId, err)
	}

	// 3. Build maps: expected set and actual-with-file set.
	// expectedSet: (season, episode) → episodeId
	type epKey struct{ season, episode int }
	expectedSet := make(map[epKey]struct{})
	for season, eps := range expected {
		for _, ep := range eps {
			expectedSet[epKey{season, ep}] = struct{}{}
		}
	}

	// Build index of actual episodes by (season, episode).
	// actualWithFile: episodes that have a file on disk.
	type episodeInfo struct {
		id     int
		fileId int
	}
	actualWithFile := make(map[epKey]episodeInfo)
	// episodesByKey: all episodes for monitor/search lookup.
	episodesByKey := make(map[epKey]episodeInfo)
	for _, ep := range episodes {
		k := epKey{ep.SeasonNumber, ep.EpisodeNumber}
		episodesByKey[k] = episodeInfo{id: ep.ID, fileId: ep.EpisodeFileId}
		if ep.HasFile {
			actualWithFile[k] = episodeInfo{id: ep.ID, fileId: ep.EpisodeFileId}
		}
	}

	// 4. Compute missing: expected but no file.
	var missingIds []int
	for k := range expectedSet {
		if _, hasFile := actualWithFile[k]; !hasFile {
			if info, ok := episodesByKey[k]; ok {
				missingIds = append(missingIds, info.id)
			}
		}
	}

	// 5. Compute orphaned: has file but not expected.
	var orphanedFileIds []int
	for k, info := range actualWithFile {
		if _, inExpected := expectedSet[k]; !inExpected {
			if info.fileId > 0 {
				orphanedFileIds = append(orphanedFileIds, info.fileId)
			}
		}
	}

	// 5b. Unmonitor episodes outside the expected window so Sonarr's own
	// scheduler doesn't download them between reconcile runs.
	var toUnmonitor []int
	for k, info := range episodesByKey {
		if _, inExpected := expectedSet[k]; !inExpected {
			toUnmonitor = append(toUnmonitor, info.id)
		}
	}
	if len(toUnmonitor) > 0 {
		if err := r.sonarr.MonitorEpisodes(toUnmonitor, false); err != nil {
			log.Printf("[reconcile] tvdb=%d unmonitor out-of-window: %v", tvdbId, err)
		}
	}

	// 6. Delete orphaned files.
	for _, fileId := range orphanedFileIds {
		if err := r.sonarr.DeleteEpisodeFile(fileId); err != nil {
			log.Printf("[reconcile] tvdb=%d delete file %d: %v", tvdbId, fileId, err)
		} else {
			log.Printf("[reconcile] tvdb=%d deleted orphaned file %d", tvdbId, fileId)
		}
	}

	// 7. Monitor + search missing episodes.
	if len(missingIds) > 0 {
		if err := r.sonarr.MonitorEpisodes(missingIds, true); err != nil {
			log.Printf("[reconcile] tvdb=%d monitor episodes: %v", tvdbId, err)
		}
		if err := r.sonarr.SearchEpisodes(missingIds); err != nil {
			log.Printf("[reconcile] tvdb=%d search episodes: %v", tvdbId, err)
			// Insert a discrepancy flag for each missing episode that couldn't be searched.
			for _, epId := range missingIds {
				flag := repository.DiscrepancyFlag{
					TVDBId:           tvdbId,
					SonarrEpisodeId:  &epId,
					IssueDescription: fmt.Sprintf("episode %d expected but search failed: %v", epId, err),
					Status:           "open",
				}
				if ferr := r.flags.Insert(flag); ferr != nil {
					log.Printf("[reconcile] tvdb=%d insert flag: %v", tvdbId, ferr)
				}
			}
		} else {
			log.Printf("[reconcile] tvdb=%d searched %d missing episodes", tvdbId, len(missingIds))
		}
	}

	// 8. Update last_activity_at from Plex history if available.
	r.updateLastActivity(show)

	log.Printf("[reconcile] tvdb=%d done in %s: %d unmonitored, %d missing searched, %d orphaned deleted",
		tvdbId, time.Since(start), len(toUnmonitor), len(missingIds), len(orphanedFileIds))
	return nil
}

// ReconcileAll iterates all active shows and reconciles each one.
func (r *Reconciler) ReconcileAll() error {
	start := time.Now()
	log.Printf("[reconcile] ReconcileAll starting")

	shows, err := r.shows.FindByStatus("active")
	if err != nil {
		return fmt.Errorf("reconcile all: find active shows: %w", err)
	}

	log.Printf("[reconcile] ReconcileAll: %d active shows", len(shows))

	var firstErr error
	for _, show := range shows {
		if err := r.ReconcileShow(show.TVDBId); err != nil {
			log.Printf("[reconcile] show %d (%s): %v", show.TVDBId, show.Title, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	log.Printf("[reconcile] ReconcileAll done in %s", time.Since(start))
	return firstErr
}

// PruneInactive marks shows as inactive when last_activity_at is older than
// the effective inactivity threshold, then deletes all their episode files and
// unmonitors all episodes.
func (r *Reconciler) PruneInactive() error {
	shows, err := r.shows.FindByStatus("active")
	if err != nil {
		return fmt.Errorf("prune inactive: find active shows: %w", err)
	}

	now := time.Now()
	for _, show := range shows {
		if show.LastActivityAt == nil {
			continue
		}
		days := r.shows.EffectiveInactivityDays(show.TVDBId)
		threshold := time.Duration(days) * 24 * time.Hour
		if now.Sub(*show.LastActivityAt) < threshold {
			continue
		}

		log.Printf("[reconcile] marking show %d (%s) as inactive", show.TVDBId, show.Title)

		// Mark inactive.
		if err := r.shows.UpdateStatus(show.TVDBId, "inactive"); err != nil {
			log.Printf("[reconcile] update status %d: %v", show.TVDBId, err)
			continue
		}

		// Delete all episode files and unmonitor.
		episodes, err := r.sonarr.GetEpisodes(show.SonarrId)
		if err != nil {
			log.Printf("[reconcile] get episodes for inactive show %d: %v", show.TVDBId, err)
			continue
		}

		var allIds []int
		for _, ep := range episodes {
			allIds = append(allIds, ep.ID)
			if ep.HasFile && ep.EpisodeFileId > 0 {
				if err := r.sonarr.DeleteEpisodeFile(ep.EpisodeFileId); err != nil {
					log.Printf("[reconcile] delete file %d for show %d: %v", ep.EpisodeFileId, show.TVDBId, err)
				}
			}
		}

		if len(allIds) > 0 {
			if err := r.sonarr.MonitorEpisodes(allIds, false); err != nil {
				log.Printf("[reconcile] unmonitor all for show %d: %v", show.TVDBId, err)
			}
		}
	}
	return nil
}

// updateLastActivity fetches the most recent Plex watch event for the show and
// updates last_activity_at in the DB.
func (r *Reconciler) updateLastActivity(show *repository.Show) {
	showKey, err := r.plex.FindShowByTVDB(show.TVDBId)
	if err != nil {
		return // show not in Plex yet
	}

	entries, err := r.plex.GetShowHistory(showKey)
	if err != nil || len(entries) == 0 {
		return
	}

	// Find the most recent entry.
	var latest time.Time
	for _, e := range entries {
		if e.ViewedAt.After(latest) {
			latest = e.ViewedAt
		}
	}

	if latest.IsZero() {
		return
	}

	if err := r.shows.UpdateLastActivity(show.TVDBId, latest); err != nil {
		log.Printf("[reconcile] update last_activity_at for %d: %v", show.TVDBId, err)
	}
}
