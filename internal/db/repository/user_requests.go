package repository

import (
	"database/sql"
	"fmt"
	"time"
)

// UserRequest mirrors a row from the user_requests table.
type UserRequest struct {
	ID               int
	PlexUserID       string
	TVDBId           int
	RequestTimestamp time.Time
	IsRewatching     bool
}

// UserRequestRepository provides access to the user_requests table.
type UserRequestRepository struct {
	db *sql.DB
}

// NewUserRequestRepository constructs a UserRequestRepository.
func NewUserRequestRepository(db *sql.DB) *UserRequestRepository {
	return &UserRequestRepository{db: db}
}

// Upsert inserts a new user_request row or replaces the existing one for the
// same (plex_user_id, tvdb_id) pair.
func (r *UserRequestRepository) Upsert(req UserRequest) error {
	rewatching := 0
	if req.IsRewatching {
		rewatching = 1
	}
	_, err := r.db.Exec(
		`INSERT INTO user_requests (plex_user_id, tvdb_id, request_timestamp, is_rewatching)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(plex_user_id, tvdb_id) DO UPDATE SET
		 	request_timestamp = excluded.request_timestamp,
		 	is_rewatching     = excluded.is_rewatching`,
		req.PlexUserID, req.TVDBId, req.RequestTimestamp, rewatching,
	)
	if err != nil {
		return fmt.Errorf("user_requests.Upsert(%q, %d): %w", req.PlexUserID, req.TVDBId, err)
	}
	return nil
}

// FindByShow returns all user requests associated with the given tvdb_id.
func (r *UserRequestRepository) FindByShow(tvdbId int) ([]UserRequest, error) {
	rows, err := r.db.Query(
		`SELECT id, plex_user_id, tvdb_id, request_timestamp, is_rewatching
		 FROM user_requests WHERE tvdb_id = ?`, tvdbId,
	)
	if err != nil {
		return nil, fmt.Errorf("user_requests.FindByShow(%d): %w", tvdbId, err)
	}
	defer rows.Close()

	var reqs []UserRequest
	for rows.Next() {
		var req UserRequest
		var rewatching int
		if err := rows.Scan(&req.ID, &req.PlexUserID, &req.TVDBId, &req.RequestTimestamp, &rewatching); err != nil {
			return nil, fmt.Errorf("user_requests.FindByShow scan: %w", err)
		}
		req.IsRewatching = rewatching != 0
		reqs = append(reqs, req)
	}
	return reqs, rows.Err()
}

// Delete removes the user_request for the given (tvdb_id, plex_user_id) pair.
func (r *UserRequestRepository) Delete(tvdbId int, plexUserId string) error {
	_, err := r.db.Exec(
		`DELETE FROM user_requests WHERE tvdb_id = ? AND plex_user_id = ?`,
		tvdbId, plexUserId,
	)
	if err != nil {
		return fmt.Errorf("user_requests.Delete(%d, %q): %w", tvdbId, plexUserId, err)
	}
	return nil
}

// SetRewatching updates the is_rewatching flag for the given (tvdb_id, plex_user_id) pair.
func (r *UserRequestRepository) SetRewatching(tvdbId int, plexUserId string, v bool) error {
	rewatching := 0
	if v {
		rewatching = 1
	}
	_, err := r.db.Exec(
		`UPDATE user_requests SET is_rewatching = ? WHERE tvdb_id = ? AND plex_user_id = ?`,
		rewatching, tvdbId, plexUserId,
	)
	if err != nil {
		return fmt.Errorf("user_requests.SetRewatching(%d, %q): %w", tvdbId, plexUserId, err)
	}
	return nil
}
