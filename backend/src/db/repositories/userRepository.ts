import db from '../database';
import type { UserRow } from '@rollarr/shared';

export const userRepository = {
  findAll(): UserRow[] {
    return db.prepare(`SELECT * FROM users ORDER BY plex_username`).all() as UserRow[];
  },

  findById(id: number): UserRow | undefined {
    return db.prepare(`SELECT * FROM users WHERE id = ?`).get(id) as UserRow | undefined;
  },

  findByPlexAccountId(plexAccountId: string): UserRow | undefined {
    return db
      .prepare(`SELECT * FROM users WHERE plex_account_id = ?`)
      .get(plexAccountId) as UserRow | undefined;
  },

  upsert(plexAccountId: string, plexUsername: string): UserRow {
    // If a synthetic record exists for this username, upgrade it with the real account ID
    db.prepare(
      `UPDATE users SET plex_account_id = ?, plex_username = ?
       WHERE plex_account_id = ? AND plex_account_id LIKE 'seerr:%'`
    ).run(plexAccountId, plexUsername, `seerr:${plexUsername}`);

    db.prepare(
      `INSERT INTO users (plex_account_id, plex_username)
       VALUES (?, ?)
       ON CONFLICT(plex_account_id) DO UPDATE SET plex_username = excluded.plex_username`
    ).run(plexAccountId, plexUsername);
    return this.findByPlexAccountId(plexAccountId)!;
  },

  // Used when only a Seerr username is known — creates a placeholder until Plex history
  // provides the real account ID, at which point upsert() upgrades the record.
  upsertByUsername(plexUsername: string): UserRow {
    const syntheticId = `seerr:${plexUsername}`;
    db.prepare(
      `INSERT INTO users (plex_account_id, plex_username)
       VALUES (?, ?)
       ON CONFLICT(plex_account_id) DO UPDATE SET plex_username = excluded.plex_username`
    ).run(syntheticId, plexUsername);
    return this.findByPlexAccountId(syntheticId)!;
  },

  updateLastActive(id: number): void {
    db.prepare(`UPDATE users SET last_active_at = datetime('now') WHERE id = ?`).run(id);
  },
};
