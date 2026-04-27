import db from '../db/database';
import { showRepository } from '../db/repositories/showRepository';
import { userRepository } from '../db/repositories/userRepository';
import { trackerRepository } from '../db/repositories/trackerRepository';
import { episodeRepository } from '../db/repositories/episodeRepository';
import { settingsRepository } from '../db/repositories/settingsRepository';
import { sonarrService, SonarrError } from '../services/sonarrService';
import { plexService, PlexError } from '../services/plexService';
import { rollingWindowService } from '../services/rollingWindowService';
import { createLogger } from '../utils/logger';
import { EpisodeStatus, ShowStatus } from '@rollarr/shared';
import type { SonarrSeries } from '@rollarr/shared';

const logger = createLogger('discovery');

// Polls the Sonarr queue at increasing intervals and removes any items for
// non-buffer episodes. Runs fire-and-forget after bootstrap to handle Sonarr's
// post-add search firing before Rollarr can unmonitor non-buffer episodes.
async function pruneNonBufferDownloads(
  sonarrId: number,
  nonBufferEpisodeIds: Set<number>,
  title: string
): Promise<void> {
  // Check at 5s, 20s, 45s, 90s after bootstrap completes
  const delays = [5_000, 15_000, 25_000, 45_000];
  const nonBufferIdList = Array.from(nonBufferEpisodeIds);

  for (const delay of delays) {
    await new Promise((r) => setTimeout(r, delay));
    try {
      // Re-enforce unmonitored state — Sonarr's own series-add search can race and
      // re-monitor non-buffer episodes after our initial setEpisodesMonitored call
      if (nonBufferIdList.length > 0) {
        await sonarrService.setEpisodesMonitored(nonBufferIdList, false).catch(() => {});
      }

      const queue = await sonarrService.getQueueForSeries(sonarrId);
      for (const item of queue) {
        if (item.episodeId != null && nonBufferEpisodeIds.has(item.episodeId)) {
          await sonarrService.removeFromQueue(item.id).catch(() => {});
          logger.info(`Pruned non-buffer download "${item.title ?? item.id}" for "${title}"`);
        }
      }
    } catch {
      // Best-effort; stop silently if Sonarr is unreachable
    }
  }
}

// Handles all Sonarr operations for a given series: unmonitor all, monitor buffer,
// cancel queued downloads, delete pre-imported files, search buffer.
// Called for both new bootstraps and stale show reactivations.
async function setupSonarrBuffer(
  sonarrSeries: SonarrSeries,
  showId: number,
  bufferSize: number,
  lastWatched: number = 0
): Promise<boolean> {
  const title = sonarrSeries.title;

  // Sonarr populates episode metadata asynchronously after series add — retry until
  // the list is non-empty. Delays match the TVDB refresh window (typically 3–10s).
  let s01Episodes: import('@rollarr/shared').SonarrEpisode[] = [];
  const EP_RETRY_DELAYS = [3_000, 5_000, 8_000, 12_000];
  for (let attempt = 0; attempt <= EP_RETRY_DELAYS.length; attempt++) {
    try {
      s01Episodes = await sonarrService.getEpisodes(sonarrSeries.id, 1);
    } catch (err) {
      if (err instanceof SonarrError) {
        logger.error(`Failed to get S01 episodes for ${title}`, err.message);
        return false;
      }
      throw err;
    }
    if (s01Episodes.length > 0) break;
    if (attempt < EP_RETRY_DELAYS.length) {
      logger.info(`${title}: S01 episode list empty (attempt ${attempt + 1}/${EP_RETRY_DELAYS.length + 1}), retrying in ${EP_RETRY_DELAYS[attempt]}ms`);
      await new Promise((r) => setTimeout(r, EP_RETRY_DELAYS[attempt]));
    }
  }

  if (s01Episodes.length === 0) {
    logger.warn(`No S01 episodes found for ${title} after ${EP_RETRY_DELAYS.length + 1} attempts`);
    return false;
  }

  const maxS01Ep = Math.max(...s01Episodes.map((e) => e.episodeNumber));

  // Plex may use absolute or split-season numbering that doesn't match TVDB/Sonarr.
  // Clamp to the actual S1 episode count so the buffer window doesn't land beyond S1.
  if (lastWatched > maxS01Ep) {
    logger.warn(`${title}: Plex reported lastWatched=E${lastWatched} but Sonarr S1 only has ${maxS01Ep} eps — possible Plex/TVDB numbering mismatch. Clamping to E${maxS01Ep}.`);
    lastWatched = maxS01Ep;
  }

  // E1..starter_buffer_size always kept for new watchers; forward buffer starts at lastWatched+1
  const starterBuffer = settingsRepository.getNumber('starter_buffer_size', 1);
  const bufferEpNumbers = new Set<number>();
  for (let i = 1; i <= starterBuffer; i++) bufferEpNumbers.add(i);
  for (let i = 1; i <= bufferSize; i++) {
    bufferEpNumbers.add(lastWatched + i);
  }

  const allIds        = s01Episodes.map((e) => e.id);
  const bufferIds     = s01Episodes.filter((e) => bufferEpNumbers.has(e.episodeNumber)).map((e) => e.id);
  const outsideBuffer = s01Episodes.filter((e) => !bufferEpNumbers.has(e.episodeNumber));
  const dryMode               = settingsRepository.get('dry_mode') === 'true';
  const cancelQueuedDownloads = settingsRepository.get('cancel_queued_downloads') === 'true';

  try {
    await sonarrService.setEpisodesMonitored(allIds, false);
    await sonarrService.setEpisodesMonitored(bufferIds, true);

    if (cancelQueuedDownloads) {
      // Cancel whatever is already in the queue right now
      const queue = await sonarrService.getQueueForSeries(sonarrSeries.id);
      for (const item of queue) {
        try {
          await sonarrService.removeFromQueue(item.id);
          logger.info(`Cancelled queued download "${item.title ?? item.id}" for "${title}"`);
        } catch (err) {
          logger.error(`Failed to cancel queue item ${item.id} for "${title}"`, err);
        }
      }
      // Sonarr's post-add search fires after its own disk scan completes — typically
      // 2-10s after the series is added, which is before Rollarr can unmonitor episodes.
      // Those downloads race into the queue AFTER our initial cancel above, so we keep
      // pruning non-buffer items in the background for the next minute.
      const nonBufferIds = new Set(outsideBuffer.map((e) => e.id));
      pruneNonBufferDownloads(sonarrSeries.id, nonBufferIds, title).catch(() => {});
    }

    for (const ep of outsideBuffer) {
      if (ep.hasFile && ep.episodeFileId) {
        if (dryMode) {
          logger.info(`[DRY MODE] Would delete pre-downloaded file for S01E${ep.episodeNumber} of "${title}"`);
        } else {
          try {
            await sonarrService.deleteEpisodeFile(ep.episodeFileId);
            logger.info(`Deleted pre-downloaded S01E${ep.episodeNumber} of "${title}"`);
          } catch {
            // Non-fatal
          }
        }
      }
    }

    await sonarrService.episodeSearch(bufferIds);
  } catch (err) {
    if (err instanceof SonarrError) {
      logger.error(`Sonarr setup failed for ${title}`, err.message);
      return false;
    }
    throw err;
  }

  // Sync episode rows to DB
  db.transaction(() => {
    for (const ep of s01Episodes) {
      const inBuffer      = bufferEpNumbers.has(ep.episodeNumber);
      const alreadyWatched = ep.episodeNumber <= lastWatched && ep.episodeNumber !== 1;
      episodeRepository.upsert({
        show_id:           showId,
        sonarr_episode_id: ep.id,
        sonarr_file_id:    ep.hasFile ? ep.episodeFileId : null,
        season:            1,
        episode_number:    ep.episodeNumber,
        status:            inBuffer      ? EpisodeStatus.Monitored
                         : alreadyWatched ? EpisodeStatus.Deleted
                         :                  EpisodeStatus.Unmonitored,
      });
    }
    showRepository.update(showId, { current_window_start: lastWatched + 1, current_season: 1 });
  })();

  logger.info(`Sonarr buffer set up for "${title}" — ${bufferIds.length} ep(s) monitored`);
  return true;
}

async function bootstrapNewShow(
  tvdbId: number,
  sonarrSeries: SonarrSeries,
  bufferSize: number,
  lastWatched: number = 0
): Promise<number | undefined> {
  let showId: number;

  db.transaction(() => {
    const show = showRepository.insert({
      sonarr_id:            sonarrSeries.id,
      title:                sonarrSeries.title,
      tvdb_id:              tvdbId,
      status:               ShowStatus.Active,
      buffer_size:          bufferSize,
      current_window_start: lastWatched + 1,
      current_season:       1,
      poster_url:   sonarrSeries.images?.find((i) => i.coverType === 'poster')?.remoteUrl ?? null,
      backdrop_url: sonarrSeries.images?.find((i) => i.coverType === 'fanart')?.remoteUrl ?? null,
    });
    showId = show.id;
  })();

  const ok = await setupSonarrBuffer(sonarrSeries, showId!, bufferSize, lastWatched);
  if (!ok) {
    // Roll back the show row if Sonarr setup failed
    db.prepare(`DELETE FROM shows WHERE id = ?`).run(showId!);
    return undefined;
  }

  return showId!;
}

// Polls Plex history to add new trackers to shows already managed by Rollarr.
// New shows are only bootstrapped via the Seerr webhook (triggerForTvdbId).
export async function runDiscoveryJob(): Promise<void> {
  logger.info('Discovery job starting');

  const userMap = plexService.getCachedUserMap();

  let history;
  try {
    history = await plexService.getGlobalHistory(500);
  } catch (err) {
    if (err instanceof PlexError) {
      logger.error('Failed to fetch Plex history', err.message);
      return;
    }
    throw err;
  }

  const managedShows = showRepository.findAll();
  if (managedShows.length === 0) {
    logger.info('No managed shows yet — discovery job skipped');
    return;
  }

  // Any play of a managed show adds the user as a tracker.
  // Deduplicate by (show, accountId) so multiple history entries don't cause redundant DB hits.
  const seen = new Set<string>();

  for (const play of history) {
    if (!play.grandparentTitle) continue;

    const managed = managedShows.find(
      (s) => s.title.toLowerCase() === play.grandparentTitle!.toLowerCase()
    );
    if (!managed) continue;

    const accountId = String(plexService.normalizeAccountId(play.accountID));
    const key = `${managed.id}:${accountId}`;
    if (seen.has(key)) continue;
    seen.add(key);

    const username = userMap.get(accountId) ?? plexService.getUsernameForAccountId(accountId);
    const user = userRepository.upsert(accountId, username);

    const existing = trackerRepository.findByShowAndUser(managed.id, user.id);
    if (!existing) {
      trackerRepository.upsert(managed.id, user.id);
      logger.info(`New tracker: ${username} → ${managed.title} (via S${play.parentIndex}E${play.index})`);
    } else if (!existing.is_active) {
      trackerRepository.upsert(managed.id, user.id);
      logger.info(`Reactivated tracker: ${username} → ${managed.title} (via S${play.parentIndex}E${play.index})`);
    }
  }

  logger.info('Discovery job complete');
}

// Triggered directly by webhook for a known tvdbId
export async function triggerForTvdbId(tvdbId: number, plexUsername?: string): Promise<void> {
  const bufferSize = settingsRepository.getNumber('buffer_size', 3);
  const existing = showRepository.findByTvdbId(tvdbId);

  // Always get the Sonarr series (with retry) — needed for both new and reactivated shows
  const sonarrSeries = await sonarrService.getSeriesByTvdbId(tvdbId);
  if (!sonarrSeries) {
    logger.warn(`Webhook: tvdbId ${tvdbId} not found in Sonarr library after retries`);
    return;
  }

  // Resolve accountId early (synchronous) so history can be scoped to the requesting user.
  // Buffer start must reflect WHERE THIS USER is, not a different user's (e.g. admin's) progress.
  const earlyAccountId = plexUsername ? plexService.resolveAccountId(plexUsername) : undefined;

  // Check Plex for existing watch progress so the buffer starts at the right episode.
  // When a requesting user is known, use their history specifically — avoids bootstrapping
  // the buffer past episodes they haven't watched yet just because another user (e.g. admin)
  // is further ahead.
  let lastWatched = 0;
  let isFullRewatch = false;
  let plexKey: string | undefined;
  let showHistory: import('@rollarr/shared').PlexHistoryEntry[] = [];
  try {
    plexKey = await plexService.findShowRatingKey(tvdbId, sonarrSeries.title);
    if (plexKey) {
      const [allEps, fetchedHistory] = await Promise.all([
        plexService.getAllEpisodes(plexKey),
        plexService.getShowHistory(plexKey),
      ]);
      showHistory = fetchedHistory;

      if (earlyAccountId) {
        // Per-user: derive lastWatched from this user's play history only
        const uid = Number(earlyAccountId);
        for (const entry of showHistory) {
          if (plexService.normalizeAccountId(entry.accountID) === uid && entry.parentIndex === 1 && entry.index > lastWatched) {
            lastWatched = entry.index;
          }
        }
        // Supplement with Plex DB (covers "Mark as Watched")
        const markedWatched = plexService.getMarkedWatchedFromDb(plexKey);
        const userDbSeasons = markedWatched.get(uid);
        if (userDbSeasons) {
          const s1 = userDbSeasons.get(1) ?? 0;
          if (s1 > lastWatched) lastWatched = s1;
        }
        logger.debug(`${sonarrSeries.title}: ${plexUsername} history → S1E${lastWatched}`);

        // Per-user rewatch detection: count unique S1 eps this user has played
        const s1Total = allEps.filter(e => e.parentIndex === 1).length;
        const userS1Played = new Set(
          showHistory.filter(e => plexService.normalizeAccountId(e.accountID) === uid && e.parentIndex === 1).map(e => e.index)
        ).size;
        if (s1Total > 0 && userS1Played >= s1Total) {
          isFullRewatch = true;
          logger.info(`${sonarrSeries.title}: ${plexUsername} has watched all ${s1Total} S1 ep(s) — rewatch detected, buffer starts at E1`);
          lastWatched = 0;
        }
      } else {
        // No requesting user — fall back to global max (admin viewCount + all history)
        for (const ep of allEps) {
          if (ep.parentIndex === 1 && (ep.viewCount ?? 0) > 0 && ep.index > lastWatched) {
            lastWatched = ep.index;
          }
        }
        logger.debug(`${sonarrSeries.title}: allLeaves viewCount → S1E${lastWatched}`);
        for (const entry of showHistory) {
          if (entry.parentIndex === 1 && entry.index > lastWatched) {
            lastWatched = entry.index;
          }
        }
        logger.debug(`${sonarrSeries.title}: global history max → S1E${lastWatched}`);

        const s1Eps     = allEps.filter(e => e.parentIndex === 1);
        const s1Watched = s1Eps.filter(e => (e.viewCount ?? 0) > 0).length;
        if (s1Eps.length > 0 && s1Watched === s1Eps.length) {
          isFullRewatch = true;
          logger.info(`${sonarrSeries.title}: all ${s1Eps.length} S1 ep(s) previously watched — rewatch detected, buffer starts at E1`);
          lastWatched = 0;
        }
      }
    }
  } catch {
    logger.warn(`${sonarrSeries.title}: Plex watch-history check failed, defaulting buffer to E1`);
  }
  if (!isFullRewatch && lastWatched > 0) {
    logger.info(`${sonarrSeries.title}: ${lastWatched} ep(s) already watched in S1 — buffer starts at E${lastWatched + 1}`);
  }

  let showId: number;

  if (!existing) {
    const newId = await bootstrapNewShow(tvdbId, sonarrSeries, bufferSize, lastWatched);
    if (!newId) return;
    showId = newId;
  } else {
    showId = existing.id;
    logger.info(`Reactivating existing show "${sonarrSeries.title}" (was ${existing.status})`);
    showRepository.update(showId, {
      sonarr_id:    sonarrSeries.id,
      status:       ShowStatus.Active,
      poster_url:   sonarrSeries.images?.find((i) => i.coverType === 'poster')?.remoteUrl ?? null,
      backdrop_url: sonarrSeries.images?.find((i) => i.coverType === 'fanart')?.remoteUrl ?? null,
    });
    // Re-run Sonarr setup — Seerr will have re-monitored everything on re-request
    await setupSonarrBuffer(sonarrSeries, showId, bufferSize, lastWatched);
  }

  if (plexUsername) {
    // Resolve real Plex accountId — tries stable account username first, display name second.
    const realAccountId = plexService.resolveAccountId(plexUsername);
    const user = realAccountId
      ? userRepository.upsert(realAccountId, plexUsername)
      : userRepository.upsertByUsername(plexUsername);
    if (!realAccountId) {
      logger.warn(`Webhook: ${plexUsername} not in Plex user map — synthetic record created, will upgrade on first history poll`);
    }
    const rewatchSince = isFullRewatch ? new Date().toISOString() : undefined;

    // Derive this user's initial watched position directly from the show history already
    // fetched above — avoids relying on advanceWindow's global-history cap (5000 entries)
    // to backfill progress for plays that may have aged out of the global window.
    let userLastEp     = 0;
    let userLastSeason = 1;
    if (!isFullRewatch && realAccountId) {
      const uid = Number(realAccountId);
      for (const entry of showHistory) {
        if (plexService.normalizeAccountId(entry.accountID) !== uid) continue;
        if (entry.parentIndex > userLastSeason ||
            (entry.parentIndex === userLastSeason && entry.index > userLastEp)) {
          userLastSeason = entry.parentIndex;
          userLastEp     = entry.index;
        }
      }
      // Also check Plex DB (covers "Mark as Watched" which doesn't appear in play history)
      if (plexKey) {
        const markedWatched = plexService.getMarkedWatchedFromDb(plexKey);
        const userDbSeasons = markedWatched.get(uid);
        if (userDbSeasons) {
          for (const [season, ep] of userDbSeasons) {
            if (season > userLastSeason || (season === userLastSeason && ep > userLastEp)) {
              userLastSeason = season;
              userLastEp     = ep;
            }
          }
        }
      }
      if (userLastEp > 0) {
        logger.info(`Webhook: ${plexUsername} watch history → S${userLastSeason}E${userLastEp} (used as initial tracker position)`);
      }
    }

    trackerRepository.upsert(showId, user.id, rewatchSince, userLastEp, userLastSeason);
    logger.info(`Webhook: tracker created ${plexUsername} → ${sonarrSeries.title}${isFullRewatch ? ' (rewatch)' : ''}`);
  }

  // Immediately position the window based on actual Plex watch history.
  // Handles re-requests mid-season without waiting for the first poll cycle.
  // Safe no-op if Plex doesn't have the show yet (new request, no files).
  const windowResult = await rollingWindowService.advanceWindow(showId);
  if (windowResult.advanced > 0) {
    logger.info(`${sonarrSeries.title}: initial window advance — ${windowResult.advanced} ep(s) consumed from Plex history`);
  }
  await rollingWindowService.checkSeasonTransition(showId);
}
