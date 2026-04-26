import cron from 'node-cron';
import { jobQueue } from '../utils/asyncQueue';
import { runDiscoveryJob } from './discoveryJob';
import { runProgressJob } from './progressJob';
import { runMaintenanceJob } from './maintenanceJob';
import { settingsRepository } from '../db/repositories/settingsRepository';
import { createLogger } from '../utils/logger';

const logger = createLogger('scheduler');

function minutesToCron(minutes: number): string {
  if (minutes < 60) return `*/${minutes} * * * *`;
  const hours = Math.floor(minutes / 60);
  return `0 */${hours} * * *`;
}

export function startScheduler(): void {
  const pollMinutes     = settingsRepository.getNumber('poll_interval_minutes', 15);
  const maintMinutes    = settingsRepository.getNumber('maintenance_interval_minutes', 60);

  const pollCron  = minutesToCron(pollMinutes);
  const maintCron = minutesToCron(maintMinutes);

  logger.info(`Poll schedule: ${pollCron} (every ${pollMinutes}min)`);
  logger.info(`Maintenance schedule: ${maintCron} (every ${maintMinutes}min)`);

  cron.schedule(pollCron, () => {
    jobQueue.enqueue(async () => {
      await runDiscoveryJob();
      await runProgressJob();
    });
  });

  cron.schedule(maintCron, () => {
    jobQueue.enqueue(runMaintenanceJob);
  });

  // Run immediately on startup
  jobQueue.enqueue(runMaintenanceJob);
  jobQueue.enqueue(async () => {
    await runDiscoveryJob();
    await runProgressJob();
  });
}
