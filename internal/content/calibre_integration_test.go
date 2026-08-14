//go:build linux

package content

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/chmouel/liseur-sync/internal/store"
	sqlitestore "github.com/chmouel/liseur-sync/internal/store/sqlite"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

// calibreFixture is a real Calibre library on disk — metadata.db and a
// tree of EPUBs — in front of a real store and a real CAS, so these
// tests exercise the refresh the server runs rather than a stand-in.
type calibreFixture struct {
	t       *testing.T
	ctx     context.Context
	store   *sqlitestore.Store
	cas     *CAS
	root    string
	library store.Library
	now     time.Time
}

func newCalibreFixture(t *testing.T) *calibreFixture {
	t.Helper()
	dir := t.TempDir()
	cas, err := Open(filepath.Join(dir, "content"))
	if err != nil {
		t.Fatalf("open cas: %v", err)
	}
	t.Cleanup(func() { cas.Close() })
	st, err := sqlitestore.Open(filepath.Join(dir, "liseur.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	user := storetest.MkUser(t, st, "curator")
	root := filepath.Join(dir, "calibre")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	library := store.Library{
		ID: "lib-calibre", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Source:  store.LibraryCalibre,
		Storage: store.LibraryStorageInPlace,
		Refresh: store.LibraryRefreshInterval, Name: "Calibre",
		RootPath: &root, CreatedAt: now,
	}
	if err := st.CreateLibrary(ctx, library); err != nil {
		t.Fatalf("create library: %v", err)
	}
	f := &calibreFixture{
		t: t, ctx: ctx, store: st, cas: cas,
		root: root, library: library, now: now,
	}
	f.createDB()
	return f
}

func (f *calibreFixture) clock() func() time.Time {
	return func() time.Time {
		f.now = f.now.Add(time.Second)
		return f.now
	}
}

// exec runs one statement against metadata.db, which is how these tests
// stand in for somebody using Calibre.
func (f *calibreFixture) exec(statement string, args ...any) {
	f.t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(f.root, "metadata.db"))
	if err != nil {
		f.t.Fatalf("open metadata.db: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(f.ctx, statement, args...); err != nil {
		f.t.Fatalf("metadata.db: %v\n%s", err, statement)
	}
}

func (f *calibreFixture) createDB() {
	f.t.Helper()
	f.exec(`
CREATE TABLE books (
	id INTEGER PRIMARY KEY, title TEXT NOT NULL DEFAULT 'Unknown',
	sort TEXT, timestamp TIMESTAMP, pubdate TIMESTAMP,
	series_index REAL NOT NULL DEFAULT 1.0, path TEXT NOT NULL DEFAULT '',
	has_cover BOOL DEFAULT 0,
	last_modified TIMESTAMP NOT NULL DEFAULT '2000-01-01 00:00:00+00:00');
CREATE TABLE data (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL, format TEXT NOT NULL,
	uncompressed_size INTEGER NOT NULL, name TEXT NOT NULL);
CREATE TABLE authors (id INTEGER PRIMARY KEY, name TEXT NOT NULL, sort TEXT);
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
`)
}

// addBook writes one book to metadata.db and its EPUB to the tree, the
// way Calibre does.
func (f *calibreFixture) addBook(id int64, title, dir, name string, body []byte) {
	f.t.Helper()
	f.exec(`INSERT INTO books (id, title, path, last_modified, pubdate)
		VALUES (?, ?, ?, '2024-01-01 00:00:00+00:00',
		        '1992-05-01 00:00:00+00:00')`, id, title, dir)
	f.exec(`INSERT INTO data (book, format, uncompressed_size, name)
		VALUES (?, 'EPUB', ?, ?)`, id, len(body), name)
	f.write(dir+"/"+name+".epub", body)
}

func (f *calibreFixture) write(rel string, body []byte) {
	f.t.Helper()
	full := filepath.Join(f.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		f.t.Fatalf("write %s: %v", rel, err)
	}
}

func (f *calibreFixture) refresh() CalibreSyncReport {
	f.t.Helper()
	report, err := SyncCalibreLibrary(f.ctx, f.store, f.cas, f.scanned(),
		WatchedSyncOptions{
			MaxFileBytes: 1 << 20,
			Patterns:     NewLibraryPatterns(f.store),
		}, f.clock())
	if err != nil {
		f.t.Fatalf("calibre refresh: %v", err)
	}
	return report
}

// scanned reads the library back, so the refresh sees the digest the
// previous one recorded exactly as the worker would.
func (f *calibreFixture) scanned() ScannedLibrary {
	f.t.Helper()
	library, err := f.store.AdminLibraryByID(f.ctx, f.library.ID)
	if err != nil {
		f.t.Fatalf("read library: %v", err)
	}
	return ScannedLibrary{
		ID: library.ID, Source: library.Source, Storage: library.Storage,
		RootPath: f.root, ActorUserID: library.OwnerUserID,
		InventoryDigest: library.LastInventoryDigest,
	}
}

func (f *calibreFixture) books() []store.CatalogBook {
	f.t.Helper()
	books, err := f.store.ListCatalogBooks(
		f.ctx, f.library.OwnerUserID, f.library.ID, nil, 100)
	if err != nil {
		f.t.Fatalf("list books: %v", err)
	}
	return books
}

func (f *calibreFixture) bookByTitle(title string) store.CatalogBook {
	f.t.Helper()
	for _, book := range f.books() {
		if book.Title == title {
			return book
		}
	}
	f.t.Fatalf("no book titled %q in %+v", title, f.books())
	return store.CatalogBook{}
}

// TestCalibreRefreshCatalogsWhatTheDatabaseSays is the whole path: books
// come from metadata.db, their bytes stay where they are, and Calibre's
// metadata is what the catalog ends up holding.
func TestCalibreRefreshCatalogsWhatTheDatabaseSays(t *testing.T) {
	f := newCalibreFixture(t)
	f.addBook(1, "Small Gods", "Pratchett/Small Gods (1)",
		"Small Gods - Terry Pratchett", minimalEPUB(t))
	f.exec(`INSERT INTO authors (id, name) VALUES (1, 'Terry Pratchett')`)
	f.exec(`INSERT INTO books_authors_link (book, author) VALUES (1, 1)`)
	f.exec(`INSERT INTO tags (id, name) VALUES (1, 'Fantasy')`)
	f.exec(`INSERT INTO books_tags_link (book, tag) VALUES (1, 1)`)
	f.exec(`INSERT INTO comments (book, text) VALUES (1, 'A novel of Omnia.')`)

	// An in-place pass publishes the book itself, so one refresh both
	// catalogs the file and describes it.
	first := f.refresh()
	if first.Books != 1 || first.Sync.Ingested != 1 {
		t.Fatalf("first refresh: %+v", first)
	}
	if first.Mapped != 1 || first.MetadataUpdated != 1 {
		t.Fatalf("first refresh did not describe the book: %+v", first)
	}

	// Nothing under the root was copied: this library is served in place.
	assertEmptyContentTree(t, filepath.Join(filepath.Dir(f.root), "content"))

	book := f.bookByTitle("Small Gods")
	if book.TitleSource != store.MetadataCalibre {
		t.Errorf("title provenance = %q", book.TitleSource)
	}
	if book.Description != "A novel of Omnia." {
		t.Errorf("description = %q", book.Description)
	}
	metadata, err := f.store.CatalogBookMetadata(
		f.ctx, f.library.OwnerUserID, book.ID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Tags) != 1 || metadata.Tags[0].Name != "Fantasy" {
		t.Errorf("tags = %+v", metadata.Tags)
	}
	if len(metadata.Contributors) != 1 ||
		metadata.Contributors[0].Name != "Terry Pratchett" {
		t.Errorf("contributors = %+v", metadata.Contributors)
	}

	// A second pass with nothing changed stops at the gate and writes
	// nothing at all.
	f.now = f.now.Add(time.Hour)
	second := f.refresh()
	if !second.Skipped {
		t.Fatalf("an unchanged library was refreshed anyway: %+v", second)
	}
	if second.MetadataUpdated != 0 || second.Sync.Ingested != 0 {
		t.Fatalf("a skipped refresh did work: %+v", second)
	}
}

// TestCalibreRefreshFollowsAndYieldsToEdits is the precedence rule where
// it matters: Calibre owns a field until a human takes it.
func TestCalibreRefreshFollowsAndYieldsToEdits(t *testing.T) {
	f := newCalibreFixture(t)
	f.addBook(1, "Small Gods", "Pratchett/Small Gods (1)",
		"Small Gods", minimalEPUB(t))
	f.refresh()

	// A title corrected in Calibre lands.
	f.exec(`UPDATE books SET title = 'Small Gods (Discworld 13)',
		last_modified = '2024-02-02 00:00:00+00:00' WHERE id = 1`)
	f.now = f.now.Add(time.Hour)
	if report := f.refresh(); report.MetadataUpdated != 1 {
		t.Fatalf("a title changed in Calibre did not land: %+v", report)
	}
	book := f.bookByTitle("Small Gods (Discworld 13)")

	// A title corrected here is locked, and survives Calibre changing
	// the same field again.
	current, err := f.store.CatalogBookMetadata(
		f.ctx, f.library.OwnerUserID, book.ID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	current.Book.Title = "The One I Chose"
	current.Book.TitleSource = store.MetadataManual
	current.Book.TitleLocked = true
	if _, err := f.store.ApplyCatalogBookMetadata(
		f.ctx, f.library.OwnerUserID, store.ApplyBookMetadataRequest{
			Metadata:         current,
			ExpectedRevision: current.Book.Revision,
			UpdatedAt:        f.clock()(),
		}); err != nil {
		t.Fatal(err)
	}

	f.exec(`UPDATE books SET title = 'Something Else Entirely',
		last_modified = '2024-03-03 00:00:00+00:00' WHERE id = 1`)
	f.now = f.now.Add(time.Hour)
	f.refresh()
	if got := f.bookByTitle("The One I Chose"); got.ID != book.ID {
		t.Fatalf("a manual title did not survive a Calibre refresh: %+v",
			f.books())
	}
}

// TestCalibreRefreshDeletesWhatCalibreForgot is the deletion rule, and
// the promise that goes with it: the file on disk is not ours to remove.
func TestCalibreRefreshDeletesWhatCalibreForgot(t *testing.T) {
	f := newCalibreFixture(t)
	f.addBook(1, "Small Gods", "Pratchett/Small Gods (1)",
		"Small Gods", minimalEPUB(t))
	f.addBook(2, "Good Omens", "Pratchett/Good Omens (2)",
		"Good Omens", variantEPUB(t, "good-omens"))
	f.refresh()
	if len(f.books()) != 2 {
		t.Fatalf("books after the first refreshes: %+v", f.books())
	}

	// Somebody has been reading the book Calibre is about to forget.
	// Reading history hangs off user-scoped works, not off catalog rows,
	// and this is the assertion that says so.
	work := storetest.MkWork(f.t, f.store, store.User{ID: f.library.OwnerUserID},
		"work-good-omens", "good-omens-edition")
	if _, err := f.store.AppendOps(f.ctx, f.library.OwnerUserID, "d-reader",
		[]store.Op{{
			OpID:        "018e6f1a-0000-7000-8000-0000000000c1",
			WorkID:      work.ID,
			EditionSHA:  ptr("good-omens-edition"),
			ClientTS:    f.clock()(),
			Progression: 0.61,
			Origin:      store.OriginNative,
		}}); err != nil {
		t.Fatal(err)
	}

	f.exec(`DELETE FROM books WHERE id = 2`)
	f.exec(`DELETE FROM data WHERE book = 2`)
	f.now = f.now.Add(time.Hour)
	report := f.refresh()
	if report.Deleted != 1 {
		t.Fatalf("a book deleted in Calibre was not deleted here: %+v", report)
	}
	books := f.books()
	if len(books) != 1 || books[0].Title != "Small Gods" {
		t.Fatalf("books after the deletion: %+v", books)
	}
	// The bytes were never the server's to delete.
	path := filepath.Join(f.root, "Pratchett/Good Omens (2)/Good Omens.epub")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a deleted catalog book took somebody's file with it: %v", err)
	}
	positions, err := f.store.Positions(f.ctx, f.library.OwnerUserID, work.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].Progression != 0.61 {
		t.Fatalf("reading history did not survive the deletion: %+v", positions)
	}
}

// TestCalibreRefreshRefusesADirectoryThatIsNotACalibreLibrary keeps a
// misconfigured root from looking like an empty library, which would
// delete everything the next pass.
func TestCalibreRefreshRefusesADirectoryThatIsNotACalibreLibrary(t *testing.T) {
	f := newCalibreFixture(t)
	f.addBook(1, "Small Gods", "Pratchett/Small Gods (1)",
		"Small Gods", minimalEPUB(t))
	f.refresh()

	if err := os.Remove(filepath.Join(f.root, "metadata.db")); err != nil {
		t.Fatal(err)
	}
	_, err := SyncCalibreLibrary(f.ctx, f.store, f.cas, f.scanned(),
		WatchedSyncOptions{MaxFileBytes: 1 << 20}, f.clock())
	if err == nil {
		t.Fatal("a root with no metadata.db refreshed successfully")
	}
	if len(f.books()) != 1 {
		t.Fatalf("a failed refresh changed the catalog: %+v", f.books())
	}
}

// assertEmptyContentTree is the in-place promise: a library served where
// it lies writes nothing into content-addressed storage.
func assertEmptyContentTree(t *testing.T, contentRoot string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(contentRoot, "sha256"))
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("an in-place library wrote %d entries into the CAS",
			len(entries))
	}
}
