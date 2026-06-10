package reconcile

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/plex"
	"github.com/kiwi3007/rollarr/internal/sonarr"
	"github.com/kiwi3007/rollarr/internal/state"
)

// Search backoff: an episode that stays missing after a search is retried with
// exponentially increasing delay so unfindable episodes don't hammer indexers
// every reconcile cycle. State is in-memory; a restart resets it, which is fine.
const (
	searchBackoffBase = 30 * time.Minute
	searchBackoffMax  = 12 * time.Hour
)

type searchAttempt struct {
	attempts int
	nextAt   time.Time
}

// Reconciler computes expected vs actual episode state and drives Sonarr ops
// to converge them.
type Reconciler struct {
	shows    *repository.ShowRepository
	requests *repository.UserRequestRepository
	flags    *repository.DiscrepancyFlagRepository
	sonarr   *sonarr.Client
	plex     *plex.Client
	plexdb   *plex.PlexDB // may be nil
	engine   *state.Engine

	mu            sync.Mutex
	searchBackoff map[int]searchAttempt // sonarr episode ID → backoff state
}

// NewReconciler constructs a Reconciler.
func NewReconciler(
	shows *repository.ShowRepository,
	requests *repository.UserRequestRepository,
	flags *repository.DiscrepancyFlagRepository,
	sonarrClient *sonarr.Client,
	plexClient *plex.Client,
	plexDB *plex.PlexDB,
	engine *state.Engine,
) *Reconciler {
	return &Reconciler{
		shows:         shows,
		requests:      requests,
		flags:         flags,
		sonarr:        sonarrClient,
		plex:          plexClient,
		plexdb:        plexDB,
		engine:        engine,
		searchBackoff: make(map[int]searchAttempt),
	}
}

// searchAllowed reports whether the episode is past its backoff delay.
func (r *Reconciler) searchAllowed(episodeId int, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.searchBackoff[episodeId]
	return !ok || now.After(a.nextAt)
}

// recordSearch advances the backoff state for episodes that were just searched.
func (r *Reconciler) recordSearch(episodeIds []int, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range episodeIds {
		a := r.searchBackoff[id]
		a.attempts++
		delay := searchBackoffBase << (a.attempts - 1)
		if delay > searchBackoffMax || delay <= 0 {
			delay = searchBackoffMax
		}
		a.nextAt = now.Add(delay)
		r.searchBackoff[id] = a
	}
}

// clearSearchBackoff resets backoff for an episode that now has a file.
func (r *Reconciler) clearSearchBackoff(episodeId int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.searchBackoff, episodeId)
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

	// 1a. Auto-discover watchers who started playing without a Seerr request.
	if showKey, err := r.plex.FindShowByTVDB(tvdbId); err == nil {
		r.discoverNewWatchers(show, showKey)
	}

	// 1b. Safety guard: a show with no user requests must never be reconciled —
	// an empty expected state would orphan-delete every file. This state means
	// the request row was lost (API delete, failed webhook); flag it instead.
	reqs, err := r.requests.FindByShow(tvdbId)
	if err != nil {
		return fmt.Errorf("reconcile(%d): load requests: %w", tvdbId, err)
	}
	if len(reqs) == 0 {
		log.Printf("[reconcile] tvdb=%d has no user requests — skipping all Sonarr ops", tvdbId)
		return nil
	}

	// 1. Compute expected state and per-user progress.
	stateResult, err := r.engine.Compute(tvdbId)
	if err != nil {
		return fmt.Errorf("reconcile(%d): compute expected: %w", tvdbId, err)
	}
	expected := stateResult.Expected

	// Persist per-user watch progress so the UI can display it.
	for plexUserID, seasons := range stateResult.UserProgress {
		// Find the highest season then highest episode in that season.
		bestSeason, bestEp := 0, 0
		for season, ep := range seasons {
			if season > bestSeason || (season == bestSeason && ep > bestEp) {
				bestSeason = season
				bestEp = ep
			}
		}
		if bestSeason > 0 {
			if err := r.requests.UpdateWatchProgress(tvdbId, plexUserID, bestSeason, bestEp); err != nil {
				log.Printf("[reconcile] tvdb=%d update progress for %s: %v", tvdbId, plexUserID, err)
			}
		}
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
		id        int
		fileId    int
		monitored bool
	}
	actualWithFile := make(map[epKey]episodeInfo)
	// episodesByKey: all episodes for monitor/search lookup.
	episodesByKey := make(map[epKey]episodeInfo)
	for _, ep := range episodes {
		k := epKey{ep.SeasonNumber, ep.EpisodeNumber}
		episodesByKey[k] = episodeInfo{id: ep.ID, fileId: ep.EpisodeFileId, monitored: ep.Monitored}
		if ep.HasFile {
			actualWithFile[k] = episodeInfo{id: ep.ID, fileId: ep.EpisodeFileId, monitored: ep.Monitored}
		}
	}

	// 4. Compute missing: expected, no file, not queued.
	queuedIDs, err := r.sonarr.GetQueuedEpisodeIDs()
	if err != nil {
		log.Printf("[reconcile] tvdb=%d get queue: %v (skipping queue check)", tvdbId, err)
		queuedIDs = map[int]bool{}
	}

	now := time.Now()
	var missingIds []int
	for k := range expectedSet {
		if info, hasFile := actualWithFile[k]; hasFile {
			r.clearSearchBackoff(info.id)
			continue
		}
		// Search regardless of monitored state: a previous cycle may have
		// monitored the episode (step 7) without Sonarr successfully
		// downloading it, leaving it stuck as monitored+no-file forever.
		// Episodes already searched recently are skipped via exponential
		// backoff so unfindable releases don't hammer indexers every cycle.
		if info, ok := episodesByKey[k]; ok && !queuedIDs[info.id] && r.searchAllowed(info.id, now) {
			missingIds = append(missingIds, info.id)
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

	// 5b. Build the unmonitor list — applied AFTER file deletes (step 6) because
	// Sonarr silently sets an episode back to monitored=true when its file is
	// deleted via the API (to allow re-download). Unmonitoring before the delete
	// would be immediately undone by Sonarr.
	var toUnmonitor []int
	for k, info := range episodesByKey {
		if _, inExpected := expectedSet[k]; !inExpected {
			toUnmonitor = append(toUnmonitor, info.id)
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

	// 6b. Unmonitor out-of-window episodes now that deletes are done.
	if len(toUnmonitor) > 0 {
		if err := r.sonarr.MonitorEpisodes(toUnmonitor, false); err != nil {
			log.Printf("[reconcile] tvdb=%d unmonitor out-of-window: %v", tvdbId, err)
		}
	}

	// 7. Monitor ALL expected episodes, then search newly-entering ones.
	// Monitoring all expected each cycle ensures Sonarr state stays consistent
	// even if an episode was manually unmonitored or the flag drifted.
	var allExpectedIds []int
	for k, info := range episodesByKey {
		if _, inExpected := expectedSet[k]; inExpected {
			allExpectedIds = append(allExpectedIds, info.id)
		}
	}
	if len(allExpectedIds) > 0 {
		if err := r.sonarr.MonitorEpisodes(allExpectedIds, true); err != nil {
			log.Printf("[reconcile] tvdb=%d monitor expected episodes: %v", tvdbId, err)
		}
	}

	if len(missingIds) > 0 {
		r.recordSearch(missingIds, now)
		if err := r.sonarr.SearchEpisodes(missingIds); err != nil {
			log.Printf("[reconcile] tvdb=%d search episodes: %v", tvdbId, err)
			// Insert a discrepancy flag for each missing episode that couldn't be
			// searched — at most one open flag per episode.
			for _, epId := range missingIds {
				if exists, ferr := r.flags.HasOpenForEpisode(tvdbId, epId); ferr != nil || exists {
					continue
				}
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
		// Effective last activity = max(last watch event, newest request).
		// Including request timestamps protects a freshly (re-)added show whose
		// only watch history predates the request — without it, a re-add of a
		// show with years-old history would be pruned within the hour.
		last := show.LastActivityAt
		if reqs, err := r.requests.FindByShow(show.TVDBId); err == nil {
			for _, req := range reqs {
				if last == nil || req.RequestTimestamp.After(*last) {
					t := req.RequestTimestamp
					last = &t
				}
			}
		}
		if last == nil {
			continue
		}

		days := r.shows.EffectiveInactivityDays(show.TVDBId)
		threshold := time.Duration(days) * 24 * time.Hour
		if now.Sub(*last) < threshold {
			continue
		}

		log.Printf("[reconcile] show %d (%s) inactive — cleaning up", show.TVDBId, show.Title)

		// Clean up in Sonarr FIRST, mark inactive last: PruneInactive only
		// iterates active shows, so flipping the status before a failed cleanup
		// would strand the files forever.
		episodes, err := r.sonarr.GetEpisodes(show.SonarrId)
		if err != nil {
			log.Printf("[reconcile] get episodes for inactive show %d: %v (will retry next run)", show.TVDBId, err)
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

		if err := r.shows.UpdateStatus(show.TVDBId, "inactive"); err != nil {
			log.Printf("[reconcile] update status %d: %v", show.TVDBId, err)
		}
	}
	return nil
}

// discoverNewWatchers checks Plex history for account IDs not yet registered
// in user_requests and creates a request row for each new watcher so they get
// their own buffer window on the next ComputeExpectedState call.
func (r *Reconciler) discoverNewWatchers(show *repository.Show, showKey string) {
	// Collect account IDs from both the HTTP history API and the Plex SQLite DB.
	// The HTTP API only includes scrobbled plays; the DB also covers "Mark as
	// Watched" and is the sole source when HTTP history is empty (common on
	// managed home accounts).
	seenAccountIDs := make(map[int]bool)

	if httpEntries, err := r.plex.GetShowHistory(showKey); err == nil {
		for _, e := range httpEntries {
			if e.AccountID > 0 {
				seenAccountIDs[e.AccountID] = true
			}
		}
	}

	if r.plexdb != nil {
		if marked, err := r.plexdb.GetMarkedWatched(showKey, 0); err == nil {
			for _, m := range marked {
				if m.AccountID > 0 {
					seenAccountIDs[m.AccountID] = true
				}
			}
		}
	}

	if len(seenAccountIDs) == 0 {
		return
	}

	existing, err := r.requests.FindByShow(show.TVDBId)
	if err != nil {
		log.Printf("[reconcile] discoverNewWatchers tvdb=%d: load requests: %v", show.TVDBId, err)
		return
	}

	knownIDs := make(map[int]bool, len(existing))
	for _, req := range existing {
		if !strings.HasPrefix(req.PlexUserID, "seerr:") {
			if id, err := strconv.Atoi(req.PlexUserID); err == nil && id > 0 {
				knownIDs[id] = true
			}
		}
	}

	for accountID := range seenAccountIDs {
		if knownIDs[accountID] {
			continue
		}
		// RequestedSeason 0 marks an auto-discovered watcher: the state engine
		// skips the request-timestamp history filter for these rows so their
		// pre-existing watch progress is honoured.
		req := repository.UserRequest{
			PlexUserID:       strconv.Itoa(accountID),
			TVDBId:           show.TVDBId,
			RequestTimestamp: time.Now(),
			IsRewatching:     false,
			RequestedSeason:  0,
		}
		if err := r.requests.Upsert(req); err != nil {
			log.Printf("[reconcile] discoverNewWatchers tvdb=%d: upsert accountId=%d: %v", show.TVDBId, accountID, err)
		} else {
			log.Printf("[reconcile] discoverNewWatchers tvdb=%d: registered new watcher accountId=%d", show.TVDBId, accountID)
		}
	}

	// Drop unresolved seerr: rows whenever numeric accounts exist for this show —
	// numeric rows are the resolved form; seerr: rows are deferred placeholders.
	if len(seenAccountIDs) > 0 {
		if err := r.requests.DeleteSeerrRows(show.TVDBId); err != nil {
			log.Printf("[reconcile] discoverNewWatchers tvdb=%d: delete seerr rows: %v", show.TVDBId, err)
		}
	}
}

// updateLastActivity fetches the most recent Plex watch event for the show —
// from both the HTTP history API and the Plex SQLite DB (managed home accounts
// and "Mark as Watched" often appear only in the latter) — and advances
// last_activity_at in the DB. The value is monotonic: it never moves backwards.
func (r *Reconciler) updateLastActivity(show *repository.Show) {
	showKey, err := r.plex.FindShowByTVDB(show.TVDBId)
	if err != nil {
		return // show not in Plex yet
	}

	var latest time.Time

	if entries, err := r.plex.GetShowHistory(showKey); err == nil {
		for _, e := range entries {
			if e.ViewedAt.After(latest) {
				latest = e.ViewedAt
			}
		}
	}

	if r.plexdb != nil {
		if marked, err := r.plexdb.GetMarkedWatched(showKey, 0); err == nil {
			for _, m := range marked {
				if m.ViewedAt.After(latest) {
					latest = m.ViewedAt
				}
			}
		}
	}

	if latest.IsZero() {
		return
	}
	if show.LastActivityAt != nil && !latest.After(*show.LastActivityAt) {
		return
	}

	if err := r.shows.UpdateLastActivity(show.TVDBId, latest); err != nil {
		log.Printf("[reconcile] update last_activity_at for %d: %v", show.TVDBId, err)
	}
}
