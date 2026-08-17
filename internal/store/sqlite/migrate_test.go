package sqlite

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestMigrateUpgradesAnOlderDatabase. A database that applied the
// baseline before the claim columns existed must gain them on the next
// start. This is the case a fresh-database test cannot see: every
// migration runs on a new file, so a schema change made by editing the
// baseline passes the suite and leaves a running deployment behind,
// answering every catalog read with an error about a missing column.
func TestMigrateUpgradesAnOlderDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	// Stand up the database as an older release left it: the baseline,
	// recorded as applied, and nothing after it.
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (1, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrating an older database: %v", err)
	}

	for _, column := range []string{"deleted_at", "client_ts", "request_hash"} {
		var count int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info('book_series_overrides')
			 WHERE name = ?`, column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("book_series_overrides.%s is missing after the upgrade", column)
		}
	}

	var applied int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != len(migrations) {
		t.Errorf("applied migration %d, want %d", applied, len(migrations))
	}

	// Idempotent: a second start must not try to add the columns again.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrating an already-current database: %v", err)
	}
}

// TestBaselineIsFrozen. Migration 1 is the baseline, and it has shipped:
// a database that applied it will never apply it again, so a column
// added to it reaches new installations and no existing one. That is not
// a hypothetical — it is how a deployment came to answer every catalog
// read with an error about a missing column. Adding to the baseline
// changes this digest; when it does, move the change into a new entry in
// `migrations` and update the digest here in the same commit. The one
// edit that may move the digest on its own is a comment: it is never
// executed against anything, so a database that applied the baseline and
// one created today are still the same database.
func TestBaselineIsFrozen(t *testing.T) {
	const want = "ba6812a1e7ee24357d2dd386e967ac336ce70adad12ca7d97b161b433a0d0982"
	got := fmt.Sprintf("%x", sha256.Sum256([]byte(schema)))
	if got != want {
		t.Fatalf("the baseline changed (%s).\n"+
			"It has shipped: append a migration instead of editing it.", got)
	}
	if migrations[0] != schema {
		t.Fatal("migrations[0] is no longer the baseline")
	}
}

var _ store.Store = (*Store)(nil)
