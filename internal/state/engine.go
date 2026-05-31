package state

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/plex"
)

// ExpectedState maps season number → sorted slice of episode numbers that
// should be downloaded and monitored.
type ExpectedState map[int][]int

// Interval is an inclusive range of episode numbers within a single season.
type Interval struct {
	Start int
	End   int
}

// safetyBehind is how many episodes to retain *below* a user's highest-watched
// mark. Plex records an episode as watched at a playback threshold (~90%),
// before the user has actually finished it, so deleting at watched+1 can yank
// the file mid-watch. Keeping this many behind the mark guards against that.
const safetyBehind = 1

// Engine computes the expected download state for a show based on user watch
// progress and the configured buffer size.
type Engine struct {
	shows    *repository.ShowRepository
	requests *repository.UserRequestRepository
	plex     *plex.Client
	plexdb   *plex.PlexDB // may be nil
}

// NewEngine constructs an Engine. plexDB may be nil if the Plex SQLite path is
// not configured.
func NewEngine(
	shows *repository.ShowRepository,
	requests *repository.UserRequestRepository,
	plexClient *plex.Client,
	plexDB *plex.PlexDB,
) *Engine {
	return &Engine{
		shows:    shows,
		requests: requests,
		plex:     plexClient,
		plexdb:   plexDB,
	}
}

// StateResult holds the output of a state computation.
type StateResult struct {
	Expected     ExpectedState
	UserProgress map[string]map[int]int // plexUserID → season → highestWatchedEp
}

// ComputeExpectedState returns the set of episodes that should exist on disk
// for the show with the given TVDB ID. It is a convenience wrapper around
// Compute that discards the per-user progress map.
func (e *Engine) ComputeExpectedState(tvdbId int) (ExpectedState, error) {
	result, err := e.Compute(tvdbId)
	if err != nil {
		return nil, err
	}
	return result.Expected, nil
}

// Compute returns both the expected episode set and per-user watch progress.
//
// Algorithm:
//  1. Load all user_requests for the show.
//  2. Determine effective buffer size.
//  3. For each user, fetch Plex watch history (filtered by is_rewatching).
//  4. Build season→highestWatchedEp map per user.
//  5. Compute interval [watched+1, watched+bufferSize] per season per user.
//  6. Union intervals per season across all users.
//  7. Append exceptions: S01E01 always; E01 of every season with any request.
//  8. Return deduplicated, sorted map.
func (e *Engine) Compute(tvdbId int) (StateResult, error) {
	reqs, err := e.requests.FindByShow(tvdbId)
	if err != nil {
		return StateResult{}, fmt.Errorf("state.Compute(%d): load requests: %w", tvdbId, err)
	}

	bufferSize := e.shows.EffectiveBufferSize(tvdbId)

	// Find the Plex ratingKey for this show (needed to query history).
	showKey, err := e.plex.FindShowByTVDB(tvdbId)
	if err != nil {
		// If the show isn't in Plex yet, we can only seed the premiere episodes.
		showKey = ""
	}

	// perUserHighest: userID → season → highestWatchedEpisode
	perUserHighest := make(map[string]map[int]int)

	for _, req := range reqs {
		highest := make(map[int]int)

		if showKey != "" {
			entries, err := e.fetchHistory(showKey, tvdbId, req)
			if err != nil {
				// Non-fatal: skip this user's history.
				entries = nil
			}
			for _, entry := range entries {
				if entry.SeasonNum <= 0 || entry.EpisodeNum <= 0 {
					continue
				}
				if entry.EpisodeNum > highest[entry.SeasonNum] {
					highest[entry.SeasonNum] = entry.EpisodeNum
				}
			}
		}

		// Merge with stored high-water mark so transient plexdb fluctuations can
		// never shrink the window below what we've already recorded as watched.
		if req.LastWatchedSeason != nil && req.LastWatchedEpisode != nil &&
			*req.LastWatchedSeason > 0 && *req.LastWatchedEpisode > 0 {
			s, ep := *req.LastWatchedSeason, *req.LastWatchedEpisode
			if highest[s] < ep {
				log.Printf("[state] tvdb=%d user=%s: live S%02dE%02d < stored S%02dE%02d, using stored as floor",
					tvdbId, req.PlexUserID, s, highest[s], s, ep)
				highest[s] = ep
			}
		}

		perUserHighest[req.PlexUserID] = highest
		log.Printf("[state] tvdb=%d user=%s highest=%v", tvdbId, req.PlexUserID, highest)
	}

	// Build intervals per season across all users.
	// intervalsBySeason: season → []Interval
	intervalsBySeason := make(map[int][]Interval)

	for _, req := range reqs {
		highest := perUserHighest[req.PlexUserID]
		for season, ep := range highest {
			interval := Interval{
				Start: ep + 1 - safetyBehind,
				End:   ep + bufferSize,
			}
			if interval.Start < 1 {
				interval.Start = 1
			}
			intervalsBySeason[season] = append(intervalsBySeason[season], interval)
		}

		// No live watch history — either a genuine first-watch or a transient
		// Plex read failure. Prefer stored DB progress over the destructive seed
		// path so a momentary plexdb lock doesn't orphan all in-progress episodes.
		if len(highest) == 0 {
			if req.LastWatchedSeason != nil && req.LastWatchedEpisode != nil &&
				*req.LastWatchedSeason > 0 && *req.LastWatchedEpisode > 0 {
				s, ep := *req.LastWatchedSeason, *req.LastWatchedEpisode
				interval := Interval{
					Start: ep + 1 - safetyBehind,
					End:   ep + bufferSize,
				}
				if interval.Start < 1 {
					interval.Start = 1
				}
				intervalsBySeason[s] = append(intervalsBySeason[s], interval)
				log.Printf("[state] tvdb=%d user=%s: live history empty, using stored progress S%02dE%02d as fallback",
					tvdbId, req.PlexUserID, s, ep)
			} else {
				// Truly no history — seed from the requested season's E01.
				seedSeason := req.RequestedSeason
				if seedSeason < 1 {
					seedSeason = 1
				}
				interval := Interval{Start: 1, End: bufferSize}
				intervalsBySeason[seedSeason] = append(intervalsBySeason[seedSeason], interval)
			}
		}
	}

	// Union intervals per season.
	result := make(ExpectedState)
	for season, intervals := range intervalsBySeason {
		merged := unionIntervals(intervals)
		var eps []int
		for _, iv := range merged {
			for ep := iv.Start; ep <= iv.End; ep++ {
				if ep > 0 {
					eps = append(eps, ep)
				}
			}
		}
		result[season] = eps
	}

	// E01 of each user's requested season is always included.
	for _, req := range reqs {
		s := req.RequestedSeason
		if s < 1 {
			s = 1
		}
		result[s] = addEpisode(result[s], 1)
	}

	// E01 of every season with watch history is always kept.
	seasonsWithRequests := collectSeasonsWithRequests(reqs, perUserHighest, bufferSize)
	for season := range seasonsWithRequests {
		result[season] = addEpisode(result[season], 1)
	}

	// Deduplicate and sort each season's episode list.
	for season, eps := range result {
		result[season] = dedupSorted(eps)
	}

	log.Printf("[state] Compute tvdb=%d result: %v", tvdbId, result)
	return StateResult{Expected: result, UserProgress: perUserHighest}, nil
}

// fetchHistory retrieves watch history for a single user from both the Plex
// HTTP API (playback history) and the Plex SQLite DB (Mark as Watched).
// Results are filtered to the requesting user's account and, when rewatching,
// to entries after the request timestamp.
func (e *Engine) fetchHistory(showKey string, tvdbId int, req repository.UserRequest) ([]plex.WatchHistoryEntry, error) {
	accountId := 0
	if strings.HasPrefix(req.PlexUserID, "seerr:") {
		// User wasn't resolved at webhook time. Re-attempt resolution now so
		// that accounts added to Plex after the original request self-heal.
		username := strings.TrimPrefix(req.PlexUserID, "seerr:")
		if userMap, err := e.plex.BuildUserMap(); err == nil {
			accountId = userMap[username]
		}
	} else {
		accountId, _ = strconv.Atoi(req.PlexUserID)
	}

	log.Printf("[state] fetchHistory tvdb=%d user=%s accountId=%d rewatching=%v", tvdbId, req.PlexUserID, accountId, req.IsRewatching)

	var entries []plex.WatchHistoryEntry
	var err error
	var knownIDs map[int]bool // populated only for unresolved (seerr:) users

	if accountId > 0 {
		entries, err = e.plex.GetAccountHistory(showKey, accountId)
	} else {
		// Account still unresolvable — fetch all-accounts history and filter to
		// entries whose accountID does not match any other known user, so one
		// user's progress doesn't contaminate another's window.
		all, fetchErr := e.plex.GetShowHistory(showKey)
		if fetchErr != nil {
			return nil, fetchErr
		}
		knownIDs = e.knownAccountIDs()
		for _, entry := range all {
			if entry.AccountID == 0 || !knownIDs[entry.AccountID] {
				entries = append(entries, entry)
			}
		}
		err = nil
	}
	if err != nil {
		return nil, err
	}
	log.Printf("[state] fetchHistory tvdb=%d plex HTTP entries=%d", tvdbId, len(entries))

	// Merge "Mark as Watched" entries from the Plex SQLite DB. These are
	// episodes marked directly in Plex without reaching the playback threshold.
	// For unresolved (seerr:) users, apply the same knownIDs filter used for
	// HTTP history — otherwise all accounts' plexdb rows bleed into this user.
	if e.plexdb != nil {
		marked, dbErr := e.plexdb.GetMarkedWatched(showKey, accountId)
		if dbErr != nil {
			log.Printf("[state] fetchHistory tvdb=%d plexdb error: %v", tvdbId, dbErr)
		} else {
			log.Printf("[state] fetchHistory tvdb=%d plexdb marked-watched entries=%d", tvdbId, len(marked))
			for _, mw := range marked {
				if knownIDs != nil && knownIDs[mw.AccountID] {
					continue // exclude other users' plexdb rows for seerr: fallback
				}
				entries = append(entries, plex.WatchHistoryEntry{
					AccountID:  mw.AccountID,
					SeasonNum:  mw.SeasonNum,
					EpisodeNum: mw.EpisodeNum,
					ViewedAt:   mw.ViewedAt,
				})
			}
		}
	} else {
		log.Printf("[state] fetchHistory tvdb=%d plexdb=nil (not configured)", tvdbId)
	}

	// For any Seerr-initiated request (RequestedSeason > 0), filter to entries
	// viewed after the request timestamp. This prevents pre-request watch history
	// in other seasons (e.g. previously-watched S6 when requesting S8) from
	// creating spurious buffer windows and triggering download/delete loops.
	// For rewatches this also applies, using the rewatch timestamp as the baseline.
	// Auto-discovered watchers (RequestedSeason == 0) skip this filter so their
	// existing watch progress is not discarded.
	if req.RequestedSeason > 0 {
		filtered := entries[:0]
		for _, entry := range entries {
			if entry.ViewedAt.After(req.RequestTimestamp) {
				filtered = append(filtered, entry)
			}
		}
		label := "timestamp"
		if req.IsRewatching {
			label = "rewatch"
		}
		log.Printf("[state] fetchHistory tvdb=%d %s filter: %d → %d entries", tvdbId, label, len(entries), len(filtered))
		return filtered, nil
	}

	return entries, nil
}

// knownAccountIDs returns the set of numeric Plex account IDs currently stored
// in user_requests. Used to exclude other users' history when an unresolved
// (seerr:) user's entries can't be isolated by account ID.
func (e *Engine) knownAccountIDs() map[int]bool {
	userMap, err := e.plex.BuildUserMap()
	if err != nil {
		return map[int]bool{}
	}
	ids := make(map[int]bool, len(userMap))
	for _, id := range userMap {
		ids[id] = true
	}
	return ids
}

// collectSeasonsWithRequests returns the set of season numbers that are
// relevant to at least one user request.
func collectSeasonsWithRequests(
	reqs []repository.UserRequest,
	perUserHighest map[string]map[int]int,
	bufferSize int,
) map[int]struct{} {
	seasons := make(map[int]struct{})
	for _, req := range reqs {
		highest := perUserHighest[req.PlexUserID]
		for season := range highest {
			seasons[season] = struct{}{}
		}
		// Always include season 1 for any active request.
		seasons[1] = struct{}{}
		_ = bufferSize
	}
	return seasons
}

// unionIntervals merges a slice of intervals into the minimal covering set.
// Intervals are sorted by Start, then overlapping/adjacent intervals are merged.
func unionIntervals(intervals []Interval) []Interval {
	if len(intervals) == 0 {
		return nil
	}

	sort.Slice(intervals, func(i, j int) bool {
		return intervals[i].Start < intervals[j].Start
	})

	merged := []Interval{intervals[0]}
	for _, iv := range intervals[1:] {
		last := &merged[len(merged)-1]
		if iv.Start <= last.End+1 {
			// Overlapping or adjacent — extend.
			if iv.End > last.End {
				last.End = iv.End
			}
		} else {
			merged = append(merged, iv)
		}
	}
	return merged
}

// addEpisode appends ep to eps if it is not already present.
func addEpisode(eps []int, ep int) []int {
	for _, e := range eps {
		if e == ep {
			return eps
		}
	}
	return append(eps, ep)
}

// dedupSorted returns a sorted, deduplicated copy of eps.
func dedupSorted(eps []int) []int {
	if len(eps) == 0 {
		return nil
	}
	sort.Ints(eps)
	out := eps[:1]
	for i := 1; i < len(eps); i++ {
		if eps[i] != eps[i-1] {
			out = append(out, eps[i])
		}
	}
	return out
}
