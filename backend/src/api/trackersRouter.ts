import { Router } from 'express';
import { trackerRepository } from '../db/repositories/trackerRepository';
import { showRepository } from '../db/repositories/showRepository';

export const trackersRouter = Router({ mergeParams: true });

trackersRouter.get('/', (_req, res) => {
  // All trackers across all shows — join manually
  const shows = showRepository.findAll();
  const all = shows.flatMap((show) =>
    trackerRepository.findByShow(show.id).map((t) => ({
      ...t,
      show_title: show.title,
    }))
  );
  res.json({ data: all });
});

trackersRouter.delete('/:showId/trackers/:userId', (req, res) => {
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
