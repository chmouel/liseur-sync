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

// ClaimLibraryRefresh takes the next due library and leases it. The
// select, the stamp and the lease are one transaction — the connection
// is opened with _txlock=immediate, so a second claimer waits for the
// write lock rather than reading a row that is about to be taken.
//
// Due is either an explicit request or a schedule that has come round,
// and a library is only ever due if it has a root: a managed library has
// nothing to look at. A library somebody else still holds is not due
// either, however overdue it is, and a lease left behind by a killed
// process stops holding it the moment it expires.
func (s *Store) ClaimLibraryRefresh(
	ctx context.Context, now time.Time, lease store.RefreshLease,
) (store.Library, bool, error) {
	if !lease.Valid() || now.IsZero() || !lease.Until.After(now) {
		return store.Library{}, false, store.ErrInvalidTransition
	}
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
		   AND (refresh_lease_until IS NULL
		        OR CAST(strftime('%s', refresh_lease_until) AS INTEGER)
		           <= CAST(strftime('%s', ?) AS INTEGER))
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
		formatTime(now), formatTime(now)))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Library{}, false, nil
	}
	if err != nil {
		return store.Library{}, false, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE libraries
		    SET last_refresh_attempt_at = ?, refresh_requested_at = NULL,
		        refresh_lease_owner = ?, refresh_lease_until = ?
		  WHERE id = ?`,
		formatTime(now), lease.Owner, formatTime(lease.Until),
		lib.ID); err != nil {
		return store.Library{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return store.Library{}, false, err
	}
	lib.LastRefreshAttemptAt = &now
	lib.RefreshRequestedAt = nil
	lib.RefreshLeaseOwner = lease.Owner
	until := lease.Until
	lib.RefreshLeaseUntil = &until
	return lib, true, nil
}

// RenewLibraryRefreshLease extends a lease this worker still holds. The
// ownership test is part of the update rather than a read before it, so
// a takeover cannot slip between the two.
func (s *Store) RenewLibraryRefreshLease(
	ctx context.Context, libraryID string,
	lease store.RefreshLease, now time.Time,
) error {
	if libraryID == "" || !lease.Valid() || now.IsZero() {
		return store.ErrInvalidTransition
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE libraries SET refresh_lease_until = ?
		  WHERE id = ? AND COALESCE(refresh_lease_owner, '') = ?
		    AND CAST(strftime('%s', refresh_lease_until) AS INTEGER)
		        > CAST(strftime('%s', ?) AS INTEGER)`,
		formatTime(lease.Until), libraryID, lease.Owner, formatTime(now))
	return leaseAffectedOne(res, err)
}

// CheckLibraryRefreshLease is the read a refresh makes before each
// per-book unit of work, so a worker that was taken over stops at the
// next book rather than at its next renewal.
func (s *Store) CheckLibraryRefreshLease(
	ctx context.Context, libraryID, owner string, now time.Time,
) error {
	if libraryID == "" || owner == "" || now.IsZero() {
		return store.ErrInvalidTransition
	}
	var held int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM libraries
		  WHERE id = ? AND COALESCE(refresh_lease_owner, '') = ?
		    AND CAST(strftime('%s', refresh_lease_until) AS INTEGER)
		        > CAST(strftime('%s', ?) AS INTEGER)`,
		libraryID, owner, formatTime(now)).Scan(&held)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrRefreshLeaseLost
	}
	return err
}

// FinishLibraryRefresh records the outcome and releases the lease in one
// statement, which is what stops a dispossessed worker recording a
// success over the top of the one that replaced it.
func (s *Store) FinishLibraryRefresh(
	ctx context.Context, libraryID, owner string,
	at time.Time, code store.RefreshCode,
) error {
	if libraryID == "" || at.IsZero() || !code.Valid() {
		return store.ErrInvalidTransition
	}
	var res sql.Result
	var err error
	if code == store.RefreshCodeNone {
		res, err = s.db.ExecContext(ctx,
			`UPDATE libraries
			    SET last_refresh_at = ?, last_refresh_code = NULL,
			        refresh_lease_owner = NULL, refresh_lease_until = NULL
			  WHERE id = ? AND COALESCE(refresh_lease_owner, '') = ?`,
			formatTime(at), libraryID, owner)
	} else {
		res, err = s.db.ExecContext(ctx,
			`UPDATE libraries
			    SET last_refresh_code = ?,
			        refresh_lease_owner = NULL, refresh_lease_until = NULL
			  WHERE id = ? AND COALESCE(refresh_lease_owner, '') = ?`,
			string(code), libraryID, owner)
	}
	return leaseAffectedOne(res, err)
}

// leaseAffectedOne reads a lease-guarded update: no row means the lease
// is somebody else's now, which is not the same as a missing library and
// must not be reported as one.
func leaseAffectedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrRefreshLeaseLost
	}
	return nil
}

func (s *Store) AdminRequestLibraryRefresh(ctx context.Context, actorUserID, libraryID string, at time.Time) error {
	_ = actorUserID
	res, err := s.db.ExecContext(ctx,
		`UPDATE libraries SET refresh_requested_at = ?
		  WHERE id = ? AND root_path IS NOT NULL AND root_path <> ''`,
		formatTime(at), libraryID)
	return affectedOne(res, err)
}
