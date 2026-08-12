package sqlite

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMigration5BackfillsTokenScopes(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "token-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := t.Context()
	for i, migration := range []string{schema, migration2, migration3, migration4} {
		if _, err := s.db.ExecContext(ctx, migration); err != nil {
			t.Fatalf("apply migration %d: %v", i+1, err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			i+1, formatTime(time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	now := formatTime(time.Now())
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, name, argon2_hash, created_at) VALUES ('u1', 'alice', 'x', ?)`,
		now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (id, user_id, device_id, name, scope, sha256, created_at)
		 VALUES ('t1', 'u1', 'd1', 'legacy', 'read-insights', 'legacy-hash', ?)`,
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
		 VALUES ('t2', 'u1', 'd2', 'rolling-old', 'sync', 'rolling-hash', ?)`,
		now); err != nil {
		t.Fatal(err)
	}
	tok, err = s.TokenByHash(ctx, "u1", "rolling-hash")
	if err != nil || tok.Scopes.String() != "sync" {
		t.Fatalf("rolling-downgrade token: %+v %v", tok, err)
	}
}
