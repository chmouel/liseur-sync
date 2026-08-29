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
	created := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO users
		(id, name, argon2_hash, created_at) VALUES ('old-user', 'old-user', 'x', ?)`, created); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO folders
		(id, name, root_path, kind, created_at, updated_at)
		VALUES ('old-folder', 'Old', '/old', 'plain', ?, ?)`, created, created); err != nil {
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
	var grants int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_folders`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 1 {
		t.Fatalf("migration 6 granted %d folders, want the one it found "+
			"(one account, one folder, no grants: issue #13's state)", grants)
	}

	// Idempotent: a second start must not try to add the columns again.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrating an already-current database: %v", err)
	}
}

// TestBackfillLeavesAConfiguredServerAlone. Migration 6 repairs a server
// that migration 4 left with no grants at all. A server where somebody
// has assigned anything is somebody's decision, and the backfill must
// not have an opinion about it — least of all one that hands a second
// reader a library they were never given.
func TestBackfillLeavesAConfiguredServerAlone(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		seed  string
		want  int
		grant []string
	}{
		{
			name: "nothing assigned is the broken state and is repaired",
			want: 4, // two accounts times two folders
		},
		{
			name:  "one deliberate grant means hands off",
			seed:  `INSERT INTO user_folders VALUES ('u1', 'f1')`,
			want:  1,
			grant: []string{"u1|f1"},
		},
		{
			name: "a split library stays split",
			seed: `INSERT INTO user_folders VALUES ('u1', 'f1'), ('u2', 'f2')`,
			want: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "old.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			// Everything up to but not including the backfill, which is
			// where a server running the broken image sits.
			for i, m := range migrations[:len(migrations)-1] {
				if _, err := s.db.ExecContext(ctx, m); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.ExecContext(ctx,
					`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
					i+1, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			for _, id := range []string{"u1", "u2"} {
				if _, err := s.db.ExecContext(ctx, `INSERT INTO users
					(id, name, argon2_hash, created_at) VALUES (?, ?, 'x', ?)`,
					id, id, now); err != nil {
					t.Fatal(err)
				}
			}
			// u2 is disabled on purpose: it still gets its grants back,
			// because disabling refuses its credentials anyway and
			// leaving it out would empty its library on the day somebody
			// enables it again.
			if _, err := s.db.ExecContext(ctx,
				`UPDATE users SET disabled_at = ? WHERE id = 'u2'`, now); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"f1", "f2"} {
				if _, err := s.db.ExecContext(ctx, `INSERT INTO folders
					(id, name, root_path, kind, created_at, updated_at)
					VALUES (?, ?, ?, 'plain', ?, ?)`,
					id, id, "/"+id, now, now); err != nil {
					t.Fatal(err)
				}
			}
			if tc.seed != "" {
				if _, err := s.db.ExecContext(ctx, tc.seed); err != nil {
					t.Fatal(err)
				}
			}

			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			var grants int
			if err := s.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM user_folders`).Scan(&grants); err != nil {
				t.Fatal(err)
			}
			if grants != tc.want {
				t.Fatalf("after the backfill there are %d grants, want %d", grants, tc.want)
			}
			for _, pair := range tc.grant {
				var one int
				if err := s.db.QueryRowContext(ctx,
					`SELECT 1 FROM user_folders WHERE user_id || '|' || folder_id = ?`,
					pair).Scan(&one); err != nil {
					t.Fatalf("the grant %q did not survive: %v", pair, err)
				}
			}
			// Running it again changes nothing, whichever branch it took.
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			var again int
			if err := s.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM user_folders`).Scan(&again); err != nil {
				t.Fatal(err)
			}
			if again != tc.want {
				t.Fatalf("a second migration changed the grants to %d, want %d", again, tc.want)
			}
		})
	}
}

// TestBackfillIsANoOpOnAFreshDatabase. A new installation has no
// accounts and no folders when the slice runs, so the backfill has
// nothing to find and must leave no trace. A folder added afterwards
// gets the grant its creator asked for and nothing else.
func TestBackfillIsANoOpOnAFreshDatabase(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var grants int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_folders`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 0 {
		t.Fatalf("a fresh database came up with %d grants, want none", grants)
	}
}

// TestBaselineIsFrozen. Migration 1 is the baseline, and it has shipped:
// a database that applied it will never apply it again, so a column
// added to it reaches new installations and no existing one. That is not
// a hypothetical — it is how a deployment came to answer every catalog
// read with an error about a missing column. Adding to the baseline
// changes this digest; when it does, move the change into a new entry in
// `migrations` and update the digest here in the same commit.
func TestBaselineIsFrozen(t *testing.T) {
	const want = "9127085c2e862ecaa9e4ebd335044c62c79f86691f99a1edf58e6f7f9cce479e"
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
