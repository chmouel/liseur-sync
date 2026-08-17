//go:build linux

package content

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/store"

	_ "modernc.org/sqlite"
)

// calibreTestSchema is the subset of Calibre's schema a pass reads. It
// is written out rather than shipped as a fixture for the same reason
// the calibre package writes its own: a fixture cannot be edited to
// describe the library that broke.
const calibreTestSchema = `
CREATE TABLE books (
	id INTEGER PRIMARY KEY,
	title TEXT NOT NULL DEFAULT 'Unknown',
	sort TEXT,
	timestamp TIMESTAMP,
	pubdate TIMESTAMP,
	series_index REAL NOT NULL DEFAULT 1.0,
	path TEXT NOT NULL DEFAULT '',
	has_cover BOOL DEFAULT 0,
	last_modified TIMESTAMP NOT NULL DEFAULT '2000-01-01 00:00:00+00:00'
);
CREATE TABLE data (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL, format TEXT NOT NULL,
	uncompressed_size INTEGER NOT NULL, name TEXT NOT NULL);
CREATE TABLE authors (
	id INTEGER PRIMARY KEY, name TEXT NOT NULL, sort TEXT);
CREATE TABLE books_authors_link (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL, author INTEGER NOT NULL);
CREATE TABLE series (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE books_series_link (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL, series INTEGER NOT NULL);
CREATE TABLE publishers (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE books_publishers_link (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL, publisher INTEGER NOT NULL);
CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE books_tags_link (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL, tag INTEGER NOT NULL);
CREATE TABLE languages (id INTEGER PRIMARY KEY, lang_code TEXT NOT NULL);
CREATE TABLE books_languages_link (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL,
	lang_code INTEGER NOT NULL, item_order INTEGER NOT NULL DEFAULT 0);
CREATE TABLE comments (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL, text TEXT NOT NULL);
CREATE TABLE identifiers (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL,
	type TEXT NOT NULL DEFAULT 'isbn', val TEXT NOT NULL);
`

// writeCalibreLibrary builds a two-book Calibre library and puts only
// the first book's file on the disk. The second is the case this file
// is about: metadata.db lists it, nothing servable is there.
func writeCalibreLibrary(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, calibre.MetadataDB))
	if err != nil {
		t.Fatal(err)
	}
	run := func(statement string) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("exec: %v\n%s", err, statement)
		}
	}
	run(calibreTestSchema)
	run(`INSERT INTO books
		(id, title, sort, timestamp, pubdate, series_index, path,
		 has_cover, last_modified)
		VALUES
		(1, 'Here', 'Here', '2020-01-02 03:04:05+00:00',
		 '1992-05-01 00:00:00+00:00', 1.0, 'A Writer/Here (1)', 0,
		 '2021-01-01 00:00:00+00:00'),
		(2, 'Gone', 'Gone', '2020-01-02 03:04:05+00:00',
		 '1992-05-01 00:00:00+00:00', 1.0, 'A Writer/Gone (2)', 0,
		 '2021-01-01 00:00:00+00:00')`)
	run(`INSERT INTO data (id, book, format, uncompressed_size, name)
		VALUES (1, 1, 'EPUB', 1024, 'Here'), (2, 2, 'EPUB', 1024, 'Gone')`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	writeBook(t, root, filepath.Join("A Writer/Here (1)", "Here.epub"), "Here")
	return root
}

// TestACalibreBookWithNoFileIsObservedRatherThanSkipped. A book
// metadata.db lists but the disk does not hold is a complete
// observation, not a failure to look — and the difference decides a
// library. Reported as unservable, the pass stays complete, the row and
// everyone's reading of it survive, and the books around it are still
// judged. Left as an error, one such book would make every pass
// incomplete and no book in that library could ever be purged again.
func TestACalibreBookWithNoFileIsObservedRatherThanSkipped(t *testing.T) {
	root := writeCalibreLibrary(t)
	catalog := newFakeCatalog()
	folder := store.Folder{ID: "f-calibre", RootPath: root, Kind: store.FolderCalibre}

	if _, err := testReconciler(catalog).Reconcile(context.Background(), folder); err != nil {
		t.Fatal(err)
	}
	if !catalog.complete {
		t.Fatal("a book with no file on the disk made the whole pass incomplete")
	}
	if len(catalog.observed) != 2 {
		t.Fatalf("want both books observed, got %d: %+v", len(catalog.observed), catalog.observed)
	}
	byCalibreID := map[int64]store.ObservedBook{}
	for _, obs := range catalog.observed {
		if obs.CalibreID == nil {
			t.Fatalf("a calibre observation with no calibre id: %+v", obs)
		}
		byCalibreID[*obs.CalibreID] = obs
	}
	if here := byCalibreID[1]; here.Unservable || here.ContentSHA256 == "" {
		t.Fatalf("the book that is on the disk was not read: %+v", here)
	}
	if gone := byCalibreID[2]; !gone.Unservable {
		t.Fatalf("the book with no file was not reported unservable: %+v", gone)
	}
}
