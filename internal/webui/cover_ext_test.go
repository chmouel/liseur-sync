package webui_test

import (
	"archive/zip"
	"bytes"
	htmlpkg "html"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func (f *booksFixture) fetchCover(
	t *testing.T, path string, cookie *http.Cookie,
) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	resp, err := noRedirectClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	return resp, body.Bytes()
}

// coverEPUB builds a publication with a real cover image.
func coverEPUB(t *testing.T, width, height int) []byte {
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
	add("OPS/package.opf", []byte(`<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf"
 xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
 <metadata><dc:title>Illustrated</dc:title></metadata>
 <manifest>
  <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"
    properties="nav"/>
  <item id="cover" href="cover.png" media-type="image/png"
    properties="cover-image"/>
 </manifest>
 <spine><itemref idref="nav"/></spine>
</package>`), zip.Deflate)
	add("OPS/nav.xhtml",
		[]byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body/></html>`), zip.Deflate)

	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			picture.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	add("OPS/cover.png", encoded.Bytes(), zip.Deflate)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestBooksUIShowsCovers(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "illustrated", coverEPUB(t, 300, 400))

	_, html := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	if !strings.Contains(html, "books/"+bookID+"/cover") {
		t.Fatalf("the list shows no cover:\n%s", html)
	}
	_, html = f.get(t, "/ui/books/"+bookID, f.cookie)
	if !strings.Contains(html, "books/"+bookID+"/cover?size=full") {
		t.Fatalf("the book page shows no cover:\n%s", html)
	}

	resp, body := f.fetchCover(t, "/ui/books/"+bookID+"/cover", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cover: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q", got)
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(body)); err != nil {
		t.Fatalf("the cover is not a decodable image: %v", err)
	}
}

// A shelf of sideloaded books is mostly books without covers. A browser
// given the API's honest 404 draws a broken-image icon for every one of
// them, so the page gets something to draw instead.
func TestBooksUIDrawsAPlaceholderForBooksWithoutACover(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "plain", []byte("not an epub at all"))

	resp, body := f.fetchCover(t, "/ui/books/"+bookID+"/cover", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code = %d, want a placeholder rather than an error", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff = %q", got)
	}
	config, err := png.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("the placeholder is not an image: %v", err)
	}
	if config.Width == 0 || config.Height == 0 {
		t.Fatalf("the placeholder is %dx%d", config.Width, config.Height)
	}
	// A JSON error body reaching the browser as an image would be the
	// bug this exists to prevent.
	if bytes.Contains(body, []byte(`"error"`)) {
		t.Fatal("an error body was served as an image")
	}
}

func TestBooksUICoverRequiresASession(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "guarded", coverEPUB(t, 300, 400))

	resp, _ := f.fetchCover(t, "/ui/books/"+bookID+"/cover", nil)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("an unauthenticated cover request succeeded: %d", resp.StatusCode)
	}
}

// A failed cover response has already set headers describing the cover it
// was going to send. Keeping them would tell the browser to cache a
// placeholder for a year under the real cover's validator, so the real
// cover would never be fetched.
func TestBooksUIPlaceholderDoesNotInheritTheCoversCachingHeaders(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "ranged", coverEPUB(t, 300, 400))

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		f.ts.URL+"/ui/books/"+bookID+"/cover", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(f.cookie)
	// A range past the end of the image: the cover handler starts to
	// answer, sets its headers, and only then fails.
	request.Header.Set("Range", "bytes=99999999-")
	resp, err := noRedirectClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code = %d, want a placeholder", resp.StatusCode)
	}
	if _, err := png.DecodeConfig(bytes.NewReader(body)); err != nil {
		t.Fatalf("not a placeholder image: %v", err)
	}
	if got := resp.Header.Get("Cache-Control"); strings.Contains(got, "immutable") {
		t.Errorf("Cache-Control = %q: the placeholder inherited the cover's", got)
	}
	if got := resp.Header.Get("ETag"); strings.Contains(got, "-thumbnail") {
		t.Errorf("ETag = %q: the placeholder claims to be the cover", got)
	}
}

// An instance with no content storage still has book records, and its
// pages still ask for covers. Answering with a placeholder is what keeps
// that a plain page rather than a crash.
func TestBooksUICoverWithoutContentStorage(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "storageless", coverEPUB(t, 300, 400))

	mux := http.NewServeMux()
	ui := f.uiWithoutCovers()
	ui.Mount(mux, func(h http.Handler) http.Handler { return h })
	server := httptest.NewServer(mux)
	defer server.Close()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		server.URL+"/ui/books/"+bookID+"/cover", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(f.loginTo(t, server, "alice"))
	resp, err := noRedirectClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code = %d, want a placeholder", resp.StatusCode)
	}
	if _, err := png.DecodeConfig(bytes.NewReader(body)); err != nil {
		t.Fatalf("not an image: %v", err)
	}
}

// TestPlaceholderFollowsTheTheme: a shelf of sideloaded books is mostly
// placeholders, so a light card on the dark theme is a wall of glare.
// The theme is already in a cookie, so the image can simply follow it.
func TestPlaceholderFollowsTheTheme(t *testing.T) {
	f := newBooksFixture(t)
	bookID := bookWithMetadata(t, f, "coverless")

	dark := f.coverBytes(t, bookID, nil)
	light := f.coverBytes(t, bookID, &http.Cookie{Name: "liseur_ui", Value: "light.grid"})
	if bytes.Equal(dark, light) {
		t.Fatal("the placeholder ignored the theme")
	}
	// Both are still images, and both still say so.
	for _, body := range [][]byte{dark, light} {
		if _, _, err := image.Decode(bytes.NewReader(body)); err != nil {
			t.Fatalf("placeholder is not an image: %v", err)
		}
	}
}

// coverBytes fetches a cover with an optional extra cookie.
func (f *booksFixture) coverBytes(t *testing.T, bookID string, extra *http.Cookie) []byte {
	t.Helper()
	req, _ := http.NewRequest("GET", f.ts.URL+"/ui/books/"+bookID+"/cover", nil)
	req.AddCookie(f.cookie)
	if extra != nil {
		req.AddCookie(extra)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Vary") == "" {
		t.Error("a cookie-dependent image did not say it varies by cookie")
	}
	body, _ := io.ReadAll(resp.Body)
	return body
}

// The grid does not link to the cover route bare: it asks for a named
// variant. A size the renderer does not know is a 400, which the
// fallback turns into a placeholder, so the failure is silent — a wall
// of blank cards where every book has a picture. This asserts against
// the URL the page actually emits rather than against a substring of
// it, because the substring is how the wrong name survived: "thumb" is
// a prefix of "thumbnail".
func TestBooksGridAsksForACoverSizeTheServerRenders(t *testing.T) {
	f := newBooksFixture(t)
	f.addBook(t, "illustrated", coverEPUB(t, 300, 400))

	_, html := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	sources := coverSources(t, html)
	if len(sources) == 0 {
		t.Fatalf("the grid shows no cover images:\n%s", html)
	}
	for _, src := range sources {
		resp, body := f.fetchCover(t, "/ui/"+src, f.cookie)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", src, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
			t.Fatalf("%s: Content-Type = %q, want the rendered cover"+
				" rather than the placeholder", src, got)
		}
		if _, _, err := image.DecodeConfig(bytes.NewReader(body)); err != nil {
			t.Fatalf("%s: not a decodable image: %v", src, err)
		}
	}
}

var coverImageSrc = regexp.MustCompile(`<img[^>]+src="([^"]*cover[^"]*)"`)

func coverSources(t *testing.T, html string) []string {
	t.Helper()
	var found []string
	for _, match := range coverImageSrc.FindAllStringSubmatch(html, -1) {
		found = append(found, htmlpkg.UnescapeString(match[1]))
	}
	return found
}

// TestBooksUICoverIconVariantIsSquare: the reader page's tab icon is a
// cover through the icon variant, which crops to a square rather than
// letting the browser squish a portrait cover.
func TestBooksUICoverIconVariantIsSquare(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "illustrated", coverEPUB(t, 300, 400))

	resp, body := f.fetchCover(t, "/ui/books/"+bookID+"/cover?size=icon", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("icon cover: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q", got)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("the icon is not a decodable image: %v", err)
	}
	if config.Width != config.Height {
		t.Fatalf("icon = %dx%d, want square", config.Width, config.Height)
	}
	if config.Width > 64 {
		t.Fatalf("icon edge = %d, want at most 64", config.Width)
	}

	// A coverless book gets the drawn card rather than a 404, so the
	// reader's tab icon never comes back empty.
	plainID := f.addBook(t, "plain", []byte("not an epub at all"))
	resp, body = f.fetchCover(t, "/ui/books/"+plainID+"/cover?size=icon", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("icon for a coverless book: %d", resp.StatusCode)
	}
	config, _, err = image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("the placeholder icon is not an image: %v", err)
	}
	if config.Width != config.Height {
		t.Fatalf("placeholder icon = %dx%d, want square", config.Width, config.Height)
	}
}
