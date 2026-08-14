package sqlite

// Per-library refresh scheduling (ADR-0014). A refresh is claimed, run,
// and then finished; the claim is an UPDATE, so it is the exclusion.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// ClaimLibraryRefresh takes the next due library. The select and the
// stamp are one transaction — the connection is opened with
// _txlock=immediate, so a second claimer waits for the write lock rather
// than reading a row that is about to be taken.
//
// Due is either an explicit request or a schedule that has come round,
// and a library is only ever due if it has a root: a managed library has
// nothing to look at.
func (s *Store) ClaimLibraryRefresh(ctx context.Context, now time.Time) (store.Library, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.Library{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	// The due test is arithmetic on whole seconds: strftime('%s') turns
	// the stored RFC3339 text into a unix timestamp, and the interval is
	// already stored in seconds. Doing it in SQL rather than in Go is
	// what lets "the next library that is due" be one row rather than a
	// scan of every library the server has.
	//
	// Both sides are CAST to an integer, and that is not decoration:
	// strftime returns text, SQLite sorts every number before every
	// string, and so an unwrapped comparison of the sum against it is
	// true for every row on earth.
	lib, err := scanPlainLibrary(tx.QueryRowContext(ctx,
		`SELECT `+plainLibraryColumns+`
		 FROM libraries
		 WHERE source <> 'managed'
		   AND root_path IS NOT NULL AND root_path <> ''
		   AND (refresh_requested_at IS NOT NULL
		        OR (refresh = 'interval'
		            AND CAST(strftime('%s', COALESCE(last_refresh_attempt_at,
		                                             last_refresh_at,
		                                             created_at)) AS INTEGER)
		                + refresh_interval_seconds
		                <= CAST(strftime('%s', ?) AS INTEGER)))
		 ORDER BY refresh_requested_at IS NULL,
		          COALESCE(last_refresh_attempt_at, last_refresh_at, created_at),
		          id
		 LIMIT 1`,
		formatTime(now)))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Library{}, false, nil
	}
	if err != nil {
		return store.Library{}, false, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE libraries
		    SET last_refresh_attempt_at = ?, refresh_requested_at = NULL
		  WHERE id = ?`,
		formatTime(now), lib.ID); err != nil {
		return store.Library{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return store.Library{}, false, err
	}
	lib.LastRefreshAttemptAt = &now
	lib.RefreshRequestedAt = nil
	return lib, true, nil
}

func (s *Store) FinishLibraryRefresh(ctx context.Context, libraryID string, at time.Time, refreshErr string) error {
	var res sql.Result
	var err error
	if refreshErr == "" {
		res, err = s.db.ExecContext(ctx,
			`UPDATE libraries
			    SET last_refresh_at = ?, last_refresh_error = NULL
			  WHERE id = ?`,
			formatTime(at), libraryID)
	} else {
		res, err = s.db.ExecContext(ctx,
			`UPDATE libraries SET last_refresh_error = ? WHERE id = ?`,
			store.TruncateRefreshError(refreshErr), libraryID)
	}
	return affectedOne(res, err)
}

func (s *Store) AdminRequestLibraryRefresh(ctx context.Context, actorUserID, libraryID string, at time.Time) error {
	_ = actorUserID
	res, err := s.db.ExecContext(ctx,
		`UPDATE libraries SET refresh_requested_at = ?
		  WHERE id = ? AND root_path IS NOT NULL AND root_path <> ''`,
		formatTime(at), libraryID)
	return affectedOne(res, err)
}
