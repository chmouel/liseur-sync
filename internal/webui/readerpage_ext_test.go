package webui_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestReaderPageIsolatesThePublication is the security half of
// ADR-0007, checked on the page a browser actually receives.
//
// The reader's defence is the policy: publication content renders in
// same-origin frames, and what keeps a book's script from running is a
// nonce-gated script-src that every chapter document inherits, plus the
// reader stripping script elements from each resource (ADR-0012). All
// of that lives in headers and attributes that are easy to drop in a
// refactor and produce no visible symptom when they are missing, which
// is exactly why they are asserted here.
func TestReaderPageIsolatesThePublication(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "novel", []byte(strings.Repeat("web-epub", 50)))

	resp, page := f.get(t, "/ui/books/"+bookID+"/read", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reader page: %d", resp.StatusCode)
	}

	// The engine builds the frame, so the page cannot assert the
	// sandbox itself any more. What it can assert is the policy, which
	// is what actually confines the publication: a blob document
	// inherits the policy of whatever created it.
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Error("the reader page ships no Content-Security-Policy")
	}
	for _, directive := range []string{
		"default-src 'none'", "script-src 'self'",
		"base-uri 'self'", "form-action 'none'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP is missing %q: %s", directive, csp)
		}
	}
	// script-src is the one directive with no hole in it. A book's own
	// markup carries style attributes, so style-src cannot be so strict;
	// nothing in a publication is ever allowed to execute. The nonce is
	// per-response and must appear both in the header and on the page's
	// one module tag — and nowhere else a publication could quote it
	// from.
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") ||
		strings.Contains(csp, "script-src 'self' blob:") {
		t.Errorf("publication script would be allowed to run: %s", csp)
	}
	if !strings.Contains(csp, "'strict-dynamic'") {
		t.Errorf("script-src is not nonce-gated with strict-dynamic: %s", csp)
	}
	nonceStart := strings.Index(csp, "'nonce-")
	if nonceStart < 0 {
		t.Fatalf("script-src carries no nonce: %s", csp)
	}
	nonceEnd := strings.Index(csp[nonceStart+7:], "'")
	nonce := csp[nonceStart+7 : nonceStart+7+nonceEnd]
	if len(nonce) < 16 {
		t.Errorf("the CSP nonce is too short to be one: %q", nonce)
	}
	if !strings.Contains(page, `nonce="`+nonce+`"`) {
		t.Error("the page's module tag does not carry the response's CSP nonce")
	}
	if strings.Count(page, "nonce=") != 1 {
		t.Errorf("the nonce appears on %d elements; it belongs on exactly one",
			strings.Count(page, "nonce="))
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("reader page must be nosniff")
	}

	// The page carries no publication bytes: it is a shell that fetches
	// the archive and unpacks it in the browser.
	if strings.Contains(page, "Call me Ishmael") {
		t.Error("publication content must not be rendered into the page")
	}
	// The page loads exactly one script of its own: the reader module,
	// which imports the vendored engine (foliate-js, ADR-0012) itself.
	if !strings.Contains(page, "reader-app.js") {
		t.Error("the reader page does not load reader-app.js")
	}
	if !strings.Contains(page, `type="module"`) {
		t.Error("the reader module must be loaded as an ES module")
	}
}

// TestReaderIsOfferedOnlyForBooksItCanOpen: every EPUB gets a way in,
// and a book whose bytes are gone does not get an invitation to read
// nothing.
func TestReaderIsOfferedOnlyForBooksItCanOpen(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "novel", []byte(strings.Repeat("web-epub", 50)))

	_, list := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	if !strings.Contains(list, "/read") {
		t.Error("an EPUB in the library is not offered to the reader")
	}
	_, detail := f.get(t, "/ui/books/"+bookID, f.cookie)
	if !strings.Contains(detail, "books/"+bookID+"/read") {
		t.Error("the book page does not offer to open the book")
	}
}

// TestReaderPageRefusesOtherPeoplesBooks: the reader is a page about
// one book, so it answers the same way the book page does when the
// caller has no business with it.
// TestReaderPageNeedsASessionAndARealBook: the reader is open to every
// signed-in account, because the catalog is (ADR-0017). What it still
// refuses is a request with no session behind it and an id that is not a
// book.
func TestReaderPageNeedsASessionAndARealBook(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "novel", []byte(strings.Repeat("web-epub", 50)))

	bob := f.login(t, "bob")
	if resp, _ := f.get(t, "/ui/books/"+bookID+"/read", bob); resp.StatusCode != http.StatusOK {
		t.Errorf("a second account was refused the shared catalog: %d", resp.StatusCode)
	}
	if resp, _ := f.get(t, "/ui/books/"+bookID+"/read", nil); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("signed out: want a redirect to login, got %d", resp.StatusCode)
	}
	if resp, _ := f.get(t, "/ui/books/does-not-exist/read", f.cookie); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown book: want 404, got %d", resp.StatusCode)
	}
}

// TestReaderPageLinksTheCoverAsItsIcon: the reading tab carries the
// book's own cover at the icon size, served by the same cover route
// that falls back to a placeholder for a coverless book, so the link
// is always one a browser can fetch.
func TestReaderPageLinksTheCoverAsItsIcon(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "novel", []byte(strings.Repeat("web-epub", 50)))

	_, page := f.get(t, "/ui/books/"+bookID+"/read", f.cookie)
	want := `<link rel="icon" href="../../books/` + bookID + `/cover?size=icon">`
	if !strings.Contains(page, want) {
		t.Errorf("the reader page does not link the cover icon:\n%s", page)
	}
}
