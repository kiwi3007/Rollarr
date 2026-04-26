import { Router } from 'express';
import { showsRouter } from './showsRouter';
import { usersRouter } from './usersRouter';
import { settingsRouter } from './settingsRouter';
import { showRepository } from '../db/repositories/showRepository';
import { trackerRepository } from '../db/repositories/trackerRepository';
import type { TrackerWithUser } from '../db/repositories/trackerRepository';

export const apiRouter = Router();

apiRouter.use('/shows', showsRouter);
apiRouter.use('/users', usersRouter);
apiRouter.use('/settings', settingsRouter);

apiRouter.get('/trackers', (_req, res) => {
  const shows = showRepository.findAll();
  const all: Array<TrackerWithUser & { show_title: string }> = shows.flatMap((show) =>
    trackerRepository.findByShow(show.id).map((t) => ({ ...t, show_title: show.title }))
  );
  res.json({ data: all });
});

apiRouter.delete('/shows/:showId/trackers/:userId', (req, res) => {
  const showId = parseInt(req.params.showId, 10);
  const userId = parseInt(req.params.userId, 10);
  const tracker = trackerRepository.findByShowAndUser(showId, userId);
  if (!tracker) {
    res.status(404).json({ error: 'Tracker not found' });
    return;
  }
  trackerRepository.deactivate(showId, userId);
  res.json({ data: { ok: true } });
});
