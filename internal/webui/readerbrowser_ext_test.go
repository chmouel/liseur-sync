package webui_test

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	gopng "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Browser fixtures have independent servers and profiles. Limit concurrent
// browsers to two so their long gesture checks overlap without exhausting RAM.
var browserSlots = make(chan struct{}, 2)

func parallelBrowser(t *testing.T) {
	t.Helper()
	// Screenshot runs share an explicitly chosen output path.
	if os.Getenv("LISEUR_READER_SCREENSHOT") != "" {
		return
	}
	t.Parallel()
	browserSlots <- struct{}{}
	t.Cleanup(func() { <-browserSlots })
}

// findChrome locates a Chromium to drive, preferring one named
// explicitly. Chrome is not a build dependency of this project and the
// test skips without it, so the search is allowed to be generous.
func findChrome() string {
	if named := os.Getenv("LISEUR_CHROME"); named != "" {
		return named
	}
	// Hosted CI images may include Chrome incidentally. This is an
	// opt-in browser check, so a runner image change must not turn it
	// into a flaky gate; LISEUR_CHROME above remains the explicit opt-in.
	if os.Getenv("CI") != "" {
		return ""
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

func TestFindChromeDoesNotAutoDiscoverInCI(t *testing.T) {
	t.Setenv("CI", "1")
	t.Setenv("LISEUR_CHROME", "")

	if got := findChrome(); got != "" {
		t.Fatalf("findChrome() = %q in CI without explicit opt-in, want empty", got)
	}
}

// utf16LEWithBOM encodes text as UTF-16LE with a leading byte-order mark,
// the shape a real EPUB may ship a chapter in: XML permits UTF-16 through
// a BOM or declaration, and a reader that assumes UTF-8 must not render
// this as replacement characters.
func utf16LEWithBOM(text string) []byte {
	units := utf16.Encode([]rune(text))
	buf := make([]byte, 2+2*len(units))
	binary.LittleEndian.PutUint16(buf, 0xFEFF)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(buf[2+2*i:], unit)
	}
	return buf
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
		content := `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml">` +
			`<head><link rel="stylesheet" href="style.css"/></head><body>` + offscreen +
			fmt.Sprintf("<p>The Carpet-Bag, part %d.</p>", i) +
			strings.Repeat("<p>A cold, damp night, and the wind in the rigging.</p>\n", 20) +
			`</body></html>`
		if i == 5 {
			// Chowder (linked from nav.xhtml above) is a valid UTF-16LE
			// document with a BOM and a non-ASCII word, pinning down that
			// the reader decodes archive text instead of assuming UTF-8.
			content = `<?xml version="1.0" encoding="UTF-16"?><html xmlns="http://www.w3.org/1999/xhtml">` +
				`<head><link rel="stylesheet" href="style.css"/></head><body>` + offscreen +
				"<p>A steaming bowl of caf\u00e9 chowder awaited them.</p>" +
				strings.Repeat("<p>A cold, damp night, and the wind in the rigging.</p>\n", 20) +
				`</body></html>`
			files["OEBPS/"+name] = string(utf16LEWithBOM(content))
		} else {
			files["OEBPS/"+name] = content
		}
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

// browserTestPages is how many pages the footer must show for the
// fixture: Readium's positions, which is what the reader now counts
// (ADR-0032) so that the browser and the app name the same page.
//
// It is computed from the archive rather than written down because the
// fixture is deflated by whatever Go is compiling the test, and a
// hard-coded total would be a test of Go's compressor. The recipe is
// Readium's: per linear spine item, its stored length over 1024 rounded
// up, at least one, summed.
func browserTestPages(t *testing.T, epub []byte) int {
	t.Helper()
	spine := []string{"OEBPS/pagetitre.xhtml", "OEBPS/chapter1.xhtml"}
	for i := 2; i <= browserTestChapters; i++ {
		spine = append(spine, fmt.Sprintf("OEBPS/chapter%d.xhtml", i))
	}
	r, err := zip.NewReader(bytes.NewReader(epub), int64(len(epub)))
	if err != nil {
		t.Fatal(err)
	}
	stored := make(map[string]uint64, len(r.File))
	for _, f := range r.File {
		// A stored entry has no compressed length of its own, which is
		// where Readium falls back to the resource's own length.
		if f.Method == zip.Store {
			stored[f.Name] = f.UncompressedSize64
			continue
		}
		stored[f.Name] = f.CompressedSize64
	}
	total := 0
	for _, name := range spine {
		length, ok := stored[name]
		if !ok {
			t.Fatalf("fixture has no spine entry %q", name)
		}
		pages := (length + 1023) / 1024
		if pages < 1 {
			pages = 1
		}
		total += int(pages)
	}
	return total
}

// svgSpineTestEPUB is a minimal EPUB whose spine is not all XHTML: a
// title page, then an SVG page, then a bitmap (PNG) page. Readium's
// frame builder only routes an item through our fetcher when its
// declared type says HTML — an SVG or bitmap spine item otherwise
// becomes an <img> pointed straight at item.toURL(baseURL), which
// resolves against this reader's fake self link rather than a real
// endpoint (finding #1 of the streaming-reader review). The SVG page
// carries an embedded script the same way chapter1 does in
// browserTestEPUB: the check is that it never runs.
func svgSpineTestEPUB(t *testing.T) []byte {
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

	png := new(bytes.Buffer)
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 0x42, G: 0x86, B: 0xf4, A: 0xff}}, image.Point{}, draw.Src)
	if err := gopng.Encode(png, img); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
		"OEBPS/pagetitre.xhtml": `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml">` +
			`<head><title>Title</title></head><body><p>An SVG-spine publication.</p></body></html>`,
		// The script marks whether it ran on the SVG root's own dataset,
		// the same convention chapter1 in browserTestEPUB uses; the
		// browser check asserts that mark is absent.
		"OEBPS/illustration.svg": `<?xml version="1.0"?>` +
			`<svg xmlns="http://www.w3.org/2000/svg" width="320" height="240" viewBox="0 0 320 240">` +
			`<script>document.documentElement.dataset.svgRan = "yes";</script>` +
			`<rect width="320" height="240" fill="#642"/><text x="10" y="120" fill="#fff">An SVG page</text></svg>`,
		"OEBPS/nav.xhtml": `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">` +
			`<head><title>Contents</title></head><body><nav epub:type="toc"><h1>Contents</h1><ol>` +
			`<li><a href="pagetitre.xhtml">Title Page</a></li>` +
			`<li><a href="illustration.svg">Illustration</a></li>` +
			`<li><a href="photo.png">Photo</a></li>` +
			`</ol></nav></body></html>`,
	}
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	imgEntry, err := w.Create("OEBPS/photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := imgEntry.Write(png.Bytes()); err != nil {
		t.Fatal(err)
	}

	content := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>An SVG-Spine Book</dc:title></metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="tp" href="pagetitre.xhtml" media-type="application/xhtml+xml"/>
    <item id="svgpage" href="illustration.svg" media-type="image/svg+xml"/>
    <item id="photo" href="photo.png" media-type="image/png"/>
  </manifest>
  <spine><itemref idref="tp"/><itemref idref="svgpage"/><itemref idref="photo"/></spine>
</package>`
	opf, err := w.Create("OEBPS/content.opf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opf.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestReaderRendersSVGSpineItems is finding #1 of the streaming-reader
// review: a valid EPUB whose spine has an SVG page and a bitmap page,
// not just XHTML, must still render those pages rather than going blank.
func TestReaderRendersSVGSpineItems(t *testing.T) {
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
	epub := svgSpineTestEPUB(t)
	bookID := f.addBook(t, "illustrated", epub)

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
		"SMOKE_SVG=1",
		"SMOKE_SHOT="+os.Getenv("LISEUR_READER_SCREENSHOT"),
	)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("the reader did not render SVG/bitmap spine items in a browser: %v", err)
	}
}

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

	// Annotations (ADR-0028), through the same route a device uses:
	// one highlight whose range CFI anchors in the title page the
	// reader opens on, and two that cannot draw — a note (no anchor by
	// definition) and a highlight whose locator carries no CFI — which
	// must degrade to sidebar entries rather than errors.
	ts := time.Now().UTC().Format(time.RFC3339)
	anns, _ := json.Marshal(map[string]any{"annotations": []any{
		map[string]any{
			"id": "00000000-0000-4000-8000-0000000000a1", "base_rev": 0,
			"work_id": workID, "kind": "highlight", "color": "green",
			"excerpt": "title page, absolut", "progression": 0.001,
			"client_ts": ts,
			"locator": map[string]any{
				"href": "pagetitre.xhtml", "type": "application/xhtml+xml",
				"locations": map[string]any{
					"fragments":        []string{"epubcfi(/6/2!/4/2/2,/1:2,/1:20)"},
					"totalProgression": 0.001,
				},
			},
		},
		map[string]any{
			"id": "00000000-0000-4000-8000-0000000000a2", "base_rev": 0,
			"work_id": workID, "kind": "note",
			"body": "A thought about the whale", "progression": 0.5,
			"client_ts": ts,
		},
		map[string]any{
			"id": "00000000-0000-4000-8000-0000000000a3", "base_rev": 0,
			"work_id": workID, "kind": "highlight",
			"excerpt": "an unanchored highlight", "progression": 0.9,
			"client_ts": ts,
			"locator": map[string]any{
				"href":      "chapter5.xhtml",
				"locations": map[string]any{"totalProgression": 0.9},
			},
		},
	}})
	out := api("/v1/annotations", string(anns))
	if results, ok := out["results"].([]any); !ok || len(results) != 3 {
		t.Fatalf("annotation seeding: %v", out)
	} else {
		for _, r := range results {
			if r.(map[string]any)["status"] != "applied" {
				t.Fatalf("annotation seeding: %v", out)
			}
		}
	}
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
func TestReaderLiveInARealBrowser(t *testing.T) {
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
	bookID := f.addBook(t, "live-novel", browserTestEPUB(t))
	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")
	seedStalePosition(t, ts.URL, cookie, bookID)
	cmd := exec.Command(node, filepath.Join("testdata", "readerbrowser.mjs"))
	cmd.Env = append(os.Environ(),
		"SMOKE_CHROME="+chrome,
		"SMOKE_URL="+ts.URL+"/ui/books/"+bookID+"/read",
		"SMOKE_COOKIE="+cookie.Name+"="+cookie.Value,
		"SMOKE_HOST="+strings.TrimPrefix(ts.URL, "http://"),
		"SMOKE_LIVE=1",
		"SMOKE_SHOT="+os.Getenv("LISEUR_READER_SCREENSHOT"),
	)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("live reader did not work in a browser: %v", err)
	}
}

func TestReaderOpensInARealBrowser(t *testing.T) {
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
	bookID := f.addBook(t, "novel", epub)

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
		"SMOKE_PAGES="+strconv.Itoa(browserTestPages(t, epub)),
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

	parallelBrowser(t)
	f := newBooksFixture(t)
	epub := browserTestEPUB(t)
	bookID := f.addBook(t, "novel", epub)
	ts, readerHost := splitOriginServer(t, f)
	cookie := f.loginTo(t, ts, "alice")

	// The browser is pointed at the main origin, as a reader always is.
	// Everything after that — the redirect, the fragment, the
	// cross-origin fetches — is the feature under test.
	cmd := exec.Command(node, filepath.Join("testdata", "readerbrowser.mjs"))
	cmd.Env = append(os.Environ(),
		"SMOKE_CHROME="+chrome,
		"SMOKE_URL="+ts.URL+"/ui/books/"+bookID+"/read",
		"SMOKE_PAGES="+strconv.Itoa(browserTestPages(t, epub)),
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
	// And sittings behind that progress, because the statistics half of
	// insights are drawn from sessions, not positions: without them
	// every tile reads zero and the chart is a blank rule.
	sittingsFor(t, f, now)

	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")

	cmd := exec.Command(node, filepath.Join("testdata", "uishots.mjs"))
	cmd.Env = append(os.Environ(),
		"SHOT_CHROME="+chrome,
		"SHOT_URL="+ts.URL,
		"SHOT_COOKIE="+cookie.Name+"="+cookie.Value,
		"SHOT_DIR="+outDir,
		// The calendar view of the reading habit is only offered on a
		// span longer than a month, so the walk asks for one outright.
		"SHOT_PATHS=/ui/insights,/ui/insights?span=365d&chart=chart-calendar,"+
			"/ui/library,/ui/library?filter=reading,/ui/books/"+books[0]+","+
			"/ui/entities/contributors,/ui/settings?section=devices,/ui/settings",
		"SHOT_PREFS="+os.Getenv("LISEUR_UI_PREFS"),
		"SHOT_TAG="+os.Getenv("LISEUR_UI_TAG"),
	)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("screenshot walk failed: %v", err)
	}
}

// TestReaderRefusesANaNPositionButRecovers is the browser half of the
// position-jumps fix (the web reader once pushed a NaN fraction that the
// server stored as the start of the book). It is a local-only check:
// findChrome() returns "" under CI, and internal/api/progression_test.go
// is what actually gates the op log — this only proves the client fails
// closed on a bad fraction and, crucially, still syncs the next good one
// so the reader is not left unable to save its place.
func TestReaderRefusesANaNPositionButRecovers(t *testing.T) {
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
	bookID := f.addBook(t, "novel", epub)

	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")
	seedStalePosition(t, ts.URL, cookie, bookID)

	cmd := exec.Command(node, filepath.Join("testdata", "readerbrowser.mjs"))
	cmd.Env = append(os.Environ(),
		"SMOKE_CHROME="+chrome,
		"SMOKE_URL="+ts.URL+"/ui/books/"+bookID+"/read",
		"SMOKE_PAGES="+strconv.Itoa(browserTestPages(t, epub)),
		"SMOKE_COOKIE="+cookie.Name+"="+cookie.Value,
		"SMOKE_HOST="+strings.TrimPrefix(ts.URL, "http://"),
		"SMOKE_NAN=1",
		"SMOKE_SHOT=",
	)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("the reader did not fail closed on a NaN position: %v", err)
	}

	// The finite fraction the guard sent last must have reached the log,
	// proving recovery end to end rather than only in the page.
	page, err := f.st.Changes(t.Context(), "u1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	recovered := false
	for _, op := range page.Ops {
		if op.Progression == 0.47 {
			recovered = true
		}
	}
	if !recovered {
		t.Error("the finite position pushed after the NaN never reached the op log")
	}
}

// TestReaderRecordsReadingSessions is the browser side of ADR-0030: a
// sitting in the web reader reaches /v1/sessions, and so the statistics,
// which until then counted only what KOReader and the apps reported.
// The page is driven with a wound-forward clock and a faked visibility
// (testdata/readerbrowser.mjs, sessionGuard), and the rows are then read
// back from the store, because the probe can only see the attempt.
func TestReaderRecordsReadingSessions(t *testing.T) {
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
	bookID := f.addBook(t, "novel", epub)

	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")

	cmd := exec.Command(node, filepath.Join("testdata", "readerbrowser.mjs"))
	cmd.Env = append(os.Environ(),
		"SMOKE_CHROME="+chrome,
		"SMOKE_URL="+ts.URL+"/ui/books/"+bookID+"/read",
		"SMOKE_PAGES="+strconv.Itoa(browserTestPages(t, epub)),
		"SMOKE_COOKIE="+cookie.Name+"="+cookie.Value,
		"SMOKE_HOST="+strings.TrimPrefix(ts.URL, "http://"),
		"SMOKE_SESSIONS=1",
		"SMOKE_SHOT=",
	)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("the reader did not record its sittings: %v", err)
	}

	// Two sittings were closed with something to say and one was a
	// glance; the store must hold exactly the two, with the figures the
	// page computed rather than something the server made up.
	rows, err := f.st.SessionsInRange(t.Context(), "u1",
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("sessions in store: got %d, want 2", len(rows))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].StartedAt.Before(rows[j].StartedAt) })
	// Wall and monotonic clocks can round the same interval one millisecond apart.
	if rows[0].EndProg != 0.47 || rows[0].IdleMs < 0 || rows[0].IdleMs > 1 {
		t.Errorf("first sitting: end %v idle %d, want 0.47 and at most 1ms idle", rows[0].EndProg, rows[0].IdleMs)
	}
	if d := rows[1].IdleMs - 7*60*1000; d < -1000 || d > 1000 {
		t.Errorf("second sitting: idle %d, want about 7 minutes", rows[1].IdleMs)
	}
	for _, s := range rows {
		if s.Origin != store.OriginNative || s.DeviceID == "" {
			t.Errorf("session %s: origin %q device %q", s.SessionID, s.Origin, s.DeviceID)
		}
	}
}

// sittingsFor writes a plausible few months of reading behind the two
// works the screenshot walk puts progress on: a finished book read in a
// burst back in the spring, and a current one still being read most
// evenings. It is shaped so every span the picker offers has something
// in it — a streak in the last week, weeks to bucket at ninety days,
// months to bucket at a year.
func sittingsFor(t *testing.T, f *booksFixture, now time.Time) {
	t.Helper()
	evening := func(daysAgo int) time.Time {
		d := now.AddDate(0, 0, -daysAgo)
		return time.Date(d.Year(), d.Month(), d.Day(), 21, 10, 0, 0, time.UTC)
	}
	var out []store.Session
	sitting := func(work string, daysAgo, minutes int, from, to float64) {
		start := evening(daysAgo)
		pages := float64(minutes) / 2.2
		out = append(out, store.Session{
			SessionID: fmt.Sprintf("018e6f1b-0000-7000-8000-%012d", len(out)+1),
			WorkID:    work, DeviceID: "d-test",
			StartedAt: start, EndedAt: start.Add(time.Duration(minutes) * time.Minute),
			StartProg: from, EndProg: to, ReportedPages: &pages,
			Origin: store.OriginNative,
		})
	}

	// Neuromancer, read to the end over a fortnight a couple of months back.
	for i, minutes := range []int{35, 50, 20, 65, 40, 55, 30, 80} {
		day := 78 - i*2
		from := float64(i) / 8
		sitting("w-neuromancer", day, minutes, from, from+0.125)
	}
	// Dune, still going: a scattering, then most of this past week.
	for i, minutes := range []int{45, 25, 60, 35} {
		sitting("w-dune", 40-i*7, minutes, 0.05+float64(i)*0.04, 0.09+float64(i)*0.04)
	}
	for i, minutes := range []int{50, 30, 70, 25, 40, 55} {
		sitting("w-dune", 6-i, minutes, 0.21+float64(i)*0.035, 0.245+float64(i)*0.035)
	}

	if err := f.st.AppendSessions(t.Context(), "u1", out); err != nil {
		t.Fatal(err)
	}
}

// A refresh that redraws the wrong thing looks exactly like one that
// works: the shelf is still there, just stale, or gone and replaced by a
// reload. Asking this route the way htmx asks returns a fragment of the
// card list without the page around it, so the region a refresh replaces
// would be swapped away. Only a browser catches that.
func TestLibraryRefreshInARealBrowser(t *testing.T) {
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
	f.addBook(t, "refreshed-novel", browserTestEPUB(t))

	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")

	cmd := exec.Command(node, filepath.Join("testdata", "librarybrowser.mjs"))
	cmd.Env = append(os.Environ(),
		"SMOKE_CHROME="+chrome,
		"SMOKE_URL="+ts.URL+"/ui/",
		"SMOKE_TITLE=Moby-Dick",
		"SMOKE_COOKIE="+cookie.Name+"="+cookie.Value,
		"SMOKE_HOST="+strings.TrimPrefix(ts.URL, "http://"),
	)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("the library refresh did not work in a browser: %v", err)
	}
}
