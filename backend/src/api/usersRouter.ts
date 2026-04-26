import { Router } from 'express';
import { userRepository } from '../db/repositories/userRepository';

export const usersRouter = Router();

usersRouter.get('/', (_req, res) => {
  res.json({ data: userRepository.findAll() });
});
