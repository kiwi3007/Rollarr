import { createLogger } from './logger';

const logger = createLogger('retry');

export async function withRetry<T>(
  fn: () => Promise<T>,
  { attempts = 3, baseDelayMs = 500, context = 'operation' } = {}
): Promise<T> {
  let lastError: unknown;
  for (let i = 0; i < attempts; i++) {
    try {
      return await fn();
    } catch (err) {
      lastError = err;
      if (i < attempts - 1) {
        const delay = baseDelayMs * 2 ** i;
        logger.warn(`${context} failed (attempt ${i + 1}/${attempts}), retrying in ${delay}ms`, err);
        await new Promise((r) => setTimeout(r, delay));
      }
    }
  }
  throw lastError;
}
