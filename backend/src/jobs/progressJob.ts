import { showRepository } from '../db/repositories/showRepository';
import { trackerRepository } from '../db/repositories/trackerRepository';
import { userRepository } from '../db/repositories/userRepository';
import { rollingWindowService } from '../services/rollingWindowService';
import { createLogger } from '../utils/logger';
import { ShowStatus } from '@rollarr/shared';

const logger = createLogger('progress');

export async function runProgressJob(): Promise<void> {
  logger.info('Progress job starting');

  const activeShows = showRepository.findByStatus(ShowStatus.Active);

  for (const show of activeShows) {
    try {
      // Snapshot progress before advancing so we can detect actual changes
      const beforeProgress = new Map(
        trackerRepository.findActiveByShow(show.id).map((t) => [
          t.user_id,
          { ep: t.last_watched_episode, season: t.last_watched_season },
        ])
      );

      // Advance rolling window
      const result = await rollingWindowService.advanceWindow(show.id);
      if (result.advanced > 0) {
        logger.info(`${show.title}: window advanced ${result.advanced} ep(s)`);
      }

      // Check if we should start next season
      await rollingWindowService.checkSeasonTransition(show.id);

      // Only update last_active_at for trackers whose progress actually advanced
      const afterTrackers = trackerRepository.findActiveByShow(show.id);
      for (const tracker of afterTrackers) {
        const before = beforeProgress.get(tracker.user_id);
        if (!before) continue;
        if (tracker.last_watched_episode > before.ep || tracker.last_watched_season > before.season) {
          userRepository.updateLastActive(tracker.user_id);
        }
      }

      // Check for zero active trackers → mark stale
      const activeCount = trackerRepository.countActive(show.id);
      if (activeCount === 0) {
        logger.info(`${show.title}: no active trackers`);
        await rollingWindowService.cleanupStaleShow(show.id);
      }
    } catch (err) {
      logger.error(`Progress job error for show ${show.title}`, err);
    }
  }

  logger.info('Progress job complete');
}
