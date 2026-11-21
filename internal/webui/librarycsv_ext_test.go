//go:build linux

package webui_test

// The CSV export is the shelf's own rows written as text, so these tests
// are about the two things that makes true: the same filters, across all
// the pages the page would have made somebody click through, and the
// same answer about who may read which folder.

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

func csvRows(t *testing.T, body string) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("the export is not valid CSV: %v", err)
	}
	return rows
}

func TestTheShelfExportsAsCSV(t *testing.T) {
	f := newBooksFixture(t)
	const title = `Dune, or "Spice"`
	bookID := f.observe(t, store.ObservedBook{
		RelativePath: "dune.epub", SizeBytes: 4096, MTime: time.Now().UTC(),
		ContentSHA256: strings.Repeat("a", 64), OriginalFilename: "dune.epub",
		MediaType: "application/epub+zip", Title: title,
		Contributors: []store.ObservedContributor{{
			Name: "Frank Herbert", Role: store.ContributorRoleAuthor, Position: 1,
		}},
	})

	resp, body := f.get(t, "/ui/library.csv?folder="+f.folder+"&filter=all", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: got %d, want 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", got)
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.Contains(got, "library.csv") {
		t.Errorf("Content-Disposition = %q, want an attachment named library.csv", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}

	rows := csvRows(t, body)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want a header and one book: %q", len(rows), rows)
	}
	wantHeader := []string{"title", "author", "status", "progress", "last_read", "added", "book_id"}
	if strings.Join(rows[0], ",") != strings.Join(wantHeader, ",") {
		t.Errorf("header = %q, want %q", rows[0], wantHeader)
	}
	row := rows[1]
	// A title is somebody else's text: a comma in it must not become a
	// column and a quote in it must not end the field.
	if row[0] != title {
		t.Errorf("title = %q, want %q", row[0], title)
	}
	if row[1] != "Frank Herbert" {
		t.Errorf("author = %q, want %q", row[1], "Frank Herbert")
	}
	if row[2] != "Not started" {
		t.Errorf("status = %q, want Not started", row[2])
	}
	if row[3] != "" {
		t.Errorf("progress = %q, want empty for a book nobody has opened", row[3])
	}
	if row[4] != "" {
		t.Errorf("last_read = %q, want empty", row[4])
	}
	if _, err := time.Parse("2006-01-02", row[5]); err != nil {
		t.Errorf("added = %q, want a YYYY-MM-DD date: %v", row[5], err)
	}
	if row[6] != bookID {
		t.Errorf("book_id = %q, want %q", row[6], bookID)
	}
}

// TestTheExportIsEveryPageAndNothingElse walks a shelf longer than one
// catalogue page, because the export's whole reason to exist is that a
// spreadsheet is not a paginated view.
func TestTheExportIsEveryPageAndNothingElse(t *testing.T) {
	f := newBooksFixture(t)
	const books = 30
	for i := range books {
		f.observe(t, store.ObservedBook{
			RelativePath: fmt.Sprintf("book-%02d.epub", i), SizeBytes: 4096,
			MTime:         time.Now().UTC(),
			ContentSHA256: fmt.Sprintf("%064d", i),
			MediaType:     "application/epub+zip",
			Title:         fmt.Sprintf("Book %02d", i),
		})
	}

	_, body := f.get(t, "/ui/library.csv?folder="+f.folder+"&filter=all", f.cookie)
	if rows := csvRows(t, body); len(rows) != books+1 {
		t.Fatalf("got %d rows, want %d books and a header", len(rows)-1, books)
	}

	// A chip is a question about the shelf; the export answers the one
	// that was asked rather than quietly handing back everything.
	_, body = f.get(t, "/ui/library.csv?folder="+f.folder+"&filter=finished", f.cookie)
	if rows := csvRows(t, body); len(rows) != 1 {
		t.Fatalf("the Finished chip exported %d books, want none", len(rows)-1)
	}
}

// TestTheExportIsOnlyWhatTheReaderMaySee is the folder grant, at the
// other door: a reader with no grant must not be able to name somebody
// else's folder and get its titles back as text.
func TestTheExportIsOnlyWhatTheReaderMaySee(t *testing.T) {
	f := newBooksFixture(t)
	f.observe(t, store.ObservedBook{
		RelativePath: "private.epub", SizeBytes: 4096, MTime: time.Now().UTC(),
		ContentSHA256: strings.Repeat("b", 64), MediaType: "application/epub+zip",
		Title: "Alice's Only Book",
	})
	hash, err := auth.HashPassword("hunter2hunter")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.CreateUser(t.Context(), store.User{
		ID: "u3", Name: "carol", Argon2Hash: hash, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	carol := f.login(t, "carol")

	resp, body := f.get(t, "/ui/library.csv?folder="+f.folder+"&filter=all", carol)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a folder nobody granted: got %d, want 404", resp.StatusCode)
	}
	if strings.Contains(body, "Alice's Only Book") {
		t.Fatal("the export leaked a book from a folder this reader holds nothing in")
	}
}

// TestTheExportCoversEveryGrantedFolder pins what an address with no
// folder in it means: Settings has no folder picker, so the export
// walks every folder this reader holds and skips every folder they do
// not.
func TestTheExportCoversEveryGrantedFolder(t *testing.T) {
	f := newBooksFixture(t)
	f.observe(t, store.ObservedBook{
		RelativePath: "mine.epub", SizeBytes: 4096, MTime: time.Now().UTC(),
		ContentSHA256: strings.Repeat("d", 64), MediaType: "application/epub+zip",
		Title: "Alice's First Book",
	})
	if err := f.st.CreateFolder(t.Context(), store.Folder{
		ID: "folder-second", Name: "Alice's Second", RootPath: t.TempDir(),
		Kind: store.FolderPlain, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AssignUserFolder(t.Context(), "u1", "folder-second"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ReconcileFolder(t.Context(), "folder-second", []store.ObservedBook{{
		RelativePath: "second.epub", SizeBytes: 4096, MTime: time.Now().UTC(),
		ContentSHA256: strings.Repeat("f", 64), MediaType: "application/epub+zip",
		Title: "Alice's Second Book",
	}}, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
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

	resp, body := f.get(t, "/ui/library.csv?filter=all", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: got %d, want 200: %s", resp.StatusCode, body)
	}
	rows := csvRows(t, body)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want a header and two books: %q", len(rows), rows)
	}
	for _, want := range []string{"Alice's First Book", "Alice's Second Book"} {
		if !strings.Contains(body, want) {
			t.Errorf("the export missed %q from a granted folder", want)
		}
	}
	if strings.Contains(body, "Not Alice's Book") {
		t.Fatal("the export reached a folder this reader holds nothing in")
	}
}

// TestTheExportCarriesWhatTheReadingHalfKnows covers the rows that came
// from a work rather than from a catalog listing: they have to describe
// the book they landed on, not just its title.
func TestTheExportCarriesWhatTheReadingHalfKnows(t *testing.T) {
	f, ids := libraryFixture(t)
	for _, query := range []string{"filter=reading", "filter=all&sort=read"} {
		t.Run(query, func(t *testing.T) {
			_, body := f.get(t, "/ui/library.csv?folder="+f.folder+"&"+query, f.cookie)
			var row []string
			for _, r := range csvRows(t, body)[1:] {
				if len(r) > 6 && r[6] == ids["midway"] {
					row = r
				}
			}
			if row == nil {
				t.Fatalf("%s exported no row for the book being read", query)
			}
			if row[2] != "Reading" {
				t.Errorf("status = %q, want Reading", row[2])
			}
			if row[3] != "50" {
				t.Errorf("progress = %q, want 50 for a book read halfway", row[3])
			}
			for _, field := range []struct{ name, value string }{
				{"last_read", row[4]}, {"added", row[5]},
			} {
				if field.value == "" {
					t.Errorf("%s is empty", field.name)
					continue
				}
				if _, err := time.Parse("2006-01-02", field.value); err != nil {
					t.Errorf("%s = %q, want a YYYY-MM-DD date: %v", field.name, field.value, err)
				}
			}
		})
	}
}

// TestSettingsOffersTheExportDialog pins the wiring between the button,
// the dialog and the module on the settings page: the button is a link
// to the file, so the export still works with no script at all.
func TestSettingsOffersTheExportDialog(t *testing.T) {
	f := newBooksFixture(t)
	_, page := f.get(t, "/ui/settings", f.cookie)

	for _, want := range []string{
		`data-csv-open="csv-dialog"`,
		`id="csv-dialog"`,
		`library.csv?filter=all`,
		`download="library.csv"`,
		`data-csv-copy`,
		`library-csv.js`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the settings page is missing %q", want)
		}
	}
}
