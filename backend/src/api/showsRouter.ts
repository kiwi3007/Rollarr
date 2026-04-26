import { Router } from 'express';
import { showRepository } from '../db/repositories/showRepository';
import { trackerRepository } from '../db/repositories/trackerRepository';
import { episodeRepository } from '../db/repositories/episodeRepository';
import { rollingWindowService } from '../services/rollingWindowService';
import { jobQueue } from '../utils/asyncQueue';

export const showsRouter = Router();

showsRouter.get('/', (_req, res) => {
  const shows = showRepository.findAll();
  const data = shows.map((show) => ({
    ...show,
    trackerCount: trackerRepository.countActive(show.id),
  }));
  res.json({ data });
});

showsRouter.get('/:id', (req, res) => {
  const id = parseInt(req.params.id, 10);
  const show = showRepository.findById(id);
  if (!show) {
    res.status(404).json({ error: 'Show not found' });
    return;
  }
  res.json({
    data: {
      ...show,
      trackers: trackerRepository.findByShow(id),
      episodes: episodeRepository.findByShow(id),
    },
  });
});

showsRouter.post('/:id/refresh', (req, res) => {
  const id = parseInt(req.params.id, 10);
  const show = showRepository.findById(id);
  if (!show) {
    res.status(404).json({ error: 'Show not found' });
    return;
  }
  jobQueue.enqueue(() => rollingWindowService.advanceWindow(id)).catch(() => {});
  res.status(202).json({ data: { queued: true } });
});
