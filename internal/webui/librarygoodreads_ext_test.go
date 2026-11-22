//go:build linux

package webui_test

// The Goodreads export, end to end: the file a reader downloads and
// hands to Goodreads, read back as a CSV and checked against the shelf it
// came from.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// goodreadsIndex maps each column of an exported file to its position,
// taken from the file's own header. A test that indexes a column by a
// hardcoded number stops testing anything the moment the header moves.
func goodreadsIndex(t *testing.T, header []string) map[string]int {
	t.Helper()
	idx := make(map[string]int, len(header))
	for i, name := range header {
		idx[name] = i
	}
	for _, required := range []string{
		"Book Id", "Title", "Author", "ISBN", "ISBN13", "Date Read",
		"Date Added", "Bookshelves", "Exclusive Shelf", "Read Count",
		"Owned Copies",
	} {
		if _, ok := idx[required]; !ok {
			t.Fatalf("the header is missing %q: %q", required, header)
		}
	}
	return idx
}

// goodreadsBookRow finds one book's row by title.
func goodreadsBookRow(t *testing.T, rows [][]string, idx map[string]int, title string) []string {
	t.Helper()
	for _, row := range rows[1:] {
		if row[idx["Title"]] == title {
			return row
		}
	}
	t.Fatalf("no row for %q in %d rows", title, len(rows)-1)
	return nil
}

// seedGoodreadsBook catalogues a book with the metadata the ISBN columns
// read, and hangs a work and a status off it the way a sync would.
func seedGoodreadsBook(
	t *testing.T, f *booksFixture, now time.Time, name, title string,
	ids []store.BookIdentifier, at float64, status store.ReadingStatus,
) string {
	t.Helper()
	book := f.observe(t, store.ObservedBook{
		RelativePath: name + ".epub", SizeBytes: 4096, MTime: now,
		ContentSHA256: strings.Repeat(name[:1], 64),
		MediaType:     "application/epub+zip",
		Title:         title,
		Contributors: []store.ObservedContributor{
			{Name: "Frank Herbert", Role: store.ContributorRoleAuthor, Position: 1},
			{Name: "Brian Herbert", Role: store.ContributorRoleAuthor, Position: 2},
		},
		Identifiers: ids,
	})
	work := "w-" + name
	if _, err := f.st.ResolveCatalogBookWork(t.Context(), "u1", book,
		store.Work{ID: work, UserID: "u1", Title: title, CreatedAt: now},
		nil, nil, true, now); err != nil {
		t.Fatal(err)
	}
	if at > 0 {
		progressOn(t, f, work, "pos-"+name, at, now)
	}
	if status != "" {
		statusOn(t, f, work, "act-"+name, status, at, now)
	}
	return book
}

// TestTheShelfExportsAsGoodreadsImportsIt is the file itself: their
// header, one row per book, the reading status folded onto their three
// shelves with the original status kept as a custom shelf, and the dates
// in their own format.
func TestTheShelfExportsAsGoodreadsImportsIt(t *testing.T) {
	f := newBooksFixture(t)
	now := time.Now().UTC()

	// A file's own title: a comma, a quote and a line break, all of which
	// have to survive as one cell of readable text — the break becomes a
	// space, because a row is a row.
	const messy = `Dune, or "Spice"`
	const messyTitle = messy + "\nPart Two"
	const messyExported = messy + " Part Two"
	seedGoodreadsBook(t, f, now, "done", messyTitle,
		[]store.BookIdentifier{{Scheme: "isbn", Value: "0-4390-2348-3"}},
		1, store.ReadingStatusFinished)

	seedGoodreadsBook(t, f, now, "paused", "Children of Dune", nil,
		0.8, store.ReadingStatusPaused)

	seedGoodreadsBook(t, f, now, "abandoned", "Heretics of Dune", nil,
		0.3, store.ReadingStatusDidNotFinish)

	// A catalog book nobody has opened: no work behind it at all.
	f.observe(t, store.ObservedBook{
		RelativePath: "fresh.epub", SizeBytes: 4096, MTime: now,
		ContentSHA256: strings.Repeat("f", 64), MediaType: "application/epub+zip",
		Title: "God Emperor of Dune",
		Contributors: []store.ObservedContributor{{
			Name: "Frank Herbert", Role: store.ContributorRoleAuthor, Position: 1,
		}},
	})

	resp, body := f.get(t, "/ui/export/goodreads.csv?folder="+f.folder, f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: got %d, want 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", got)
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.Contains(got, "goodreads.csv") {
		t.Errorf("Content-Disposition = %q, want an attachment named goodreads.csv", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if !strings.Contains(body, "\r\n") {
		t.Error("the file has no CRLF line endings, and Goodreads' own export has them")
	}
	for i, line := range strings.Split(body, "\r\n") {
		if strings.Contains(line, "\n") {
			t.Fatalf("line %d holds a bare newline: a title ended a row", i)
		}
	}

	rows := csvRows(t, body)
	idx := goodreadsIndex(t, rows[0])
	for i, row := range rows {
		if len(row) != len(rows[0]) {
			t.Fatalf("row %d has %d cells, the header has %d: %q", i, len(row), len(rows[0]), row)
		}
	}

	done := goodreadsBookRow(t, rows, idx, messyExported)
	for _, field := range []struct{ column, want string }{
		{"Book Id", ""}, // Goodreads assigns one; ours would be an edit of their catalog.
		{"Author", "Frank Herbert"},
		{"Additional Authors", "Brian Herbert"},
		{"ISBN", "0439023483"},
		{"ISBN13", "9780439023481"},
		{"Exclusive Shelf", "read"},
		{"Bookshelves", "read, liseur-finished"},
		{"Bookshelves with positions", "read (#1), liseur-finished (#2)"},
		{"Read Count", "1"},
		{"Owned Copies", "0"},
		{"My Rating", ""},
		{"Author l-f", ""},
	} {
		if got := done[idx[field.column]]; got != field.want {
			t.Errorf("%s = %q, want %q", field.column, got, field.want)
		}
	}
	// Date Read stays empty even for a book read to the end: the only
	// timestamp this server has for a work is when the newest op was
	// received, which is a late sync or a reread, not the day the book was
	// closed — and a wrong year is worse than a blank one in a reading
	// challenge.
	if got := done[idx["Date Read"]]; got != "" {
		t.Errorf("Date Read = %q, want empty: receipt time is not the finish date", got)
	}
	if value := done[idx["Date Added"]]; value != "" {
		if _, err := time.Parse("2006/01/02", value); err != nil {
			t.Errorf("Date Added = %q, want a YYYY/MM/DD date: %v", value, err)
		}
	} else {
		t.Error("Date Added is empty for a book the catalog holds")
	}

	paused := goodreadsBookRow(t, rows, idx, "Children of Dune")
	if got := paused[idx["Exclusive Shelf"]]; got != "currently-reading" {
		t.Errorf("a paused book is on %q, want currently-reading", got)
	}
	if got := paused[idx["Bookshelves"]]; got != "currently-reading, liseur-paused" {
		t.Errorf("a paused book's shelf = %q, want liseur-paused", got)
	}
	// A book in progress has not been read, so it has no Date Read. The
	// name is theirs, and it means the day it was finished.
	if got := paused[idx["Date Read"]]; got != "" {
		t.Errorf("a paused book has Date Read = %q, want empty", got)
	}
	if got := paused[idx["Read Count"]]; got != "0" {
		t.Errorf("a paused book has Read Count = %q, want 0", got)
	}

	abandoned := goodreadsBookRow(t, rows, idx, "Heretics of Dune")
	if got := abandoned[idx["Exclusive Shelf"]]; got != "did-not-finish" {
		t.Errorf("an abandoned book is on %q, want did-not-finish", got)
	}
	if got := abandoned[idx["Bookshelves"]]; got != "did-not-finish, liseur-did-not-finish" {
		t.Errorf("an abandoned book's shelves = %q", got)
	}
	if got := abandoned[idx["Read Count"]]; got != "0" {
		t.Errorf("an abandoned book has Read Count = %q, want 0", got)
	}

	fresh := goodreadsBookRow(t, rows, idx, "God Emperor of Dune")
	if got := fresh[idx["Exclusive Shelf"]]; got != "to-read" {
		t.Errorf("a book nobody opened is on %q, want to-read", got)
	}
	if got := fresh[idx["Bookshelves"]]; got != "to-read, liseur-not-started" {
		t.Errorf("a book nobody opened has shelf %q, want liseur-not-started", got)
	}
	if got := fresh[idx["Date Read"]]; got != "" {
		t.Errorf("a book nobody opened has Date Read = %q, want empty", got)
	}
}

// TestTheGoodreadsExportIsOnlyWhatTheReaderMaySee is the same folder
// grant as the plain export, because both read the shelf through the same
// code and neither may be the one that forgot to ask.
func TestTheGoodreadsExportIsOnlyWhatTheReaderMaySee(t *testing.T) {
	f := newBooksFixture(t)
	f.observe(t, store.ObservedBook{
		RelativePath: "mine.epub", SizeBytes: 4096, MTime: time.Now().UTC(),
		ContentSHA256: strings.Repeat("a", 64), MediaType: "application/epub+zip",
		Title: "Alice's Only Book",
	})
	if err := f.st.CreateFolder(t.Context(), store.Folder{
		ID: "folder-other", Name: "Somebody Else's", RootPath: t.TempDir(),
		Kind: store.FolderPlain, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ReconcileFolder(t.Context(), "folder-other", []store.ObservedBook{{
		RelativePath: "theirs.epub", SizeBytes: 4096, MTime: time.Now().UTC(),
		ContentSHA256: strings.Repeat("e", 64), MediaType: "application/epub+zip",
		Title: "Not Alice's Book",
	}}, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	resp, body := f.get(t, "/ui/export/goodreads.csv?folder=folder-other", f.cookie)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a folder nobody granted: got %d, want 404: %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "Not Alice's Book") {
		t.Fatal("the export leaked a book from a folder this reader holds nothing in")
	}

	// With no folder in the address, Settings means every folder this
	// account holds and no more.
	_, body = f.get(t, "/ui/export/goodreads.csv?filter=all", f.cookie)
	if !strings.Contains(body, "Alice's Only Book") {
		t.Error("the export missed a book from a granted folder")
	}
	if strings.Contains(body, "Not Alice's Book") {
		t.Fatal("the export reached a folder this reader holds nothing in")
	}
}

// TestSettingsOffersBothExports pins the two buttons to the two files, so
// neither dialog can be wired to the other's address.
func TestSettingsOffersBothExports(t *testing.T) {
	f := newBooksFixture(t)
	_, page := f.get(t, "/ui/settings", f.cookie)

	for _, want := range []string{
		`data-csv-open="csv-dialog"`,
		`id="csv-dialog"`,
		`export/library.csv?filter=all`,
		`download="library.csv"`,
		`data-csv-open="goodreads-dialog"`,
		`id="goodreads-dialog"`,
		`export/goodreads.csv?filter=all`,
		`download="goodreads.csv"`,
		`data-csv-copy`,
		`library-csv.js`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the settings page is missing %q", want)
		}
	}
}
