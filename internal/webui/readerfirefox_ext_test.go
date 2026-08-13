package webui_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// findFirefox locates a Firefox to drive. Like Chromium it is not a
// build dependency, so the test skips rather than fails without one.
func findFirefox() string {
	if named := os.Getenv("LISEUR_FIREFOX"); named != "" {
		return named
	}
	for _, name := range []string{"firefox", "firefox-esr", "firefox-bin"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

// TestReaderOpensInFirefox runs the same judgement as the Chromium check
// against the other engine. It is not redundant: the two browsers
// disagree about exactly what this reader leans on — how a sandboxed
// frame inherits a policy, how a blocked script is reported, and how a
// document that measures badly gets laid out — and the page-turn bug was
// reported from Firefox first.
func TestReaderOpensInFirefox(t *testing.T) {
	firefox := findFirefox()
	if firefox == "" {
		t.Skip("no firefox; set LISEUR_FIREFOX to run the browser check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node to drive the browser with")
	}

	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	f.uploadForm(t, f.cookie, csrfFrom(t, html), f.library, "novel.epub", browserTestEPUB(t))
	bookID := f.promote(t, "novel")

	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")

	if resp, _ := f.get(t, "/ui/books/"+bookID+"/read", f.cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("reader page: %d", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join("testdata", "readerfirefox.mjs"))
	cmd.Env = append(os.Environ(),
		"SMOKE_FIREFOX="+firefox,
		"SMOKE_URL="+ts.URL+"/ui/books/"+bookID+"/read",
		"SMOKE_COOKIE="+cookie.Name+"="+cookie.Value,
		"SMOKE_HOST="+strings.TrimPrefix(ts.URL, "http://"),
		"SMOKE_SHOT="+os.Getenv("LISEUR_READER_SCREENSHOT"),
	)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("the reader did not work in firefox: %v", err)
	}
}
