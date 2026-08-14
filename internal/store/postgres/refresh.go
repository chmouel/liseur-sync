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

// ClaimLibraryRefresh takes the next due library in a single statement
// and leases it. FOR UPDATE SKIP LOCKED is what makes two servers
// sharing one database safe *for the claim*: the second one steps over
// the row the first is taking rather than waiting for it and then
// sweeping the same root.
//
// The row lock ends with the statement, so it is not what keeps the two
// apart for the length of the refresh — the lease is. A library another
// worker still holds is not claimed, however overdue it is, and a lease
// left behind by a killed process stops holding it when it expires.
func (s *Store) ClaimLibraryRefresh(
	ctx context.Context, now time.Time, lease store.RefreshLease,
) (store.Library, bool, error) {
	if !lease.Valid() || now.IsZero() || !lease.Until.After(now) {
		return store.Library{}, false, store.ErrInvalidTransition
	}
	lib, err := scanPlainLibrary(s.db.QueryRowContext(ctx, q(
		`UPDATE libraries SET last_refresh_attempt_at = ?,
		                      refresh_requested_at = NULL,
		                      refresh_lease_owner = ?,
		                      refresh_lease_until = ?
		  WHERE id = (
		      SELECT id FROM libraries
		       WHERE source <> 'managed'
		         AND root_path IS NOT NULL AND root_path <> ''
		         AND (refresh_lease_until IS NULL OR refresh_lease_until <= ?)
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
		  RETURNING `+plainLibraryColumns),
		now, lease.Owner, lease.Until, now, now))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Library{}, false, nil
	}
	if err != nil {
		return store.Library{}, false, err
	}
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
	res, err := s.db.ExecContext(ctx, q(
		`UPDATE libraries SET refresh_lease_until = ?
		  WHERE id = ? AND COALESCE(refresh_lease_owner, '') = ?
		    AND refresh_lease_until > ?`),
		lease.Until, libraryID, lease.Owner, now)
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
	err := s.db.QueryRowContext(ctx, q(
		`SELECT 1 FROM libraries
		  WHERE id = ? AND COALESCE(refresh_lease_owner, '') = ?
		    AND refresh_lease_until > ?`),
		libraryID, owner, now).Scan(&held)
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
		res, err = s.db.ExecContext(ctx, q(
			`UPDATE libraries
			    SET last_refresh_at = ?, last_refresh_code = NULL,
			        refresh_lease_owner = NULL, refresh_lease_until = NULL
			  WHERE id = ? AND COALESCE(refresh_lease_owner, '') = ?`),
			at, libraryID, owner)
	} else {
		res, err = s.db.ExecContext(ctx, q(
			`UPDATE libraries
			    SET last_refresh_code = ?,
			        refresh_lease_owner = NULL, refresh_lease_until = NULL
			  WHERE id = ? AND COALESCE(refresh_lease_owner, '') = ?`),
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
	res, err := s.db.ExecContext(ctx, q(
		`UPDATE libraries SET refresh_requested_at = ?
		  WHERE id = ? AND root_path IS NOT NULL AND root_path <> ''`),
		at, libraryID)
	return affectedOne(res, err)
}
