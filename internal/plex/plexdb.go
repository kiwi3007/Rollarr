package plex

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// PlexDB provides read-only access to the Plex SQLite database, enabling
// detection of episodes that have been "Marked as Watched" directly in Plex
// (which does not appear in the HTTP history API).
type PlexDB struct {
	db *sql.DB
}

// MarkedWatched represents an episode that was manually marked as watched.
type MarkedWatched struct {
	AccountID  int
	SeasonNum  int
	EpisodeNum int
	ViewedAt   time.Time
}

// ResolvePlexDBPath returns the path to the Plex SQLite database file.
// If path points to a directory it appends the standard Plex DB filename.
func ResolvePlexDBPath(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", path, err)
	}
	if info.IsDir() {
		return filepath.Join(path, "com.plexapp.plugins.library.db"), nil
	}
	return path, nil
}

// OpenPlexDB opens the Plex SQLite database at path in read-only WAL mode.
func OpenPlexDB(path string) (*PlexDB, error) {
	// immutable=1: skip WAL coordination entirely, read only the main DB file.
	// Plex holds a write lock on the WAL; the pure-Go SQLite driver can't share
	// the SHM file with it, which causes SQLITE_CORRUPT(11). We accept a
	// slightly stale read in exchange for reliability.
	dsn := fmt.Sprintf("file:%s?mode=ro&immutable=1", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open plex db %q: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping plex db %q: %w", path, err)
	}
	return &PlexDB{db: db}, nil
}

// resolveLocalAccountID translates a cloud account ID to its local SQLite
// account ID. In Plex's local DB:
//   - Non-admin users: local id == cloud id
//   - Admin: local id is always 1; their cloud id differs (obtained from
//     plex.tv, not from the local server /accounts endpoint)
//
// If the cloud ID is not found in the local accounts table we assume the caller
// is the admin and return 1.
func (p *PlexDB) resolveLocalAccountID(cloudID int) int {
	if cloudID <= 0 {
		return 0
	}
	var count int
	err := p.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id = ?`, cloudID).Scan(&count)
	if err != nil || count == 0 {
		log.Printf("[plexdb] account_id=%d not in local accounts table — treating as admin (local id=1)", cloudID)
		return 1
	}
	return cloudID
}

// GetMarkedWatched returns episodes for the given Plex ratingKey that were
// manually marked as watched (not via playback). Pass accountId=0 to return
// all accounts. accountId is the cloud/HTTP-API account ID; it is translated
// to the local SQLite account ID internally.
func (p *PlexDB) GetMarkedWatched(plexRatingKey string, accountId int) ([]MarkedWatched, error) {
	ratingKeyInt, err := strconv.Atoi(strings.TrimPrefix(plexRatingKey, "/library/metadata/"))
	if err != nil {
		return nil, fmt.Errorf("plexdb.GetMarkedWatched: invalid ratingKey %q: %w", plexRatingKey, err)
	}

	localID := 0
	if accountId > 0 {
		localID = p.resolveLocalAccountID(accountId)
		log.Printf("[plexdb] GetMarkedWatched ratingKey=%s cloudAccountId=%d localAccountId=%d", plexRatingKey, accountId, localID)
	}

	// Join: episode item (type=4) → season (type=3) → show (parent_id = ratingKey).
	// Use view_count > 0 like the reference TS implementation — covers both
	// "Mark as Watched" and plays that reached the scrobble threshold.
	query := `
		SELECT
			mis.account_id,
			s."index"  AS season_number,
			mi."index" AS episode_number,
			mis.last_viewed_at
		FROM metadata_item_settings mis
		JOIN metadata_items mi ON mis.guid = mi.guid
		JOIN metadata_items s  ON mi.parent_id = s.id
		WHERE s.parent_id = ?
		  AND s.metadata_type = 3
		  AND mi.metadata_type = 4
		  AND mis.view_count > 0`

	args := []interface{}{ratingKeyInt}

	if localID > 0 {
		query += ` AND mis.account_id = ?`
		args = append(args, localID)
	}

	rows, err := p.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("plexdb.GetMarkedWatched(%s): %w", plexRatingKey, err)
	}
	defer rows.Close()

	var results []MarkedWatched
	for rows.Next() {
		var mw MarkedWatched
		var viewedAt sql.NullInt64
		if err := rows.Scan(&mw.AccountID, &mw.SeasonNum, &mw.EpisodeNum, &viewedAt); err != nil {
			return nil, fmt.Errorf("plexdb.GetMarkedWatched scan: %w", err)
		}
		if viewedAt.Valid {
			mw.ViewedAt = time.Unix(viewedAt.Int64, 0)
		}
		results = append(results, mw)
	}
	log.Printf("[plexdb] GetMarkedWatched ratingKey=%s localId=%d → %d rows", plexRatingKey, localID, len(results))
	return results, rows.Err()
}

// Close releases the database connection.
func (p *PlexDB) Close() error {
	return p.db.Close()
}
