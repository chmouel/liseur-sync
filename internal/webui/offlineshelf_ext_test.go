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

// TestOfflineShelfModel runs the node unit tests for
// static/offline/offline-shelf-model.js: what a saved card says, what
// the header offers when the network is gone, and the two presses a
// removal takes.
func TestOfflineShelfModel(t *testing.T) {
	runNodeTests(t, "offlineshelf.test.mjs")
}

// TestOfflineShelfInBrowser drives the shelf itself: the cover it kept,
// the question a removal asks, and a header that stops offering a
// library it cannot reach. The rules are unit-tested above; this is
// what proves the page is built out of them.
func TestOfflineShelfInBrowser(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("no chromium; set LISEUR_CHROME to run the browser check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node to drive the browser with")
	}
	parallelBrowser(t)
	f := newBooksFixture(t)
	bookID := f.addBook(t, "shelf-novel", coverEPUB(t, 300, 400))
	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")
	// Model a reverse proxy which strips its deployment prefix.
	prefixed := httptest.NewServer(http.StripPrefix("/sync", ts.Config.Handler))
	t.Cleanup(prefixed.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join("testdata", "offlineshelf.mjs"))
	cmd.Env = append(os.Environ(),
		"DISPLAY=", "WAYLAND_DISPLAY=",
		"SMOKE_CHROME="+chrome,
		"SMOKE_PROFILE="+t.TempDir(),
		"SMOKE_BASE="+prefixed.URL+"/sync/",
		"SMOKE_BOOK="+bookID,
		"SMOKE_COOKIE="+cookie.Name+"="+cookie.Value,
	)
	output, err := cmd.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatalf("offline shelf browser check: %v", err)
	}
}

// TestOfflineShelfScreenshots photographs a shelf with books on it:
// real covers, a card with progress, a card asking whether to remove,
// and the header with the network gone, in both themes. It is opt-in —
// LISEUR_SHOTS names the directory — because a picture of a page
// belongs in a review, not in the suite.
func TestOfflineShelfScreenshots(t *testing.T) {
	shots := os.Getenv("LISEUR_SHOTS")
	if shots == "" {
		t.Skip("set LISEUR_SHOTS=<dir> to take shots of the offline shelf")
	}
	chrome := findChrome()
	if chrome == "" {
		t.Skip("no chromium; set LISEUR_CHROME to take shots")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node to drive the browser with")
	}
	parallelBrowser(t)
	f := newBooksFixture(t)
	var ids []string
	for _, book := range []struct{ name, title, author string }{
		{"shelf-le-guin", "The Left Hand of Darkness", "Ursula K. Le Guin"},
		{"shelf-wells", "The Time Machine", "H. G. Wells"},
		{"shelf-shelley", "Frankenstein", "Mary Shelley"},
	} {
		ids = append(ids, f.addBook(t, book.name, coverEPUBTitled(t, book.title, book.author, 600, 900)))
	}
	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")
	prefixed := httptest.NewServer(http.StripPrefix("/sync", ts.Config.Handler))
	t.Cleanup(prefixed.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 240*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join("testdata", "offlineshelfshots.mjs"))
	cmd.Env = append(os.Environ(),
		"DISPLAY=", "WAYLAND_DISPLAY=",
		"SMOKE_CHROME="+chrome,
		"SMOKE_PROFILE="+t.TempDir(),
		"SMOKE_BASE="+prefixed.URL+"/sync/",
		"SMOKE_BOOKS="+strings.Join(ids, ","),
		"SMOKE_COOKIE="+cookie.Name+"="+cookie.Value,
		"SMOKE_SHOTS="+shots,
	)
	output, err := cmd.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatalf("offline shelf shots: %v", err)
	}
}
