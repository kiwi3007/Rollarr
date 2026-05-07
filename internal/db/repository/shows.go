package repository

import (
	"database/sql"
	"fmt"
	"time"
)

// Show mirrors a row from the shows table.
type Show struct {
	TVDBId               int
	SonarrId             int
	Title                string
	PosterURL            string
	Status               string // active|inactive|removed
	CustomBufferSize     *int
	CustomInactivityDays *int
	LastActivityAt       *time.Time
	CreatedAt            time.Time
}

// ShowRepository provides CRUD access to the shows table.
type ShowRepository struct {
	db       *sql.DB
	settings *SettingsRepository
}

// NewShowRepository constructs a ShowRepository.
func NewShowRepository(db *sql.DB, settings *SettingsRepository) *ShowRepository {
	return &ShowRepository{db: db, settings: settings}
}

// scanShow reads a Show from a sql.Row or sql.Rows.
func scanShow(scan func(...interface{}) error) (*Show, error) {
	var s Show
	var posterURL sql.NullString
	var customBuf, customInact sql.NullInt64
	var lastActivity sql.NullTime

	err := scan(
		&s.TVDBId, &s.SonarrId, &s.Title, &posterURL,
		&s.Status, &customBuf, &customInact, &lastActivity, &s.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if posterURL.Valid {
		s.PosterURL = posterURL.String
	}
	if customBuf.Valid {
		v := int(customBuf.Int64)
		s.CustomBufferSize = &v
	}
	if customInact.Valid {
		v := int(customInact.Int64)
		s.CustomInactivityDays = &v
	}
	if lastActivity.Valid {
		s.LastActivityAt = &lastActivity.Time
	}

	return &s, nil
}

const showColumns = `tvdb_id, sonarr_id, title, poster_url, status,
	custom_buffer_size, custom_inactivity_days, last_activity_at, created_at`

// FindAll returns every row from the shows table.
func (r *ShowRepository) FindAll() ([]Show, error) {
	rows, err := r.db.Query(`SELECT ` + showColumns + ` FROM shows`)
	if err != nil {
		return nil, fmt.Errorf("shows.FindAll: %w", err)
	}
	defer rows.Close()

	var shows []Show
	for rows.Next() {
		s, err := scanShow(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("shows.FindAll scan: %w", err)
		}
		shows = append(shows, *s)
	}
	return shows, rows.Err()
}

// FindByTVDB returns the show with the given TVDB ID, or nil if not found.
func (r *ShowRepository) FindByTVDB(tvdbId int) (*Show, error) {
	row := r.db.QueryRow(`SELECT `+showColumns+` FROM shows WHERE tvdb_id = ?`, tvdbId)
	s, err := scanShow(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("shows.FindByTVDB(%d): %w", tvdbId, err)
	}
	return s, nil
}

// FindBySonarrId returns the show with the given Sonarr series ID, or nil if not found.
func (r *ShowRepository) FindBySonarrId(sonarrId int) (*Show, error) {
	row := r.db.QueryRow(`SELECT `+showColumns+` FROM shows WHERE sonarr_id = ?`, sonarrId)
	s, err := scanShow(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("shows.FindBySonarrId(%d): %w", sonarrId, err)
	}
	return s, nil
}

// FindByStatus returns all shows with the given status value.
func (r *ShowRepository) FindByStatus(status string) ([]Show, error) {
	rows, err := r.db.Query(`SELECT `+showColumns+` FROM shows WHERE status = ?`, status)
	if err != nil {
		return nil, fmt.Errorf("shows.FindByStatus(%q): %w", status, err)
	}
	defer rows.Close()

	var shows []Show
	for rows.Next() {
		s, err := scanShow(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("shows.FindByStatus scan: %w", err)
		}
		shows = append(shows, *s)
	}
	return shows, rows.Err()
}

// Upsert inserts or replaces a show row.
func (r *ShowRepository) Upsert(s Show) error {
	_, err := r.db.Exec(
		`INSERT INTO shows
			(tvdb_id, sonarr_id, title, poster_url, status,
			 custom_buffer_size, custom_inactivity_days, last_activity_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tvdb_id) DO UPDATE SET
			sonarr_id              = excluded.sonarr_id,
			title                  = excluded.title,
			poster_url             = excluded.poster_url,
			status                 = excluded.status,
			custom_buffer_size     = excluded.custom_buffer_size,
			custom_inactivity_days = excluded.custom_inactivity_days,
			last_activity_at       = excluded.last_activity_at`,
		s.TVDBId, s.SonarrId, s.Title, nullString(s.PosterURL),
		s.Status, nullIntPtr(s.CustomBufferSize), nullIntPtr(s.CustomInactivityDays),
		nullTimePtr(s.LastActivityAt),
	)
	if err != nil {
		return fmt.Errorf("shows.Upsert(%d): %w", s.TVDBId, err)
	}
	return nil
}

// UpdateStatus sets the status field for the given tvdb_id.
func (r *ShowRepository) UpdateStatus(tvdbId int, status string) error {
	_, err := r.db.Exec(`UPDATE shows SET status = ? WHERE tvdb_id = ?`, status, tvdbId)
	if err != nil {
		return fmt.Errorf("shows.UpdateStatus(%d, %q): %w", tvdbId, status, err)
	}
	return nil
}

// UpdateLastActivity sets last_activity_at for the given tvdb_id.
func (r *ShowRepository) UpdateLastActivity(tvdbId int, t time.Time) error {
	_, err := r.db.Exec(`UPDATE shows SET last_activity_at = ? WHERE tvdb_id = ?`, t, tvdbId)
	if err != nil {
		return fmt.Errorf("shows.UpdateLastActivity(%d): %w", tvdbId, err)
	}
	return nil
}

// EffectiveBufferSize returns the custom buffer size for the show if set,
// otherwise the global setting (default 3).
func (r *ShowRepository) EffectiveBufferSize(tvdbId int) int {
	show, err := r.FindByTVDB(tvdbId)
	if err != nil || show == nil {
		return r.settings.GetInt("global_buffer_size", 3)
	}
	if show.CustomBufferSize != nil {
		return *show.CustomBufferSize
	}
	return r.settings.GetInt("global_buffer_size", 3)
}

// EffectiveInactivityDays returns the custom inactivity threshold for the show
// if set, otherwise the global setting (default 30).
func (r *ShowRepository) EffectiveInactivityDays(tvdbId int) int {
	show, err := r.FindByTVDB(tvdbId)
	if err != nil || show == nil {
		return r.settings.GetInt("global_inactivity_days", 30)
	}
	if show.CustomInactivityDays != nil {
		return *show.CustomInactivityDays
	}
	return r.settings.GetInt("global_inactivity_days", 30)
}

// --- helpers ----------------------------------------------------------------

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullIntPtr(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

func nullTimePtr(p *time.Time) sql.NullTime {
	if p == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *p, Valid: true}
}
