import db from '../database';
import type { ShowRow, ShowStatus } from '@rollarr/shared';

export const showRepository = {
  findAll(): ShowRow[] {
    return db.prepare(`SELECT * FROM shows ORDER BY title`).all() as ShowRow[];
  },

  findById(id: number): ShowRow | undefined {
    return db.prepare(`SELECT * FROM shows WHERE id = ?`).get(id) as ShowRow | undefined;
  },

  findBySonarrId(sonarrId: number): ShowRow | undefined {
    return db.prepare(`SELECT * FROM shows WHERE sonarr_id = ?`).get(sonarrId) as
      | ShowRow
      | undefined;
  },

  findByTvdbId(tvdbId: number): ShowRow | undefined {
    return db.prepare(`SELECT * FROM shows WHERE tvdb_id = ?`).get(tvdbId) as
      | ShowRow
      | undefined;
  },

  findByStatus(status: ShowStatus): ShowRow[] {
    return db.prepare(`SELECT * FROM shows WHERE status = ?`).all(status) as ShowRow[];
  },

  insert(show: Omit<ShowRow, 'id' | 'created_at' | 'updated_at'>): ShowRow {
    const result = db
      .prepare(
        `INSERT INTO shows (sonarr_id, title, tvdb_id, status, buffer_size, current_window_start, current_season)
         VALUES (@sonarr_id, @title, @tvdb_id, @status, @buffer_size, @current_window_start, @current_season)`
      )
      .run(show);
    return this.findById(result.lastInsertRowid as number)!;
  },

  update(
    id: number,
    fields: Partial<Pick<ShowRow, 'sonarr_id' | 'status' | 'buffer_size' | 'current_window_start' | 'current_season'>>
  ): void {
    const setClauses = Object.keys(fields)
      .map((k) => `${k} = @${k}`)
      .join(', ');
    db.prepare(
      `UPDATE shows SET ${setClauses}, updated_at = datetime('now') WHERE id = @id`
    ).run({ ...fields, id });
  },
};
