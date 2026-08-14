package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testCalibreFileReconciliation pins what a Calibre book keeps when it
// changes (ADR-0014): a rename moves the file row, a conversion adds one
// and supersedes the old, and the cover somebody chose is recorded
// beside them. All three are what stop a changed book becoming a second
// book, so both backends have to agree on them.
func testCalibreFileReconciliation(t *testing.T, open OpenFunc) {
	s := open(t)
	inserter, ok := s.(bookFileTestInserter)
	if !ok {
		t.Fatalf("%T cannot insert book files for shared tests", s)
	}
	ctx := context.Background()
	owner := MkUser(t, s, "calibre-owner")
	now := time.Date(2026, time.November, 3, 9, 0, 0, 0, time.UTC)
	root := "/srv/calibre"
	library := store.Library{
		ID: "lib-calibre-files", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Source:  store.LibraryCalibre,
		Storage: store.LibraryStorageInPlace,
		Refresh: store.LibraryRefreshInterval, Name: "Calibre",
		RootPath: &root, CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateCatalogBook(ctx, owner.ID, store.CatalogBook{
		ID: "book-1", LibraryID: library.ID, Status: store.BookActive,
		Title: "Small Gods", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	original := "Pratchett/Small Gods (1)/Small Gods.epub"
	modified := now.Add(-48 * time.Hour)
	if err := inserter.InsertBookFileForTest(ctx, store.BookFile{
		ID: "file-1", LibraryID: library.ID, BookID: "book-1",
		Storage: store.LibraryStorageInPlace, ContentSHA256: hash64('a'),
		Source: store.IngestScanned, MediaType: "application/epub+zip",
		SourceRelativePath: &original, OriginalFilename: "Small Gods.epub",
		SourceModifiedAt: &modified,
		Availability:     store.BookFileAvailable,
		CreatedAt:        now, UpdatedAt: now,
	}, 400); err != nil {
		t.Fatal(err)
	}

	// A rename in Calibre: the same bytes at a new path keep the row.
	renamed := "Pratchett/Small Gods (Discworld 13) (1)/Small Gods.epub"
	moved := now.Add(-time.Hour)
	if err := s.RelocateWatchedFile(
		ctx, library.ID, "file-1", renamed, moved, now); err != nil {
		t.Fatal(err)
	}
	if found, err := s.WatchedFilesByPath(
		ctx, library.ID, renamed); err != nil {
		t.Fatal(err)
	} else if len(found) != 1 || found[0].FileID != "file-1" {
		t.Fatalf("a relocated file is not at its new path: %+v", found)
	}
	if found, err := s.WatchedFilesByPath(
		ctx, library.ID, original); err != nil {
		t.Fatal(err)
	} else if len(found) != 0 {
		t.Fatalf("a relocated file is still at its old path: %+v", found)
	}
	// Relocating is scoped to its library, and a file that is not there
	// is not silently created.
	if err := s.RelocateWatchedFile(ctx, "lib-nobody", "file-1",
		renamed, moved, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("relocating across libraries: %v", err)
	}

	// A conversion: a new file on the same book, the old one kept.
	replacementModified := now.Add(time.Hour)
	replacement := store.BookFile{
		ID: "file-2", LibraryID: library.ID, BookID: "book-1",
		Storage: store.LibraryStorageInPlace, ContentSHA256: hash64('b'),
		ContentSizeBytes: 512, Source: store.IngestScanned,
		SourceRelativePath: &renamed, OriginalFilename: "Small Gods.epub",
		MediaType:        "application/epub+zip",
		SourceModifiedAt: &replacementModified,
		Availability:     store.BookFileAvailable,
	}
	stored, err := s.SupersedeInPlaceBookFile(
		ctx, library.ID, "file-1", replacement, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != "file-2" ||
		stored.Availability != store.BookFileAvailable {
		t.Fatalf("the replacement file: %+v", stored)
	}
	files, err := s.ListBookFiles(
		ctx, owner.ID, "book-1", store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("a conversion left %d files", len(files))
	}
	for _, file := range files {
		if file.ID == "file-1" &&
			file.Availability != store.BookFileSuperseded {
			t.Fatalf("the old file is %q", file.Availability)
		}
	}
	// Doing it again is the same two rows, because a refresh that dies
	// after the commit runs the same statement again.
	if _, err := s.SupersedeInPlaceBookFile(
		ctx, library.ID, "file-1", replacement, now.Add(3*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if again, err := s.ListBookFiles(
		ctx, owner.ID, "book-1", store.LibraryRoleManage,
	); err != nil {
		t.Fatal(err)
	} else if len(again) != 2 {
		t.Fatalf("a replayed conversion left %d files", len(again))
	}
	// The same id with different bytes is a conflict, never an
	// overwrite.
	different := replacement
	different.ContentSHA256 = hash64('c')
	if _, err := s.SupersedeInPlaceBookFile(
		ctx, library.ID, "file-1", different, now.Add(4*time.Hour),
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a contradicting replacement: %v", err)
	}

	// The cover somebody chose, and the digest that keys its rendering.
	coverPath := "Pratchett/Small Gods (Discworld 13) (1)/cover.jpg"
	changed, err := s.SetBookFileCover(
		ctx, library.ID, "file-2", coverPath, hash64('d'), now.Add(5*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("recording a cover reported no change")
	}
	if again, err := s.SetBookFileCover(ctx, library.ID, "file-2",
		coverPath, hash64('d'), now.Add(6*time.Hour)); err != nil {
		t.Fatal(err)
	} else if again {
		t.Fatal("re-recording the same cover reported a change")
	}
	files, err = s.ListBookFiles(ctx, owner.ID, "book-1", store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.ID != "file-2" {
			continue
		}
		if file.CoverSHA256 != hash64('d') ||
			file.CoverRelativePath == nil ||
			*file.CoverRelativePath != coverPath {
			t.Fatalf("the recorded cover: %+v", file)
		}
	}
	// A cover Calibre no longer has is cleared, and half a cover is
	// refused: a key naming nothing serves nothing.
	if _, err := s.SetBookFileCover(ctx, library.ID, "file-2",
		coverPath, "", now.Add(7*time.Hour),
	); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("half a cover: %v", err)
	}
	if changed, err := s.SetBookFileCover(
		ctx, library.ID, "file-2", "", "", now.Add(7*time.Hour),
	); err != nil {
		t.Fatal(err)
	} else if !changed {
		t.Fatal("clearing a cover reported no change")
	}
}

// hash64 is a digest-shaped string, which is all these tests need: the
// store stores it, and nothing here hashes anything.
func hash64(c byte) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = c
	}
	return string(out)
}
