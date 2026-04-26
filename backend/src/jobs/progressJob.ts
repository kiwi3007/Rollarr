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
      // Advance rolling window
      const result = await rollingWindowService.advanceWindow(show.id);
      if (result.advanced > 0) {
        logger.info(`${show.title}: window advanced ${result.advanced} ep(s)`);
      }

      // Check if we should start next season
      await rollingWindowService.checkSeasonTransition(show.id);

      // Update last_active_at for all users who have been watching
      const trackers = trackerRepository.findActiveByShow(show.id);
      for (const tracker of trackers) {
        if (tracker.last_watched_episode > 0) {
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
