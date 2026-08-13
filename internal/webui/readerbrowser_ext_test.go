package webui_test

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// findChrome locates a Chromium to drive, preferring one named
// explicitly. Chrome is not a build dependency of this project and the
// test skips without it, so the search is allowed to be generous.
func findChrome() string {
	if named := os.Getenv("LISEUR_CHROME"); named != "" {
		return named
	}
	for _, name := range []string{
		"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome",
	} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	// Playwright's download, which a developer working on the web UI is
	// likely to have already.
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	found, _ := filepath.Glob(filepath.Join(
		home, ".cache", "ms-playwright", "chromium-*", "chrome-linux*", "chrome"))
	if len(found) > 0 {
		return found[len(found)-1]
	}
	return ""
}

// browserTestEPUB is a small but real publication: two spine documents,
// a separate stylesheet entry, and a chapter long enough to scroll.
func browserTestEPUB(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	stored, err := w.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stored.Write([]byte("application/epub+zip")); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat(
		"<p>Call me Ishmael. Some years ago, never mind how long precisely.</p>\n", 60)
	for name, content := range map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Moby-Dick</dc:title></metadata>
  <manifest>
    <item id="c1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="chapter2.xhtml" media-type="application/xhtml+xml"/>
    <item id="css" href="style.css" media-type="text/css"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`,
		"OEBPS/style.css": `body { color: rgb(17, 34, 51); }`,
		// The script is the test: a publication that tries to act must
		// not be able to. It marks the document, and the browser check
		// asserts the mark is absent.
		"OEBPS/chapter1.xhtml": `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml">` +
			`<head><link rel="stylesheet" href="style.css"/>` +
			`<script>document.documentElement.dataset.publicationRan = "yes";</script>` +
			`</head><body>` + body + `</body></html>`,
		"OEBPS/chapter2.xhtml": `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml">` +
			`<body><p>The Carpet-Bag.</p></body></html>`,
	} {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestReaderOpensInARealBrowser is the only test that can judge whether
// the reader works, because everything it does is a browser behaviour.
// The bug that brought the vendored engine in — a book that would not go
// past page two — was invisible to every unit test here and obvious
// within seconds of opening a book. So this turns pages until the book
// ends, reads the publication's own styling out of the frame, and checks
// that the publication's script did not run.
//
// It skips where there is no Chromium, including CI, which makes it a
// developer's check rather than a gate. That is the right trade: the
// alternative is either a browser in CI for one test or shipping a
// renderer nobody ever ran.
func TestReaderOpensInARealBrowser(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("no chromium; set LISEUR_CHROME to run the browser check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node to drive the browser with")
	}

	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	f.uploadForm(t, f.cookie, csrfFrom(t, html), f.library, "novel.epub", browserTestEPUB(t))
	bookID := f.promote(t, "novel")

	// The API is mounted beside the UI, as it is in the binary, so the
	// reader's sync calls are real and their failures are visible.
	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")

	if resp, _ := f.get(t, "/ui/books/"+bookID+"/read", f.cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("reader page: %d", resp.StatusCode)
	}

	cmd := exec.Command(node, filepath.Join("testdata", "readerbrowser.mjs"))
	cmd.Env = append(os.Environ(),
		"SMOKE_CHROME="+chrome,
		"SMOKE_URL="+ts.URL+"/ui/books/"+bookID+"/read",
		"SMOKE_COOKIE="+cookie.Name+"="+cookie.Value,
		"SMOKE_HOST="+strings.TrimPrefix(ts.URL, "http://"),
		"SMOKE_SHOT="+os.Getenv("LISEUR_READER_SCREENSHOT"),
	)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("the reader did not work in a browser: %v", err)
	}

	page, err := f.st.Changes(t.Context(), "u1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Ops) == 0 {
		t.Error("the reader never managed to sync a position")
	}
}

// TestDetachedReaderOpensInARealBrowser is the same check against the
// two-origin deployment (ADR-0007 phase 3), which cannot be judged
// anywhere else: the handoff is a redirect, the credential arrives in a
// URL fragment, and the API calls that follow are cross-origin. Only a
// browser enforces any of that.
func TestDetachedReaderOpensInARealBrowser(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("no chromium; set LISEUR_CHROME to run the browser check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node to drive the browser with")
	}

	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	f.uploadForm(t, f.cookie, csrfFrom(t, html), f.library, "novel.epub", browserTestEPUB(t))
	bookID := f.promote(t, "novel")
	ts, readerHost := splitOriginServer(t, f)
	cookie := f.loginTo(t, ts, "alice")

	// The browser is pointed at the main origin, as a reader always is.
	// Everything after that — the redirect, the fragment, the
	// cross-origin fetches — is the feature under test.
	cmd := exec.Command(node, filepath.Join("testdata", "readerbrowser.mjs"))
	cmd.Env = append(os.Environ(),
		"SMOKE_CHROME="+chrome,
		"SMOKE_URL="+ts.URL+"/ui/books/"+bookID+"/read",
		"SMOKE_COOKIE="+cookie.Name+"="+cookie.Value,
		"SMOKE_HOST="+strings.TrimPrefix(ts.URL, "http://"),
		"SMOKE_MAP="+readerHost,
		"SMOKE_DETACHED=1",
		"SMOKE_SHOT=",
	)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("the detached reader did not work in a browser: %v", err)
	}

	// Rendering a book proves the download crossed origins. Sync is the
	// other half and is invisible from the page, so it is checked here:
	// a position written by the detached reader has to reach the same op
	// log every other client reads.
	page, err := f.st.Changes(t.Context(), "u1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Ops) == 0 {
		t.Error("the detached reader never managed to sync a position")
	}
}
