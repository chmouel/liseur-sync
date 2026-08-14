package postgres

// Per-library refresh scheduling (ADR-0014), the twin of the SQLite
// file of the same name. See it for what a claim is for.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// ClaimLibraryRefresh takes the next due library in a single statement.
// FOR UPDATE SKIP LOCKED is what makes two servers sharing one database
// safe: the second one steps over the row the first is taking rather
// than waiting for it and then sweeping the same root.
func (s *Store) ClaimLibraryRefresh(ctx context.Context, now time.Time) (store.Library, bool, error) {
	lib, err := scanPlainLibrary(s.db.QueryRowContext(ctx, q(
		`UPDATE libraries SET last_refresh_attempt_at = ?,
		                      refresh_requested_at = NULL
		  WHERE id = (
		      SELECT id FROM libraries
		       WHERE source <> 'managed'
		         AND root_path IS NOT NULL AND root_path <> ''
		         AND (refresh_requested_at IS NOT NULL
		              OR (refresh = 'interval'
		                  AND COALESCE(last_refresh_attempt_at,
		                               last_refresh_at, created_at)
		                      + make_interval(secs => refresh_interval_seconds)
		                      <= ?))
		       ORDER BY refresh_requested_at IS NULL,
		                COALESCE(last_refresh_attempt_at, last_refresh_at,
		                         created_at),
		                id
		       LIMIT 1
		       FOR UPDATE SKIP LOCKED)
		  RETURNING `+plainLibraryColumns), now, now))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Library{}, false, nil
	}
	if err != nil {
		return store.Library{}, false, err
	}
	return lib, true, nil
}

func (s *Store) FinishLibraryRefresh(ctx context.Context, libraryID string, at time.Time, refreshErr string) error {
	var res sql.Result
	var err error
	if refreshErr == "" {
		res, err = s.db.ExecContext(ctx, q(
			`UPDATE libraries
			    SET last_refresh_at = ?, last_refresh_error = NULL
			  WHERE id = ?`),
			at, libraryID)
	} else {
		res, err = s.db.ExecContext(ctx, q(
			`UPDATE libraries SET last_refresh_error = ? WHERE id = ?`),
			store.TruncateRefreshError(refreshErr), libraryID)
	}
	return affectedOne(res, err)
}

func (s *Store) AdminRequestLibraryRefresh(ctx context.Context, actorUserID, libraryID string, at time.Time) error {
	_ = actorUserID
	res, err := s.db.ExecContext(ctx, q(
		`UPDATE libraries SET refresh_requested_at = ?
		  WHERE id = ? AND root_path IS NOT NULL AND root_path <> ''`),
		at, libraryID)
	return affectedOne(res, err)
}
