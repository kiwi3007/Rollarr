import { Router, Request, Response } from 'express';
import { settingsRepository } from '../db/repositories/settingsRepository';
import { triggerForTvdbId } from '../jobs/discoveryJob';
import { createLogger } from '../utils/logger';
import type { SeerrWebhookPayload } from '@rollarr/shared';

const logger = createLogger('webhook');
export const seerrRouter = Router();

const APPROVAL_TYPES = new Set([
  'MEDIA_APPROVED',
  'MEDIA_AUTO_APPROVED',
]);

function handleSeerrPayload(req: Request, res: Response): void {
  const secret = settingsRepository.get('seerr_webhook_secret') ?? '';
  if (secret && req.headers['x-webhook-secret'] !== secret) {
    res.status(401).json({ error: 'Unauthorized' });
    return;
  }

  const payload = req.body as SeerrWebhookPayload;

  // Log every incoming webhook so failures are visible in logs
  logger.info(`Webhook received: type=${payload.notification_type} media_type=${payload.media?.media_type}`);

  if (!APPROVAL_TYPES.has(payload.notification_type) || payload.media?.media_type !== 'tv') {
    res.status(200).json({ ok: true });
    return;
  }

  // tvdbId may arrive as a string from some Seerr versions
  const tvdbId = Number(payload.media.tvdbId);
  if (!tvdbId) {
    logger.warn('Webhook payload missing or invalid tvdbId', payload.media);
    res.status(200).json({ ok: true });
    return;
  }

  // Overseerr sends flat keys: requestedBy_username. Jellyseerr sends nested object.
  const raw = payload.request as Record<string, unknown> | undefined;
  const plexUsername =
    (raw?.requestedBy as Record<string, unknown> | undefined)?.plexUsername as string | undefined
    ?? raw?.requestedBy_username as string | undefined;

  logger.info(`Processing: tvdbId=${tvdbId} requested by ${plexUsername ?? 'unknown'}`);

  triggerForTvdbId(tvdbId, plexUsername).catch((err) =>
    logger.error('Webhook trigger error', err)
  );

  res.status(202).json({ ok: true });
}

seerrRouter.post('/seerr',      handleSeerrPayload);
seerrRouter.post('/jellyseerr', handleSeerrPayload);
