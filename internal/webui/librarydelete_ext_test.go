//go:build linux

package webui_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Deleting a book outright from the browser (ADR-0025). This is the one
// control on this server that destroys a reader's bytes, so what these
// tests are mostly about is where it does not appear.

func TestDeleteBookFileIsOfferedOnlyWhereABookCouldBeUploaded(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "here", []byte(strings.Repeat("here", 80)))
	allowUploads(t, f)

	// Never to an ordinary reader, who has no role for it. The last
	// enabled admin cannot be demoted, so the plain reader is the
	// fixture's own account before it is promoted.
	_, page := f.get(t, "/ui/books/"+bookID, f.cookie)
	if strings.Contains(page, bookID+"/destroy") {
		t.Fatalf("a plain reader was offered the delete:\n%s", page)
	}
	if resp := f.postForm(t, "/ui/books/"+bookID+"/destroy", f.cookie,
		url.Values{"csrf": {csrfFrom(t, page)}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a plain reader deleting a book: status %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(f.root, "here.epub")); err != nil {
		t.Errorf("the file went anyway: %v", err)
	}

	admin := makeAdmin(t, f)
	_, page = f.get(t, "/ui/books/"+bookID, admin)
	if !strings.Contains(page, `action="../books/`+bookID+`/destroy"`) {
		t.Fatalf("an admin was not offered the delete:\n%s", page)
	}
}

// A folder nobody marked as accepting uploads is still read-only to
// this server, even to an administrator: that flag is the whole
// permission (ADR-0023, ADR-0025).
func TestDeleteBookFileIsNotOfferedInAFolderThatAcceptsNoUploads(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "elsewhere", []byte(strings.Repeat("else", 80)))
	admin := makeAdmin(t, f)

	_, page := f.get(t, "/ui/books/"+bookID, admin)
	if strings.Contains(page, bookID+"/destroy") {
		t.Fatalf("a delete was offered in a folder that accepts no uploads:\n%s", page)
	}
	if resp := f.postForm(t, "/ui/books/"+bookID+"/destroy", admin,
		url.Values{"csrf": {csrfFrom(t, page)}}); !strings.Contains(
		resp.Header.Get("Location"), "problem=") {
		t.Fatalf("the route allowed it anyway: %d %v", resp.StatusCode, resp.Header)
	}
	if _, err := os.Stat(filepath.Join(f.root, "elsewhere.epub")); err != nil {
		t.Errorf("the file was deleted anyway: %v", err)
	}
}

func TestDeleteBookFileTakesTheFileAndTheRow(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "doomed", []byte(strings.Repeat("doom", 80)))
	allowUploads(t, f)
	admin := makeAdmin(t, f)

	_, page := f.get(t, "/ui/books/"+bookID, admin)
	resp := f.postForm(t, "/ui/books/"+bookID+"/destroy", admin,
		url.Values{"csrf": {csrfFrom(t, page)}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); !strings.Contains(got, "notice=deleted") {
		t.Fatalf("the delete sent the browser to %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.root, "doomed.epub")); !os.IsNotExist(err) {
		t.Errorf("the file survived: %v", err)
	}
	if _, err := f.st.CatalogBookByID(t.Context(), "", bookID); err == nil {
		t.Error("the catalog row survived")
	}
}

// A delete that destroys bytes is exactly the request an attacker would
// like to make on somebody else's behalf.
func TestDeleteBookFileNeedsTheCSRFToken(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "safe", []byte(strings.Repeat("safe", 80)))
	allowUploads(t, f)
	admin := makeAdmin(t, f)

	if resp := f.postForm(t, "/ui/books/"+bookID+"/destroy", admin,
		url.Values{}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a delete with no CSRF token: status %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(f.root, "safe.epub")); err != nil {
		t.Errorf("the file went without a token: %v", err)
	}
}
