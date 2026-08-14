package webui_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// flagForReview puts a promoted book into review the way a watched sweep
// does, so the page is tested against the state it exists to report.
func flagForReview(t *testing.T, f *booksFixture, bookID, reason string) {
	t.Helper()
	changed, err := f.st.SetCatalogBookReview(
		t.Context(), f.library, bookID, reason, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("the book did not enter review")
	}
}

func TestBooksUIShowsAndAcceptsAChangedWatchedFile(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/library", f.cookie)
	csrf := csrfFrom(t, html)
	f.uploadForm(t, f.cookie, csrf, f.library, "changed.epub",
		[]byte(strings.Repeat("changed", 40)))
	bookID := f.promote(t, "changed")
	flagForReview(t, f, bookID, "the file at this watched path was replaced")

	_, html = f.get(t, "/ui/library/manage?library="+f.library, f.cookie)
	if !strings.Contains(html, "Changed under us") ||
		!strings.Contains(html, "books/"+bookID+"/accept") {
		t.Fatalf("review queue is not on the page:\n%s", html)
	}
	// The reason is the whole point of the section: without it the page
	// says a book needs attention and refuses to say why.
	if !strings.Contains(html, "replaced") {
		t.Fatalf("review row did not say why:\n%s", html)
	}

	form := url.Values{"csrf": {csrf}, "library": {f.library}}
	if resp := f.postForm(
		t, "/ui/books/"+bookID+"/accept", f.cookie, form,
	); resp.StatusCode != http.StatusSeeOther ||
		strings.Contains(resp.Header.Get("Location"), "problem=") {
		t.Fatalf("accept: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	_, html = f.get(t, "/ui/library/manage?library="+f.library, f.cookie)
	if strings.Contains(html, "Changed under us") {
		t.Fatalf("accepted book is still in the queue:\n%s", html)
	}
	// Accepting says the stored copy is fine, not that the book is
	// servable. Only the availability pass decides that, so the book
	// waits at `missing` until it runs.
	book, err := f.st.CatalogBookByID(
		t.Context(), "u1", bookID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	if book.Status != store.BookMissing {
		t.Fatalf("accept left status %q, want missing", book.Status)
	}
	if book.ReviewReason != "" {
		t.Fatalf("accept kept the reason %q", book.ReviewReason)
	}
}

func TestBooksUIAcceptRequiresManageAndCSRF(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/library", f.cookie)
	csrf := csrfFrom(t, html)
	f.uploadForm(t, f.cookie, csrf, f.library, "guarded.epub",
		[]byte(strings.Repeat("guarded", 40)))
	bookID := f.promote(t, "guarded")
	flagForReview(t, f, bookID, "replaced")

	for _, bad := range []string{"", "not-the-token"} {
		resp := f.postForm(t, "/ui/books/"+bookID+"/accept", f.cookie,
			url.Values{"csrf": {bad}, "library": {f.library}})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("csrf %q: %d, want 403", bad, resp.StatusCode)
		}
	}

	// A reader can see the library but must not resolve its librarian's
	// queue, and must not be shown it either.
	reader := f.readerCookie(t)
	_, readerHTML := f.get(t, "/ui/library?library="+f.library, reader)
	if strings.Contains(readerHTML, "Changed under us") {
		t.Fatalf("a reader was shown the review queue:\n%s", readerHTML)
	}
	resp := f.postForm(t, "/ui/books/"+bookID+"/accept", reader,
		url.Values{"csrf": {csrfFrom(t, readerHTML)}, "library": {f.library}})
	if resp.StatusCode == http.StatusOK ||
		!strings.Contains(resp.Header.Get("Location"), "problem=") {
		t.Fatalf("a reader cleared a review: %d %s",
			resp.StatusCode, resp.Header.Get("Location"))
	}
	book, err := f.st.CatalogBookByID(
		t.Context(), "u1", bookID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	if book.ReviewReason == "" {
		t.Fatal("a reader's post cleared the review anyway")
	}
}
