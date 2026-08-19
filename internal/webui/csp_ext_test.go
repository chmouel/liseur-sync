package webui_test

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestUIPagesShipAPolicy checks the fence behind the sanitizer.
//
// Everything this UI renders about a book — title, author, series,
// description — arrived inside somebody's EPUB, and the description is
// HTML by the time it reaches a template. sanitize.go is what removes
// the dangerous parts of it; this header is what makes a mistake in
// sanitize.go survivable, because a script that reaches the page still
// cannot run and one that runs anyway cannot reach another origin.
//
// A policy is trivially deleted by a refactor and produces no visible
// symptom at all when it is missing, so it is asserted per page family
// rather than once.
func TestUIPagesShipAPolicy(t *testing.T) {
	f := newBooksFixture(t)

	pages := map[string]*http.Cookie{
		"/ui/":        f.cookie,
		"/ui/library": f.cookie,
		"/ui/settings?section=devices": f.cookie,
		// The page a signed-out browser sees, and the one that posts a
		// password: it needs the policy at least as much as the rest.
		"/ui/login": nil,
	}
	for path, cookie := range pages {
		resp, _ := f.get(t, path, cookie)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		csp := resp.Header.Get("Content-Security-Policy")
		if csp == "" {
			t.Fatalf("%s ships no Content-Security-Policy", path)
		}
		for _, directive := range []string{
			"default-src 'self'",
			"script-src 'self'",
			"style-src 'self'",
			"object-src 'none'",
			"base-uri 'none'",
			"form-action 'self'",
			"frame-ancestors 'none'",
		} {
			if !strings.Contains(csp, directive) {
				t.Errorf("%s: CSP is missing %q: %s", path, directive, csp)
			}
		}
		// The two holes that would make the rest of it decorative.
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
			t.Errorf("%s: CSP has an escape hatch in it: %s", path, csp)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: not nosniff", path)
		}
	}
}

// TestReaderKeepsItsOwnPolicy guards the one route where the shared
// page policy would be wrong. The reader has to admit the blob: URLs a
// rendering engine builds each chapter from; it does that with a
// nonce and strict-dynamic instead, which is stricter about script than
// the page policy is. The two are set by different layers, so this
// asserts the ordering between them rather than the reader's policy
// (TestReaderPageIsolatesThePublication does that).
func TestReaderKeepsItsOwnPolicy(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "novel", []byte(strings.Repeat("web-epub", 50)))

	resp, _ := f.get(t, "/ui/books/"+bookID+"/read", f.cookie)
	csp := resp.Header.Get("Content-Security-Policy")
	if strings.Contains(csp, "default-src 'self'") {
		t.Errorf("the page policy overwrote the reader's: %s", csp)
	}
	if !strings.Contains(csp, "'strict-dynamic'") {
		t.Errorf("the reader lost its nonce policy: %s", csp)
	}
	if strings.Count(csp, "default-src") != 1 {
		t.Errorf("two policies got merged into one header: %s", csp)
	}
}

// TestTemplatesStayPolicyClean fails when a template reintroduces
// something the policy above would silently break.
//
// This is a lint, not a test of behaviour, and it exists because the
// failure mode it catches is invisible in development: a style
// attribute or an onclick works perfectly until the day the policy is
// enforced, and then a control simply stops responding with the reason
// in a console nobody is watching. ADR-0011 banned style attributes for
// this reason; the ban needs something that notices.
func TestTemplatesStayPolicyClean(t *testing.T) {
	files, err := filepath.Glob("*.templ")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no templates found; this test is checking nothing")
	}

	// on*= handlers, but not templ's own hx-on or Go attribute
	// expressions — the pattern wants a literal HTML event attribute.
	handler := regexp.MustCompile(`(?i)[\s"]on[a-z]+\s*=\s*["']`)
	styleAttr := regexp.MustCompile(`(?i)[\s"]style\s*=\s*["'{]`)

	for _, name := range files {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		src := string(body)

		if m := handler.FindString(src); m != "" {
			t.Errorf("%s: inline event handler %q — CSP blocks it; put the listener in static/ui.js",
				name, strings.TrimSpace(m))
		}
		if m := styleAttr.FindString(src); m != "" {
			t.Errorf("%s: inline style attribute %q — CSP blocks it; use a class (ADR-0011)",
				name, strings.TrimSpace(m))
		}
		// An inline <script> without a nonce. The reader page has one
		// with a nonce; nothing else may have any.
		for _, chunk := range strings.Split(src, "<script")[1:] {
			head, _, ok := strings.Cut(chunk, ">")
			if !ok {
				continue
			}
			if strings.Contains(head, "src=") || strings.Contains(head, "nonce") {
				continue
			}
			t.Errorf("%s: inline <script%s> — CSP blocks it; move it to static/ and load it with src",
				name, head)
		}
	}
}
