package calibre_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/calibre"
	_ "modernc.org/sqlite"
)

// A Calibre book is rows as much as bytes, and the rows come apart by
// Calibre's own books_delete_trg rather than by an enumeration here
// that would drift out of date. This runs against the real schema in
// testdata, which is where those triggers actually live.
func TestDeleteBookLetsCalibresOwnTriggersRun(t *testing.T) {
	root := newLibrary(t)
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()

	id, relative, err := writer.AddBook(t.Context(), calibre.NewBook{
		Title:       "The Word for World Is Forest",
		Authors:     []string{"Ursula K. Le Guin"},
		Publisher:   "Berkley",
		Languages:   []string{"eng"},
		Tags:        []string{"Science Fiction"},
		Series:      "Hainish Cycle",
		SeriesIndex: 5,
		Cover:       []byte("cover bytes"),
	}, strings.NewReader("bytes"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
		t.Fatalf("the book was never written: %v", err)
	}

	if err := writer.DeleteBook(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()

	db, err := sql.Open("sqlite", filepath.Join(root, calibre.MetadataDB))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// The links the trigger is responsible for, not just the row. The
	// authors and tags themselves stay: Calibre's trigger unlinks them
	// and its own "clean" pass is what prunes the orphans, which is a
	// library-wide decision this server does not get to make.
	var books, data, authorLinks, tagLinks, seriesLinks, langLinks int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM books),
		(SELECT COUNT(*) FROM data),
		(SELECT COUNT(*) FROM books_authors_link),
		(SELECT COUNT(*) FROM books_tags_link),
		(SELECT COUNT(*) FROM books_series_link),
		(SELECT COUNT(*) FROM books_languages_link)`,
	).Scan(&books, &data, &authorLinks, &tagLinks,
		&seriesLinks, &langLinks); err != nil {
		t.Fatal(err)
	}
	if books != 0 || data != 0 {
		t.Errorf("books=%d data=%d, want the book gone", books, data)
	}
	if authorLinks+tagLinks+seriesLinks+langLinks != 0 {
		t.Errorf("links left behind: authors=%d tags=%d series=%d languages=%d",
			authorLinks, tagLinks, seriesLinks, langLinks)
	}
	if _, err := os.Stat(filepath.Join(root, relative)); !os.IsNotExist(err) {
		t.Errorf("the file survived the delete: %v", err)
	}
	// The author directory goes only because it emptied.
	if _, err := os.Stat(filepath.Join(root, "Ursula K. Le Guin")); !os.IsNotExist(err) {
		t.Errorf("the emptied author directory survived: %v", err)
	}
}

// Calibre renames a book's directory when its title changes, and
// metadata.db is authoritative (ADR-0022). A delete that trusted a path
// cached at an earlier pass would miss the files — or, worse, remove
// somebody else's. The path has to come from the row being deleted.
func TestDeleteBookFollowsARenamedDirectory(t *testing.T) {
	root := newLibrary(t)
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()

	id, relative, err := writer.AddBook(t.Context(), calibre.NewBook{
		Title: "Rocannon's World", Authors: []string{"Ursula K. Le Guin"},
	}, strings.NewReader("bytes"), 5)
	if err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Dir(relative)
	_ = writer.Close()

	// Calibre moves the directory and updates the row; this server was
	// not there for it.
	newDir := "Ursula K. Le Guin/Renamed By Calibre (" + itoa(id) + ")"
	if err := os.Rename(
		filepath.Join(root, oldDir), filepath.Join(root, newDir),
	); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, calibre.MetadataDB))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`UPDATE books SET path = ? WHERE id = ?`, newDir, id); err != nil {
		t.Fatal(err)
	}
	db.Close()

	writer2, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer2.Close() }()
	if err := writer2.DeleteBook(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, newDir)); !os.IsNotExist(err) {
		t.Errorf("the renamed directory survived: %v", err)
	}
}

// An author with another book keeps their directory, because Remove
// refuses a directory that is not empty.
func TestDeleteBookKeepsAnAuthorWhoStillHasABook(t *testing.T) {
	root := newLibrary(t)
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()

	var first int64
	for i, title := range []string{"A Wizard of Earthsea", "The Tombs of Atuan"} {
		id, _, err := writer.AddBook(t.Context(), calibre.NewBook{
			Title: title, Authors: []string{"Ursula K. Le Guin"},
		}, strings.NewReader("x"), 1)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = id
		}
	}
	if err := writer.DeleteBook(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Ursula K. Le Guin")); err != nil {
		t.Errorf("an author with a book left lost their directory: %v", err)
	}
}

func TestDeleteBookAnswersForABookThatIsNotThere(t *testing.T) {
	root := newLibrary(t)
	writer, err := calibre.OpenWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if err := writer.DeleteBook(t.Context(), 4242); !errors.Is(
		err, calibre.ErrNoSuchBook,
	) {
		t.Fatalf("err = %v, want ErrNoSuchBook", err)
	}
}
