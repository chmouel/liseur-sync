package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Store is the SQLite backend.
type Store struct {
	db *sql.DB
}

// timeFormat is the canonical timestamp encoding: RFC3339 in UTC with a
// fixed nine-digit fraction.
//
// The width is not cosmetic. SQLite compares TEXT byte by byte, and
// time.RFC3339Nano trims trailing zeros, so its output is
// variable-length: "…:00.5Z" sorts *after* "…:00.55Z" because 'Z'
// (0x5A) is greater than '5' (0x35), and after "…:00.9Z" for the same
// reason. Every `ORDER BY … _at` in this backend would then be subtly
// out of order for rows written within the same second — precisely
// what a bulk import produces. Padding the fraction makes byte order
// and chronological order the same thing.
const timeFormat = "2006-01-02T15:04:05.000000000Z07:00"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

// parseTime reads with RFC3339Nano, which accepts any fraction width, so
// rows written before the padding above still load.
func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }

// parseTimePtr reads a nullable timestamp column.
func parseTimePtr(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid {
		return nil, nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func formatTimePtr(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*t), Valid: true}
}

// Open opens (creating if needed) the SQLite database at path with WAL
// mode, foreign keys on, and a busy timeout so concurrent admin CLI
// access cooperates with a running server.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite is single-writer; one connection keeps the single-writer
	// queue explicit and avoids SQLITE_BUSY on intra-process overlap.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Migrate applies pending migrations under BEGIN IMMEDIATE — a real
// cross-process file lock that coordinates the server, the admin CLI,
// and concurrent starters. Failure aborts the transaction: the server
// never runs against a partially migrated schema.
//
// Foreign keys are switched off for the duration, which is what SQLite's
// own table-rebuild procedure requires and cannot be done inside a
// transaction. Two behaviours make it necessary: with foreign keys on, a
// table rename rewrites every *other* table's REFERENCES clauses to
// follow it — even with legacy_alter_table on — so a rebuild that moves
// the old table aside repoints the whole catalog at the scratch table;
// and DROP TABLE performs an
// implicit DELETE FROM that fires ON DELETE CASCADE, so dropping the
// table the books hang off would take the books with it. The
// enforcement given up is bought back by the foreign_key_check below,
// which runs before the commit and fails the upgrade rather than
// shipping a catalog with dangling references.
func (s *Store) Migrate(ctx context.Context) error {
	// The pragma is per connection, so the whole migration has to run on
	// one pinned connection rather than whatever the pool hands out.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	// Both flags are needed, and only together: with foreign keys on, a
	// rename rewrites other tables' REFERENCES clauses whatever this one
	// says, and with foreign keys off it is this one that decides.
	if _, err := conn.ExecContext(ctx,
		`PRAGMA legacy_alter_table = ON`); err != nil {
		return err
	}
	defer func() {
		// The connection goes back to a pool where every other caller
		// expects enforcement, so it is restored even when a migration
		// failed and even when the caller's context is already done.
		restore := context.WithoutCancel(ctx)
		_, _ = conn.ExecContext(restore, `PRAGMA legacy_alter_table = OFF`)
		_, _ = conn.ExecContext(restore, `PRAGMA foreign_keys = ON`)
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Bootstrap the migrations table itself.
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var applied int
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&applied)
	if err != nil {
		return err
	}
	for i := applied; i < len(migrations); i++ {
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			i+1, formatTime(time.Now())); err != nil {
			return err
		}
	}
	if err := checkForeignKeys(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// checkForeignKeys refuses a migration that left a dangling reference.
// PRAGMA foreign_key_check reports violations as rows rather than as an
// error, so a migration cannot catch this itself by running it.
func checkForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var table any
		var rowid any
		var parent any
		var fkid any
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return fmt.Errorf("migration left a dangling foreign key")
		}
		return fmt.Errorf(
			"migration left a dangling foreign key: %v references %v",
			table, parent)
	}
	return rows.Err()
}

// --- helpers ---

func nullStr(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func isUniqueErr(err error) bool {
	return err != nil && (errors.Is(err, store.ErrConflict) ||
		// modernc.org/sqlite wraps constraint errors; match on message.
		contains(err.Error(), "UNIQUE constraint failed") ||
		contains(err.Error(), "constraint failed"))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// scanUser scans one row of the users table.
func scanUser(row interface{ Scan(...any) error }) (store.User, error) {
	var u store.User
	var tz string
	var kosync, koplugin, isAdmin int
	var created string
	var disabled sql.NullString
	err := row.Scan(&u.ID, &u.Name, &u.Argon2Hash, &tz, &kosync, &koplugin,
		&isAdmin, &disabled, &created)
	if err != nil {
		return u, err
	}
	u.Timezone = tz
	u.KosyncEnabled = kosync != 0
	u.KopluginEnabled = koplugin != 0
	u.IsAdmin = isAdmin != 0
	if disabled.Valid {
		t, err := parseTime(disabled.String)
		if err != nil {
			return u, err
		}
		u.DisabledAt = &t
	}
	u.CreatedAt, err = parseTime(created)
	return u, err
}

// scanToken scans one row of the tokens table.
func scanToken(row interface{ Scan(...any) error }) (store.Token, error) {
	var t store.Token
	var created string
	var expires, lastUsed, revoked sql.NullString
	err := row.Scan(&t.ID, &t.UserID, &t.DeviceID, &t.Name,
		&t.SHA256, &created, &expires, &lastUsed, &revoked)
	if err != nil {
		return t, err
	}
	if t.CreatedAt, err = parseTime(created); err != nil {
		return t, err
	}
	parsePtr := func(ns sql.NullString) (*time.Time, error) {
		if !ns.Valid {
			return nil, nil
		}
		tm, err := parseTime(ns.String)
		if err != nil {
			return nil, err
		}
		return &tm, nil
	}
	if t.ExpiresAt, err = parsePtr(expires); err != nil {
		return t, err
	}
	if t.LastUsed, err = parsePtr(lastUsed); err != nil {
		return t, err
	}
	t.RevokedAt, err = parsePtr(revoked)
	return t, err
}

// scanOp scans one row of the ops table.
func scanOp(row interface{ Scan(...any) error }) (store.Op, error) {
	var o store.Op
	var editionSHA, foreignPos, originAlias sql.NullString
	var clientTS, receivedAt string
	err := row.Scan(&o.UserID, &o.Seq, &o.OpID, &o.WorkID, &editionSHA,
		&o.DeviceID, &clientTS, &o.Progression, &o.LocatorJSON,
		&foreignPos, &o.Origin, &originAlias, &receivedAt)
	if err != nil {
		return o, err
	}
	if editionSHA.Valid {
		o.EditionSHA = &editionSHA.String
	}
	if foreignPos.Valid {
		o.ForeignPos = &foreignPos.String
	}
	if originAlias.Valid {
		o.OriginAlias = &originAlias.String
	}
	if o.ClientTS, err = parseTime(clientTS); err != nil {
		return o, err
	}
	o.ReceivedAt, err = parseTime(receivedAt)
	return o, err
}
