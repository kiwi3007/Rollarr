package repository

import (
	"database/sql"
	"fmt"
	"time"
)

// Event mirrors a row from the events audit table: one Sonarr-affecting action
// and the human-readable reason it happened.
type Event struct {
	ID        int       `json:"id"`
	TVDBId    int       `json:"tvdb_id"`
	Action    string    `json:"action"` // deleted|searched|pruned|discovered|onboarded|reactivated
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// EventRepository provides access to the events table.
type EventRepository struct {
	db *sql.DB
}

// NewEventRepository constructs an EventRepository.
func NewEventRepository(db *sql.DB) *EventRepository {
	return &EventRepository{db: db}
}

// Insert records an event. Failures are returned but callers typically just log
// them — the audit trail must never block a reconcile.
func (r *EventRepository) Insert(tvdbId int, action, detail string) error {
	_, err := r.db.Exec(
		`INSERT INTO events (tvdb_id, action, detail) VALUES (?, ?, ?)`,
		tvdbId, action, detail,
	)
	if err != nil {
		return fmt.Errorf("events.Insert(%d, %q): %w", tvdbId, action, err)
	}
	return nil
}

// FindByShow returns the most recent events for a show, newest first.
func (r *EventRepository) FindByShow(tvdbId, limit int) ([]Event, error) {
	rows, err := r.db.Query(
		`SELECT id, tvdb_id, action, detail, created_at
		 FROM events WHERE tvdb_id = ? ORDER BY id DESC LIMIT ?`,
		tvdbId, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("events.FindByShow(%d): %w", tvdbId, err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TVDBId, &e.Action, &e.Detail, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("events.FindByShow scan: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// PruneOlderThan deletes events older than the given number of days.
func (r *EventRepository) PruneOlderThan(days int) error {
	_, err := r.db.Exec(
		`DELETE FROM events WHERE created_at < datetime('now', ?)`,
		fmt.Sprintf("-%d days", days),
	)
	if err != nil {
		return fmt.Errorf("events.PruneOlderThan(%d): %w", days, err)
	}
	return nil
}
