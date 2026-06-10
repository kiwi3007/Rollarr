package repository

import (
	"database/sql"
	"fmt"
	"time"
)

// UserRequest mirrors a row from the user_requests table.
type UserRequest struct {
	ID                 int        `json:"id"`
	PlexUserID         string     `json:"plex_user_id"`
	DisplayName        string     `json:"display_name"`
	TVDBId             int        `json:"tvdb_id"`
	RequestTimestamp   time.Time  `json:"request_timestamp"`
	IsRewatching       bool       `json:"is_rewatching"`
	RequestedSeason    int        `json:"requested_season"`
	LastWatchedSeason  *int       `json:"last_watched_season"`
	LastWatchedEpisode *int       `json:"last_watched_episode"`
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
// same (plex_user_id, tvdb_id) pair. requested_season is written explicitly:
// 0 marks an auto-discovered watcher (no history-timestamp filtering), >=1 a
// Seerr-initiated request.
func (r *UserRequestRepository) Upsert(req UserRequest) error {
	rewatching := 0
	if req.IsRewatching {
		rewatching = 1
	}
	_, err := r.db.Exec(
		`INSERT INTO user_requests (plex_user_id, display_name, tvdb_id, request_timestamp, is_rewatching, requested_season)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(plex_user_id, tvdb_id) DO UPDATE SET
		 	display_name      = COALESCE(excluded.display_name, display_name),
		 	request_timestamp = excluded.request_timestamp,
		 	is_rewatching     = excluded.is_rewatching,
		 	requested_season  = excluded.requested_season`,
		req.PlexUserID, nullString(req.DisplayName), req.TVDBId, req.RequestTimestamp, rewatching, req.RequestedSeason,
	)
	if err != nil {
		return fmt.Errorf("user_requests.Upsert(%q, %d): %w", req.PlexUserID, req.TVDBId, err)
	}
	return nil
}

// ClearWatchProgress nulls the stored high-water mark for a user. Called when a
// rewatch begins so the stored floor doesn't pin the window at the old position.
func (r *UserRequestRepository) ClearWatchProgress(tvdbId int, plexUserId string) error {
	_, err := r.db.Exec(
		`UPDATE user_requests SET last_watched_season = NULL, last_watched_episode = NULL
		 WHERE tvdb_id = ? AND plex_user_id = ?`,
		tvdbId, plexUserId,
	)
	if err != nil {
		return fmt.Errorf("user_requests.ClearWatchProgress(%d, %q): %w", tvdbId, plexUserId, err)
	}
	return nil
}

// FindByShow returns all user requests associated with the given tvdb_id.
func (r *UserRequestRepository) FindByShow(tvdbId int) ([]UserRequest, error) {
	rows, err := r.db.Query(
		`SELECT id, plex_user_id, COALESCE(display_name,''), tvdb_id, request_timestamp, is_rewatching,
		        requested_season, last_watched_season, last_watched_episode
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
		if err := rows.Scan(
			&req.ID, &req.PlexUserID, &req.DisplayName, &req.TVDBId, &req.RequestTimestamp, &rewatching,
			&req.RequestedSeason, &req.LastWatchedSeason, &req.LastWatchedEpisode,
		); err != nil {
			return nil, fmt.Errorf("user_requests.FindByShow scan: %w", err)
		}
		req.IsRewatching = rewatching != 0
		reqs = append(reqs, req)
	}
	return reqs, rows.Err()
}

// UpdateWatchProgress stores the highest-watched season and episode for a user.
func (r *UserRequestRepository) UpdateWatchProgress(tvdbId int, plexUserId string, season, episode int) error {
	_, err := r.db.Exec(
		`UPDATE user_requests SET last_watched_season = ?, last_watched_episode = ?
		 WHERE tvdb_id = ? AND plex_user_id = ?`,
		season, episode, tvdbId, plexUserId,
	)
	if err != nil {
		return fmt.Errorf("user_requests.UpdateWatchProgress(%d, %q): %w", tvdbId, plexUserId, err)
	}
	return nil
}

// SetRequestedSeason updates requested_season on the most-recently-created
// request for a show. Called from the Sonarr proxy after a series add, where
// we know the season but not the user. The recency assumption is safe because
// the webhook and proxy series-add always fire as a pair for the same request.
func (r *UserRequestRepository) SetRequestedSeason(tvdbId, season int) error {
	_, err := r.db.Exec(
		`UPDATE user_requests SET requested_season = ?
		 WHERE tvdb_id = ?
		   AND id = (SELECT id FROM user_requests WHERE tvdb_id = ? ORDER BY request_timestamp DESC LIMIT 1)`,
		season, tvdbId, tvdbId,
	)
	if err != nil {
		return fmt.Errorf("user_requests.SetRequestedSeason(%d, %d): %w", tvdbId, season, err)
	}
	return nil
}

// DeleteSeerrRows removes all unresolved seerr:* rows for a show. Called after
// discoverNewWatchers registers the real numeric account IDs, so the deferred
// rows don't cause duplicate buffer windows.
func (r *UserRequestRepository) DeleteSeerrRows(tvdbId int) error {
	_, err := r.db.Exec(
		`DELETE FROM user_requests WHERE tvdb_id = ? AND plex_user_id LIKE 'seerr:%'`,
		tvdbId,
	)
	if err != nil {
		return fmt.Errorf("user_requests.DeleteSeerrRows(%d): %w", tvdbId, err)
	}
	return nil
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

// SetRewatching updates is_rewatching and request_timestamp for the given
// (tvdb_id, plex_user_id) pair. since sets the timestamp baseline used to
// filter history when is_rewatching=true. Enabling a rewatch also clears the
// stored watch progress so the window reseeds instead of staying pinned at the
// old high-water mark.
func (r *UserRequestRepository) SetRewatching(tvdbId int, plexUserId string, v bool, since time.Time) error {
	rewatching := 0
	if v {
		rewatching = 1
	}
	_, err := r.db.Exec(
		`UPDATE user_requests SET is_rewatching = ?, request_timestamp = ? WHERE tvdb_id = ? AND plex_user_id = ?`,
		rewatching, since, tvdbId, plexUserId,
	)
	if err != nil {
		return fmt.Errorf("user_requests.SetRewatching(%d, %q): %w", tvdbId, plexUserId, err)
	}
	if v {
		return r.ClearWatchProgress(tvdbId, plexUserId)
	}
	return nil
}
