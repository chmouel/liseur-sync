//go:build linux

package content

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chmouel/liseur-sync/internal/calibre"
)

// TestAStaleFormatRowFallsThroughToTheFileOnDisk. Calibre's format rows
// outlive the files they name: converting a book to KEPUB and deleting
// the EPUB leaves the EPUB row behind. A reader that insisted on the
// first row would call the book unreadable and make the whole pass
// incomplete, while the file sat in the same directory.
func TestAStaleFormatRowFallsThroughToTheFileOnDisk(t *testing.T) {
	root := t.TempDir()
	dir := "Duncan Stearn/Learning business (65)"
	writeBook(t, root, filepath.Join(dir, "Learning business.kepub"), "Learning business")

	opened, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()

	book := calibre.Book{
		ID:    65,
		Title: "Learning business",
		Path:  dir,
		// Calibre lists the EPUB first and it is not on the disk.
		Formats: []calibre.Format{
			{Format: "EPUB", Name: "Learning business"},
			{Format: "KEPUB", Name: "Learning business"},
		},
	}
	obs, err := testReconciler(newFakeCatalog()).readCalibreBookFormats(
		context.Background(), opened, book, book.ReadableFormats())
	if err != nil {
		t.Fatalf("a book whose KEPUB is on the disk was called unreadable: %v", err)
	}
	if want := filepath.Join(dir, "Learning business.kepub"); obs.RelativePath != want {
		t.Errorf("read %q, want the file that exists (%q)", obs.RelativePath, want)
	}
	if obs.Title == "" || obs.ContentSHA256 == "" {
		t.Errorf("the kepub was not actually read: %+v", obs)
	}
	// A KEPUB is an EPUB with Kobo's spans in it, so it is served as one.
	if obs.MediaType != "application/epub+zip" {
		t.Errorf("media type = %q", obs.MediaType)
	}
}

// TestThePreferredFormatWinsWhenBothAreOnDisk. Kobo's injected spans
// shift the document structure a reading position is expressed against,
// so when a book has both files the plain EPUB is the stabler thing to
// point at — and the choice must not drift between passes.
func TestThePreferredFormatWinsWhenBothAreOnDisk(t *testing.T) {
	root := t.TempDir()
	dir := "Pierce Brown/Red Rising (14)"
	writeBook(t, root, filepath.Join(dir, "Red Rising.epub"), "Red Rising")
	writeBook(t, root, filepath.Join(dir, "Red Rising.kepub"), "Red Rising")

	opened, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()

	book := calibre.Book{
		ID: 14, Title: "Red Rising", Path: dir,
		// Listed KEPUB first, to prove the order comes from the
		// preference and not from Calibre's row order.
		Formats: []calibre.Format{
			{Format: "KEPUB", Name: "Red Rising"},
			{Format: "EPUB", Name: "Red Rising"},
		},
	}
	obs, err := testReconciler(newFakeCatalog()).readCalibreBookFormats(
		context.Background(), opened, book, book.ReadableFormats())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "Red Rising.epub"); obs.RelativePath != want {
		t.Errorf("read %q, want the plain EPUB (%q)", obs.RelativePath, want)
	}
}

// TestABookWithNoFileAtAllIsStillUnreadable. Falling through the
// formats must not become a way of reporting success: a book whose
// files are all gone is what makes a pass incomplete, and an incomplete
// pass is what stops the store marking everything missing.
func TestABookWithNoFileAtAllIsStillUnreadable(t *testing.T) {
	root := t.TempDir()
	opened, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()

	book := calibre.Book{
		ID: 15, Title: "Iron Gold", Path: "Pierce Brown/Iron Gold (15)",
		Formats: []calibre.Format{
			{Format: "EPUB", Name: "Iron Gold"},
			{Format: "KEPUB", Name: "Iron Gold"},
		},
	}
	if _, err := testReconciler(newFakeCatalog()).readCalibreBookFormats(
		context.Background(), opened, book, book.ReadableFormats()); err == nil {
		t.Fatal("a book with no file on the disk was read successfully")
	}
}
