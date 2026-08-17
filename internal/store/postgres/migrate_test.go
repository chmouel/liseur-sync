package postgres

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestMigrateUpgradesAnOlderDatabase. The case a fresh-database test
// cannot see: a database that applied the baseline before a column
// existed must gain it on the next start, or the deployment comes back
// up and answers reads with an error about a missing column.
func TestMigrateUpgradesAnOlderDatabase(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	ctx := context.Background()
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	reset(t, s)

	// Stand the database up as an older release left it.
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (1, $1)`,
		time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrating an older database: %v", err)
	}

	for _, column := range []string{"deleted_at", "client_ts", "request_hash"} {
		var count int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM information_schema.columns
			 WHERE table_name = 'book_series_overrides' AND column_name = $1`,
			column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("book_series_overrides.%s is missing after the upgrade", column)
		}
	}

	// Idempotent: a second start must not try to add the columns again.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrating an already-current database: %v", err)
	}
}

// TestBaselineIsFrozen. See the SQLite copy: migration 1 has shipped, so
// a column added to it reaches new installations and no existing one.
// When this digest changes, move the change into a new entry in
// `migrations` and update the digest in the same commit.
func TestBaselineIsFrozen(t *testing.T) {
	const want = "46f6fb4468968b615052212879c831e3823a3ee28797e43b40ae35f73d5d9afd"
	got := fmt.Sprintf("%x", sha256.Sum256([]byte(schema)))
	if got != want {
		t.Fatalf("the baseline changed (%s).\n"+
			"It has shipped: append a migration instead of editing it.", got)
	}
	if migrations[0] != schema {
		t.Fatal("migrations[0] is no longer the baseline")
	}
}
