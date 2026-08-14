package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// terminalJobStates are the ingest states a job never leaves. The
// overview reports the age of the oldest job in every *other* state,
// because a promoted job from last year is history while a staged one
// from last year is a stuck worker.
var terminalJobStates = map[string]bool{
	"promoted":    true,
	"failed":      true,
	"quarantined": true,
}

// AdminCounts collects the panel's aggregate numbers. Every query here
// is a COUNT, a SUM or a MIN over a whole table: nothing it returns can
// identify a user, a book or a path, which is the property that lets an
// instance administrator see it (ADR-0013).
func (s *Store) AdminCounts(ctx context.Context) (store.AdminCounts, error) {
	var c store.AdminCounts
	c.BooksByStatus = map[string]int{}
	c.JobsByState = map[string]int{}
	c.OldestJobByState = map[string]time.Time{}

	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(is_admin), 0),
		       COALESCE(SUM(CASE WHEN disabled_at IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM users`).Scan(&c.Users, &c.AdminUsers, &c.DisabledUsers)
	if err != nil {
		return c, err
	}

	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN kind = 'managed' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN kind = 'watched' THEN 1 ELSE 0 END), 0)
		FROM libraries`).Scan(&c.Libraries, &c.ManagedLibraries, &c.WatchedLibraries)
	if err != nil {
		return c, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM books GROUP BY status`)
	if err != nil {
		return c, err
	}
	if err := scanCountsByKey(rows, c.BooksByStatus); err != nil {
		return c, err
	}

	var nextExpiry sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT MIN(trash_expires_at) FROM books
		WHERE status = 'trashed' AND trash_expires_at IS NOT NULL`).Scan(&nextExpiry)
	if err != nil {
		return c, err
	}
	if nextExpiry.Valid {
		t, err := parseTime(nextExpiry.String)
		if err != nil {
			return c, err
		}
		c.TrashNextExpiry = &t
	}

	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(size_bytes), 0),
		       COALESCE(SUM(CASE WHEN orphaned_at IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM blobs`).Scan(&c.Blobs, &c.BlobBytes, &c.OrphanBlobs)
	if err != nil {
		return c, err
	}
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blob_reservations`).Scan(&c.BlobsPending)
	if err != nil {
		return c, err
	}

	rows, err = s.db.QueryContext(ctx,
		`SELECT state, COUNT(*), MIN(created_at) FROM ingest_jobs GROUP BY state`)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var state, oldest string
		var n int
		if err := rows.Scan(&state, &n, &oldest); err != nil {
			return c, err
		}
		c.JobsByState[state] = n
		if terminalJobStates[state] {
			continue
		}
		t, err := parseTime(oldest)
		if err != nil {
			return c, err
		}
		c.OldestJobByState[state] = t
	}
	return c, rows.Err()
}

func scanCountsByKey(rows *sql.Rows, into map[string]int) error {
	defer rows.Close()
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return err
		}
		into[key] = n
	}
	return rows.Err()
}
