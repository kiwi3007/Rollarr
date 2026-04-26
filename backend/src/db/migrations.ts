import db from './database';

const DEFAULT_SETTINGS: Record<string, string> = {
  buffer_size:                    '3',
  starter_buffer_size:            '1',
  inactivity_warn_days:           '14',
  inactivity_remove_days:         '30',
  poll_interval_minutes:          '15',
  maintenance_interval_minutes:   '60',
  sonarr_url:                     '',
  sonarr_api_key:                 '',
  plex_url:                       '',
  plex_token:                     '',
  plex_db_path:                   '',
  seerr_webhook_secret:           '',
  dry_mode:                       'false',
  cancel_queued_downloads:        'true',
};

const migrations: Array<() => void> = [
  // Migration 0 — initial schema
  () => {
    db.exec(`
      CREATE TABLE IF NOT EXISTS shows (
        id                   INTEGER PRIMARY KEY AUTOINCREMENT,
        sonarr_id            INTEGER NOT NULL UNIQUE,
        title                TEXT NOT NULL,
        tvdb_id              INTEGER NOT NULL,
        status               TEXT NOT NULL DEFAULT 'Active',
        buffer_size          INTEGER NOT NULL DEFAULT 3,
        current_window_start INTEGER NOT NULL DEFAULT 1,
        current_season       INTEGER NOT NULL DEFAULT 1,
        created_at           TEXT NOT NULL DEFAULT (datetime('now')),
        updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
      );

      CREATE TABLE IF NOT EXISTS users (
        id              INTEGER PRIMARY KEY AUTOINCREMENT,
        plex_account_id TEXT NOT NULL UNIQUE,
        plex_username   TEXT NOT NULL,
        last_active_at  TEXT NOT NULL DEFAULT (datetime('now'))
      );

      CREATE TABLE IF NOT EXISTS trackers (
        id                   INTEGER PRIMARY KEY AUTOINCREMENT,
        show_id              INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
        user_id              INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
        last_watched_episode INTEGER NOT NULL DEFAULT 0,
        last_watched_season  INTEGER NOT NULL DEFAULT 1,
        is_active            INTEGER NOT NULL DEFAULT 1,
        watchlist_active     INTEGER NOT NULL DEFAULT 1,
        created_at           TEXT NOT NULL DEFAULT (datetime('now')),
        UNIQUE(show_id, user_id)
      );

      CREATE TABLE IF NOT EXISTS episodes (
        id                INTEGER PRIMARY KEY AUTOINCREMENT,
        show_id           INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
        sonarr_episode_id INTEGER NOT NULL UNIQUE,
        sonarr_file_id    INTEGER,
        season            INTEGER NOT NULL,
        episode_number    INTEGER NOT NULL,
        status            TEXT NOT NULL DEFAULT 'Monitored'
      );

      CREATE TABLE IF NOT EXISTS settings (
        key   TEXT PRIMARY KEY,
        value TEXT NOT NULL
      );
    `);

    // Seed defaults without overwriting existing values
    const upsertSetting = db.prepare(
      `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`
    );
    for (const [key, value] of Object.entries(DEFAULT_SETTINGS)) {
      upsertSetting.run(key, value);
    }
  },

  // Migration 1 — rewatch support
  () => {
    db.exec(`ALTER TABLE trackers ADD COLUMN rewatch_since TEXT;`);
  },
];

export function runMigrations(): void {
  // schema_version lives in settings table after migration 0
  // Bootstrap: check if settings table exists
  const tableExists = db
    .prepare(`SELECT name FROM sqlite_master WHERE type='table' AND name='settings'`)
    .get();

  let currentVersion = 0;
  if (tableExists) {
    const row = db.prepare(`SELECT value FROM settings WHERE key='schema_version'`).get() as
      | { value: string }
      | undefined;
    if (row) currentVersion = parseInt(row.value, 10);
  }

  for (let i = currentVersion; i < migrations.length; i++) {
    db.transaction(() => {
      migrations[i]();
      db.prepare(
        `INSERT INTO settings (key, value) VALUES ('schema_version', ?)
         ON CONFLICT(key) DO UPDATE SET value=excluded.value`
      ).run(String(i + 1));
    })();
  }
}
