package state

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/plex"
	"github.com/kiwi3007/rollarr/internal/sonarr"
)

// ExpectedState maps season number → sorted slice of episode numbers that
// should be downloaded and monitored.
type ExpectedState map[int][]int

// safetyBehind is how many episodes to retain at-and-below a user's
// highest-watched mark. Plex records an episode as watched at a playback
// threshold (~90%), before the user has actually finished it, so deleting at
// watched+1 can yank the file mid-watch. safetyBehind=1 keeps the watched
// episode itself.
const safetyBehind = 1

// epRef identifies one episode as (season, episode).
type epRef struct {
	Season  int
	Episode int
}

// layout is the linear episode structure of a show as known to Sonarr.
// Episodes are ordered season-major (S01E01, S01E02, …, S02E01, …) so buffer
// windows can slide across season boundaries. Specials (season 0) are excluded.
type layout struct {
	linear []epRef
	index  map[epRef]int
}

// buildLayout constructs a layout from Sonarr's episode list.
func buildLayout(episodes []sonarr.Episode) *layout {
	bySeason := make(map[int][]int)
	for _, ep := range episodes {
		if ep.SeasonNumber <= 0 || ep.EpisodeNumber <= 0 {
			continue
		}
		bySeason[ep.SeasonNumber] = append(bySeason[ep.SeasonNumber], ep.EpisodeNumber)
	}

	seasons := make([]int, 0, len(bySeason))
	for s := range bySeason {
		seasons = append(seasons, s)
	}
	sort.Ints(seasons)

	l := &layout{index: make(map[epRef]int)}
	for _, s := range seasons {
		eps := bySeason[s]
		sort.Ints(eps)
		prev := 0
		for _, e := range eps {
			if e == prev {
				continue // duplicate episode rows (multi-file)
			}
			prev = e
			ref := epRef{Season: s, Episode: e}
			l.index[ref] = len(l.linear)
			l.linear = append(l.linear, ref)
		}
	}
	return l
}

// positionIndex returns the linear index of the latest episode at or before
// (season, episode), or -1 if (season, episode) precedes the first episode.
// Handles watched episodes Sonarr doesn't know about by snapping to the
// nearest earlier known episode.
func (l *layout) positionIndex(season, episode int) int {
	if idx, ok := l.index[epRef{Season: season, Episode: episode}]; ok {
		return idx
	}
	i := sort.Search(len(l.linear), func(i int) bool {
		e := l.linear[i]
		return e.Season > season || (e.Season == season && e.Episode > episode)
	})
	return i - 1
}

// Engine computes the expected download state for a show based on user watch
// progress and the configured buffer size.
type Engine struct {
	shows    *repository.ShowRepository
	requests *repository.UserRequestRepository
	plex     *plex.Client
	plexdb   *plex.PlexDB // may be nil
	sonarr   *sonarr.Client
}

// NewEngine constructs an Engine. plexDB may be nil if the Plex SQLite path is
// not configured.
func NewEngine(
	shows *repository.ShowRepository,
	requests *repository.UserRequestRepository,
	plexClient *plex.Client,
	plexDB *plex.PlexDB,
	sonarrClient *sonarr.Client,
) *Engine {
	return &Engine{
		shows:    shows,
		requests: requests,
		plex:     plexClient,
		plexdb:   plexDB,
		sonarr:   sonarrClient,
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
//  1. Load all user_requests for the show. No requests → empty expected state
//     (the reconciler separately refuses to act on shows with no requests).
//  2. Fetch the show's episode list from Sonarr and flatten it into linear
//     (season-major) order so windows can cross season boundaries.
//  3. For each user, fetch Plex watch history (HTTP + plexdb), merge with the
//     stored high-water mark, and reduce to a single linear position.
//  4. Window per user = linear[pos-safetyBehind+1 … pos+bufferSize].
//     A user with no episodes ahead of their position (finished everything
//     Sonarr knows) contributes no window — it revives when new episodes air.
//     A user with no history seeds at their requested season's E01.
//  5. Union windows across users, then add anchors: S01E01 whenever the show
//     has any watcher, plus E01 of each request's requested season.
func (e *Engine) Compute(tvdbId int) (StateResult, error) {
	reqs, err := e.requests.FindByShow(tvdbId)
	if err != nil {
		return StateResult{}, fmt.Errorf("state.Compute(%d): load requests: %w", tvdbId, err)
	}
	if len(reqs) == 0 {
		return StateResult{Expected: ExpectedState{}, UserProgress: map[string]map[int]int{}}, nil
	}

	show, err := e.shows.FindByTVDB(tvdbId)
	if err != nil {
		return StateResult{}, fmt.Errorf("state.Compute(%d): load show: %w", tvdbId, err)
	}
	if show == nil {
		return StateResult{}, fmt.Errorf("state.Compute(%d): show not in DB", tvdbId)
	}

	// Fail safe: if Sonarr is unreachable we cannot know the episode layout, so
	// return an error rather than an empty state that would orphan everything.
	episodes, err := e.sonarr.GetEpisodes(show.SonarrId)
	if err != nil {
		return StateResult{}, fmt.Errorf("state.Compute(%d): sonarr episodes: %w", tvdbId, err)
	}
	lay := buildLayout(episodes)

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
		// Cleared on rewatch, so it cannot pin a rewatcher at the old position.
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

	if len(lay.linear) == 0 {
		// Sonarr hasn't indexed any episodes yet (fresh series add).
		log.Printf("[state] Compute tvdb=%d: sonarr has no episodes yet", tvdbId)
		return StateResult{Expected: ExpectedState{}, UserProgress: perUserHighest}, nil
	}

	include := make(map[epRef]struct{})
	addRange := func(from, to int) {
		if from < 0 {
			from = 0
		}
		if to >= len(lay.linear) {
			to = len(lay.linear) - 1
		}
		for i := from; i <= to; i++ {
			include[lay.linear[i]] = struct{}{}
		}
	}

	for _, req := range reqs {
		highest := perUserHighest[req.PlexUserID]

		if len(highest) > 0 {
			// Reduce multi-season history to a single linear position: the
			// highest (season, episode) watched.
			ps, pe := 0, 0
			for s, ep := range highest {
				if s > ps || (s == ps && ep > pe) {
					ps, pe = s, ep
				}
			}
			idx := lay.positionIndex(ps, pe)
			if idx+1 >= len(lay.linear) {
				// Finished every episode Sonarr knows — no window. The window
				// revives automatically when new episodes appear in Sonarr.
				log.Printf("[state] tvdb=%d user=%s finished at S%02dE%02d — no window", tvdbId, req.PlexUserID, ps, pe)
				continue
			}
			addRange(idx-safetyBehind+1, idx+bufferSize)
			continue
		}

		// No history at all — seed from the requested season's premiere.
		seedSeason := req.RequestedSeason
		if seedSeason < 1 {
			seedSeason = 1
		}
		startIdx, ok := lay.index[epRef{Season: seedSeason, Episode: 1}]
		if !ok {
			startIdx = 0 // requested season unknown to Sonarr — seed from the start
		}
		addRange(startIdx, startIdx+bufferSize-1)
	}

	// Anchors: S01E01 is always available while the show has any watcher, and
	// each request's requested-season premiere stays available.
	if _, ok := lay.index[epRef{Season: 1, Episode: 1}]; ok {
		include[epRef{Season: 1, Episode: 1}] = struct{}{}
	}
	for _, req := range reqs {
		s := req.RequestedSeason
		if s < 1 {
			s = 1
		}
		ref := epRef{Season: s, Episode: 1}
		if _, ok := lay.index[ref]; ok {
			include[ref] = struct{}{}
		}
	}

	result := make(ExpectedState)
	for ref := range include {
		result[ref.Season] = append(result[ref.Season], ref.Episode)
	}
	for s := range result {
		sort.Ints(result[s])
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
