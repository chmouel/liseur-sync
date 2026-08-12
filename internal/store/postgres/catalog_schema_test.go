package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func TestIngestJobRejectsPartialBookReference(t *testing.T) {
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
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.CreateUser(ctx, store.User{
		ID: "u1", Name: "alice", Argon2Hash: "x", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLibrary(ctx, store.Library{
		ID: "lib1", OwnerUserID: "u1", QuotaUserID: "u1",
		Kind: store.LibraryManaged, Name: "Library", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ingest_jobs
		 (id, user_id, library_id, quota_user_id, source, state,
		  book_library_id, book_id, created_at, updated_at)
		 VALUES ('job1', 'u1', 'lib1', 'u1', 'upload', 'received',
		         NULL, 'missing-book', $1, $1)`,
		now); err == nil {
		t.Fatal("ingest job accepted a partial dangling book reference")
	}
}

func TestSplitMovesMissingCatalogMapping(t *testing.T) {
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
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	user := store.User{ID: "u1", Name: "alice", Argon2Hash: "x", CreatedAt: now}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	root := "/srv/books"
	library := store.Library{
		ID: "lib1", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Kind: store.LibraryWatched, Name: "Library", RootPath: &root, CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	book := store.CatalogBook{
		ID: "book1", LibraryID: library.ID, Status: store.BookMissing,
		Title: "Missing Book", TitleSource: store.MetadataEmbedded, CreatedAt: now,
	}
	if err := s.CreateCatalogBook(ctx, user.ID, book); err != nil {
		t.Fatal(err)
	}
	oldWork := store.Work{ID: "work1", UserID: user.ID, Title: book.Title, CreatedAt: now}
	result, err := s.ResolveCatalogBookWork(ctx, user.ID, book.ID, oldWork,
		[]store.Edition{{UserID: user.ID, SHA256: "book-sha", WorkID: oldWork.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "book-sha"}}, false, now)
	if err != nil || result.WorkID != oldWork.ID {
		t.Fatalf("catalog resolution: %+v %v", result, err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO blobs (sha256, size_bytes, created_at) VALUES ($1, 1, $2)`,
		"book-sha", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO book_files
		 (id, library_id, book_id, blob_sha256, source, source_relative_path,
		  availability, created_at, updated_at)
		 VALUES ('file1', $1, $2, 'book-sha', 'watched', 'missing.epub',
		         'missing', $3, $3)`,
		library.ID, book.ID, now); err != nil {
		t.Fatal(err)
	}
	newWork := store.Work{ID: "work2", UserID: user.ID, Title: book.Title, CreatedAt: now}
	if err := s.SplitWork(ctx, user.ID, oldWork.ID, "book-sha", nil, newWork); err != nil {
		t.Fatal(err)
	}
	mapping, err := s.UserBookWork(ctx, user.ID, book.ID)
	if err != nil || mapping.WorkID != newWork.ID {
		t.Fatalf("missing file mapping after split: %+v %v", mapping, err)
	}
	aliases, err := s.ResolveAliases(ctx, user.ID, []store.Identifier{
		{Kind: "sha256", Value: "book-sha"},
		{Kind: "source", Value: "liseur-sync:" + book.ID},
	})
	if err != nil ||
		aliases["sha256:book-sha"] != newWork.ID ||
		aliases["source:liseur-sync:"+book.ID] != newWork.ID {
		t.Fatalf("catalog aliases after split: %v %v", aliases, err)
	}
}
