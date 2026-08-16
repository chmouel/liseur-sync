package webui_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
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

// browserTestEPUB is a small but real publication, and it is deliberately
// awkward in the way real books are. A title page living entirely inside
// position:absolute, twelve chapters, a separate stylesheet, a chapter
// long enough to need several pages — and a heading
// parked at left: -9999px, the trick publishers use to speak to a screen
// reader without showing anything. Those layout details are the fixture's
// whole reason for being this shape: each one, in a real book, produced
// an engine that laid pages out wrong or not at all (ADR-0012).
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
	// Announced but never seen. epub.js measures the whole document, so
	// this is what used to make a chapter forty blank pages long.
	offscreen := `<h2 class="offscreen">Chapter heading for a screen reader</h2>`

	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
		"OEBPS/style.css": "body { color: rgb(17, 34, 51); }\n" +
			".offscreen { position: absolute; left: -9999px; width: 1px; overflow: hidden; }\n" +
			".sectionpp { position: absolute; top: 0; left: 0; height: 100%; width: 100%; overflow: hidden; }",
		// The other publisher trick this fixture pins down: a title page
		// whose entire content sits inside position:absolute. An engine
		// that measures the document's bounding boxes to size its page
		// sees zero width and paints a blank page — the failure that
		// retired epub.js here (ADR-0012).
		"OEBPS/pagetitre.xhtml": `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml">` +
			`<head><link rel="stylesheet" href="style.css"/>` +
			`<script>document.documentElement.dataset.publicationRan = "yes";` +
			`try { parent.document.title = "pwned"; } catch (e) {}</script>` +
			`</head><body>` +
			`<div class="sectionpp"><p>A title page, absolutely positioned, the way real publishers ship them.</p></div>` +
			`</body></html>`,
		// The scripts are the test: a publication that tries to act must
		// not be able to. Each one marks what it would have done — ran at
		// all, ran from an SVG island, ran a same-origin file, reached
		// the parent page — and the browser check asserts every mark is
		// absent. The wrapper divs' width and max-width are a different
		// publisher habit: a book that caps its own measure through
		// wrappers (the engine already lifts caps on the body itself,
		// but nowhere deeper, and never lifts width) must lose those
		// caps when the reader's font-size slider moves, or a bigger
		// type arrives as blank page instead of longer lines.
		"OEBPS/chapter1.xhtml": `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml">` +
			`<head><link rel="stylesheet" href="style.css"/>` +
			`<script>document.documentElement.dataset.publicationRan = "yes";` +
			`try { parent.document.title = "pwned"; } catch (e) {}</script>` +
			`<script src="/ui/static/htmx.min.js"></script>` +
			`</head><body><div style="width: 30em"><div style="max-width: 30em">` + offscreen +
			`<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1">` +
			`<script>document.documentElement.dataset.svgRan = "yes";</script></svg>` +
			body + `</div></div></body></html>`,
		// A real EPUB 3 nav document: the reader's contents drawer is
		// built from this, entry labels shown as-is, one entry nested to
		// prove subitems survive the trip.
		"OEBPS/nav.xhtml": `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">` +
			`<head><title>Contents</title></head><body><nav epub:type="toc"><h1>Contents</h1><ol>` +
			`<li><a href="pagetitre.xhtml">Title Page</a></li>` +
			`<li><a href="chapter1.xhtml">Loomings</a></li>` +
			`<li><a href="chapter2.xhtml">The Carpet-Bag</a>` +
			`<ol><li><a href="chapter3.xhtml">The Spouter-Inn</a></li></ol></li>` +
			`<li><a href="chapter5.xhtml">Chowder</a></li>` +
			`</ol></nav></body></html>`,
	}
	manifest := `<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="tp" href="pagetitre.xhtml" media-type="application/xhtml+xml"/>
    <item id="c1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>`
	spine := `<itemref idref="tp"/><itemref idref="c1"/>`
	for i := 2; i <= browserTestChapters; i++ {
		name := fmt.Sprintf("chapter%d.xhtml", i)
		files["OEBPS/"+name] = `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml">` +
			`<head><link rel="stylesheet" href="style.css"/></head><body>` + offscreen +
			fmt.Sprintf("<p>The Carpet-Bag, part %d.</p>", i) +
			strings.Repeat("<p>A cold, damp night, and the wind in the rigging.</p>\n", 20) +
			`</body></html>`
		manifest += fmt.Sprintf("\n    <item id=\"c%d\" href=\"%s\" media-type=\"application/xhtml+xml\"/>", i, name)
		spine += fmt.Sprintf("<itemref idref=\"c%d\"/>", i)
	}
	files["OEBPS/content.opf"] = `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Moby-Dick</dc:title></metadata>
  <manifest>
    ` + manifest + `
    <item id="css" href="style.css" media-type="text/css"/>
  </manifest>
  <spine>` + spine + `</spine>
</package>`

	for name, content := range files {
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

// browserTestChapters is how many spine documents the fixture has. It is
// more than a page-turn test needs so that a book which refuses to leave
// its first section is obvious rather than borderline.
const browserTestChapters = 12

// seededStaleOpID names the op the test plants before the browser
// opens: a stored position whose CFI has a valid spine step for this
// book but a garbage path inside the chapter. The engine only walks
// that path after the chapter loads, so a reader that trusts the
// pointer once it "resolves" dies there — the harness's "no error
// banner" and "Chapter 1 of 13" checks are what catch it, because the
// reader must quietly descend to the stored fraction instead.
const seededStaleOpID = "00000000-0000-4000-8000-00000000feed"

func seedStalePosition(t *testing.T, base string, cookie *http.Cookie, bookID string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/ui/books/"+bookID+"/read", nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(raw)
	const marker = `data-csrf="`
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatal("no csrf on the reader page")
	}
	csrf := html[i+len(marker):]
	csrf = csrf[:strings.Index(csrf, `"`)]

	// The same credential path the reader itself takes: a short-lived
	// token from the session, then the native API with it.
	req, _ = http.NewRequest(http.MethodPost, base+"/ui/reader/token",
		strings.NewReader(url.Values{"csrf": {csrf}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var minted struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&minted); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	api := func(path, body string) map[string]any {
		req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+minted.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("%s: %d %s", path, resp.StatusCode, raw)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	workID, _ := api("/v1/books/"+bookID+"/resolve", "{}")["work_id"].(string)
	if workID == "" {
		t.Fatal("the book did not resolve to a work")
	}
	op := map[string]any{
		"op_id": seededStaleOpID, "work_id": workID,
		"client_ts":   time.Now().UTC().Format(time.RFC3339),
		"progression": 0.001,
		"locator": map[string]any{
			"href": "pagetitre.xhtml", "type": "application/xhtml+xml",
			"locations": map[string]any{
				// A spine step this book has, a chapter path it does not.
				"fragments":        []string{"epubcfi(/6/2!/4/9999/1:12)"},
				"totalProgression": 0.001,
			},
		},
	}
	payload, _ := json.Marshal(map[string]any{"ops": []any{op}})
	api("/v1/ops", string(payload))
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
	bookID := f.addBook(t, "novel", browserTestEPUB(t))

	// The API is mounted beside the UI, as it is in the binary, so the
	// reader's sync calls are real and their failures are visible.
	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")
	seedStalePosition(t, ts.URL, cookie, bookID)

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
	synced := false
	for _, op := range page.Ops {
		if op.OpID != seededStaleOpID {
			synced = true
		}
	}
	if !synced {
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
	bookID := f.addBook(t, "novel", browserTestEPUB(t))
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

// TestUIScreenshots is the visual review of ADR-0011. It asserts
// nothing: a layout is judged by looking at it, and the assertions that
// can be written about one are already in the other tests. Set
// LISEUR_UI_SHOTS to a directory to get a PNG per page per width.
func TestUIScreenshots(t *testing.T) {
	outDir := os.Getenv("LISEUR_UI_SHOTS")
	if outDir == "" {
		t.Skip("set LISEUR_UI_SHOTS=<dir> to take screenshots of the UI")
	}
	chrome := findChrome()
	if chrome == "" {
		t.Skip("no chromium; set LISEUR_CHROME to take screenshots")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node to drive the browser with")
	}

	f := newBooksFixture(t)
	var books []string
	for _, name := range []string{"dune", "neuromancer", "solaris", "ubik"} {
		books = append(books, f.addBook(t, name, []byte(strings.Repeat(name, 60))))
	}
	// A shelf with nothing read on it hides the half of the page that
	// this walk exists to look at.
	now := time.Now().UTC()
	for i, m := range []struct {
		work string
		at   float64
	}{{"w-dune", 0.42}, {"w-neuromancer", 1}} {
		if _, err := f.st.ResolveCatalogBookWork(t.Context(), "u1", books[i],
			store.Work{ID: m.work, UserID: "u1", Title: m.work, CreatedAt: now},
			nil, nil, true, now); err != nil {
			t.Fatal(err)
		}
		progressOn(t, f, m.work, fmt.Sprintf("018e6f1a-0000-7000-8000-00000000005%d", i),
			m.at, now)
	}

	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")

	cmd := exec.Command(node, filepath.Join("testdata", "uishots.mjs"))
	cmd.Env = append(os.Environ(),
		"SHOT_CHROME="+chrome,
		"SHOT_URL="+ts.URL,
		"SHOT_COOKIE="+cookie.Name+"="+cookie.Value,
		"SHOT_DIR="+outDir,
		"SHOT_PATHS=/ui/,/ui/library,/ui/library?filter=reading,/ui/books/"+books[0]+","+
			"/ui/folders/"+f.folder+"/contributors,/ui/devices,/ui/settings",
		"SHOT_PREFS="+os.Getenv("LISEUR_UI_PREFS"),
		"SHOT_TAG="+os.Getenv("LISEUR_UI_TAG"),
	)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("screenshot walk failed: %v", err)
	}
}
