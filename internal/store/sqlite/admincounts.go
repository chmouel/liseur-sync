package sqlite

import (
	"context"
	"database/sql"

	"github.com/chmouel/liseur-sync/internal/store"
)

// AdminCounts collects the panel's aggregate numbers. Every query here
// is a COUNT over a whole table: nothing it returns can identify a user,
// a book or a path, which is the property that lets an instance
// administrator see it (ADR-0013).
func (s *Store) AdminCounts(ctx context.Context) (store.AdminCounts, error) {
	var c store.AdminCounts
	c.BooksByStatus = map[string]int{}
	c.FoldersByKind = map[string]int{}

	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(is_admin), 0),
		       COALESCE(SUM(CASE WHEN disabled_at IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM users`).Scan(&c.Users, &c.AdminUsers, &c.DisabledUsers)
	if err != nil {
		return c, err
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM folders`).Scan(&c.Folders); err != nil {
		return c, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT kind, COUNT(*) FROM folders GROUP BY kind`)
	if err != nil {
		return c, err
	}
	if err := scanCountsByKey(rows, c.FoldersByKind); err != nil {
		return c, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM books GROUP BY status`)
	if err != nil {
		return c, err
	}
	return c, scanCountsByKey(rows, c.BooksByStatus)
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
