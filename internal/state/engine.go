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

// ComputeExpectedState returns the set of episodes that should exist on disk
// for the show with the given TVDB ID.
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
func (e *Engine) ComputeExpectedState(tvdbId int) (ExpectedState, error) {
	reqs, err := e.requests.FindByShow(tvdbId)
	if err != nil {
		return nil, fmt.Errorf("state.ComputeExpectedState(%d): load requests: %w", tvdbId, err)
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
				Start: ep + 1,
				End:   ep + bufferSize,
			}
			if interval.Start < 1 {
				interval.Start = 1
			}
			intervalsBySeason[season] = append(intervalsBySeason[season], interval)
		}

		// If user has no history for a season that was requested, seed from E01.
		if len(highest) == 0 {
			// Start from S01E01 if user has made a request but watched nothing.
			interval := Interval{Start: 1, End: bufferSize}
			intervalsBySeason[1] = append(intervalsBySeason[1], interval)
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

	// Exceptions: S01E01 is always included.
	result[1] = addEpisode(result[1], 1)

	// E01 of every season that has any active request.
	seasonsWithRequests := collectSeasonsWithRequests(reqs, perUserHighest, bufferSize)
	for season := range seasonsWithRequests {
		result[season] = addEpisode(result[season], 1)
	}

	// Deduplicate and sort each season's episode list.
	for season, eps := range result {
		result[season] = dedupSorted(eps)
	}

	log.Printf("[state] ComputeExpectedState tvdb=%d result: %v", tvdbId, result)
	return result, nil
}

// fetchHistory retrieves watch history for a single user from both the Plex
// HTTP API (playback history) and the Plex SQLite DB (Mark as Watched).
// Results are filtered to the requesting user's account and, when rewatching,
// to entries after the request timestamp.
func (e *Engine) fetchHistory(showKey string, tvdbId int, req repository.UserRequest) ([]plex.WatchHistoryEntry, error) {
	// Parse account ID from PlexUserID. "seerr:username" prefixes indicate the
	// user wasn't found in Plex /accounts; fall back to all-accounts history.
	accountId := 0
	if !strings.HasPrefix(req.PlexUserID, "seerr:") {
		accountId, _ = strconv.Atoi(req.PlexUserID)
	}

	log.Printf("[state] fetchHistory tvdb=%d user=%s accountId=%d rewatching=%v", tvdbId, req.PlexUserID, accountId, req.IsRewatching)

	var entries []plex.WatchHistoryEntry
	var err error

	if accountId > 0 {
		entries, err = e.plex.GetAccountHistory(showKey, accountId)
	} else {
		entries, err = e.plex.GetShowHistory(showKey)
	}
	if err != nil {
		return nil, err
	}
	log.Printf("[state] fetchHistory tvdb=%d plex HTTP entries=%d", tvdbId, len(entries))

	// Merge "Mark as Watched" entries from the Plex SQLite DB. These are
	// episodes marked directly in Plex without reaching the playback threshold.
	if e.plexdb != nil {
		marked, dbErr := e.plexdb.GetMarkedWatched(showKey, accountId)
		if dbErr != nil {
			log.Printf("[state] fetchHistory tvdb=%d plexdb error: %v", tvdbId, dbErr)
		} else {
			log.Printf("[state] fetchHistory tvdb=%d plexdb marked-watched entries=%d", tvdbId, len(marked))
			for _, mw := range marked {
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

	if !req.IsRewatching {
		return entries, nil
	}

	// Filter to entries viewed after the rewatch request was created.
	filtered := entries[:0]
	for _, entry := range entries {
		if entry.ViewedAt.After(req.RequestTimestamp) {
			filtered = append(filtered, entry)
		}
	}
	log.Printf("[state] fetchHistory tvdb=%d rewatch filter: %d → %d entries", tvdbId, len(entries), len(filtered))
	return filtered, nil
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
