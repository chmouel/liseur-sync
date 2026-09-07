package webui_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestOfflineReaderColdNavigationInBrowser(t *testing.T) {
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
	epub := browserTestEPUB(t)
	bookID := f.addBook(t, "offline-novel", epub)
	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")
	// Model a reverse proxy which strips its deployment prefix.
	prefixed := httptest.NewServer(http.StripPrefix("/sync", ts.Config.Handler))
	t.Cleanup(prefixed.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join("testdata", "offlinereader.mjs"))
	cmd.Env = append(os.Environ(),
		"DISPLAY=", "WAYLAND_DISPLAY=",
		"SMOKE_CHROME="+chrome,
		"SMOKE_PROFILE="+t.TempDir(),
		"SMOKE_BASE="+prefixed.URL+"/sync/",
		"SMOKE_BOOK="+bookID,
		"SMOKE_PAGES="+strconv.Itoa(browserTestPages(t, epub)),
		"SMOKE_COOKIE="+cookie.Name+"="+cookie.Value,
	)
	output, err := cmd.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatalf("offline reader browser check: %v", err)
	}
}
