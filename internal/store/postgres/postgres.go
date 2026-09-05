// Package postgres implements the store.Store interface against
// PostgreSQL using pgx via database/sql.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Store is the Postgres backend.
type Store struct {
	store.Notifications
	db *sql.DB
}

// Open connects to Postgres at the given DSN.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Migrate applies pending migrations under a transaction-scoped
// advisory lock held on a dedicated connection for the whole run, so
// concurrent starters serialize. Failure aborts: the server never runs
// against a partially migrated schema.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(727499)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL)`); err != nil {
		return err
	}
	var applied int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&applied); err != nil {
		return err
	}
	for i := applied; i < int64(len(migrations)); i++ {
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)`,
			i+1, time.Now().UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// q converts ? placeholders to $N for Postgres.
func q(query string) string {
	out := make([]byte, 0, len(query)+8)
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			out = append(out, '$')
			out = append(out, []byte(fmt.Sprintf("%d", n))...)
			continue
		}
		out = append(out, query[i])
	}
	return string(out)
}

func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}

var _ = store.ErrConflict

// tsEqual compares timestamps at timestamptz precision (microseconds),
// since PG truncates nanoseconds on write.
func tsEqual(a, b time.Time) bool {
	return a.UTC().Truncate(time.Microsecond).Equal(b.UTC().Truncate(time.Microsecond))
}

// withTx runs fn in a transaction, rolling back on any error. It exists
// because every multi-statement write in this backend wants the same
// three lines and getting one of them wrong leaks a connection.
func (s *Store) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// nullStr binds an optional string, so an absent value is SQL NULL
// rather than the empty string, which means something different.
func nullStr(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

// The compiler, not a reviewer, is what keeps the two backends in step
// with the interface.
var _ store.Store = (*Store)(nil)
