package db

import (
	"database/sql"
	"fmt"
)

// migration is a function that applies a single schema migration within a transaction.
type migration func(tx *sql.Tx) error

// migrations is the ordered list of all schema migrations.
// To add a migration: append a new function to this slice.
var migrations = []migration{
	migration0,
}

// migration0 creates the initial 4-table schema and seeds default settings.
func migration0(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY,
			value TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS shows (
			tvdb_id                INTEGER PRIMARY KEY,
			sonarr_id              INTEGER UNIQUE NOT NULL,
			title                  TEXT NOT NULL,
			poster_url             TEXT,
			status                 TEXT NOT NULL DEFAULT 'active',
			custom_buffer_size     INTEGER,
			custom_inactivity_days INTEGER,
			last_activity_at       DATETIME,
			created_at             DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS user_requests (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			plex_user_id      TEXT NOT NULL,
			tvdb_id           INTEGER NOT NULL REFERENCES shows(tvdb_id) ON DELETE CASCADE,
			request_timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			is_rewatching     INTEGER NOT NULL DEFAULT 0,
			UNIQUE(plex_user_id, tvdb_id)
		)`,
		`CREATE TABLE IF NOT EXISTS discrepancy_flags (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			tvdb_id           INTEGER NOT NULL,
			sonarr_episode_id INTEGER,
			issue_description TEXT NOT NULL,
			status            TEXT NOT NULL DEFAULT 'open',
			created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("execute schema statement: %w", err)
		}
	}

	// Seed default settings — only insert rows that don't already exist.
	defaults := map[string]string{
		"global_buffer_size":            "3",
		"global_inactivity_days":        "30",
		"reconcile_interval_minutes":    "15",
		"inactivity_interval_minutes":   "60",
		"sonarr_url":                    "",
		"sonarr_api_key":                "",
		"plex_url":                      "",
		"plex_token":                    "",
		"plex_db_path":                  "",
		"seerr_webhook_secret":          "",
	}

	for k, v := range defaults {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)`, k, v,
		); err != nil {
			return fmt.Errorf("seed setting %q: %w", k, err)
		}
	}

	return nil
}

// RunMigrations applies all pending migrations to the database.
// The current schema version is stored as settings.schema_version.
func RunMigrations(db *sql.DB) error {
	// Ensure the settings table exists before we try to read schema_version.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		return fmt.Errorf("create settings table: %w", err)
	}

	// Read the current schema version.
	var currentVersion int
	row := db.QueryRow(`SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'schema_version'`)
	if err := row.Scan(&currentVersion); err != nil {
		// No row — version is 0 (no migrations applied yet).
		currentVersion = 0
	}

	for i := currentVersion; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d transaction: %w", i, err)
		}

		if err := migrations[i](tx); err != nil {
			tx.Rollback() //nolint:errcheck
			return fmt.Errorf("apply migration %d: %w", i, err)
		}

		// Advance schema_version within the same transaction.
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO settings (key, value) VALUES ('schema_version', ?)`,
			fmt.Sprintf("%d", i+1),
		); err != nil {
			tx.Rollback() //nolint:errcheck
			return fmt.Errorf("update schema_version after migration %d: %w", i, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", i, err)
		}
	}

	return nil
}
