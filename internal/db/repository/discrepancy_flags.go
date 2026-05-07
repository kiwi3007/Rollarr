package repository

import (
	"database/sql"
	"fmt"
	"time"
)

// DiscrepancyFlag mirrors a row from the discrepancy_flags table.
type DiscrepancyFlag struct {
	ID               int
	TVDBId           int
	SonarrEpisodeId  *int
	IssueDescription string
	Status           string // open|resolved|ignored
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// DiscrepancyFlagRepository provides access to the discrepancy_flags table.
type DiscrepancyFlagRepository struct {
	db *sql.DB
}

// NewDiscrepancyFlagRepository constructs a DiscrepancyFlagRepository.
func NewDiscrepancyFlagRepository(db *sql.DB) *DiscrepancyFlagRepository {
	return &DiscrepancyFlagRepository{db: db}
}

// Insert adds a new discrepancy flag row.
func (r *DiscrepancyFlagRepository) Insert(f DiscrepancyFlag) error {
	_, err := r.db.Exec(
		`INSERT INTO discrepancy_flags (tvdb_id, sonarr_episode_id, issue_description, status)
		 VALUES (?, ?, ?, ?)`,
		f.TVDBId, nullIntPtr(f.SonarrEpisodeId), f.IssueDescription, f.Status,
	)
	if err != nil {
		return fmt.Errorf("discrepancy_flags.Insert: %w", err)
	}
	return nil
}

func scanFlag(scan func(...interface{}) error) (*DiscrepancyFlag, error) {
	var f DiscrepancyFlag
	var epID sql.NullInt64
	err := scan(&f.ID, &f.TVDBId, &epID, &f.IssueDescription, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if epID.Valid {
		v := int(epID.Int64)
		f.SonarrEpisodeId = &v
	}
	return &f, nil
}

// FindOpen returns all flags with status='open'.
func (r *DiscrepancyFlagRepository) FindOpen() ([]DiscrepancyFlag, error) {
	rows, err := r.db.Query(
		`SELECT id, tvdb_id, sonarr_episode_id, issue_description, status, created_at, updated_at
		 FROM discrepancy_flags WHERE status = 'open'`,
	)
	if err != nil {
		return nil, fmt.Errorf("discrepancy_flags.FindOpen: %w", err)
	}
	defer rows.Close()
	return scanFlags(rows)
}

// FindByShow returns all flags for the given tvdb_id.
func (r *DiscrepancyFlagRepository) FindByShow(tvdbId int) ([]DiscrepancyFlag, error) {
	rows, err := r.db.Query(
		`SELECT id, tvdb_id, sonarr_episode_id, issue_description, status, created_at, updated_at
		 FROM discrepancy_flags WHERE tvdb_id = ?`, tvdbId,
	)
	if err != nil {
		return nil, fmt.Errorf("discrepancy_flags.FindByShow(%d): %w", tvdbId, err)
	}
	defer rows.Close()
	return scanFlags(rows)
}

// UpdateStatus sets the status field (and updated_at) for the given flag ID.
func (r *DiscrepancyFlagRepository) UpdateStatus(id int, status string) error {
	_, err := r.db.Exec(
		`UPDATE discrepancy_flags SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("discrepancy_flags.UpdateStatus(%d, %q): %w", id, status, err)
	}
	return nil
}

func scanFlags(rows *sql.Rows) ([]DiscrepancyFlag, error) {
	var flags []DiscrepancyFlag
	for rows.Next() {
		f, err := scanFlag(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("discrepancy_flags scan: %w", err)
		}
		flags = append(flags, *f)
	}
	return flags, rows.Err()
}
