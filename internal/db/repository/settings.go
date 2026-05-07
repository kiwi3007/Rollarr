package repository

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/kiwi3007/rollarr/internal/config"
)

// SettingsRepository provides typed access to the settings table.
type SettingsRepository struct {
	db *sql.DB
}

// NewSettingsRepository constructs a SettingsRepository.
func NewSettingsRepository(db *sql.DB) *SettingsRepository {
	return &SettingsRepository{db: db}
}

// Get returns the value for the given key, or an empty string if not found.
func (r *SettingsRepository) Get(key string) string {
	var val string
	err := r.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&val)
	if err != nil {
		return ""
	}
	return val
}

// GetWithDefault returns the value for the given key, or def if the key is absent or empty.
func (r *SettingsRepository) GetWithDefault(key, def string) string {
	val := r.Get(key)
	if val == "" {
		return def
	}
	return val
}

// GetInt returns the integer value for the given key, or def on error/absence.
func (r *SettingsRepository) GetInt(key string, def int) int {
	val := r.Get(key)
	if val == "" {
		return def
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return def
	}
	return n
}

// Set writes a single key/value pair to the settings table.
func (r *SettingsRepository) Set(key, value string) error {
	_, err := r.db.Exec(
		`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, key, value,
	)
	if err != nil {
		return fmt.Errorf("settings.Set %q: %w", key, err)
	}
	return nil
}

// SetMany writes multiple key/value pairs in a single transaction.
func (r *SettingsRepository) SetMany(m map[string]string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("settings.SetMany begin tx: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`)
	if err != nil {
		tx.Rollback() //nolint:errcheck
		return fmt.Errorf("settings.SetMany prepare: %w", err)
	}
	defer stmt.Close()

	for k, v := range m {
		if _, err := stmt.Exec(k, v); err != nil {
			tx.Rollback() //nolint:errcheck
			return fmt.Errorf("settings.SetMany exec %q: %w", k, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("settings.SetMany commit: %w", err)
	}
	return nil
}

// All returns every key/value pair in the settings table.
func (r *SettingsRepository) All() (map[string]string, error) {
	rows, err := r.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("settings.All query: %w", err)
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("settings.All scan: %w", err)
		}
		result[k] = v
	}
	return result, rows.Err()
}

// SeedFromEnv writes env-sourced values into the settings table only when the
// existing stored value is empty. This preserves any values the user has
// configured via the UI.
func (r *SettingsRepository) SeedFromEnv(cfg *config.Config) error {
	seeds := map[string]string{
		"sonarr_url":           cfg.SonarrURL,
		"sonarr_api_key":       cfg.SonarrAPIKey,
		"plex_url":             cfg.PlexURL,
		"plex_token":           cfg.PlexToken,
		"plex_db_path":         cfg.PlexDBPath,
		"seerr_webhook_secret": cfg.SeerrWebhookSecret,
	}

	for k, v := range seeds {
		if v == "" {
			continue
		}
		existing := r.Get(k)
		if existing != "" {
			continue
		}
		if err := r.Set(k, v); err != nil {
			return err
		}
	}
	return nil
}
