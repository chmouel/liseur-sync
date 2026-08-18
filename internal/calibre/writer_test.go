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
	_ "modernc.org/sqlite"
)

// The fixture here is the schema of a real Calibre library, dumped
// wholesale into testdata rather than hand-written. That matters more
// than it looks: the hand-written fixtures elsewhere in this package
// leave the triggers out, and the triggers are exactly where writing to
// Calibre goes wrong. books_insert_trg calls title_sort() and uuid4(),
// which Calibre registers from Python and SQLite does not have, so a
// writer that forgets them passes every simplified fixture and fails
// against every real library.

func newLibrary(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	schema, err := os.ReadFile(filepath.Join("testdata", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, calibre.MetadataDB))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("apply Calibre schema: %v", err)
	}
	return root
}

func TestAddBookWritesABookCalibreCanRead(t *testing.T) {
	root := newLibrary(t)
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()

	payload := []byte("not really an epub, but bytes are bytes")
	id, relative, err := writer.AddBook(t.Context(), calibre.NewBook{
		Title:       "The Left Hand of Darkness",
		Authors:     []string{"Ursula K. Le Guin"},
		Publisher:   "Ace Books",
		Description: "Winter.",
		Languages:   []string{"eng"},
		Tags:        []string{"Science Fiction"},
		Series:      "Hainish Cycle",
		SeriesIndex: 4,
		Identifiers: []calibre.Identifier{{Type: "isbn", Value: "9780441478125"}},
		Cover:       []byte("jpeg-ish"),
	}, bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("AddBook: %v", err)
	}
	if id <= 0 {
		t.Fatalf("book id = %d", id)
	}

	// The path Calibre builds contains the id, which does not exist
	// until the row does. Getting this wrong is how a book ends up in a
	// directory the database does not name.
	wantDir := "Ursula K. Le Guin/The Left Hand of Darkness (" +
		itoa(id) + ")"
	wantFile := wantDir + "/The Left Hand of Darkness - Ursula K. Le Guin.epub"
	if relative != wantFile {
		t.Fatalf("relative = %q, want %q", relative, wantFile)
	}
	for _, name := range []string{
		wantFile, wantDir + "/cover.jpg", wantDir + "/metadata.opf",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	if got, err := os.ReadFile(
		filepath.Join(root, filepath.FromSlash(wantFile))); err != nil ||
		!bytes.Equal(got, payload) {
		t.Errorf("publication bytes = %q, %v", got, err)
	}

	// Reading it back through the package's own read path is the real
	// assertion: whatever the writer produced, the pass has to be able
	// to catalog it.
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
		t.Fatalf("books = %d, want 1", len(books))
	}
	book := books[0]
	if book.Title != "The Left Hand of Darkness" {
		t.Errorf("title = %q", book.Title)
	}
	if len(book.Formats) != 1 || book.Formats[0].Format != "EPUB" {
		t.Fatalf("formats = %+v, want one EPUB", book.Formats)
	}
	if got := book.RelativePath(book.Formats[0]); got != wantFile {
		t.Errorf("RelativePath = %q, want %q", got, wantFile)
	}
	if len(book.Authors) != 1 || book.Authors[0] != "Ursula K. Le Guin" {
		t.Errorf("authors = %v", book.Authors)
	}
	if book.Publisher != "Ace Books" {
		t.Errorf("publisher = %q", book.Publisher)
	}
	if book.Series != "Hainish Cycle" || book.SeriesIndex != 4 {
		t.Errorf("series = %q %v", book.Series, book.SeriesIndex)
	}
	if !book.HasCover {
		t.Error("has_cover is false but a cover was written")
	}
}

// The triggers are the point. If title_sort and uuid4 were not
// registered the insert above would have failed outright, but a writer
// could also satisfy them and still leave the columns Calibre sorts and
// identifies by empty.
func TestAddBookLetsCalibresOwnTriggersRun(t *testing.T) {
	root := newLibrary(t)
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if _, _, err := writer.AddBook(t.Context(), calibre.NewBook{
		Title: "The Dispossessed", Authors: []string{"Ursula K. Le Guin"},
	}, strings.NewReader("x"), 1); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()

	db, err := sql.Open("sqlite", filepath.Join(root, calibre.MetadataDB))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sortTitle, uuid, authorSort string
	if err := db.QueryRow(
		`SELECT sort, uuid, author_sort FROM books`,
	).Scan(&sortTitle, &uuid, &authorSort); err != nil {
		t.Fatal(err)
	}
	if sortTitle != "Dispossessed, The" {
		t.Errorf("sort = %q, want Calibre's title_sort", sortTitle)
	}
	if len(uuid) != 36 {
		t.Errorf("uuid = %q, want one uuid4() made", uuid)
	}
	if authorSort != "Guin, Ursula K. Le" {
		t.Errorf("author_sort = %q", authorSort)
	}
}

// Two books by one author are two books and one author. Calibre's
// authors table has UNIQUE(name), so a writer that inserted blindly
// would fail on the second book rather than share the row.
func TestAddBookSharesAnAuthorRow(t *testing.T) {
	root := newLibrary(t)
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	for _, title := range []string{"A Wizard of Earthsea", "The Tombs of Atuan"} {
		if _, _, err := writer.AddBook(t.Context(), calibre.NewBook{
			Title: title, Authors: []string{"Ursula K. Le Guin"},
		}, strings.NewReader("x"), 1); err != nil {
			t.Fatalf("%s: %v", title, err)
		}
	}
	_ = writer.Close()

	db, err := sql.Open("sqlite", filepath.Join(root, calibre.MetadataDB))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var authors, books, links int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM authors),
		(SELECT COUNT(*) FROM books), (SELECT COUNT(*) FROM books_authors_link)`,
	).Scan(&authors, &books, &links); err != nil {
		t.Fatal(err)
	}
	if authors != 1 || books != 2 || links != 2 {
		t.Fatalf("authors=%d books=%d links=%d, want 1/2/2", authors, books, links)
	}
}

// ADR-0022 makes metadata.db the authority, so a file whose row did not
// land is invisible forever and a row whose file did not land is a book
// that cannot be served. Neither is recoverable, so a failure has to
// leave nothing at all behind — rows and directories both.
func TestAddBookLeavesNothingBehindWhenTheFileCannotBeWritten(t *testing.T) {
	root := newLibrary(t)
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()

	// A transfer that dies half way, which is the realistic version of
	// this failure: the rows are written, the directories are made, and
	// then the bytes stop arriving.
	if _, _, err := writer.AddBook(t.Context(), calibre.NewBook{
		Title: "Doomed", Authors: []string{"Nobody"},
	}, brokenReader{}, 10); err == nil {
		t.Fatal("AddBook succeeded but the publication could not be written")
	}
	_ = writer.Close()
	assertLibraryIsEmpty(t, root)
}

// brokenReader is a transfer that stops part way through.
type brokenReader struct{}

func (brokenReader) Read(p []byte) (int, error) {
	n := copy(p, "half a bo")
	return n, errors.New("the connection went away")
}

// A book's directory carries its id, so finding one already there means
// something this code did not create is in the way. Writing into it
// would overwrite a book somebody has. Refusing is the only safe answer,
// and the directory has to survive being refused.
func TestAddBookWillNotWriteIntoADirectoryItDidNotMake(t *testing.T) {
	root := newLibrary(t)
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()

	// The first book takes id 1, so this is where it would go.
	existing := filepath.Join(root, "Nobody", "Doomed (1)")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(existing, "someone-elses-book.epub")
	if err := os.WriteFile(precious, []byte("do not touch"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := writer.AddBook(t.Context(), calibre.NewBook{
		Title: "Doomed", Authors: []string{"Nobody"},
	}, strings.NewReader("x"), 1); err == nil {
		t.Fatal("AddBook overwrote a directory it did not create")
	}
	_ = writer.Close()

	got, err := os.ReadFile(precious)
	if err != nil || string(got) != "do not touch" {
		t.Fatalf("the existing book was disturbed: %q %v", got, err)
	}
	assertRowsAreEmpty(t, root)
}

// A symlink in the library must not turn a write — or a rollback's
// delete — into one somewhere else. The plain-folder path has always
// refused to follow one; a Calibre library deserves the same care.
func TestAddBookDoesNotFollowASymlinkOutOfTheLibrary(t *testing.T) {
	root := newLibrary(t)
	outside := t.TempDir()
	victim := filepath.Join(outside, "keep-me")
	if err := os.WriteFile(victim, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An author directory that is really a door out of the library.
	if err := os.Symlink(outside, filepath.Join(root, "Nobody")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if _, _, err := writer.AddBook(t.Context(), calibre.NewBook{
		Title: "Doomed", Authors: []string{"Nobody"},
	}, strings.NewReader("x"), 1); err == nil {
		t.Fatal("AddBook followed a symlink out of the library")
	}
	_ = writer.Close()

	if got, err := os.ReadFile(victim); err != nil || string(got) != "mine" {
		t.Fatalf("a file outside the library was touched: %q %v", got, err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 1 {
		t.Fatalf("the directory outside the library changed: %v %v", entries, err)
	}
	assertRowsAreEmpty(t, root)
}

// assertRowsAreEmpty checks the transaction rolled back.
func assertRowsAreEmpty(t *testing.T, root string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, calibre.MetadataDB))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var books, data, authors int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM books),
		(SELECT COUNT(*) FROM data), (SELECT COUNT(*) FROM authors)`,
	).Scan(&books, &data, &authors); err != nil {
		t.Fatal(err)
	}
	if books != 0 || data != 0 || authors != 0 {
		t.Fatalf("books=%d data=%d authors=%d, want the transaction rolled back",
			books, data, authors)
	}
}

// assertLibraryIsEmpty checks both halves: no rows, and no directory
// this code created left on the disk.
func assertLibraryIsEmpty(t *testing.T, root string) {
	t.Helper()
	assertRowsAreEmpty(t, root)
	if _, err := os.Stat(filepath.Join(root, "Nobody")); !os.IsNotExist(err) {
		t.Errorf("the book directory survived a failed add: %v", err)
	}
}

func TestTitleSortMatchesCalibre(t *testing.T) {
	for in, want := range map[string]string{
		"The Left Hand of Darkness": "Left Hand of Darkness, The",
		"A Wizard of Earthsea":      "Wizard of Earthsea, A",
		"An Ordinary Book":          "Ordinary Book, An",
		"Dune":                      "Dune",
		"Theatre":                   "Theatre",
		"Anathem":                   "Anathem",
	} {
		if got := calibre.TitleSort(in); got != want {
			t.Errorf("TitleSort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilenameSafeMatchesCalibre(t *testing.T) {
	for in, want := range map[string]string{
		`A/B`:           "A_B",
		`Yes: No`:       "Yes_ No",
		`Either|Or`:     "Either,Or",
		`Trailing.`:     "Trailing",
		`What?`:         "What_",
		`   Spaced   `:  "Spaced",
		`"Quoted"`:      "_Quoted_",
		`<script>`:      "_script_",
		`Fine — Really`: "Fine — Really",
	} {
		if got := calibre.FilenameSafe(in, 96); got != want {
			t.Errorf("FilenameSafe(%q) = %q, want %q", in, got, want)
		}
	}
	if got := calibre.FilenameSafe("///", 96); got != "___" {
		t.Errorf("FilenameSafe(%q) = %q", "///", got)
	}
	if got := calibre.FilenameSafe("", 96); got != "Unknown" {
		t.Errorf("empty name = %q, want Unknown", got)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}
