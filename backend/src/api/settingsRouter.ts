import { Router } from 'express';
import { settingsRepository } from '../db/repositories/settingsRepository';

export const settingsRouter = Router();

settingsRouter.get('/', (_req, res) => {
  res.json({ data: settingsRepository.getAll() });
});

settingsRouter.put('/', (req, res) => {
  const body = req.body as Record<string, unknown>;
  // Only allow string values, filter schema_version
  const safe: Record<string, string> = {};
  for (const [key, val] of Object.entries(body)) {
    if (key !== 'schema_version' && typeof val === 'string') {
      safe[key] = val;
    }
  }
  settingsRepository.setMany(safe);
  res.json({ data: settingsRepository.getAll() });
});
