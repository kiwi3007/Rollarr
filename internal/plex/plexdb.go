package plex

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// PlexDB provides read-only access to the Plex SQLite database, enabling
// detection of episodes that have been "Marked as Watched" directly in Plex
// (which does not appear in the HTTP history API).
type PlexDB struct {
	db *sql.DB

	// ownerID resolves the server owner's plex.tv account ID, which differs
	// from the owner's local ID (always 1). Resolved lazily and cached on
	// success. Nil means unknown.
	ownerID     func() (int, error)
	ownerMu     sync.Mutex
	ownerCached int
}

// SetOwnerIDResolver sets how the server owner's plex.tv account ID is found
// (normally Client.OwnerAccountID).
func (p *PlexDB) SetOwnerIDResolver(fn func() (int, error)) {
	p.ownerMu.Lock()
	defer p.ownerMu.Unlock()
	p.ownerID = fn
}

// owner returns the owner's plex.tv account ID, or 0 if unknown.
func (p *PlexDB) owner() int {
	p.ownerMu.Lock()
	defer p.ownerMu.Unlock()
	if p.ownerCached == 0 && p.ownerID != nil {
		if id, err := p.ownerID(); err == nil {
			p.ownerCached = id
		} else {
			log.Printf("[plexdb] owner account lookup failed: %v", err)
		}
	}
	return p.ownerCached
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
	// mode=ro allows the Go SQLite driver to read through the WAL, so recently
	// marked-as-watched episodes are visible without waiting for a checkpoint.
	// busy_timeout gives Plex up to 5 s to finish a checkpoint before we error.
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout%%3D5000", path)
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
//   - Owner: local id is always 1; their cloud id differs
//
// It returns -1 when the account can't be matched. The old fallback treated
// every unknown ID as the owner, so an unrecognised user inherited the
// owner's watch history (and the owner's window drove their deletes).
func (p *PlexDB) resolveLocalAccountID(cloudID int) int {
	if cloudID <= 0 {
		return 0
	}
	var count int
	err := p.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id = ?`, cloudID).Scan(&count)
	if err == nil && count > 0 {
		return cloudID
	}
	if owner := p.owner(); owner > 0 && owner == cloudID {
		return 1
	}
	log.Printf("[plexdb] account_id=%d not in local accounts and not the owner — no plexdb history", cloudID)
	return -1
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
		if localID < 0 {
			return nil, nil
		}
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
