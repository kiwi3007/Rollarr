// Serialises async job execution — prevents concurrent SQLite writes from overlapping jobs.
export class AsyncQueue {
  private queue: Promise<void> = Promise.resolve();

  enqueue<T>(fn: () => Promise<T>): Promise<T> {
    const next = this.queue.then(fn);
    // Swallow errors so the queue chain never breaks
    this.queue = next.then(
      () => {},
      () => {}
    );
    return next;
  }
}

export const jobQueue = new AsyncQueue();
