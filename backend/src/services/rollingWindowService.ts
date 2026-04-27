import db from '../db/database';
import { showRepository } from '../db/repositories/showRepository';
import { trackerRepository } from '../db/repositories/trackerRepository';
import { episodeRepository } from '../db/repositories/episodeRepository';
import { userRepository } from '../db/repositories/userRepository';
import { settingsRepository } from '../db/repositories/settingsRepository';
import { sonarrService, SonarrError } from './sonarrService';
import { plexService, PlexError } from './plexService';
import { createLogger } from '../utils/logger';
import { EpisodeStatus, ShowStatus } from '@rollarr/shared';
import type { PlexHistoryEntry } from '@rollarr/shared';

function isDryMode(): boolean {
  return settingsRepository.get('dry_mode') === 'true';
}

const logger = createLogger('rollingWindow');

export interface WindowResult {
  success: boolean;
  advanced: number;   // episodes advanced
  reason?: string;
}

export const rollingWindowService = {
  // Main entry: advance the window for a single show
  async advanceWindow(showId: number): Promise<WindowResult> {
    const show = showRepository.findById(showId);
    if (!show || show.status !== ShowStatus.Active) {
      return { success: true, advanced: 0 };
    }

    const trackers = trackerRepository.findActiveByShow(showId);
    if (trackers.length === 0) {
      return { success: true, advanced: 0 };
    }

    // --- Phase 1: Fetch per-user Plex history for this show/season ---
    // Global history carries accountID per entry, so each tracker's progress can be
    // updated independently. Using admin viewCount (getEpisodesForSeason) would only
    // reflect what the admin has watched, not other users.
    let historyBySeason: Map<number, Map<number, number>>;  // accountId → season → highest episode watched
    // Hoisted so Phase 2 rewatch filtering can access raw entries and showKey.
    let showHistory:   PlexHistoryEntry[] = [];
    let globalHistory: PlexHistoryEntry[] = [];
    let showKey = '';
    try {
      const plexRatingKey = await plexService.findShowRatingKey(show.tvdb_id, show.title);
      if (!plexRatingKey) {
        logger.warn(`Show ${show.title}: no Plex ratingKey found — skipping window advance`);
        return { success: false, advanced: 0, reason: 'Plex show not found' };
      }
      showKey = `/library/metadata/${plexRatingKey}`;

      // Fetch Plex history and admin viewCounts in parallel.
      // Sonarr episode data (live file IDs) is deferred until Phase 3 — only fetched
      // when the DB pre-check shows episodes actually need to be deleted or monitored.
      const [sh, gh, adminEps] = await Promise.all([
        plexService.getShowHistory(plexRatingKey).catch((err) => {
          logger.warn(`Show ${show.title}: show history failed — ${err instanceof Error ? err.message : String(err)}`);
          return [] as PlexHistoryEntry[];
        }),
        plexService.getGlobalHistory(5000).catch(() => [] as PlexHistoryEntry[]),
        plexService.getEpisodesForSeason(plexRatingKey, show.current_season).catch((err) => {
          logger.warn(`Show ${show.title}: failed to fetch admin season episodes — ${err instanceof Error ? err.message : String(err)}`);
          return [];
        }),
      ]);
      showHistory = sh;
      globalHistory = gh;

      // Merge both history sources. Per-show entries need no grandparent check.
      // Global entries use key/title matching to scope to this show and catch old ratingKeys.
      historyBySeason = new Map();
      let historyMatchCount = 0;

      const mergeEntry = (entry: PlexHistoryEntry) => {
        historyMatchCount++;
        const accountId = plexService.normalizeAccountId(entry.accountID);
        let seasons = historyBySeason.get(accountId);
        if (!seasons) { seasons = new Map(); historyBySeason.set(accountId, seasons); }
        const prev = seasons.get(entry.parentIndex) ?? 0;
        if (entry.index > prev) seasons.set(entry.parentIndex, entry.index);
      };

      for (const entry of showHistory) {
        mergeEntry(entry);
      }
      for (const entry of globalHistory) {
        const keyMatch   = entry.grandparentKey === showKey;
        const titleMatch = entry.grandparentTitle?.toLowerCase() === show.title.toLowerCase();
        if (!keyMatch && !titleMatch) continue;
        mergeEntry(entry);
      }
      logger.debug(`Show ${show.title}: scanned ${showHistory.length} show + ${globalHistory.length} global history entries, ${historyMatchCount} matched (all seasons)`);

      // Supplement with "Mark as Watched" from Plex SQLite DB — covers ALL user types.
      const markedWatched = plexService.getMarkedWatchedFromDb(plexRatingKey);
      logger.debug(`Show ${show.title}: markedWatched DB returned ${markedWatched.size} user(s)`);
      for (const [accountId, seasonMap] of markedWatched) {
        let seasons = historyBySeason.get(accountId);
        if (!seasons) { seasons = new Map(); historyBySeason.set(accountId, seasons); }
        for (const [season, epNum] of seasonMap) {
          const prev = seasons.get(season) ?? 0;
          if (epNum > prev) seasons.set(season, epNum);
        }
      }

      // Admin HTTP supplement for current_season — safety net when Plex DB not configured.
      const adminId = plexService.getAdminAccountId();
      logger.debug(`Show ${show.title}: adminId=${adminId ?? 'undefined'}, adminEps=${adminEps.length}`);
      if (adminId) {
        let adminSeasons = historyBySeason.get(adminId);
        if (!adminSeasons) { adminSeasons = new Map(); historyBySeason.set(adminId, adminSeasons); }
        let adminHighest = adminSeasons.get(show.current_season) ?? 0;
        for (const ep of adminEps) {
          if ((ep.viewCount ?? 0) > 0 && ep.index > adminHighest) adminHighest = ep.index;
        }
        if (adminHighest > 0) adminSeasons.set(show.current_season, adminHighest);
        logger.debug(`Show ${show.title}: admin viewCount supplement → S${show.current_season}E${adminHighest}`);
      }

      logger.debug(`Show ${show.title}: ${historyBySeason.size} watcher(s) found across all seasons (ratingKey=${plexRatingKey})`);
    } catch (err) {
      if (err instanceof PlexError) {
        logger.error(`Plex error for show ${show.title}`, err.message);
        return { success: false, advanced: 0, reason: err.message };
      }
      throw err;
    }

    // --- Phase 2: Update each tracker's progress from their own Plex history ---
    for (const tracker of trackers) {
      let user = userRepository.findById(tracker.user_id);
      if (!user) continue;

      if (user.plex_account_id.startsWith('seerr:')) {
        // Try to resolve the real accountId from the cached user map — avoids waiting
        // for the discovery job to see an S01E01 play before progress tracking works.
        const username = user.plex_account_id.slice('seerr:'.length);
        const resolved = plexService.resolveAccountId(username);
        if (resolved) {
          user = userRepository.upsert(resolved, username);
          logger.info(`Show ${show.title}: resolved synthetic account seerr:${username} → accountId=${resolved}`);
        } else {
          logger.debug(`Show ${show.title}: tracker for user_id=${tracker.user_id} skipped (synthetic account, not yet in user map)`);
          continue;
        }
      }

      const accountId = Number(user.plex_account_id);

      // Find the highest (season, episode) this user has watched at or above their tracker season.
      let newSeason = tracker.last_watched_season;
      let newEp     = tracker.last_watched_episode;

      if (tracker.rewatch_since) {
        // Rewatch: only plays after the request timestamp count. Filter raw history
        // arrays per-tracker rather than using the shared (unfiltered) historyBySeason.
        // Plex DB source (getMarkedWatchedFromDb) has no timestamps — skip it here.
        const sinceUnix = Math.floor(new Date(tracker.rewatch_since).getTime() / 1000);
        const rewatchSeasons = new Map<number, number>();
        for (const entry of showHistory) {
          if (plexService.normalizeAccountId(entry.accountID) !== accountId || entry.viewedAt < sinceUnix) continue;
          const prev = rewatchSeasons.get(entry.parentIndex) ?? 0;
          if (entry.index > prev) rewatchSeasons.set(entry.parentIndex, entry.index);
        }
        for (const entry of globalHistory) {
          if (plexService.normalizeAccountId(entry.accountID) !== accountId || entry.viewedAt < sinceUnix) continue;
          const keyMatch   = entry.grandparentKey === showKey;
          const titleMatch = entry.grandparentTitle?.toLowerCase() === show.title.toLowerCase();
          if (!keyMatch && !titleMatch) continue;
          const prev = rewatchSeasons.get(entry.parentIndex) ?? 0;
          if (entry.index > prev) rewatchSeasons.set(entry.parentIndex, entry.index);
        }
        for (const [season, highestEp] of rewatchSeasons) {
          if (season < tracker.last_watched_season) continue;
          if (season > newSeason || (season === newSeason && highestEp > newEp)) {
            newSeason = season;
            newEp     = highestEp;
          }
        }
      } else {
        const userSeasons = historyBySeason.get(accountId);
        if (userSeasons) {
          for (const [season, highestEp] of userSeasons) {
            if (season < tracker.last_watched_season) continue;
            if (season > newSeason || (season === newSeason && highestEp > newEp)) {
              newSeason = season;
              newEp     = highestEp;
            }
          }
        }
      }
      logger.debug(`Show ${show.title}: ${user.plex_username} (accountId=${accountId}) best watched = S${newSeason}E${newEp}, tracker was S${tracker.last_watched_season}E${tracker.last_watched_episode}`);
      if (newSeason > tracker.last_watched_season || newEp > tracker.last_watched_episode) {
        trackerRepository.updateProgress(showId, tracker.user_id, newEp, newSeason);
      }
    }

    // Reload trackers after progress update
    const updatedTrackers = trackerRepository.findActiveByShow(showId);
    if (updatedTrackers.length === 0) {
      return { success: true, advanced: 0 };
    }
    // Treat trackers on a previous season as having watched 0 episodes of the current season.
    const effectiveEp = (t: typeof updatedTrackers[0]) =>
      t.last_watched_season === show.current_season ? t.last_watched_episode : 0;
    const safeFloor = Math.min(...updatedTrackers.map(effectiveEp));

    const dbEpisodes = episodeRepository.findBySeason(showId, show.current_season);

    // Desired monitored set: E1..starter_buffer_size (always, for new watchers) +
    // each tracker's personal forward buffer.
    const starterBuffer = settingsRepository.getNumber('starter_buffer_size', 1);
    const keepEpNumbers = new Set<number>();
    for (let i = 1; i <= starterBuffer; i++) keepEpNumbers.add(i);
    for (const tracker of updatedTrackers) {
      for (let i = 1; i <= show.buffer_size; i++) {
        keepEpNumbers.add(effectiveEp(tracker) + i);
      }
    }

    // DB-only pre-check: skip the Sonarr getEpisodes call entirely when nothing has changed.
    // Monitored episodes leaving the window and Unmonitored episodes entering it are both
    // visible from DB status alone. Re-downloaded Deleted episodes (the liveFileIds safety net)
    // are only worth checking when the window is already moving.
    const dbNeedsDelete  = dbEpisodes.some(ep => !keepEpNumbers.has(ep.episode_number) && ep.status === EpisodeStatus.Monitored);
    const dbNeedsMonitor = dbEpisodes.some(ep =>  keepEpNumbers.has(ep.episode_number) && ep.status === EpisodeStatus.Unmonitored);

    if (!dbNeedsDelete && !dbNeedsMonitor) {
      return { success: true, advanced: 0 };
    }

    // Fetch live file IDs only when we know there's work to do.
    // Catches re-downloaded Deleted episodes that left the window before a file existed.
    let liveFileIds = new Map<number, number>();
    try {
      const sonarrEps = await sonarrService.getEpisodes(show.sonarr_id, show.current_season);
      liveFileIds = new Map(sonarrEps.filter(e => e.hasFile).map(e => [e.id, e.episodeFileId]));
    } catch {
      // Non-fatal: proceed without live file IDs; re-downloaded Deleted eps won't be caught this cycle
    }

    const toDelete = dbEpisodes.filter(
      (ep) =>
        !keepEpNumbers.has(ep.episode_number) &&
        (ep.status === EpisodeStatus.Monitored || liveFileIds.has(ep.sonarr_episode_id))
    );

    // Episodes to start monitoring: in a buffer window but not yet monitored
    const toMonitor = dbEpisodes.filter(
      (ep) => keepEpNumbers.has(ep.episode_number) && ep.status === EpisodeStatus.Unmonitored
    );

    if (toDelete.length === 0 && toMonitor.length === 0) {
      return { success: true, advanced: 0 };
    }

    // --- Phase 3: Sonarr operations ---
    const dryMode = isDryMode();

    // File deletes are independent — run in parallel. Monitor/search must be sequential
    // (search after monitor so Sonarr picks up the newly-watched episode list correctly).
    const fileDeleteOps: Array<Promise<void>> = [];
    for (const ep of toDelete) {
      const fileId = liveFileIds.get(ep.sonarr_episode_id);
      if (fileId) {
        if (dryMode) {
          logger.info(`[DRY MODE] Would delete file ${fileId} for S${show.current_season}E${ep.episode_number} of "${show.title}"`);
        } else {
          fileDeleteOps.push(sonarrService.deleteEpisodeFile(fileId));
        }
      }
    }

    try {
      await Promise.all(fileDeleteOps);
      if (toDelete.length > 0) {
        const deleteIds = toDelete.map((ep) => ep.sonarr_episode_id);
        await sonarrService.setEpisodesMonitored(deleteIds, false);
      }
      if (toMonitor.length > 0) {
        const monitorIds = toMonitor.map((ep) => ep.sonarr_episode_id);
        await sonarrService.setEpisodesMonitored(monitorIds, true);
        await sonarrService.episodeSearch(monitorIds);
      }
    } catch (err) {
      if (err instanceof SonarrError) {
        if (err.status === 500) {
          // Sonarr returns 500 "Expected query to return N rows but returned M" when episode IDs
          // are stale — happens when a show is removed and re-added, issuing new episode IDs.
          logger.warn(`Show ${show.title}: stale Sonarr episode IDs detected (500), resyncing...`);
          try {
            const freshEps = await sonarrService.getEpisodes(show.sonarr_id, show.current_season);
            episodeRepository.resyncSeason(showId, show.current_season, freshEps);
            logger.info(`Show ${show.title}: episode IDs resynced from Sonarr, will retry next poll`);
          } catch (syncErr) {
            logger.error(`Show ${show.title}: resync failed`, syncErr);
          }
          return { success: false, advanced: 0, reason: 'Stale episode IDs resynced — will retry next poll' };
        }
        logger.error(`Sonarr op failed for show ${show.title}, will retry next poll`, err.message);
        return { success: false, advanced: 0, reason: err.message };
      }
      throw err;
    }

    // --- Phase 4: Commit DB state (synchronous transaction) ---
    db.transaction(() => {
      for (const ep of toDelete) {
        episodeRepository.updateStatus(ep.sonarr_episode_id, EpisodeStatus.Deleted);
      }
      for (const ep of toMonitor) {
        episodeRepository.updateStatus(ep.sonarr_episode_id, EpisodeStatus.Monitored);
      }
      showRepository.update(showId, { current_window_start: safeFloor + 1 });
    })();

    logger.info(`Show ${show.title}: deleted ${toDelete.length} ep(s), monitoring ${toMonitor.length} new ep(s), window floor at ep ${safeFloor + 1}`);
    return { success: true, advanced: toDelete.length };
  },

  // Season transition: start monitoring S(n+1) once any tracker is within buffer_size of season end
  async checkSeasonTransition(showId: number): Promise<void> {
    const show = showRepository.findById(showId);
    if (!show) return;

    const trackers = trackerRepository.findActiveByShow(showId);
    if (trackers.length === 0) return;

    const dbEpisodes = episodeRepository.findBySeason(showId, show.current_season);
    const totalEps = dbEpisodes.length;
    if (totalEps === 0) return;

    // Only count trackers actually on the current season — stale trackers from previous seasons
    // would produce negative remaining values and trigger a runaway chain of season advances.
    const maxWatched = Math.max(0, ...trackers
      .filter(t => t.last_watched_season === show.current_season)
      .map(t => t.last_watched_episode));
    const remaining = totalEps - maxWatched;

    if (remaining >= show.buffer_size) return;

    const nextSeason = show.current_season + 1;
    // Check if we already have episodes for next season
    const existingNextSeason = episodeRepository.findBySeason(showId, nextSeason);
    if (existingNextSeason.length > 0) return; // already seeded

    logger.info(`Show ${show.title}: within buffer of season end, seeding S${nextSeason}`);

    let nextSeasonEpisodes;
    try {
      nextSeasonEpisodes = await sonarrService.getEpisodes(show.sonarr_id, nextSeason);
    } catch (err) {
      if (err instanceof SonarrError) {
        logger.warn(`Could not fetch S${nextSeason} episodes for ${show.title}: ${err.message}`);
        return;
      }
      throw err;
    }

    if (nextSeasonEpisodes.length === 0) {
      logger.info(`Show ${show.title}: S${nextSeason} has no episodes yet`);
      return;
    }

    // Seed only enough S(n+1) episodes to keep the combined buffer at buffer_size.
    // e.g. buffer=3, remaining=2 in S1 → seed 1 from S2. Full buffer_size seeded only
    // when the user explicitly requests S2 via webhook.
    const seedCount = show.buffer_size - remaining;
    const allIds = nextSeasonEpisodes.map((e) => e.id);
    const bufferIds = nextSeasonEpisodes
      .slice(0, seedCount)
      .map((e) => e.id);

    try {
      await sonarrService.setEpisodesMonitored(allIds, false);
      await sonarrService.setEpisodesMonitored(bufferIds, true);
      await sonarrService.episodeSearch(bufferIds);
    } catch (err) {
      if (err instanceof SonarrError) {
        logger.error(`Sonarr error seeding S${nextSeason} for ${show.title}`, err.message);
        return;
      }
      throw err;
    }

    // Commit to DB
    db.transaction(() => {
      for (const ep of nextSeasonEpisodes) {
        const isBuffer = bufferIds.includes(ep.id);
        episodeRepository.upsert({
          show_id:           showId,
          sonarr_episode_id: ep.id,
          sonarr_file_id:    ep.hasFile ? ep.episodeFileId : null,
          season:            nextSeason,
          episode_number:    ep.episodeNumber,
          status:            isBuffer ? EpisodeStatus.Monitored : EpisodeStatus.Unmonitored,
        });
      }
      showRepository.update(showId, {
        current_season:       nextSeason,
        current_window_start: 1,
      });
    })();

    logger.info(`Show ${show.title}: seeded S${nextSeason} with ${bufferIds.length} ep(s) (${remaining} remaining in S${show.current_season})`);
  },

  // Zero-tracker cleanup: unmonitor all, delete all files
  async cleanupStaleShow(showId: number): Promise<void> {
    const show = showRepository.findById(showId);
    if (!show) return;

    logger.info(`Show ${show.title}: zero active trackers, cleaning up`);
    const episodes = episodeRepository.findByShow(showId);

    // Query live file IDs from Sonarr — DB sonarr_file_id may be stale for files
    // that were downloaded after our last sync cycle.
    let liveFileIds = new Map<number, number>();
    try {
      const sonarrEps = await sonarrService.getAllEpisodes(show.sonarr_id);
      liveFileIds = new Map(sonarrEps.filter(e => e.hasFile).map(e => [e.id, e.episodeFileId]));
    } catch (err) {
      if (err instanceof SonarrError) {
        logger.warn(`Show ${show.title}: could not fetch live Sonarr file IDs, falling back to DB`, err.message);
        for (const ep of episodes) {
          if (ep.sonarr_file_id) liveFileIds.set(ep.sonarr_episode_id, ep.sonarr_file_id);
        }
      } else {
        throw err;
      }
    }

    const monitored = episodes.filter((e) => e.status === EpisodeStatus.Monitored);
    const dryMode = isDryMode();

    try {
      for (const ep of episodes) {
        if (ep.status === EpisodeStatus.Deleted) continue;
        const fileId = liveFileIds.get(ep.sonarr_episode_id);
        if (!fileId) continue;
        if (dryMode) {
          logger.info(`[DRY MODE] Would delete file ${fileId} for "${show.title}"`);
          continue;
        }
        try {
          await sonarrService.deleteEpisodeFile(fileId);
        } catch {
          // Best-effort; continue deleting others
        }
      }
      // Unmonitor all
      if (monitored.length > 0) {
        await sonarrService.setEpisodesMonitored(
          monitored.map((e) => e.sonarr_episode_id),
          false
        );
      }
    } catch (err) {
      if (err instanceof SonarrError) {
        logger.error(`Sonarr error during stale cleanup for ${show.title}`, err.message);
      }
    }

    db.transaction(() => {
      for (const ep of episodes) {
        episodeRepository.updateStatus(ep.sonarr_episode_id, EpisodeStatus.Deleted);
      }
      showRepository.update(showId, { status: ShowStatus.Stale });
    })();
  },
};
