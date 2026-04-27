import db from '../database';
import type { TrackerRow } from '@rollarr/shared';

export interface TrackerWithUser extends TrackerRow {
  plex_username: string;
  last_activity: string;
}

export const trackerRepository = {
  findByShow(showId: number): TrackerWithUser[] {
    return db
      .prepare(
        `SELECT t.*, u.plex_username, u.last_active_at as last_activity
         FROM trackers t
         JOIN users u ON u.id = t.user_id
         WHERE t.show_id = ?`
      )
      .all(showId) as TrackerWithUser[];
  },

  findActiveByShow(showId: number): TrackerWithUser[] {
    return db
      .prepare(
        `SELECT t.*, u.plex_username, u.last_active_at as last_activity
         FROM trackers t
         JOIN users u ON u.id = t.user_id
         WHERE t.show_id = ? AND t.is_active = 1`
      )
      .all(showId) as TrackerWithUser[];
  },

  findByShowAndUser(showId: number, userId: number): TrackerRow | undefined {
    return db
      .prepare(`SELECT * FROM trackers WHERE show_id = ? AND user_id = ?`)
      .get(showId, userId) as TrackerRow | undefined;
  },

  upsert(
    showId: number,
    userId: number,
    rewatchSince?: string,
    initialEpisode?: number,
    initialSeason?: number,
  ): TrackerRow {
    db.prepare(
      `INSERT INTO trackers (show_id, user_id, rewatch_since, last_watched_episode, last_watched_season)
       VALUES (?, ?, ?, ?, ?)
       ON CONFLICT(show_id, user_id) DO UPDATE SET
         is_active = 1,
         watchlist_active = 1,
         rewatch_since = COALESCE(excluded.rewatch_since, rewatch_since)`
    ).run(showId, userId, rewatchSince ?? null, initialEpisode ?? 0, initialSeason ?? 1);
    return this.findByShowAndUser(showId, userId)!;
  },

  updateProgress(
    showId: number,
    userId: number,
    lastWatchedEpisode: number,
    lastWatchedSeason: number
  ): void {
    db.prepare(
      `UPDATE trackers
       SET last_watched_episode = ?, last_watched_season = ?
       WHERE show_id = ? AND user_id = ?`
    ).run(lastWatchedEpisode, lastWatchedSeason, showId, userId);
  },

  deactivate(showId: number, userId: number): void {
    db.prepare(
      `UPDATE trackers SET is_active = 0 WHERE show_id = ? AND user_id = ?`
    ).run(showId, userId);
  },

  deactivateAllForShow(showId: number): void {
    db.prepare(`UPDATE trackers SET is_active = 0 WHERE show_id = ?`).run(showId);
  },

  deactivateInactive(removeAfterDays: number): number {
    const result = db
      .prepare(
        `UPDATE trackers SET is_active = 0
         WHERE is_active = 1
         AND user_id IN (
           SELECT id FROM users
           WHERE last_active_at < datetime('now', ? || ' days')
         )`
      )
      .run(`-${removeAfterDays}`);
    return result.changes;
  },

  countActive(showId: number): number {
    const row = db
      .prepare(`SELECT COUNT(*) as n FROM trackers WHERE show_id = ? AND is_active = 1`)
      .get(showId) as { n: number };
    return row.n;
  },
};
