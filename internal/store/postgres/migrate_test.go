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
	created := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO users
		(id, name, argon2_hash, created_at) VALUES ('old-user', 'old-user', 'x', $1)`, created); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO folders
		(id, name, root_path, kind, created_at, updated_at)
		VALUES ('old-folder', 'Old', '/old', 'plain', $1, $1)`, created); err != nil {
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
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_folders`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 1 {
		t.Fatalf("a second migration changed the grants to %d, want 1", grants)
	}
}

// TestBackfillLeavesAConfiguredServerAlone. See the SQLite copy for the
// argument: migration 6 repairs a server with no grants at all and has
// no opinion about one somebody has configured.
func TestBackfillLeavesAConfiguredServerAlone(t *testing.T) {
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

	for _, tc := range []struct {
		name string
		seed string
		want int
	}{
		{name: "nothing assigned is the broken state and is repaired", want: 4},
		{
			name: "one deliberate grant means hands off",
			seed: `INSERT INTO user_folders VALUES ('u1', 'f1')`,
			want: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reset(t, s)
			for i, m := range migrations[:len(migrations)-2] {
				if _, err := s.db.ExecContext(ctx, m); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.ExecContext(ctx,
					`INSERT INTO schema_migrations(version, applied_at) VALUES ($1, $2)`,
					i+1, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC()
			for _, id := range []string{"u1", "u2"} {
				if _, err := s.db.ExecContext(ctx, `INSERT INTO users
					(id, name, argon2_hash, created_at) VALUES ($1, $1, 'x', $2)`,
					id, now); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.ExecContext(ctx,
				`UPDATE users SET disabled_at = $1 WHERE id = 'u2'`, now); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"f1", "f2"} {
				if _, err := s.db.ExecContext(ctx, `INSERT INTO folders
					(id, name, root_path, kind, created_at, updated_at)
					VALUES ($1, $1, $2, 'plain', $3, $3)`, id, "/"+id, now); err != nil {
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
		})
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
