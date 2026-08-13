package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

// TestStore runs the shared backend suite against PostgreSQL. Skipped
// unless LISEUR_PG_TEST_DSN is set (point it at a throwaway database —
// the suite drops and recreates everything).
func TestStore(t *testing.T) {
	dsn := os.Getenv("LISEUR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("LISEUR_PG_TEST_DSN not set")
	}
	open := func(t *testing.T) store.Store {
		t.Helper()
		s, err := Open(dsn)
		if err != nil {
			t.Fatal(err)
		}
		reset(t, s)
		if err := s.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	storetest.Run(t, open)
}

// reset drops all tables so each test starts from an empty schema.
func reset(t *testing.T, s *Store) {
	t.Helper()
	tables := []string{
		"ingest_blob_holds",
		"reading_list_books", "reading_lists", "collection_books", "collections",
		"book_languages", "book_genres", "genres", "book_tags", "tags",
		"book_contributors", "contributors", "book_series", "series",
		"book_identifiers", "user_book_works", "ingest_jobs",
		"blob_reservations", "book_files", "books", "library_access", "libraries", "blobs",
		"session_supersessions", "session_tombstones", "session_rollups", "sessions", "ops", "aliases", "editions",
		"works", "seq_counters", "compaction_state", "kosync_devices",
		"koplugin_devices", "pairing_codes", "invites", "auth_sessions",
		"token_scopes", "tokens", "users", "schema_migrations",
	}
	for _, tbl := range tables {
		if _, err := s.db.ExecContext(context.Background(),
			`DROP TABLE IF EXISTS `+tbl+` CASCADE`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigration3MarksLegacyInference(t *testing.T) {
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
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, migration2); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	retained := now.Add(5 * time.Minute)
	ended := now.Add(10 * time.Minute)
	recent := now.Add(20 * time.Minute)
	rolledAt := now.Add(-48 * time.Hour)
	rolledDay := rolledAt.Format("2006-01-02")
	for _, version := range []int{1, 2} {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)`,
			version, now); err != nil {
			t.Fatal(err)
		}
	}
	setup := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users (id, name, argon2_hash, timezone, created_at)
			   VALUES ('u1', 'alice', 'x', 'UTC', $1)`, []any{now}},
		{`INSERT INTO works (id, user_id, title, author, pending, created_at)
			   VALUES ('w1', 'u1', '', '', FALSE, $1), ('w2', 'u1', '', '', FALSE, $1)`, []any{now}},
		{`INSERT INTO works (id, user_id, title, author, pending, created_at)
			   VALUES ('w3', 'u1', '', '', FALSE, $1), ('w4', 'u1', '', '', FALSE, $1)`, []any{now}},
		{`INSERT INTO ops (user_id, seq, op_id, work_id, device_id, client_ts,
			                  progression, origin, origin_alias, received_at)
			   VALUES ('u1', 1, 'legacy-op', 'w1', 'kosync:kobo', $1, 0.4,
			           'kosync', 'partial-md5:legacy', $1)`, []any{retained}},
		{`INSERT INTO ops (user_id, seq, op_id, work_id, device_id, client_ts,
			                  progression, origin, origin_alias, received_at)
			   VALUES ('u1', 2, 'recent-op', 'w1', 'kosync:kobo', $1, 0.5,
			           'kosync', 'partial-md5:legacy', $1)`, []any{recent}},
		{`INSERT INTO sessions (user_id, session_id, work_id, device_id, started_at,
			                       ended_at, start_prog, end_prog, idle_ms, origin, received_at)
			   VALUES ('u1', 'legacy-session', 'w1', 'kosync:kobo', $1, $2, 0.4, 0.4, 0,
			           'inferred', $2)`, []any{now, ended}},
		{`INSERT INTO ops (user_id, seq, op_id, work_id, device_id, client_ts,
			                  progression, origin, origin_alias, received_at)
			   VALUES ('u1', 3, 'rolled-op', 'w4', 'kosync:rolled', $1, 0.7,
			           'kosync', 'partial-md5:rolled', $1)`, []any{rolledAt}},
		{`INSERT INTO session_rollups
			   (user_id, work_id, day, active_seconds, pages, prog_delta, session_count)
			   VALUES ('u1', 'w3', $1, 60, 1, 0.1, 1)`, []any{rolledDay}},
		{`UPDATE ops SET work_id = 'w2' WHERE user_id = 'u1'`, nil},
	}
	for _, step := range setup {
		if _, err := s.db.ExecContext(ctx, step.query, step.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingInferenceOps(ctx, "u1")
	if err != nil || len(pending) != 2 {
		t.Fatalf("migration did not preserve ambiguous activity: %+v %v", pending, err)
	}
	pendingIDs := map[string]bool{pending[0].OpID: true, pending[1].OpID: true}
	if !pendingIDs["recent-op"] || !pendingIDs["rolled-op"] {
		t.Fatalf("wrong pending activity after migration: %+v", pending)
	}
	sessions, err := s.SessionsForWork(ctx, "u1", "w2", 10)
	if err != nil || len(sessions) != 1 || sessions[0].OriginAlias == nil ||
		*sessions[0].OriginAlias != "partial-md5:legacy" {
		t.Fatalf("legacy session provenance not backfilled: %+v %v", sessions, err)
	}
}
