package postgres

import (
	"os"
	"testing"
	"time"
)

func TestMigration5BackfillsTokenScopes(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	reset(t, s)
	ctx := t.Context()
	for i, migration := range []string{schema, migration2, migration3, migration4} {
		if _, err := s.db.ExecContext(ctx, migration); err != nil {
			t.Fatalf("apply migration %d: %v", i+1, err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)`,
			i+1, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, name, argon2_hash, created_at) VALUES ('u1', 'alice', 'x', $1)`,
		now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (id, user_id, device_id, name, scope, sha256, created_at)
		 VALUES ('t1', 'u1', 'd1', 'legacy', 'read-insights', 'legacy-hash', $1)`,
		now); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	tok, err := s.TokenByHash(ctx, "u1", "legacy-hash")
	if err != nil || tok.Scopes.String() != "read-insights" {
		t.Fatalf("backfilled token: %+v %v", tok, err)
	}

	// A rolling downgrade remains safe: an older binary can still insert
	// through the legacy scalar column after migration 5.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (id, user_id, device_id, name, scope, sha256, created_at)
		 VALUES ('t2', 'u1', 'd2', 'rolling-old', 'sync', 'rolling-hash', $1)`,
		now); err != nil {
		t.Fatal(err)
	}
	tok, err = s.TokenByHash(ctx, "u1", "rolling-hash")
	if err != nil || tok.Scopes.String() != "sync" {
		t.Fatalf("rolling-downgrade token: %+v %v", tok, err)
	}
}
