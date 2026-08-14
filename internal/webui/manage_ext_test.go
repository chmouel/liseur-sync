//go:build linux

package webui_test

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestLibraryManageIsClosedToReaders: read access to a library is not
// access to its queues. A reader asking for the page by id is told the
// page does not exist, which is what every other manager-only surface
// says rather than admitting the library is there.
func TestLibraryManageIsClosedToReaders(t *testing.T) {
	f := newBooksFixture(t)
	reader := f.readerCookie(t)

	resp, html := f.get(t, "/ui/library/manage?library="+f.library, reader)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a reader reached library management: %d\n%s", resp.StatusCode, html)
	}

	// Without an id they get the page, and it is empty: they manage none.
	resp, html = f.get(t, "/ui/library/manage", reader)
	if resp.StatusCode != http.StatusOK ||
		!strings.Contains(html, "You do not manage any libraries") {
		t.Fatalf("reader's empty management page: %d\n%s", resp.StatusCode, html)
	}
	if strings.Contains(html, "Recently deleted") ||
		strings.Contains(html, "Uploads in progress") {
		t.Fatalf("a reader was shown somebody's queues:\n%s", html)
	}
}

// TestTheLibraryPickerCarriesTheIDTheScriptSubmits pins the management
// page's select to the id static/ui.js submits on. The manual button is
// inside <noscript>, so with the wrong id the picker is inert for
// everybody who has JavaScript: a manager of several libraries would be
// stuck on the first one.
func TestTheLibraryPickerCarriesTheIDTheScriptSubmits(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/library/manage", f.cookie)
	if !strings.Contains(html, `id="library-pick"`) {
		t.Fatalf("the management picker is not the one ui.js submits:\n%s", html)
	}
}

// TestWorkingThroughAQueueStaysOnTheManagementPage: the queues only
// exist here, so an action started here has to come back here, and with
// its notice. Landing on the catalog after every item would mean
// navigating back for the next one, and the catalog no longer shows what
// is left to do.
func TestWorkingThroughAQueueStaysOnTheManagementPage(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/library", f.cookie)
	csrf := csrfFrom(t, html)
	f.uploadForm(t, f.cookie, csrf, f.library, "regret.epub",
		bytes.Repeat([]byte("deletable"), 40))
	bookID := f.promote(t, "regret")

	form := url.Values{"csrf": {csrf}, "library": {f.library}}
	if resp := f.postForm(
		t, "/ui/books/"+bookID+"/delete", f.cookie, form,
	); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}

	// The trash form the page renders is the one posted below.
	_, html = f.get(t, "/ui/library/manage?library="+f.library, f.cookie)
	if !strings.Contains(html, `name="back" value="manage"`) {
		t.Fatalf("the queue forms do not say where they came from:\n%s", html)
	}

	form.Set("back", "manage")
	resp := f.postForm(t, "/ui/books/"+bookID+"/restore", f.cookie, form)
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther ||
		!strings.Contains(loc, "library/manage?") {
		t.Fatalf("restore went to %q (%d)", loc, resp.StatusCode)
	}
	if !strings.Contains(loc, "notice=") {
		t.Fatalf("restore said nothing: %q", loc)
	}

	_, html = f.get(t, "/ui/library/manage?"+strings.SplitN(loc, "?", 2)[1], f.cookie)
	if !strings.Contains(html, "Restored") {
		t.Fatalf("the management page swallowed the notice:\n%s", html)
	}
}

// TestAQueueFormCannotChooseAnyPageItLikes: `back` is a bounded choice,
// not a path. Anything else lands on the catalog rather than wherever
// the form asked to go.
func TestAQueueFormCannotChooseAnyPageItLikes(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/library", f.cookie)
	csrf := csrfFrom(t, html)
	f.uploadForm(t, f.cookie, csrf, f.library, "elsewhere.epub",
		bytes.Repeat([]byte("elsewhere"), 40))
	bookID := f.promote(t, "elsewhere")

	form := url.Values{
		"csrf": {csrf}, "library": {f.library},
		"back": {"https://example.invalid/"},
	}
	resp := f.postForm(t, "/ui/books/"+bookID+"/delete", f.cookie, form)
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "library?") ||
		strings.Contains(loc, "library/manage") {
		t.Fatalf("a form chose its own destination: %q", loc)
	}
}
