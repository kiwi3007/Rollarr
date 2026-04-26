import db from '../db/database';
import { showRepository } from '../db/repositories/showRepository';
import { trackerRepository } from '../db/repositories/trackerRepository';
import { episodeRepository } from '../db/repositories/episodeRepository';
import { settingsRepository } from '../db/repositories/settingsRepository';
import { rollingWindowService } from '../services/rollingWindowService';
import { sonarrService, SonarrError } from '../services/sonarrService';
import { plexService } from '../services/plexService';
import { createLogger } from '../utils/logger';
import { ShowStatus } from '@rollarr/shared';

const logger = createLogger('maintenance');

export async function runMaintenanceJob(): Promise<void> {
  logger.info('Maintenance job starting');

  // Refresh Plex user map
  try {
    await plexService.buildUserMap();
  } catch (err) {
    logger.warn('Failed to refresh Plex user map', err);
  }

  const removeAfterDays = settingsRepository.getNumber('inactivity_remove_days', 30);

  // Deactivate inactive trackers
  const deactivated = trackerRepository.deactivateInactive(removeAfterDays);
  if (deactivated > 0) {
    logger.info(`Deactivated ${deactivated} inactive tracker(s)`);
  }

  // Watchlist sync (best-effort for Plex Home users; external users skipped)
  // Skipped for now — requires per-user token or admin watchlist API

  // Zero-tracker cleanup
  const allShows = showRepository.findByStatus(ShowStatus.Active);
  for (const show of allShows) {
    const activeCount = trackerRepository.countActive(show.id);
    if (activeCount === 0) {
      logger.info(`${show.title}: zero active trackers → cleaning up`);
      try {
        await rollingWindowService.cleanupStaleShow(show.id);
      } catch (err) {
        logger.error(`Cleanup failed for ${show.title}`, err);
      }
    }
  }

  // Sonarr sync: detect shows removed from Sonarr and mark them Removed
  try {
    const sonarrSeries = await sonarrService.getAllSeries();
    if (sonarrSeries.length === 0) {
      logger.warn('Sonarr returned empty series list — skipping removal sync to avoid false positives');
      logger.info('Maintenance job complete');
      return;
    }
    const sonarrIds = new Set(sonarrSeries.map((s) => s.id));
    const managedShows = showRepository.findAll().filter((s) => s.status !== ShowStatus.Removed);

    for (const show of managedShows) {
      if (!sonarrIds.has(show.sonarr_id)) {
        logger.info(`${show.title} no longer in Sonarr — marking Removed`);
        db.transaction(() => {
          episodeRepository.markAllDeletedForShow(show.id);
          trackerRepository.deactivateAllForShow(show.id);
          showRepository.update(show.id, { status: ShowStatus.Removed });
        })();
      }
    }
  } catch (err) {
    if (err instanceof SonarrError) {
      logger.warn('Sonarr sync skipped — could not fetch series list', err.message);
    } else {
      throw err;
    }
  }

  logger.info('Maintenance job complete');
}
