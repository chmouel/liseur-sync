//go:build linux

package content

// What a Calibre book keeps when it changes (ADR-0014): its catalog row.
// Renaming a book in Calibre moves its directory, converting it replaces
// its bytes, and neither is a new book — which is exactly what a
// path-keyed reconciliation would decide if the mapping were not
// consulted first.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (f *calibreFixture) filesOf(bookID string) []store.BookFile {
	f.t.Helper()
	files, err := f.store.ListBookFiles(
		f.ctx, f.library.OwnerUserID, bookID, store.LibraryRoleManage)
	if err != nil {
		f.t.Fatalf("list files of %q: %v", bookID, err)
	}
	return files
}

// TestCalibreRenameMovesTheBookRatherThanCopyingIt is the case a
// path-keyed sweep gets wrong: Calibre renames a directory when the
// title or the author is edited, and the book on the other side of that
// rename is the same book.
func TestCalibreRenameMovesTheBookRatherThanCopyingIt(t *testing.T) {
	f := newCalibreFixture(t)
	body := minimalEPUB(t)
	f.addBook(1, "Small Gods", "Pratchett/Small Gods (1)", "Small Gods", body)
	if report := f.refresh(); report.Mapped != 1 {
		t.Fatalf("the first refresh did not map the book: %+v", report)
	}
	book := f.bookByTitle("Small Gods")

	// Calibre renames: the file moves and the row follows it.
	f.write("Pratchett/Small Gods (Discworld 13) (1)/Small Gods.epub", body)
	if err := os.RemoveAll(filepath.Join(
		f.root, "Pratchett", "Small Gods (1)")); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE books SET path = 'Pratchett/Small Gods (Discworld 13) (1)',
		last_modified = '2024-03-03 00:00:00+00:00' WHERE id = 1`)

	f.now = f.now.Add(time.Hour)
	report := f.refresh()
	if report.Sync.Relocated != 1 {
		t.Fatalf("a renamed book was not relocated: %+v", report.Sync)
	}
	if report.Sync.Ingested != 0 {
		t.Fatalf("a renamed book was catalogued a second time: %+v", report.Sync)
	}
	if books := f.books(); len(books) != 1 {
		t.Fatalf("a rename left %d books", len(books))
	}
	files := f.filesOf(book.ID)
	if len(files) != 1 {
		t.Fatalf("a rename left %d files", len(files))
	}
	want := "Pratchett/Small Gods (Discworld 13) (1)/Small Gods.epub"
	if files[0].SourceRelativePath == nil ||
		*files[0].SourceRelativePath != want {
		t.Fatalf("file path = %v, want %q", files[0].SourceRelativePath, want)
	}
	if files[0].Availability != store.BookFileAvailable {
		t.Fatalf("a relocated file is %q", files[0].Availability)
	}
}

// TestCalibreConversionSupersedesTheFileNotTheBook is the other half:
// different bytes under the same Calibre id are a new file on the book
// that already exists, never a second book and never a review item.
func TestCalibreConversionSupersedesTheFileNotTheBook(t *testing.T) {
	f := newCalibreFixture(t)
	f.addBook(1, "Small Gods", "Pratchett/Small Gods (1)",
		"Small Gods", minimalEPUB(t))
	f.refresh()
	book := f.bookByTitle("Small Gods")
	before := f.filesOf(book.ID)
	if len(before) != 1 {
		t.Fatalf("the first refresh made %d files", len(before))
	}

	converted := variantEPUB(t, "reconverted by calibre")
	f.write("Pratchett/Small Gods (1)/Small Gods.epub", converted)
	f.exec(`UPDATE data SET uncompressed_size = ? WHERE book = 1`, len(converted))
	f.exec(`UPDATE books SET last_modified = '2024-04-04 00:00:00+00:00'
		WHERE id = 1`)

	f.now = f.now.Add(time.Hour)
	report := f.refresh()
	if report.Sync.Superseded != 1 || report.Sync.Review != 0 {
		t.Fatalf("a reconverted book: %+v", report.Sync)
	}
	if books := f.books(); len(books) != 1 || books[0].ID != book.ID {
		t.Fatalf("a conversion changed which book this is: %+v", books)
	}
	if again := f.bookByTitle("Small Gods"); again.Status != store.BookActive {
		t.Fatalf("a reconverted book is %q", again.Status)
	}
	files := f.filesOf(book.ID)
	if len(files) != 2 {
		t.Fatalf("a conversion left %d files, want the old and the new", len(files))
	}
	var available, superseded int
	for _, file := range files {
		switch file.Availability {
		case store.BookFileAvailable:
			available++
			if file.ContentSHA256 != digestOf(converted) {
				t.Errorf("the served file is not the converted one")
			}
		case store.BookFileSuperseded:
			superseded++
			if file.ID != before[0].ID {
				t.Errorf("the superseded file is not the original")
			}
		}
	}
	if available != 1 || superseded != 1 {
		t.Fatalf("availability after a conversion: %+v", files)
	}

	// Twice through the same conversion is the same two rows: the
	// replacement's id comes from the bytes, so a refresh that died
	// after the commit does not make a third file.
	f.exec(`UPDATE books SET last_modified = '2024-04-05 00:00:00+00:00'
		WHERE id = 1`)
	f.now = f.now.Add(time.Hour)
	f.refresh()
	if files := f.filesOf(book.ID); len(files) != 2 {
		t.Fatalf("a repeated conversion left %d files", len(files))
	}
}

// TestCalibreCoverIsTheOneCalibreChose covers the picture: cover.jpg is
// somebody's choice and beats what the EPUB declares, and the digest
// recorded with it is what keeps two books sharing one EPUB from
// sharing one cover.
func TestCalibreCoverIsTheOneCalibreChose(t *testing.T) {
	f := newCalibreFixture(t)
	f.addBook(1, "Small Gods", "Pratchett/Small Gods (1)",
		"Small Gods", minimalEPUB(t))
	chosen := []byte("not really a jpeg, but bytes somebody chose")
	f.write("Pratchett/Small Gods (1)/cover.jpg", chosen)
	f.exec(`UPDATE books SET has_cover = 1 WHERE id = 1`)

	if report := f.refresh(); report.CoversUpdated != 1 {
		t.Fatalf("the first refresh recorded no cover: %+v", report)
	}
	book := f.bookByTitle("Small Gods")
	file := f.filesOf(book.ID)[0]
	if file.CoverSHA256 != digestOf(chosen) {
		t.Fatalf("cover digest = %q", file.CoverSHA256)
	}
	if file.CoverRelativePath == nil ||
		*file.CoverRelativePath != "Pratchett/Small Gods (1)/cover.jpg" {
		t.Fatalf("cover path = %v", file.CoverRelativePath)
	}

	// A replaced cover is a new key, which is what stops the cache
	// serving the old picture.
	replaced := []byte("the cover the curator preferred")
	f.write("Pratchett/Small Gods (1)/cover.jpg", replaced)
	f.exec(`UPDATE books SET last_modified = '2024-05-05 00:00:00+00:00'
		WHERE id = 1`)
	f.now = f.now.Add(time.Hour)
	if report := f.refresh(); report.CoversUpdated != 1 {
		t.Fatalf("a replaced cover was not recorded: %+v", report)
	}
	if file := f.filesOf(book.ID)[0]; file.CoverSHA256 != digestOf(replaced) {
		t.Fatalf("cover digest after replacement = %q", file.CoverSHA256)
	}

	// A cover Calibre no longer has is cleared, so the book falls back
	// to whatever its publication declares rather than to a key naming
	// bytes that are gone.
	if err := os.Remove(filepath.Join(f.root, "Pratchett",
		"Small Gods (1)", "cover.jpg")); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE books SET has_cover = 0,
		last_modified = '2024-06-06 00:00:00+00:00' WHERE id = 1`)
	f.now = f.now.Add(time.Hour)
	if report := f.refresh(); report.CoversUpdated != 1 {
		t.Fatalf("a removed cover was not cleared: %+v", report)
	}
	file = f.filesOf(book.ID)[0]
	if file.CoverSHA256 != "" || file.CoverRelativePath != nil {
		t.Fatalf("a removed cover left %q at %v",
			file.CoverSHA256, file.CoverRelativePath)
	}
}

// TestRefreshWithdrawsAndRestoresAFileWhileTheServerRuns is the
// availability half of a refresh: recording that a source went away is
// not the same as the catalog acting on it, and a household that
// deletes a file must not have to restart the server before it stops
// being offered (ADR-0014).
func TestRefreshWithdrawsAndRestoresAFileWhileTheServerRuns(t *testing.T) {
	f := newCalibreFixture(t)
	body := minimalEPUB(t)
	f.addBook(1, "Small Gods", "Pratchett/Small Gods (1)", "Small Gods", body)
	options := WatchedSyncOptions{
		MaxFileBytes: 1 << 20,
		Patterns:     NewLibraryPatterns(f.store),
	}

	f.now = f.now.Add(store.DefaultRefreshInterval + time.Minute)
	report, err := RunRefreshPass(f.ctx, f.store, f.cas, options, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.Libraries != 1 || report.Ingested != 1 {
		t.Fatalf("first pass: %+v", report)
	}
	book := f.bookByTitle("Small Gods")

	// Somebody deletes the file behind Calibre's back.
	epub := filepath.Join(f.root, "Pratchett", "Small Gods (1)",
		"Small Gods.epub")
	if err := os.Remove(epub); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(store.DefaultRefreshInterval + time.Minute)
	report, err = RunRefreshPass(f.ctx, f.store, f.cas, options, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesUnavailable != 1 {
		t.Fatalf("a deleted file was not withdrawn by the pass: %+v", report)
	}
	if files := f.filesOf(book.ID); files[0].Availability !=
		store.BookFileMissing {
		t.Fatalf("a deleted file is still %q", files[0].Availability)
	}

	// And puts it back.
	f.write("Pratchett/Small Gods (1)/Small Gods.epub", body)
	f.now = f.now.Add(store.DefaultRefreshInterval + time.Minute)
	report, err = RunRefreshPass(f.ctx, f.store, f.cas, options, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesRestored != 1 {
		t.Fatalf("a returned file was not restored by the pass: %+v", report)
	}
	if files := f.filesOf(book.ID); files[0].Availability !=
		store.BookFileAvailable {
		t.Fatalf("a returned file is still %q", files[0].Availability)
	}
}

// TestARenamedBookKeepsItsChosenCover: renaming a book in Calibre moves
// cover.jpg with everything else, so the same bytes turn up at a new
// path. Recording covers by digest alone would leave the row pointing
// into a directory that no longer exists — and, because the digest never
// changes again, no later refresh could correct it, so the book would
// silently fall back to its EPUB cover forever (ADR-0014).
func TestARenamedBookKeepsItsChosenCover(t *testing.T) {
	f := newCalibreFixture(t)
	body := minimalEPUB(t)
	f.addBook(1, "Small Gods", "Pratchett/Small Gods (1)", "Small Gods", body)
	chosen := []byte("not really a jpeg, but bytes somebody chose")
	f.write("Pratchett/Small Gods (1)/cover.jpg", chosen)
	f.exec(`UPDATE books SET has_cover = 1 WHERE id = 1`)
	f.refresh()
	book := f.bookByTitle("Small Gods")

	renamed := "Pratchett/Small Gods (Discworld 13) (1)"
	f.write(renamed+"/Small Gods.epub", body)
	f.write(renamed+"/cover.jpg", chosen)
	if err := os.RemoveAll(filepath.Join(
		f.root, "Pratchett", "Small Gods (1)")); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE books SET path = '` + renamed + `',
		last_modified = '2024-03-03 00:00:00+00:00' WHERE id = 1`)

	f.now = f.now.Add(time.Hour)
	f.refresh()

	file := f.filesOf(book.ID)[0]
	if file.CoverSHA256 != digestOf(chosen) {
		t.Fatalf("the cover lost its digest: %q", file.CoverSHA256)
	}
	want := renamed + "/cover.jpg"
	if file.CoverRelativePath == nil || *file.CoverRelativePath != want {
		t.Fatalf("cover path = %v, want %q", file.CoverRelativePath, want)
	}
	if _, _, err := f.cas.OpenBookFileCover(t.Context(), file); err != nil {
		t.Fatalf("the recorded cover cannot be opened: %v", err)
	}
}

// TestACoverSwollenSinceItWasRecordedIsRefused: the bytes belong to
// somebody else and can be replaced after a refresh bounded them, so the
// size is checked again on the way out rather than after hashing the
// whole file.
func TestACoverSwollenSinceItWasRecordedIsRefused(t *testing.T) {
	f := newCalibreFixture(t)
	f.addBook(1, "Small Gods", "Pratchett/Small Gods (1)",
		"Small Gods", minimalEPUB(t))
	chosen := []byte("a small cover")
	f.write("Pratchett/Small Gods (1)/cover.jpg", chosen)
	f.exec(`UPDATE books SET has_cover = 1 WHERE id = 1`)
	f.refresh()

	file := f.filesOf(f.bookByTitle("Small Gods").ID)[0]
	f.write("Pratchett/Small Gods (1)/cover.jpg",
		bytes.Repeat([]byte("x"), MaxCoverBytes+1))
	if _, _, err := f.cas.OpenBookFileCover(t.Context(), file); err == nil {
		t.Fatal("a cover swapped for an enormous one was read anyway")
	}
}
