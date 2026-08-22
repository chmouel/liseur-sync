package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestSplitMovesMissingCatalogMapping. A book whose file went missing is
// still a book a reader has a position in, so splitting its work must
// carry the catalog mapping across. Nothing about the file being gone
// changes who the reading history belongs to.
func TestSplitMovesMissingCatalogMapping(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	splitMovesMissingCatalogMapping(t, s)
}

func splitMovesMissingCatalogMapping(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	user := store.User{ID: "u1", Name: "alice", Argon2Hash: "x", CreatedAt: now}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	folder := store.Folder{
		ID: "f1", Name: "Books", RootPath: "/srv/books",
		Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateFolder(ctx, folder); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignUserFolder(ctx, user.ID, folder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileFolder(ctx, folder.ID, []store.ObservedBook{{
		RelativePath:     "missing.epub",
		SizeBytes:        1,
		MTime:            now,
		ContentSHA256:    "book-sha",
		OriginalFilename: "missing.epub",
		MediaType:        "application/epub+zip",
		Title:            "Missing Book",
	}}, true, now); err != nil {
		t.Fatal(err)
	}
	known, err := s.BooksInFolder(ctx, folder.ID)
	if err != nil || len(known) != 1 {
		t.Fatalf("BooksInFolder: %+v %v", known, err)
	}
	bookID := known[0].ID

	oldWork := store.Work{ID: "work1", UserID: user.ID, Title: "Missing Book", CreatedAt: now}
	result, err := s.ResolveCatalogBookWork(ctx, user.ID, bookID, oldWork,
		[]store.Edition{{UserID: user.ID, SHA256: "book-sha", WorkID: oldWork.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "book-sha"}}, false, now)
	if err != nil || result.WorkID != oldWork.ID {
		t.Fatalf("catalog resolution: %+v %v", result, err)
	}
	// The file goes away. A complete pass that observed nothing else in
	// the folder marks it missing; the mapping stays.
	if _, err := s.ReconcileFolder(ctx, folder.ID, []store.ObservedBook{{
		RelativePath:  "other.epub",
		SizeBytes:     1,
		MTime:         now,
		ContentSHA256: "other-sha",
		MediaType:     "application/epub+zip",
		Title:         "Other",
	}}, true, now); err != nil {
		t.Fatal(err)
	}

	newWork := store.Work{ID: "work2", UserID: user.ID, Title: "Missing Book", CreatedAt: now}
	if err := s.SplitWork(ctx, user.ID, oldWork.ID, "book-sha", nil, newWork); err != nil {
		t.Fatal(err)
	}
	mapping, err := s.UserBookWork(ctx, user.ID, bookID)
	if err != nil || mapping.WorkID != newWork.ID {
		t.Fatalf("missing file mapping after split: %+v %v", mapping, err)
	}
	aliases, err := s.ResolveAliases(ctx, user.ID, []store.Identifier{
		{Kind: "sha256", Value: "book-sha"},
		{Kind: "source", Value: "liseur-sync:" + bookID},
	})
	if err != nil ||
		aliases["sha256:book-sha"] != newWork.ID ||
		aliases["source:liseur-sync:"+bookID] != newWork.ID {
		t.Fatalf("catalog aliases after split: %v %v", aliases, err)
	}
}
