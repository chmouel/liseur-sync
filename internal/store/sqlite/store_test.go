package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

func openStore(t *testing.T) store.Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestStore runs the shared backend suite.
func TestStore(t *testing.T) {
	storetest.Run(t, openStore)
}

func (s *Store) InsertBookFileForTest(ctx context.Context, file store.BookFile, sizeBytes int64) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO blobs (sha256, size_bytes, created_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(sha256) DO NOTHING`,
		file.BlobSHA256, sizeBytes, formatTime(file.CreatedAt)); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO book_files
		 (id, library_id, book_id, blob_sha256, source,
		  source_relative_path, original_filename, media_type,
		  partial_md5, dc_identifier, availability, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		file.ID, file.LibraryID, file.BookID, file.BlobSHA256,
		string(file.Source), file.SourceRelativePath, file.OriginalFilename,
		file.MediaType, file.PartialMD5, file.DCIdentifier,
		string(file.Availability), formatTime(file.CreatedAt),
		formatTime(file.UpdatedAt))
	return err
}

func TestMigration3MarksLegacyInference(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := t.Context()
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, migration2); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	retained := formatTime(now.Add(5 * time.Minute))
	ended := formatTime(now.Add(10 * time.Minute))
	recent := formatTime(now.Add(20 * time.Minute))
	rolledAt := formatTime(now.Add(-48 * time.Hour))
	rolledDay := now.Add(-48 * time.Hour).Format("2006-01-02")
	for _, version := range []int{1, 2} {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, formatTime(now)); err != nil {
			t.Fatal(err)
		}
	}
	formatted := formatTime(now)
	setup := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users (id, name, argon2_hash, timezone, created_at)
		   VALUES ('u1', 'alice', 'x', 'UTC', ?)`, []any{formatted}},
		{`INSERT INTO works (id, user_id, title, author, pending, created_at)
		   VALUES ('w1', 'u1', '', '', 0, ?), ('w2', 'u1', '', '', 0, ?)`, []any{formatted, formatted}},
		{`INSERT INTO works (id, user_id, title, author, pending, created_at)
		   VALUES ('w3', 'u1', '', '', 0, ?), ('w4', 'u1', '', '', 0, ?)`, []any{formatted, formatted}},
		{`INSERT INTO ops (user_id, seq, op_id, work_id, device_id, client_ts,
		                  progression, origin, origin_alias, received_at)
		   VALUES ('u1', 1, 'legacy-op', 'w1', 'kosync:kobo', ?, 0.4,
		           'kosync', 'partial-md5:legacy', ?)`, []any{retained, retained}},
		{`INSERT INTO ops (user_id, seq, op_id, work_id, device_id, client_ts,
		                  progression, origin, origin_alias, received_at)
		   VALUES ('u1', 2, 'recent-op', 'w1', 'kosync:kobo', ?, 0.5,
		           'kosync', 'partial-md5:legacy', ?)`, []any{recent, recent}},
		{`INSERT INTO sessions (user_id, session_id, work_id, device_id, started_at,
		                       ended_at, start_prog, end_prog, idle_ms, origin, received_at)
		   VALUES ('u1', 'legacy-session', 'w1', 'kosync:kobo', ?, ?, 0.4, 0.4, 0,
		           'inferred', ?)`, []any{formatted, ended, ended}},
		{`INSERT INTO ops (user_id, seq, op_id, work_id, device_id, client_ts,
		                  progression, origin, origin_alias, received_at)
		   VALUES ('u1', 3, 'rolled-op', 'w4', 'kosync:rolled', ?, 0.7,
		           'kosync', 'partial-md5:rolled', ?)`, []any{rolledAt, rolledAt}},
		{`INSERT INTO session_rollups
		   (user_id, work_id, day, active_seconds, pages, prog_delta, session_count)
		   VALUES ('u1', 'w3', ?, 60, 1, 0.1, 1)`, []any{rolledDay}},
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
