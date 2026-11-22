package webui

// What the Goodreads export does to one cell and to one ISBN, tested
// apart from the shelf: these are the rules a file that must survive an
// importer and a spreadsheet depends on.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/reading"
	"github.com/chmouel/liseur-sync/internal/store"
)

// TestGoodreadsShelvesAreTheOnesThereAre pins the mapping: an abandoned
// book has a shelf of its own there now, a paused one is in progress, and
// an explicit unread is waiting. The status itself has to survive the fold
// as a custom shelf, because Goodreads has no column for the rest of it.
func TestGoodreadsShelvesAreTheOnesThereAre(t *testing.T) {
	for _, tc := range []struct {
		status    reading.Status
		exclusive string
		shelf     string
	}{
		{reading.StatusReading, "currently-reading", "liseur-reading"},
		{reading.StatusPaused, "currently-reading", "liseur-paused"},
		{reading.StatusFinished, "read", "liseur-finished"},
		{reading.StatusDidNotFinish, "did-not-finish", "liseur-did-not-finish"},
		{reading.StatusWantToRead, "to-read", "liseur-want-to-read"},
		{reading.StatusUnread, "to-read", "liseur-unread"},
		{reading.StatusReadyToRead, "to-read", "liseur-not-started"},
		{reading.Status("something new"), "to-read", "liseur-not-started"},
	} {
		exclusive, shelf := goodreadsShelf(tc.status)
		if exclusive != tc.exclusive || shelf != tc.shelf {
			t.Errorf("%s: got %q / %q, want %q / %q",
				tc.status, exclusive, shelf, tc.exclusive, tc.shelf)
		}
	}
}

// TestGoodreadsCellsSurviveAnImporter is the robustness that matters:
// a title is somebody else's text, and it must not be able to end a row,
// change a delimiter, or be evaluated as a formula.
func TestGoodreadsCellsSurviveAnImporter(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Dune", "Dune"},
		{`Dune, or "Spice"`, `Dune, or "Spice"`},
		{"Dune\nPart Two", "Dune Part Two"},
		{"Dune\r\nPart\tTwo", "Dune Part Two"},
		{"Dune\x00\x07", "Dune"},
		{"  Dune  ", "Dune"},
		{"", ""},
		{"=HYPERLINK(\"http://x\")", `'=HYPERLINK("http://x")`},
		{"+1", "'+1"},
		{"-1", "'-1"},
		{"@book", "'@book"},
		// The apostrophe is for a leading character only: a formula that
		// starts mid-string is text everywhere.
		{"The = sign", "The = sign"},
	} {
		if got := goodreadsCell(tc.in); got != tc.want {
			t.Errorf("goodreadsCell(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestGoodreadsRowsAreExactlyAsWideAsTheHeader keeps the two in step: a
// row one cell short shifts every value after it into the wrong column,
// which is a file that imports as nonsense rather than as an error.
func TestGoodreadsRowsAreExactlyAsWideAsTheHeader(t *testing.T) {
	row := goodreadsRecord(LibraryRow{Title: "Dune", Author: "Frank Herbert"},
		goodreadsBook{}, nil)
	if len(row) != len(goodreadsColumns) {
		t.Fatalf("row has %d cells, header has %d", len(row), len(goodreadsColumns))
	}
}

// TestGoodreadsHeaderIsTheirs pins the header verbatim. Their importer
// reads a column by name, so a rename is a rejected file.
func TestGoodreadsHeaderIsTheirs(t *testing.T) {
	want := "Book Id,Title,Author,Author l-f,Additional Authors,ISBN,ISBN13," +
		"My Rating,Average Rating,Publisher,Binding,Number of Pages," +
		"Year Published,Original Publication Year,Date Read,Date Added," +
		"Bookshelves,Bookshelves with positions,Exclusive Shelf,My Review," +
		"Spoilers,Private Notes,Read Count,Owned Copies"
	if got := strings.Join(goodreadsColumns, ","); got != want {
		t.Errorf("header = %q,\nwant %q", got, want)
	}
}

// TestGoodreadsISBNsComeFromIdentifiers covers the conversion both ways:
// a book whose file carries an ISBN-10 still gets an ISBN13 column, and
// one carrying only the 13 gets its 10 back.
func TestGoodreadsISBNsComeFromIdentifiers(t *testing.T) {
	ten := isbnFromIdentifiers([]store.BookIdentifier{{Scheme: "isbn", Value: "0-4390-2348-3"}})
	if ten.isbn10 != "0439023483" || ten.isbn13 != "9780439023481" {
		t.Errorf("from an ISBN-10: got %+v", ten)
	}
	thirteen := isbnFromIdentifiers([]store.BookIdentifier{{Scheme: "ISBN", Value: "9780439023481"}})
	if thirteen.isbn10 != "0439023483" || thirteen.isbn13 != "9780439023481" {
		t.Errorf("from an ISBN-13: got %+v", thirteen)
	}
	// 979 is not representable as an ISBN-10, so that column stays empty
	// rather than holding something invented.
	nine := isbnFromIdentifiers([]store.BookIdentifier{{Scheme: "isbn", Value: "9791234567896"}})
	if nine.isbn10 != "" || nine.isbn13 != "9791234567896" {
		t.Errorf("from a 979 ISBN-13: got %+v", nine)
	}
	// A scheme that is not an ISBN is not an ISBN, whatever it holds.
	if got := isbnFromIdentifiers([]store.BookIdentifier{
		{Scheme: "asin", Value: "0439023483"},
	}); got != (goodreadsISBN{}) {
		t.Errorf("an ASIN became an ISBN: %+v", got)
	}
}

// TestGoodreadsAuthorsAreNotACreditLine: the shelf's own author column is
// a cover credit — up to three names and "and N others" — and Goodreads
// matches an import against one author plus the additional ones, so the
// list the store can give is what gets written.
func TestGoodreadsAuthorsAreNotACreditLine(t *testing.T) {
	row := LibraryRow{Author: "Ann Author, Bo Writer and one other"}
	author, additional := goodreadsAuthors(row, []string{"Ann Author", "Bo Writer", "Cy Poet"})
	if author != "Ann Author" || additional != "Bo Writer, Cy Poet" {
		t.Errorf("three authors: got %q / %q", author, additional)
	}
	author, additional = goodreadsAuthors(row, []string{"Ann Author"})
	if author != "Ann Author" || additional != "" {
		t.Errorf("one author: got %q / %q", author, additional)
	}
	// A work with no file here has no contributor rows, only the name a
	// device reported, and that name is the author.
	author, additional = goodreadsAuthors(row, nil)
	if author != row.Author || additional != "" {
		t.Errorf("no list: got %q / %q", author, additional)
	}
}

// TestGoodreadsRejectsAnISBNThatDoesNotValidate: Goodreads refuses the
// whole row when the ISBN it was handed is wrong, so a typo in a book's
// metadata must not reach the column. The book still imports, matched on
// title and author.
func TestGoodreadsRejectsAnISBNThatDoesNotValidate(t *testing.T) {
	// The last one is a checksum-valid EAN-13 from outside the ISBN
	// prefixes: a barcode, not a book.
	for _, bad := range []string{"9780575076847", "0575076846", "1234567890123", "12345", "4006381333931"} {
		if got := isbnFromIdentifiers([]store.BookIdentifier{{Scheme: "isbn", Value: bad}}); got != (goodreadsISBN{}) {
			t.Errorf("%q was exported as %+v", bad, got)
		}
	}
}

// TestGoodreadsISBNFrom13HandlesTheCheckDigit tables: the 979 prefix has
// no ISBN-10 form, and the modulo arithmetic has an X for a check digit.
func TestGoodreadsISBNFrom13HandlesTheCheckDigit(t *testing.T) {
	if got := isbn10From13("9791234567896"); got != "" {
		t.Errorf("a 979 ISBN-13 became %q", got)
	}
	if got := isbn13From10("not-an-isbn"); got != "" {
		t.Errorf("garbage became %q", got)
	}
	if !validISBN10("0439023483") {
		t.Error("a known-good ISBN-10 did not validate")
	}
	if !validISBN13("9780439023481") {
		t.Error("a known-good ISBN-13 did not validate")
	}
}

// countingRelations answers a relations lookup with nothing and counts
// how often it was asked, which is the only way to watch the batching
// from outside: a shelf of 500 books must not be 500 round trips.
type countingRelations struct {
	store.Store
	calls int
	err   error
}

func (c *countingRelations) CatalogBookRelationsForBooks(
	context.Context, string, []string,
) (store.CatalogBookRelations, error) {
	c.calls++
	return store.CatalogBookRelations{}, c.err
}

// TestGoodreadsExportBatchesItsRelationReads: a big shelf is a few
// queries, not one per book, because an export that takes long enough
// times out halfway and looks like it worked.
func TestGoodreadsExportBatchesItsRelationReads(t *testing.T) {
	st := &countingRelations{Store: NewTestStore(t)}
	s := &Server{St: st}
	rows := make([]LibraryRow, 0, 500)
	for i := range 500 {
		rows = append(rows, LibraryRow{BookID: strconv.Itoa(i), Title: "Dune"})
	}
	books, err := s.goodreadsBooks(
		httptest.NewRequest(http.MethodGet, "/ui/export/goodreads.csv", nil), "u1", rows)
	if err != nil {
		t.Fatal(err)
	}
	if want := (500 + goodreadsRelationBatch - 1) / goodreadsRelationBatch; st.calls != want {
		t.Errorf("500 books took %d relation reads, want %d", st.calls, want)
	}
	if len(books) != 500 {
		t.Errorf("resolved %d books, want 500", len(books))
	}
}

// TestGoodreadsRelationFailureIsAnError: a book this server could not read
// the metadata of is not silently exported without an ISBN. The file would
// download, import, and quietly shelve the book somewhere else.
func TestGoodreadsRelationFailureIsAnError(t *testing.T) {
	st := &countingRelations{Store: NewTestStore(t), err: errors.New("boom")}
	s := &Server{St: st}
	_, err := s.goodreadsBooks(
		httptest.NewRequest(http.MethodGet, "/ui/export/goodreads.csv", nil), "u1",
		[]LibraryRow{{BookID: "book-1", Title: "Dune"}})
	if err == nil {
		t.Fatal("a failed relations read was exported as a book without an ISBN")
	}
}
