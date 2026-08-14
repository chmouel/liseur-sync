package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLibraryAxesMigrationKeepsExistingLibraries pins the one migration
// in this schema that rebuilds a table other tables point at. SQLite
// rewrites foreign key references when a table is renamed, so a rebuild
// done the obvious way silently repoints the whole catalog at the
// scratch table; the only way to know it did not is to migrate a
// database that already holds a library with books in it.
func TestLibraryAxesMigrationKeepsExistingLibraries(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()

	// Migrate to the version before the axes split, then populate it the
	// way a running instance would have.
	before := len(migrations) - 1
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE TABLE schema_migrations (
		   version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	now := formatTime(time.Now().UTC())
	for i := 0; i < before; i++ {
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			i+1, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, name, argon2_hash, created_at)
		 VALUES ('u1', 'alice', 'x', ?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO libraries
		   (id, owner_user_id, quota_user_id, kind, name, root_path,
		    created_at, updated_at)
		 VALUES ('lib-managed', 'u1', 'u1', 'managed', 'Uploads', NULL, ?, ?),
		        ('lib-watched', 'u1', 'u1', 'watched', 'Shelf', '/srv/books', ?, ?)`,
		now, now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO books (library_id, id, title, status, created_at, updated_at)
		 VALUES ('lib-watched', 'b1', 'A book', 'active', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("axes migration: %v", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, source, storage, refresh, refresh_interval_seconds
		 FROM libraries ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type axes struct{ source, storage, refresh string }
	got := map[string]axes{}
	for rows.Next() {
		var id string
		var a axes
		var seconds int64
		if err := rows.Scan(&id, &a.source, &a.storage, &a.refresh, &seconds); err != nil {
			t.Fatal(err)
		}
		if seconds <= 0 {
			t.Fatalf("%s migrated with a refresh interval of %d", id, seconds)
		}
		got[id] = a
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]axes{
		"lib-managed": {"managed", "cas", "manual"},
		"lib-watched": {"directory", "cas", "interval"},
	}
	for id, w := range want {
		if got[id] != w {
			t.Fatalf("%s migrated to %+v, want %+v", id, got[id], w)
		}
	}

	// The book must still be attached to a library that exists, which is
	// exactly what a rewritten foreign key would have broken.
	var refs string
	if err := s.db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'books'`).
		Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(refs, "libraries_new") ||
		strings.Contains(refs, "libraries_old") {
		t.Fatalf("books now references a scratch table:\n%s", refs)
	}
	var violations int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil {
		t.Fatal(err)
	}
	if violations != 0 {
		t.Fatalf("%d foreign key violations after the rebuild", violations)
	}
	var books int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM books WHERE library_id = 'lib-watched'`).
		Scan(&books); err != nil {
		t.Fatal(err)
	}
	if books != 1 {
		t.Fatalf("the migrated library holds %d books, want 1", books)
	}
}
