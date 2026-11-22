package webui

// The Goodreads export: the same shelf, written as the file Goodreads'
// importer expects it.
//
// It is a second format rather than a replacement, because Goodreads has
// nowhere to put two of the columns the library's own export carries: a
// reading position is not a shelf, and their Book Id is theirs to assign,
// not ours. Everything else crosses — the title, the author, when the
// book arrived, when it was last read, and the reading state, folded onto
// the three exclusive shelves they have.
//
// The rows are the same rows, collected by [Server.exportShelfRows], so
// the two files can never disagree about which books are on the shelf.
// What differs is the writing: their importer matches a column by name
// and refuses a file whose header it does not recognise, so the header
// below is their own, verbatim and in order, and every cell is written
// through [goodreadsCell] so that nothing in a book's metadata can break
// the file or a spreadsheet.

import (
	"encoding/csv"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/chmouel/liseur-sync/internal/reading"
	"github.com/chmouel/liseur-sync/internal/store"
)

// goodreadsColumns is Goodreads' own export header, verbatim and in its
// order. Their importer reads a column by this name and rejects a file
// whose header it does not recognise, so a column is never renamed,
// reordered or dropped here — including the ones this server has no data
// for, which are written empty.
var goodreadsColumns = []string{
	"Book Id", "Title", "Author", "Author l-f", "Additional Authors",
	"ISBN", "ISBN13", "My Rating", "Average Rating", "Publisher", "Binding",
	"Number of Pages", "Year Published", "Original Publication Year",
	"Date Read", "Date Added", "Bookshelves", "Bookshelves with positions",
	"Exclusive Shelf", "My Review", "Spoilers", "Private Notes",
	"Read Count", "Owned Copies",
}

// goodreadsDate is the date format Goodreads writes and reads in its own
// export: slashes, year first, no timezone.
const goodreadsDate = "2006/01/02"

// The exclusive shelves, as Goodreads spells them. A book is on exactly
// one of them.
const (
	goodreadsRead             = "read"
	goodreadsCurrentlyReading = "currently-reading"
	goodreadsToRead           = "to-read"
	goodreadsDidNotFinish     = "did-not-finish"
)

// goodreadsRelationBatch is how many book ids one relations lookup
// carries. The store takes a list; the batch keeps it under every
// backend's parameter limit, which matters on PostgreSQL long before
// SQLite.
const goodreadsRelationBatch = 200

func (s *Server) handleLibraryGoodreads(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	rows, ok := s.exportShelfRows(w, r, u)
	if !ok {
		return
	}
	// What the ISBN and author columns need is resolved before the first
	// byte is written, for the same reason the rows are: an error halfway
	// down a file nobody can re-open is worse than an error page.
	books, err := s.goodreadsBooks(r, u.ID, rows)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	loc := userLoc(u)
	writeCSV(w, "goodreads.csv", true, goodreadsColumns, func(cw *csv.Writer) {
		for _, row := range rows {
			_ = cw.Write(goodreadsRecord(row, books[row.BookID], loc))
		}
	})
}

// goodreadsBook is what the export needs about one catalog book beyond
// the shelf row itself: the identifiers Goodreads matches an import
// against, and the authors as a list rather than as one credit line.
type goodreadsBook struct {
	isbn    goodreadsISBN
	authors []string
}

// goodreadsBooks resolves both, in batches, for every book on the shelf.
//
// One call per batch of books rather than two per book: an export of a
// ten-thousand-book shelf would otherwise be twenty thousand round trips,
// which is the difference between a download and a timeout on a busy
// server. A failed read is an error and not a file: a book silently
// dropped from a matching key imports as a book nobody shelved, and a
// download that says nothing about it looks like it worked.
func (s *Server) goodreadsBooks(
	r *http.Request, userID string, rows []LibraryRow,
) (map[string]goodreadsBook, error) {
	out := make(map[string]goodreadsBook, len(rows))
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.BookID == "" {
			continue
		}
		if _, done := out[row.BookID]; done {
			continue
		}
		ids = append(ids, row.BookID)
		out[row.BookID] = goodreadsBook{}
	}
	for i := 0; i < len(ids); i += goodreadsRelationBatch {
		chunk := ids[i:min(i+goodreadsRelationBatch, len(ids))]
		rel, err := s.St.CatalogBookRelationsForBooks(r.Context(), userID, chunk)
		if err != nil {
			return nil, err
		}
		for _, id := range chunk {
			book := out[id]
			book.isbn = isbnFromIdentifiers(rel.Identifiers[id])
			// Contributors come back in reading order, roles included:
			// only the authors belong in a Goodreads author column.
			for _, c := range rel.Contributors[id] {
				if c.Role == store.ContributorRoleAuthor {
					book.authors = append(book.authors, c.Name)
				}
			}
			out[id] = book
		}
	}
	return out, nil
}

// goodreadsRecord is one row, exactly as wide as the header: the fields
// are keyed by column name and laid out by [goodreadsColumns], so a
// rename there cannot silently shift every value into the wrong column.
func goodreadsRecord(row LibraryRow, book goodreadsBook, loc *time.Location) []string {
	shelf, statusShelf := goodreadsShelf(row.resolved())
	author, additionalAuthors := goodreadsAuthors(row, book.authors)
	dateAdded := ""
	if !row.addedAt.IsZero() {
		dateAdded = row.addedAt.In(loc).Format(goodreadsDate)
	}
	// Date Read is when the book was finished, and this server does not
	// know that: the only timestamp it has for a work is the receipt time
	// of its newest op, which is a late sync or a reread, not the day the
	// book was closed. A wrong date is worse than none here — it files
	// the book in the wrong year of somebody's reading challenge — so the
	// cell stays empty and the reader can date it in Goodreads.
	dateRead := ""
	readCount := "0"
	if shelf == goodreadsRead {
		readCount = "1"
	}
	// Bookshelves carries the exclusive shelf as well as the status
	// shelf, which is what Goodreads' own export writes and what its
	// importer expects to find there; the exclusive column alone is a
	// shelf a reader may end up off. Positions are the order the shelves
	// are applied in, and are theirs by convention.
	shelves := shelf + ", " + statusShelf
	positioned := shelf + " (#1), " + statusShelf + " (#2)"
	// Empty on purpose, every one of these: a rating and a review are
	// things this server does not record, Book Id is Goodreads' to
	// assign, and publisher, binding, page count and publication years
	// are edition facts that belong to a work's edition rather than to a
	// catalog row. An empty cell is a cell their importer leaves alone.
	fields := map[string]string{
		"Title":                      row.Title,
		"Author":                     author,
		"Additional Authors":         additionalAuthors,
		"ISBN":                       book.isbn.isbn10,
		"ISBN13":                     book.isbn.isbn13,
		"Date Read":                  dateRead,
		"Date Added":                 dateAdded,
		"Bookshelves":                shelves,
		"Bookshelves with positions": positioned,
		"Exclusive Shelf":            shelf,
		"Read Count":                 readCount,
		"Owned Copies":               "0",
		"My Rating":                  "",
		"Average Rating":             "",
		"Author l-f":                 "",
		"Publisher":                  "",
		"Binding":                    "",
		"Number of Pages":            "",
		"Year Published":             "",
		"Original Publication Year":  "",
		"My Review":                  "",
		"Spoilers":                   "",
		"Private Notes":              "",
	}
	out := make([]string, len(goodreadsColumns))
	for i, name := range goodreadsColumns {
		out[i] = goodreadsCell(fields[name])
	}
	return out
}

// goodreadsAuthors splits a book's authors into the two columns Goodreads
// has for them: one author, and everybody else.
//
// The shelf row's own credit line is not usable for this — it joins up to
// three names with commas and then says "and 4 others", which is right on
// a cover and wrong in a matching key. Where the store can name the
// authors, the first one is the author and the rest are additional. Where
// it cannot — a work this server holds no file for has only the one name
// a device reported — that one name is the best there is.
func goodreadsAuthors(row LibraryRow, names []string) (author, additional string) {
	if len(names) == 0 {
		return row.Author, ""
	}
	return names[0], strings.Join(names[1:], ", ")
}

// goodreadsShelf maps a resolved status onto Goodreads' exclusive
// shelves: finished is read, reading and paused are in progress,
// abandoned is did-not-finish, and everything waiting — an explicit
// "unread" included — is to be read.
//
// Two of those folds lose something — paused is not the same as reading,
// and a book waiting to be read is not the same as one marked unread — so
// the reader's actual status travels alongside as a custom shelf.
// Re-importing the file cannot restore a status this server never sees,
// but the shelf page it came from is described in the file.
func goodreadsShelf(st reading.Status) (exclusive, statusShelf string) {
	switch st {
	case reading.StatusReading:
		return goodreadsCurrentlyReading, "liseur-reading"
	case reading.StatusPaused:
		return goodreadsCurrentlyReading, "liseur-paused"
	case reading.StatusFinished:
		return goodreadsRead, "liseur-finished"
	case reading.StatusDidNotFinish:
		return goodreadsDidNotFinish, "liseur-did-not-finish"
	case reading.StatusWantToRead:
		return goodreadsToRead, "liseur-want-to-read"
	case reading.StatusUnread:
		return goodreadsToRead, "liseur-unread"
	default:
		return goodreadsToRead, "liseur-not-started"
	}
}

// goodreadsCell makes one cell safe for an importer and a spreadsheet.
//
// A title is somebody else's text, and it may hold a line break, a tab, a
// control character or a leading "=" — the first three break a CSV for
// whoever reads it, and the fourth makes Excel and Calc evaluate a book
// title as a formula. Line breaks and controls become spaces (a row is a
// row), runs of whitespace collapse, and a cell that still begins with
// something a spreadsheet would evaluate gets the apostrophe that says
// "this is text". The opposite risk is a title that starts with "-" being
// spelled with an apostrophe on Goodreads; nobody's shelf has one, and a
// formula in a book title is a real thing an uploader can arrange.
func goodreadsCell(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\u2028' || r == '\u2029' || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	switch s[0] {
	case '=', '+', '-', '@':
		return "'" + s
	}
	return s
}

// goodreadsISBN is the pair of columns Goodreads matches an imported book
// against. Either may be empty: an ISBN-10 converts to an ISBN-13 and
// back, and a book with no identifier at all is matched on title and
// author instead.
type goodreadsISBN struct {
	isbn10 string
	isbn13 string
}

// isbnFromIdentifiers picks the ISBNs out of what a pass recorded for a
// book. Values are the file's own, so they may carry hyphens, an X check
// digit or a typo; only a value whose check digit actually validates is
// written, because Goodreads rejects a row with a well-formed-looking but
// wrong ISBN rather than ignoring the column.
func isbnFromIdentifiers(ids []store.BookIdentifier) goodreadsISBN {
	var out goodreadsISBN
	for _, id := range ids {
		if !strings.EqualFold(strings.TrimSpace(id.Scheme), "isbn") {
			continue
		}
		switch v := normalizedISBN(id.Value); {
		case len(v) == 13 && out.isbn13 == "" && validISBN13(v):
			out.isbn13 = v
		case len(v) == 10 && out.isbn10 == "" && validISBN10(v):
			out.isbn10 = v
		}
	}
	if out.isbn10 == "" && out.isbn13 != "" {
		out.isbn10 = isbn10From13(out.isbn13)
	}
	if out.isbn13 == "" && out.isbn10 != "" {
		out.isbn13 = isbn13From10(out.isbn10)
	}
	return out
}

// normalizedISBN keeps the digits and the X check digit, and nothing
// else: "978-0-575-07684-6" and "9780575076846" are the same book.
func normalizedISBN(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		if (r >= '0' && r <= '9') || r == 'X' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) != 10 && len(out) != 13 {
		return ""
	}
	return out
}

// validISBN10 checks the modulo-11 check digit of an ISBN-10, where X
// stands for ten.
func validISBN10(s string) bool {
	if len(s) != 10 {
		return false
	}
	sum := 0
	for i, r := range s {
		d := 0
		switch {
		case r >= '0' && r <= '9':
			d = int(r - '0')
		case r == 'X' && i == 9:
			d = 10
		default:
			return false
		}
		sum += (10 - i) * d
	}
	return sum%11 == 0
}

// validISBN13 checks what makes a thirteen-digit number an ISBN-13 and
// not merely a well-formed EAN: a 978 or 979 prefix, which is the part of
// the number that says "book", and a modulo-10 check digit, with weights
// alternating 1 and 3. A barcode from a cereal box validates as an EAN and
// answers nothing at Goodreads, so it is not written.
func validISBN13(s string) bool {
	if len(s) != 13 || (!strings.HasPrefix(s, "978") && !strings.HasPrefix(s, "979")) {
		return false
	}
	sum := 0
	for i, r := range s {
		if r < '0' || r > '9' {
			return false
		}
		d := int(r - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return sum%10 == 0
}

// isbn13From10 is the mechanical conversion: the 978 prefix, the first
// nine digits, a computed check digit.
func isbn13From10(s string) string {
	if len(s) != 10 || !validISBN10(s) {
		return ""
	}
	body := "978" + s[:9]
	sum := 0
	for i, r := range body {
		d := int(r - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return body + string(rune('0'+(10-sum%10)%10))
}

// isbn10From13 is the conversion that only exists for the 978 prefix: 979
// ISBNs are not representable in the older scheme, so those books get an
// ISBN13 column and an empty ISBN one.
func isbn10From13(s string) string {
	if len(s) != 13 || !validISBN13(s) || !strings.HasPrefix(s, "978") {
		return ""
	}
	body := s[3:12]
	sum := 0
	for i, r := range body {
		sum += (10 - i) * int(r-'0')
	}
	check := (11 - sum%11) % 11
	if check == 10 {
		return body + "X"
	}
	return body + string(rune('0'+check))
}
