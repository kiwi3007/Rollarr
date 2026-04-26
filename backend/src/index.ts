import 'dotenv/config';
import express from 'express';
import path from 'path';
import { runMigrations } from './db/migrations';
import { apiRouter } from './api/router';
import { seerrRouter } from './webhooks/seerrWebhook';
import { startScheduler } from './jobs/scheduler';
import { createLogger } from './utils/logger';
import { config } from './config';

const logger = createLogger('server');

const app = express();
app.use(express.json());

// Webhooks
app.use('/webhooks', seerrRouter);

// API
app.use('/api', apiRouter);

// Serve frontend in production
if (config.nodeEnv === 'production') {
  const frontendDist = path.join(__dirname, '../../frontend/dist');
  app.use(express.static(frontendDist));
  app.get('*', (_req, res) => {
    res.sendFile(path.join(frontendDist, 'index.html'));
  });
}

// Run migrations before anything else
runMigrations();
logger.info('DB migrations complete');

// Start server
app.listen(config.port, () => {
  logger.info(`Rollarr listening on port ${config.port}`);
  startScheduler();
});
