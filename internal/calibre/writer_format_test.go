package calibre_test

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/calibre"
)

func addOneBook(t *testing.T, root string) (int64, string) {
	t.Helper()
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	id, relative, err := writer.AddBook(t.Context(), calibre.NewBook{
		Title: "The Dispossessed", Authors: []string{"Ursula K. Le Guin"},
	}, strings.NewReader("epub"), 4)
	if err != nil {
		t.Fatal(err)
	}
	return id, relative
}

func formatRows(t *testing.T, root string, id int64) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, calibre.MetadataDB))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM data WHERE book = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The audiobook joins the book Calibre already has: same directory,
// same name, its own data row, and the pass reads it back as an M4B.
func TestAddFormatPutsTheAudiobookBesideItsBook(t *testing.T) {
	root := newLibrary(t)
	id, epubPath := addOneBook(t, root)
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()

	payload := []byte("m4b bytes")
	relative, err := writer.AddFormat(t.Context(), id, "m4b",
		bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("AddFormat: %v", err)
	}
	want := strings.TrimSuffix(epubPath, ".epub") + ".m4b"
	if relative != want {
		t.Fatalf("relative = %q, want %q", relative, want)
	}
	if got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative))); err != nil ||
		!bytes.Equal(got, payload) {
		t.Fatalf("audiobook bytes = %q, %v", got, err)
	}
	_ = writer.Close()

	lib, err := calibre.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	books, err := lib.Books(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 {
		t.Fatalf("books = %d", len(books))
	}
	formats := books[0].AudiobookFormats()
	if len(formats) != 1 || books[0].RelativePath(formats[0]) != want {
		t.Fatalf("audiobook formats = %+v", formats)
	}
	if len(books[0].ReadableFormats()) != 1 {
		t.Fatalf("the EPUB format was disturbed: %+v", books[0].Formats)
	}
}

// Adding never replaces: a second M4B, an unknown book and a file that
// is already on disk without a row are all refused, and leave nothing.
func TestAddFormatRefusesWhatItWouldReplace(t *testing.T) {
	root := newLibrary(t)
	id, epubPath := addOneBook(t, root)
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()

	if _, err := writer.AddFormat(t.Context(), id+100, "M4B",
		strings.NewReader("x"), 1); !errors.Is(err, calibre.ErrNoSuchBook) {
		t.Fatalf("unknown book: err = %v", err)
	}

	stray := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(epubPath, ".epub")+".m4b"))
	if err := os.WriteFile(stray, []byte("somebody's"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.AddFormat(t.Context(), id, "M4B",
		strings.NewReader("x"), 1); !errors.Is(err, calibre.ErrFormatExists) {
		t.Fatalf("stray file: err = %v", err)
	}
	if got, _ := os.ReadFile(stray); string(got) != "somebody's" {
		t.Fatalf("the stray file was touched: %q", got)
	}
	if n := formatRows(t, root, id); n != 1 {
		t.Fatalf("data rows = %d, want the EPUB only", n)
	}
	if err := os.Remove(stray); err != nil {
		t.Fatal(err)
	}

	if _, err := writer.AddFormat(t.Context(), id, "M4B",
		strings.NewReader("x"), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.AddFormat(t.Context(), id, "M4B",
		strings.NewReader("y"), 1); !errors.Is(err, calibre.ErrFormatExists) {
		t.Fatalf("second M4B: err = %v", err)
	}
}

// A transfer that dies half way leaves neither a row nor a file.
func TestAddFormatLeavesNothingBehindOnFailure(t *testing.T) {
	root := newLibrary(t)
	id, epubPath := addOneBook(t, root)
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if _, err := writer.AddFormat(t.Context(), id, "M4B", brokenReader{}, 10); err == nil {
		t.Fatal("AddFormat succeeded on a broken transfer")
	}
	if n := formatRows(t, root, id); n != 1 {
		t.Fatalf("data rows = %d, want the EPUB only", n)
	}
	m4b := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(epubPath, ".epub")+".m4b"))
	if _, err := os.Stat(m4b); !os.IsNotExist(err) {
		t.Fatalf("a partial audiobook survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(epubPath))); err != nil {
		t.Fatalf("the EPUB was removed: %v", err)
	}
}
