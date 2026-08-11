package state

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kiwi3007/rollarr/internal/db/repository"
)

// ManualWatcher is an explicit watcher choice supplied by the admin when
// previewing/adding a show manually (overrides history auto-detection).
type ManualWatcher struct {
	PlexUserID      string // numeric Plex account ID as a string
	DisplayName     string
	RequestedSeason int
}

// WindowSegment is one per-season run of a user's buffer window.
type WindowSegment struct {
	Season int `json:"season"`
	Start  int `json:"start"`
	End    int `json:"end"`
}

// PreviewWatcher describes one watcher in a preview result.
type PreviewWatcher struct {
	PlexUserID     string          `json:"plex_user_id"`
	DisplayName    string          `json:"display_name"`
	Detected       bool            `json:"detected"` // false = manual override
	IsRewatching   bool            `json:"is_rewatching"`
	HighestSeason  int             `json:"highest_season"` // 0 = no history
	HighestEpisode int             `json:"highest_episode"`
	Segments       []WindowSegment `json:"segments"`
}

// PreviewResult is the outcome of a dry-run window computation for a show that
// is not (yet) tracked by Rollarr.
type PreviewResult struct {
	Expected    ExpectedState
	AllEpisodes map[int][]int
	Watchers    []PreviewWatcher
}

// Preview computes what the expected state would be if the show were added now,
// without writing anything to the database. Watchers are auto-detected from
// Plex history exactly like reconcile's discoverNewWatchers (requested_season
// 0 semantics); manual, if non-nil, replaces/adds one watcher seeded at the
// chosen season with rewatch detection.
func (e *Engine) Preview(tvdbId, sonarrId, bufferSize int, manual *ManualWatcher) (PreviewResult, error) {
	showKey, err := e.plex.FindShowByTVDB(tvdbId)
	if err != nil {
		showKey = "" // not in Plex — seed-only preview
	}
	return e.PreviewWithKey(tvdbId, sonarrId, showKey, bufferSize, manual)
}

// PreviewWithKey is Preview with an already-resolved Plex ratingKey. Callers
// computing previews for many shows pass the key they already hold so each
// preview skips FindShowByTVDB's full library scan. showKey "" means the show
// isn't in Plex (seed-only preview).
func (e *Engine) PreviewWithKey(tvdbId, sonarrId int, showKey string, bufferSize int, manual *ManualWatcher) (PreviewResult, error) {
	// Fail safe: a Sonarr error must abort the preview, never show an empty state.
	episodes, err := e.sonarr.GetEpisodes(sonarrId)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("state.Preview(%d): sonarr episodes: %w", tvdbId, err)
	}
	lay := buildLayout(episodes)

	reqs := e.previewRequests(tvdbId, showKey, manual)

	perUserHighest, perUserRecent := e.gatherHighest(showKey, tvdbId, reqs, e.shows.RewatchWindowDays())

	allEpisodes := make(map[int][]int)
	for _, ref := range lay.linear {
		allEpisodes[ref.Season] = append(allEpisodes[ref.Season], ref.Episode)
	}

	if len(lay.linear) == 0 {
		return PreviewResult{Expected: ExpectedState{}, AllEpisodes: allEpisodes}, nil
	}

	// A preview never persists anything, so any rewatch reset it detects is
	// discarded — the reconciler is the only writer.
	expected, windows, _ := computeWindows(lay, reqs, perUserHighest, perUserRecent, bufferSize, tvdbId)

	idToName := e.reverseUserMap()
	watchers := make([]PreviewWatcher, 0, len(reqs))
	for _, req := range reqs {
		w := PreviewWatcher{
			PlexUserID:   req.PlexUserID,
			Detected:     req.RequestedSeason == 0,
			IsRewatching: req.IsRewatching,
			DisplayName:  req.DisplayName,
			Segments:     []WindowSegment{},
		}
		if w.DisplayName == "" {
			if id, err := strconv.Atoi(req.PlexUserID); err == nil {
				w.DisplayName = idToName[id]
			}
		}
		if w.DisplayName == "" {
			w.DisplayName = req.PlexUserID
		}
		for s, ep := range perUserHighest[req.PlexUserID] {
			if s > w.HighestSeason || (s == w.HighestSeason && ep > w.HighestEpisode) {
				w.HighestSeason, w.HighestEpisode = s, ep
			}
		}
		// A user can hold more than one window: their forward buffer plus, when
		// they're partway through a rewatch of earlier episodes, a second one.
		for _, win := range windows[req.PlexUserID] {
			if win.from < 0 {
				continue
			}
			w.Segments = append(w.Segments, segmentWindow(lay, win)...)
		}
		watchers = append(watchers, w)
	}
	sort.Slice(watchers, func(i, j int) bool { return watchers[i].PlexUserID < watchers[j].PlexUserID })

	return PreviewResult{Expected: expected, AllEpisodes: allEpisodes, Watchers: watchers}, nil
}

// previewRequests builds the synthetic user_requests a manual add would create:
// one auto-discovered row (requested_season 0) per account seen in Plex history,
// plus/replaced-by the manual override seeded at its chosen season. Nothing is
// written to the database.
func (e *Engine) previewRequests(tvdbId int, showKey string, manual *ManualWatcher) []repository.UserRequest {
	now := time.Now()
	var reqs []repository.UserRequest

	if showKey != "" {
		// Mirror reconcile.discoverNewWatchers: HTTP history covers scrobbled
		// plays; plexdb also covers "Mark as Watched".
		seen := make(map[int]bool)
		if entries, err := e.plex.GetShowHistory(showKey); err == nil {
			for _, entry := range entries {
				if entry.AccountID > 0 {
					seen[entry.AccountID] = true
				}
			}
		}
		if e.plexdb != nil {
			if marked, err := e.plexdb.GetMarkedWatched(showKey, 0); err == nil {
				for _, m := range marked {
					if m.AccountID > 0 {
						seen[m.AccountID] = true
					}
				}
			}
		}
		ids := make([]int, 0, len(seen))
		for id := range seen {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			reqs = append(reqs, repository.UserRequest{
				PlexUserID:       strconv.Itoa(id),
				TVDBId:           tvdbId,
				RequestTimestamp: now,
				RequestedSeason:  0,
			})
		}
	}

	if manual != nil {
		season := manual.RequestedSeason
		if season < 1 {
			season = 1
		}
		// RequestedSeason >= 1 + a fresh timestamp makes fetchHistory discard all
		// existing history, so the window seeds at the chosen season's premiere —
		// the same state a manual add produces.
		manualReq := repository.UserRequest{
			PlexUserID:       manual.PlexUserID,
			DisplayName:      manual.DisplayName,
			TVDBId:           tvdbId,
			RequestTimestamp: now,
			RequestedSeason:  season,
			IsRewatching:     e.IsRewatch(tvdbId, showKey, manual.PlexUserID, season),
		}
		replaced := false
		for i, req := range reqs {
			if req.PlexUserID == manualReq.PlexUserID {
				reqs[i] = manualReq
				replaced = true
				break
			}
		}
		if !replaced {
			reqs = append(reqs, manualReq)
		}
	}

	return reqs
}

// IsRewatch reports whether starting at requestedSeason would be a rewatch for
// the given user: true when they have already watched a season past it.
// Same semantics as the Seerr webhook's rewatch detection.
func (e *Engine) IsRewatch(tvdbId int, showKey, plexUserID string, requestedSeason int) bool {
	if showKey == "" {
		return false
	}
	accountId, err := strconv.Atoi(plexUserID)
	if err != nil || accountId <= 0 {
		return false
	}

	highestSeason := 0
	if entries, err := e.plex.GetAccountHistory(showKey, accountId); err == nil {
		for _, entry := range entries {
			if entry.SeasonNum > highestSeason {
				highestSeason = entry.SeasonNum
			}
		}
	}
	if e.plexdb != nil {
		if marked, err := e.plexdb.GetMarkedWatched(showKey, accountId); err == nil {
			for _, m := range marked {
				if m.SeasonNum > highestSeason {
					highestSeason = m.SeasonNum
				}
			}
		}
	}

	return highestSeason > 0 && requestedSeason < highestSeason
}

// segmentWindow splits a linear window range into per-season (start, end)
// episode runs for display.
func segmentWindow(lay *layout, win userWindow) []WindowSegment {
	var segs []WindowSegment
	for i := win.from; i <= win.to && i < len(lay.linear); i++ {
		ref := lay.linear[i]
		if n := len(segs); n > 0 && segs[n-1].Season == ref.Season {
			segs[n-1].End = ref.Episode
		} else {
			segs = append(segs, WindowSegment{Season: ref.Season, Start: ref.Episode, End: ref.Episode})
		}
	}
	return segs
}

// reverseUserMap returns accountID → display name, preferring usernames over
// e-mail addresses (BuildUserMap indexes both to the same ID).
func (e *Engine) reverseUserMap() map[int]string {
	idToName := map[int]string{}
	userMap, err := e.plex.BuildUserMap()
	if err != nil {
		return idToName
	}
	for name, id := range userMap {
		if !strings.Contains(name, "@") {
			idToName[id] = name
		}
	}
	for name, id := range userMap {
		if strings.Contains(name, "@") {
			if _, exists := idToName[id]; !exists {
				idToName[id] = name
			}
		}
	}
	return idToName
}
