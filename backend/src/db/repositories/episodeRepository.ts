import db from '../database';
import type { EpisodeRow, SonarrEpisode } from '@rollarr/shared';
import { EpisodeStatus } from '@rollarr/shared';

export const episodeRepository = {
  findByShow(showId: number): EpisodeRow[] {
    return db
      .prepare(`SELECT * FROM episodes WHERE show_id = ? ORDER BY season, episode_number`)
      .all(showId) as EpisodeRow[];
  },

  findBySeason(showId: number, season: number): EpisodeRow[] {
    return db
      .prepare(
        `SELECT * FROM episodes WHERE show_id = ? AND season = ? ORDER BY episode_number`
      )
      .all(showId, season) as EpisodeRow[];
  },

  findBySonarrEpisodeId(sonarrEpisodeId: number): EpisodeRow | undefined {
    return db
      .prepare(`SELECT * FROM episodes WHERE sonarr_episode_id = ?`)
      .get(sonarrEpisodeId) as EpisodeRow | undefined;
  },

  upsert(ep: Omit<EpisodeRow, 'id'>): void {
    db.prepare(
      `INSERT INTO episodes (show_id, sonarr_episode_id, sonarr_file_id, season, episode_number, status)
       VALUES (@show_id, @sonarr_episode_id, @sonarr_file_id, @season, @episode_number, @status)
       ON CONFLICT(sonarr_episode_id) DO UPDATE SET
         sonarr_file_id = excluded.sonarr_file_id,
         status = excluded.status`
    ).run(ep);
  },

  updateStatus(sonarrEpisodeId: number, status: EpisodeStatus): void {
    db.prepare(`UPDATE episodes SET status = ? WHERE sonarr_episode_id = ?`).run(
      status,
      sonarrEpisodeId
    );
  },

  updateFileId(sonarrEpisodeId: number, fileId: number | null): void {
    db.prepare(`UPDATE episodes SET sonarr_file_id = ? WHERE sonarr_episode_id = ?`).run(
      fileId,
      sonarrEpisodeId
    );
  },

  markAllDeletedForShow(showId: number): void {
    db.prepare(`UPDATE episodes SET status = 'Deleted' WHERE show_id = ?`).run(showId);
  },

  // Re-map sonarr_episode_id values after a show is removed/re-added in Sonarr.
  // Deletes existing rows for the season and reinserts with fresh IDs, preserving status.
  // Delete+reinsert avoids UNIQUE conflicts when Sonarr reassigns IDs across episode numbers.
  resyncSeason(showId: number, season: number, freshEps: SonarrEpisode[]): void {
    db.transaction(() => {
      const existing = db
        .prepare(`SELECT episode_number, status FROM episodes WHERE show_id = ? AND season = ?`)
        .all(showId, season) as { episode_number: number; status: EpisodeStatus }[];
      const statusByEpNum = new Map(existing.map((r) => [r.episode_number, r.status]));

      db.prepare(`DELETE FROM episodes WHERE show_id = ? AND season = ?`).run(showId, season);

      for (const ep of freshEps) {
        const status = statusByEpNum.get(ep.episodeNumber) ?? EpisodeStatus.Unmonitored;
        db.prepare(
          `INSERT INTO episodes (show_id, sonarr_episode_id, sonarr_file_id, season, episode_number, status)
           VALUES (?, ?, ?, ?, ?, ?)`
        ).run(showId, ep.id, ep.hasFile ? ep.episodeFileId : null, season, ep.episodeNumber, status);
      }
    })();
  },

  countBySeason(showId: number, season: number): number {
    const row = db
      .prepare(
        `SELECT COUNT(*) as n FROM episodes WHERE show_id = ? AND season = ?`
      )
      .get(showId, season) as { n: number };
    return row.n;
  },
};
