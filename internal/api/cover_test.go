//go:build linux

package api

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
)

// coverEPUB builds a publication whose cover is a real image of a known
// size, so a test can tell a rendered cover from a passed-through one.
func coverEPUB(t *testing.T, width, height int, entry string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	add := func(name string, body []byte, method uint16) {
		t.Helper()
		target, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", []byte("application/epub+zip"), zip.Store)
	add("META-INF/container.xml", []byte(
		`<?xml version="1.0"?><container version="1.0"`+
			` xmlns="urn:oasis:names:tc:opendocument:xmlns:container">`+
			`<rootfiles><rootfile full-path="OPS/package.opf"`+
			` media-type="application/oebps-package+xml"/></rootfiles></container>`),
		zip.Deflate)

	manifest := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf"
 xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
 <metadata><dc:title>Illustrated</dc:title></metadata>
 <manifest>
  <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"
    properties="nav"/>`
	if entry != "" {
		manifest += `
  <item id="cover" href="` + entry + `" media-type="image/png"
    properties="cover-image"/>`
	}
	manifest += `
 </manifest>
 <spine><itemref idref="nav"/></spine>
</package>`
	add("OPS/package.opf", []byte(manifest), zip.Deflate)
	add("OPS/nav.xhtml", []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body/></html>`),
		zip.Deflate)

	if entry != "" {
		picture := image.NewRGBA(image.Rect(0, 0, width, height))
		for y := range height {
			for x := range width {
				picture.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
			}
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, picture); err != nil {
			t.Fatal(err)
		}
		add("OPS/"+entry, encoded.Bytes(), zip.Deflate)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func decodeCover(t *testing.T, body []byte) image.Config {
	t.Helper()
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decoding the served cover: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("format = %q, want jpeg", format)
	}
	return config
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestCoverIsServedAsABoundedImage(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "illustrated", coverEPUB(t, 900, 1200, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, body := f.get(t, "/v1/books/"+bookID+"/cover", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", got)
	}
	// The type is chosen by the server, so nosniff is what makes it
	// binding rather than advisory.
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff = %q", got)
	}
	config := decodeCover(t, body)
	if config.Width >= 900 || config.Height >= 1200 {
		t.Errorf("thumbnail is %dx%d, want smaller than the source",
			config.Width, config.Height)
	}
	// Within a pixel: the scaled edge is rounded, not exact.
	if skew := config.Width*1200 - config.Height*900; skew > 1200 || skew < -1200 {
		t.Errorf("aspect ratio not kept: %dx%d", config.Width, config.Height)
	}
}

// The two sizes must actually differ, or asking for one is asking for the
// other and the parameter is decoration.
func TestFullCoverIsLargerThanTheThumbnail(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "sizes", coverEPUB(t, 900, 1200, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	_, thumbnail := f.get(t, "/v1/books/"+bookID+"/cover?size=thumbnail", read)
	_, full := f.get(t, "/v1/books/"+bookID+"/cover?size=full", read)
	small, large := decodeCover(t, thumbnail), decodeCover(t, full)
	if small.Height >= large.Height {
		t.Fatalf("thumbnail %dx%d is not smaller than full %dx%d",
			small.Width, small.Height, large.Width, large.Height)
	}
}

// The variant becomes a cache path, so an unknown one is refused rather
// than mapped to a default.
func TestUnknownCoverSizeIsRefused(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "badsize", coverEPUB(t, 60, 90, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	for _, size := range []string{"huge", "../../etc", "THUMBNAIL"} {
		resp, body := f.get(t, "/v1/books/"+bookID+"/cover?size="+size, read)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("size=%s: code = %d, want 400: %s", size, resp.StatusCode, body)
		}
	}
}

func TestBookWithoutACoverReports404(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "plain", coverEPUB(t, 0, 0, ""))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, body := f.get(t, "/v1/books/"+bookID+"/cover", read)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("code = %d, want 404: %s", resp.StatusCode, body)
	}
}

// A book that is not an archive at all must not become a 500: a folder
// is somebody else's filesystem, and a corrupt or half-written file is
// ordinary input, not a server error.
func TestUnreadableBookReportsNoCoverRatherThanFailing(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "notazip", []byte("this is not an archive"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, body := f.get(t, "/v1/books/"+bookID+"/cover", read)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("code = %d, want 404: %s", resp.StatusCode, body)
	}
}

// Rendering is the expensive part, and a book with no cover is the one
// case success cannot cache. Without the negative marker, every scroll
// past a coverless book re-opens the archive.
func TestAbsentCoverIsRememberedWithoutReopeningTheBook(t *testing.T) {
	f := newFolderFixture(t)
	bookID, digest := f.publish(t, "remembered", coverEPUB(t, 0, 0, ""))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	if resp, _ := f.get(t, "/v1/books/"+bookID+"/cover", read); resp.StatusCode != 404 {
		t.Fatalf("first request: %d", resp.StatusCode)
	}
	// The answer is recorded where a later request will find it, so the
	// archive is not opened again.
	if _, _, err := f.cache.OpenCover(t.Context(), digest, "thumbnail"); !errors.Is(
		err, content.ErrNoCover,
	) {
		t.Fatalf("cache after a coverless book: %v, want ErrNoCover", err)
	}
	resp, body := f.get(t, "/v1/books/"+bookID+"/cover", read)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second request: %d, want the remembered 404: %s",
			resp.StatusCode, body)
	}
}

// The digest names the source bytes and the variant names the pipeline, so
// the two together are a strong validator: a client that has one may skip
// the transfer entirely.
func TestCoverSupportsConditionalRequests(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "conditional", coverEPUB(t, 300, 400, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, _ := f.get(t, "/v1/books/"+bookID+"/cover", read)
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on a cover")
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		f.ts.URL+"/v1/books/"+bookID+"/cover", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+read)
	request.Header.Set("If-None-Match", etag)
	conditional, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer conditional.Body.Close()
	if conditional.StatusCode != http.StatusNotModified {
		t.Fatalf("code = %d, want 304", conditional.StatusCode)
	}
}

// Two sizes of one book must not share a cache entry, or asking for a
// thumbnail after a full cover returns the wrong picture.
func TestCoverSizesAreCachedSeparately(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "twosizes", coverEPUB(t, 900, 1200, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	_, full := f.get(t, "/v1/books/"+bookID+"/cover?size=full", read)
	_, thumbnail := f.get(t, "/v1/books/"+bookID+"/cover?size=thumbnail", read)
	// Served after the full size and from cache the second time around.
	_, again := f.get(t, "/v1/books/"+bookID+"/cover?size=thumbnail", read)
	if !bytes.Equal(thumbnail, again) {
		t.Fatal("the cached thumbnail differs from the rendered one")
	}
	if decodeCover(t, again).Height >= decodeCover(t, full).Height {
		t.Fatal("the cached thumbnail is not a thumbnail")
	}
}

// jpegBytes is a valid JPEG of a solid colour, used where a test needs
// bytes the cache would accept and a client would decode.
func jpegBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(picture, picture.Bounds(),
		&image.Uniform{C: color.RGBA{R: 10, G: 200, B: 30, A: 255}},
		image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, picture, nil); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

// The cache has to be answered from, not merely written to. Poisoning it
// with a recognisable picture is the only way to tell a cache hit from a
// re-render that happens to produce the same bytes.
func TestACachedCoverIsServedInsteadOfRendering(t *testing.T) {
	f := newFolderFixture(t)
	bookID, digest := f.publish(t, "poisoned", coverEPUB(t, 300, 400, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	planted := jpegBytes(t, 7, 11)

	if err := f.cache.StoreCover(t.Context(), digest, "thumbnail", planted); err != nil {
		t.Fatal(err)
	}
	resp, body := f.get(t, "/v1/books/"+bookID+"/cover", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code = %d: %s", resp.StatusCode, body)
	}
	if !bytes.Equal(body, planted) {
		t.Fatal("the cover was re-rendered instead of served from the cache")
	}
}

// A recorded "no cover" answer must be believed, or the marker saves
// nothing.
func TestARememberedAbsentCoverIsBelieved(t *testing.T) {
	f := newFolderFixture(t)
	bookID, digest := f.publish(t, "marked", coverEPUB(t, 300, 400, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	if err := f.cache.MarkCoverAbsent(t.Context(), digest); err != nil {
		t.Fatal(err)
	}
	if resp, body := f.get(t, "/v1/books/"+bookID+"/cover", read); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("code = %d, want the remembered 404: %s", resp.StatusCode, body)
	}
}

// Rendering once and keeping nothing would make every request pay for the
// decode again.
func TestARenderedCoverIsWrittenToTheCache(t *testing.T) {
	f := newFolderFixture(t)
	bookID, digest := f.publish(t, "stored", coverEPUB(t, 300, 400, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, served := f.get(t, "/v1/books/"+bookID+"/cover", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code = %d: %s", resp.StatusCode, served)
	}
	cached, _, err := f.cache.OpenCover(t.Context(), digest, "thumbnail")
	if err != nil {
		t.Fatalf("nothing was cached: %v", err)
	}
	defer cached.Close()
	stored, err := io.ReadAll(cached)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, served) {
		t.Fatal("the cached cover differs from the one that was served")
	}
}

// The type is fixed by the server rather than derived from the bytes.
// Cached content is server-produced, so the only way to observe the
// difference is to plant bytes of another type: sniffing would report
// them, and this route must not.
func TestCoverContentTypeIsNotSniffedFromTheBytes(t *testing.T) {
	f := newFolderFixture(t)
	bookID, digest := f.publish(t, "sniffed", coverEPUB(t, 300, 400, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	var planted bytes.Buffer
	if err := png.Encode(&planted, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	if err := f.cache.StoreCover(t.Context(), digest, "thumbnail", planted.Bytes()); err != nil {
		t.Fatal(err)
	}
	resp, _ := f.get(t, "/v1/books/"+bookID+"/cover", read)
	if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want the server's own type", got)
	}
}

// An ETag shared between sizes would let a cached thumbnail satisfy a
// request for the full cover.
func TestCoverSizesHaveDifferentValidators(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "validators", coverEPUB(t, 900, 1200, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	thumbnail, _ := f.get(t, "/v1/books/"+bookID+"/cover?size=thumbnail", read)
	full, _ := f.get(t, "/v1/books/"+bookID+"/cover?size=full", read)
	if thumbnail.Header.Get("ETag") == full.Header.Get("ETag") {
		t.Fatalf("both sizes answer with %s", thumbnail.Header.Get("ETag"))
	}
}

// A client cannot guess where a cover lives, so the book record has to
// say. It is advertised for every book: a client that asks and gets 404
// has learned the same thing as one told in advance, without the server
// having to record the answer for every book ever catalogued.
func TestBookRecordAdvertisesItsCover(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "advertised", coverEPUB(t, 300, 400, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	code, detail := getJSON(t, f.ts.URL+"/v1/books/"+bookID, read)
	if code != http.StatusOK {
		t.Fatalf("detail: %d %v", code, detail)
	}
	url, _ := detail["cover_url"].(string)
	if url == "" {
		t.Fatal("the book record does not say where its cover is")
	}
	resp, body := f.get(t, url, read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("advertised cover: %d %s", resp.StatusCode, body)
	}

	// And the listing carries it too, so a grid does not need a request
	// per book to find out.
	code, page := getJSON(t, f.ts.URL+"/v1/folders/"+f.folder.ID+"/books", read)
	if code != http.StatusOK {
		t.Fatalf("listing: %d %v", code, page)
	}
	books := page["books"].([]any)
	if len(books) == 0 {
		t.Fatal("no books listed")
	}
	if books[0].(map[string]any)["cover_url"] == nil {
		t.Fatalf("listed book has no cover_url: %v", books[0])
	}
}

// chosenCover is a picture nobody could mistake for the one inside the
// publication: a wide, short image against the tall one coverEPUB
// builds.
func chosenCover(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(picture, picture.Bounds(),
		&image.Uniform{C: color.RGBA{R: 10, G: 200, B: 90, A: 255}},
		image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, picture, nil); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

// calibreSchema is the subset of Calibre's own schema the calibre package
// reads, copied here rather than imported because it lives in that
// package's _test.go file. It is deliberately the same DDL: a mismatch
// here would be testing a library format Calibre does not produce.
const calibreSchema = `
CREATE TABLE books (
	id INTEGER PRIMARY KEY,
	title TEXT NOT NULL DEFAULT 'Unknown',
	sort TEXT,
	timestamp TIMESTAMP,
	pubdate TIMESTAMP,
	series_index REAL NOT NULL DEFAULT 1.0,
	path TEXT NOT NULL DEFAULT '',
	has_cover BOOL DEFAULT 0,
	last_modified TIMESTAMP NOT NULL DEFAULT '2000-01-01 00:00:00+00:00'
);
CREATE TABLE data (
	id INTEGER PRIMARY KEY,
	book INTEGER NOT NULL,
	format TEXT NOT NULL,
	uncompressed_size INTEGER NOT NULL,
	name TEXT NOT NULL
);
CREATE TABLE authors (
	id INTEGER PRIMARY KEY, name TEXT NOT NULL, sort TEXT);
CREATE TABLE books_authors_link (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL, author INTEGER NOT NULL);
CREATE TABLE series (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE books_series_link (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL, series INTEGER NOT NULL);
CREATE TABLE publishers (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE books_publishers_link (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL, publisher INTEGER NOT NULL);
CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE books_tags_link (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL, tag INTEGER NOT NULL);
CREATE TABLE languages (id INTEGER PRIMARY KEY, lang_code TEXT NOT NULL);
CREATE TABLE books_languages_link (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL,
	lang_code INTEGER NOT NULL, item_order INTEGER NOT NULL DEFAULT 0);
CREATE TABLE comments (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL, text TEXT NOT NULL);
CREATE TABLE identifiers (
	id INTEGER PRIMARY KEY, book INTEGER NOT NULL,
	type TEXT NOT NULL DEFAULT 'isbn', val TEXT NOT NULL);
`

// calibreBook is one row this test's library builds; each is its own
// directory so that two books can share one EPUB's bytes while keeping
// their own cover.jpg, which is the whole point of the test that uses
// this.
type calibreBook struct {
	id       int
	title    string
	dir      string
	epub     []byte
	hasCover bool
	cover    []byte
}

// writeCalibreLibrary builds a minimal, real Calibre library on disk —
// metadata.db plus the files it points at — and registers it as a
// store.FolderCalibre. It returns the folder so the caller can reconcile
// it with the fixture's own reconciler, exercising the real
// reconcileCalibre path rather than an invented one.
func (f *folderFixture) writeCalibreLibrary(t *testing.T, books []calibreBook) store.Folder {
	t.Helper()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), calibreSchema); err != nil {
		t.Fatal(err)
	}
	for _, b := range books {
		hasCover := 0
		if b.hasCover {
			hasCover = 1
		}
		if _, err := db.ExecContext(t.Context(), `INSERT INTO books
			(id, title, sort, timestamp, pubdate, series_index, path,
			 has_cover, last_modified)
			VALUES (?, ?, ?, '2020-01-01 00:00:00+00:00',
			 '2020-01-01 00:00:00+00:00', 1.0, ?, ?,
			 '2020-01-01 00:00:00+00:00')`,
			b.id, b.title, b.title, b.dir, hasCover); err != nil {
			t.Fatal(err)
		}
		name := b.title
		if _, err := db.ExecContext(t.Context(), `INSERT INTO data
			(id, book, format, uncompressed_size, name)
			VALUES (?, ?, 'EPUB', ?, ?)`,
			b.id, b.id, len(b.epub), name); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, filepath.FromSlash(b.dir))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(dir, name+".epub"), b.epub, 0o644); err != nil {
			t.Fatal(err)
		}
		if b.cover != nil {
			if err := os.WriteFile(
				filepath.Join(dir, "cover.jpg"), b.cover, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	folder := store.Folder{
		ID: store.NewID(), Name: "Calibre", RootPath: root,
		Kind: store.FolderCalibre,
	}
	if err := f.st.CreateFolder(t.Context(), folder); err != nil {
		t.Fatal(err)
	}
	return folder
}

// TestChosenCoverBeatsTheOneInsideTheBook is the Calibre case: cover.jpg
// beside the book is a picture somebody chose, and two books sharing one
// EPUB keep their own (ADR-0014, carried over unchanged by ADR-0017 —
// only how a book enters the catalog changed, not what a cover is). If
// the cache were keyed by the publication alone, the second book here
// would be served the first one's cover.
func TestChosenCoverBeatsTheOneInsideTheBook(t *testing.T) {
	f := newFolderFixture(t)
	epubBody := coverEPUB(t, 900, 1200, "cover.png")
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	first := chosenCover(t, 400, 100)
	second := chosenCover(t, 800, 200)
	folder := f.writeCalibreLibrary(t, []calibreBook{
		{id: 1, title: "Chosen A", dir: "Author/Chosen A (1)",
			epub: epubBody, hasCover: true, cover: first},
		{id: 2, title: "Chosen B", dir: "Author/Chosen B (2)",
			epub: epubBody, hasCover: true, cover: second},
	})
	f.reconcileFolder(t, folder)

	known, err := f.st.BooksInFolder(t.Context(), folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	bookByTitle := map[string]string{}
	for _, b := range known {
		if strings.Contains(b.RelativePath, "Chosen A") {
			bookByTitle["A"] = b.ID
		}
		if strings.Contains(b.RelativePath, "Chosen B") {
			bookByTitle["B"] = b.ID
		}
	}
	if bookByTitle["A"] == "" || bookByTitle["B"] == "" {
		t.Fatalf("both books were not catalogued: %+v", known)
	}

	var served []image.Config
	for name, cover := range map[string][]byte{"A": first, "B": second} {
		resp, raw := f.get(t, "/v1/books/"+bookByTitle[name]+"/cover?size=full", read)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("cover of %s: %d %s", name, resp.StatusCode, raw)
		}
		config := decodeCover(t, raw)
		if config.Width <= config.Height {
			t.Fatalf("the publication's own cover was served for %s: %dx%d",
				name, config.Width, config.Height)
		}
		if got := resp.Header.Get("ETag"); !strings.Contains(got, sha256Hex(cover)) {
			t.Errorf("%s: ETag %q does not name the cover it served", name, got)
		}
		served = append(served, config)
	}
	// Same publication, same rendering pipeline, two different pictures:
	// the cache key is the cover's digest, not the book's.
	if served[0].Width == served[1].Width {
		t.Fatalf("two books sharing one EPUB were served one cover: %+v", served)
	}

	// A cover.jpg that goes away is not an error: the book still has the
	// one inside its EPUB, and must fall back to it rather than fail.
	if err := os.Remove(filepath.Join(folder.RootPath, "Author/Chosen A (1)/cover.jpg")); err != nil {
		t.Fatal(err)
	}
	// The store still has the old digest recorded; OpenBookCover finds no
	// file at all now, which the handler treats the same as a changed
	// one and falls back to the publication's own cover rather than
	// erroring. size=thumbnail (never requested for this book before)
	// is used here rather than size=full, because size=full for this
	// book's still-recorded CoverSHA256 was already rendered and cached
	// above: re-requesting it would be answered from the cache without
	// ever touching renderStoredCover, and would tell us nothing about
	// the fallback.
	resp, raw := f.get(t, "/v1/books/"+bookByTitle["A"]+"/cover?size=thumbnail", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a missing cover.jpg cost the book its cover: %d %s", resp.StatusCode, raw)
	}
	if config := decodeCover(t, raw); config.Width >= config.Height {
		t.Fatalf("the fallback is not the publication's own: %dx%d",
			config.Width, config.Height)
	}
	if got := resp.Header.Get("ETag"); strings.Contains(got, sha256Hex(first)) {
		t.Errorf("the fallback borrowed the missing cover's ETag: %q", got)
	}

	// And the fallback did not take the cover's place in the cache: once
	// a fresh pass re-records a chosen cover, it is what gets served.
	// Caching under the fallback's own digest (the EPUB's) would have
	// made a transient absence permanent, since that digest never
	// changes.
	replacement := chosenCover(t, 320, 80)
	if err := os.WriteFile(
		filepath.Join(folder.RootPath, "Author/Chosen A (1)/cover.jpg"),
		replacement, 0o644); err != nil {
		t.Fatal(err)
	}
	f.reconcileFolder(t, folder)
	resp, raw = f.get(t, "/v1/books/"+bookByTitle["A"]+"/cover?size=full", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the returned cover: %d %s", resp.StatusCode, raw)
	}
	if config := decodeCover(t, raw); config.Width <= config.Height {
		t.Fatalf("the fallback outlived the cover coming back: %dx%d",
			config.Width, config.Height)
	}
}

// An icon asker needs something to put in the hole: a tab with no icon
// probes /favicon.ico instead, which is the same request by a longer
// road. So the icon variant alone answers "no cover" with a drawn card
// rather than a 404 — and the sizes that mean "does this book have a
// cover" keep the honest answer.
func TestCoverIconForACoverlessBookIsAPlaceholder(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "plain", coverEPUB(t, 0, 0, ""))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, body := f.get(t, "/v1/books/"+bookID+"/cover?size=icon", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("icon for a coverless book: %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want the one type the variant serves", got)
	}
	config := decodeCover(t, body)
	if config.Width != config.Height {
		t.Fatalf("placeholder icon = %dx%d, want square", config.Width, config.Height)
	}

	// The answer must survive the negative marker: the second request
	// takes the remembered-404 path, which has to give the same card.
	resp, body = f.get(t, "/v1/books/"+bookID+"/cover?size=icon", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remembered icon for a coverless book: %d: %s", resp.StatusCode, body)
	}

	// Every other size still says what is true.
	for _, size := range []string{"", "?size=full"} {
		resp, body := f.get(t, "/v1/books/"+bookID+"/cover"+size, read)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("cover%s = %d, want the honest 404: %s", size, resp.StatusCode, body)
		}
	}
}
