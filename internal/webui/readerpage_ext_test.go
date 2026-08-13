package webui_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestReaderPageIsolatesThePublication is the security half of
// ADR-0007, checked on the page a browser actually receives.
//
// The reader's defence is not that the markup is cleaned; it is that
// publication content is given an opaque origin and no network. Both of
// those live in attributes and headers that are easy to drop in a
// refactor and produce no visible symptom when they are missing, which
// is exactly why they are asserted here.
func TestReaderPageIsolatesThePublication(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	f.uploadForm(t, f.cookie, csrfFrom(t, html), f.library, "novel.epub",
		[]byte(strings.Repeat("web-epub", 50)))
	bookID := f.promote(t, "novel")

	resp, page := f.get(t, "/ui/books/"+bookID+"/read", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reader page: %d", resp.StatusCode)
	}

	// The load-bearing attribute. allow-same-origin would hand the book
	// the authenticated origin, cookies and all.
	if !strings.Contains(page, `sandbox="allow-scripts"`) {
		t.Error("the publication frame must be sandboxed with allow-scripts only")
	}
	if strings.Contains(page, "allow-same-origin") {
		t.Error("allow-same-origin gives publication content the UI's origin")
	}

	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Error("the reader page ships no Content-Security-Policy")
	}
	for _, directive := range []string{
		"default-src 'none'", "script-src 'self'", "connect-src 'self'",
		"base-uri 'none'", "form-action 'none'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP is missing %q: %s", directive, csp)
		}
	}

	// A srcdoc document inherits this page's policy on top of its own,
	// so the nonce the chapter's script carries has to appear here or
	// nothing renders. It must also be fresh each time: a fixed nonce
	// is the same as no nonce.
	nonce := attr(t, page, "data-nonce")
	if nonce == "" {
		t.Fatal("the page ships no nonce for the chapter script")
	}
	if !strings.Contains(csp, "'nonce-"+nonce+"'") {
		t.Errorf("the page's CSP does not carry the chapter nonce: %s", csp)
	}
	if _, second := f.get(t, "/ui/books/"+bookID+"/read", f.cookie); attr(t, second, "data-nonce") == nonce {
		t.Error("the nonce must be minted per response, not baked in")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("reader page must be nosniff")
	}

	// The page carries no publication bytes: it is a shell that fetches
	// the archive and unpacks it in the browser.
	if strings.Contains(page, "Call me Ishmael") {
		t.Error("publication content must not be rendered into the page")
	}
	if !strings.Contains(page, "reader.js") || !strings.Contains(page, "reader-app.js") {
		t.Error("the reader page does not load the reader")
	}
}

// TestReaderIsOfferedOnlyForBooksItCanOpen: every EPUB gets a way in,
// and a book whose bytes are gone does not get an invitation to read
// nothing.
func TestReaderIsOfferedOnlyForBooksItCanOpen(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	f.uploadForm(t, f.cookie, csrfFrom(t, html), f.library, "novel.epub",
		[]byte(strings.Repeat("web-epub", 50)))
	bookID := f.promote(t, "novel")

	_, list := f.get(t, "/ui/books?library="+f.library, f.cookie)
	if !strings.Contains(list, "/read") {
		t.Error("an EPUB in the library is not offered to the reader")
	}
	_, detail := f.get(t, "/ui/books/"+bookID, f.cookie)
	if !strings.Contains(detail, "Read in the browser") {
		t.Error("the book page does not offer to open the book")
	}
}

// TestReaderPageRefusesOtherPeoplesBooks: the reader is a page about
// one book, so it answers the same way the book page does when the
// caller has no business with it.
func TestReaderPageRefusesOtherPeoplesBooks(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	f.uploadForm(t, f.cookie, csrfFrom(t, html), f.library, "novel.epub",
		[]byte(strings.Repeat("web-epub", 50)))
	bookID := f.promote(t, "novel")

	bob := f.login(t, "bob")
	if resp, _ := f.get(t, "/ui/books/"+bookID+"/read", bob); resp.StatusCode != http.StatusNotFound {
		t.Errorf("another user's book: want 404, got %d", resp.StatusCode)
	}
	if resp, _ := f.get(t, "/ui/books/"+bookID+"/read", nil); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("signed out: want a redirect to login, got %d", resp.StatusCode)
	}
	if resp, _ := f.get(t, "/ui/books/does-not-exist/read", f.cookie); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown book: want 404, got %d", resp.StatusCode)
	}
}

// attr pulls one attribute value out of the rendered page, so a test
// reads what the browser would rather than what the handler meant.
func attr(t *testing.T, html, name string) string {
	t.Helper()
	marker := name + `="`
	i := strings.Index(html, marker)
	if i < 0 {
		return ""
	}
	rest := html[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}
