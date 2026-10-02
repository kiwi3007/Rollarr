package reconcile

import (
	"fmt"
	"log"
	"time"

	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/sonarr"
)

// epAfter reports whether (s1, e1) comes after (s2, e2) in season-major order.
func epAfter(s1, e1, s2, e2 int) bool {
	return s1 > s2 || (s1 == s2 && e1 > e2)
}

// aired reports whether a regular (non-special) episode has aired by now.
func aired(ep sonarr.Episode, now time.Time) bool {
	return ep.SeasonNumber > 0 && ep.EpisodeNumber > 0 &&
		ep.AirDateUtc != nil && !ep.AirDateUtc.After(now)
}

// lastAiredEpisode returns the latest aired regular episode, or ok=false when
// nothing has aired.
func lastAiredEpisode(episodes []sonarr.Episode, now time.Time) (season, episode int, ok bool) {
	for _, ep := range episodes {
		if aired(ep, now) && (!ok || epAfter(ep.SeasonNumber, ep.EpisodeNumber, season, episode)) {
			season, episode, ok = ep.SeasonNumber, ep.EpisodeNumber, true
		}
	}
	return season, episode, ok
}

// caughtUpMarker returns the revival marker for a show being pruned: the last
// aired episode, but only when at least one watcher's stored progress has
// reached it. A show everyone abandoned mid-way gets no marker and stays
// pruned when new seasons air.
func caughtUpMarker(episodes []sonarr.Episode, reqs []repository.UserRequest, now time.Time) (season, episode int, ok bool) {
	ls, le, any := lastAiredEpisode(episodes, now)
	if !any {
		return 0, 0, false
	}
	for _, req := range reqs {
		if req.LastWatchedSeason == nil || req.LastWatchedEpisode == nil {
			continue
		}
		if !epAfter(ls, le, *req.LastWatchedSeason, *req.LastWatchedEpisode) {
			return ls, le, true
		}
	}
	return 0, 0, false
}

// nextEpisodeAiredAt returns, across all watchers, the latest air date of the
// next aired episode after each watcher's stored progress. A caught-up watcher
// whose next episode just aired yields a recent time; a watcher who abandoned
// mid-season yields an old one. Used as an inactivity signal so new episodes
// get a full threshold before being pruned.
func nextEpisodeAiredAt(episodes []sonarr.Episode, reqs []repository.UserRequest, now time.Time) *time.Time {
	var newest *time.Time
	for _, req := range reqs {
		if req.LastWatchedSeason == nil || req.LastWatchedEpisode == nil {
			continue
		}
		ps, pe := *req.LastWatchedSeason, *req.LastWatchedEpisode
		var next *sonarr.Episode
		for i := range episodes {
			ep := &episodes[i]
			if !aired(*ep, now) || !epAfter(ep.SeasonNumber, ep.EpisodeNumber, ps, pe) {
				continue
			}
			if next == nil || epAfter(next.SeasonNumber, next.EpisodeNumber, ep.SeasonNumber, ep.EpisodeNumber) {
				next = ep
			}
		}
		if next != nil && (newest == nil || next.AirDateUtc.After(*newest)) {
			newest = next.AirDateUtc
		}
	}
	return newest
}

// airedAfter reports whether any episode after (season, episode) has aired.
func airedAfter(episodes []sonarr.Episode, season, episode int, now time.Time) bool {
	for _, ep := range episodes {
		if aired(ep, now) && epAfter(ep.SeasonNumber, ep.EpisodeNumber, season, episode) {
			return true
		}
	}
	return false
}

// ReviveInactive reactivates inactivity-pruned shows whose caught-up watchers
// have new aired episodes waiting. The inactivity clock restarts now, so the
// watcher gets the full threshold to start the new episodes.
func (r *Reconciler) ReviveInactive() {
	shows, err := r.shows.FindByStatus("inactive")
	if err != nil {
		log.Printf("[reconcile] revive: find inactive shows: %v", err)
		return
	}

	now := time.Now()
	for _, show := range shows {
		if show.Unmanaged || show.ReviveAfterSeason == nil || show.ReviveAfterEpisode == nil {
			continue
		}
		episodes, err := r.sonarr.GetEpisodes(show.SonarrId)
		if err != nil {
			log.Printf("[reconcile] revive tvdb=%d: get episodes: %v", show.TVDBId, err)
			continue
		}
		ms, me := *show.ReviveAfterSeason, *show.ReviveAfterEpisode
		if !airedAfter(episodes, ms, me, now) {
			continue
		}

		log.Printf("[reconcile] tvdb=%d (%s): new episodes aired after S%02dE%02d — reactivating", show.TVDBId, show.Title, ms, me)
		if err := r.shows.UpdateLastActivity(show.TVDBId, now); err != nil {
			log.Printf("[reconcile] revive tvdb=%d: update activity: %v", show.TVDBId, err)
			continue
		}
		if err := r.shows.UpdateStatus(show.TVDBId, "active"); err != nil {
			log.Printf("[reconcile] revive tvdb=%d: update status: %v", show.TVDBId, err)
			continue
		}
		if err := r.shows.SetReviveMarker(show.TVDBId, nil, nil); err != nil {
			log.Printf("[reconcile] revive tvdb=%d: clear marker: %v", show.TVDBId, err)
		}
		r.logEvent(show.TVDBId, "reactivated", fmt.Sprintf("new episodes aired after S%02dE%02d", ms, me))

		if err := r.ReconcileShow(show.TVDBId); err != nil {
			log.Printf("[reconcile] revive tvdb=%d: reconcile: %v", show.TVDBId, err)
		}
	}
}
