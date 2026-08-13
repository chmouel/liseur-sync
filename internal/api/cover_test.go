//go:build linux

package api

import (
	"archive/zip"
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"testing"
	"time"

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

func TestCoverIsServedAsABoundedImage(t *testing.T) {
	f := newUploadFixture(t)
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
	f := newUploadFixture(t)
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
	f := newUploadFixture(t)
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
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "plain", coverEPUB(t, 0, 0, ""))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, body := f.get(t, "/v1/books/"+bookID+"/cover", read)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("code = %d, want 404: %s", resp.StatusCode, body)
	}
}

// A book that is not an archive at all must not become a 500: uploaded
// bytes are client input, and client input never produces a server error.
func TestUnreadableBookReportsNoCoverRatherThanFailing(t *testing.T) {
	f := newUploadFixture(t)
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
	f := newUploadFixture(t)
	bookID, digest := f.publish(t, "remembered", coverEPUB(t, 0, 0, ""))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	if resp, _ := f.get(t, "/v1/books/"+bookID+"/cover", read); resp.StatusCode != 404 {
		t.Fatalf("first request: %d", resp.StatusCode)
	}
	// The answer is recorded where a later request will find it, so the
	// archive is not opened again.
	if _, _, err := f.cas.OpenCover(t.Context(), digest, "thumbnail"); !errors.Is(
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

// A rendered cover must come back from the cache rather than be rebuilt,
// which is only observable by making rebuilding impossible.
func TestRenderedCoverIsServedFromTheCache(t *testing.T) {
	f := newUploadFixture(t)
	data := coverEPUB(t, 300, 400, "cover.png")
	bookID, digest := f.publish(t, "cached", data)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, first := f.get(t, "/v1/books/"+bookID+"/cover", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first request: %d %s", resp.StatusCode, first)
	}
	removed, err := f.cas.RemoveBlob(t.Context(), digest, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("the blob was not removed")
	}
	// RemoveBlob drops cached covers with the blob, so the cache is now
	// empty too and the second request must report the content gone
	// rather than serve a picture of a book that no longer exists.
	if resp, _ := f.get(t, "/v1/books/"+bookID+"/cover", read); resp.StatusCode != http.StatusGone {
		t.Fatalf("after removal: %d, want 410", resp.StatusCode)
	}
}

// The digest names the source bytes and the variant names the pipeline, so
// the two together are a strong validator: a client that has one may skip
// the transfer entirely.
func TestCoverSupportsConditionalRequests(t *testing.T) {
	f := newUploadFixture(t)
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
	f := newUploadFixture(t)
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

func TestCoverOfAnotherUsersBookIsNotFound(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "private-cover", coverEPUB(t, 300, 400, "cover.png"))
	stranger := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)

	resp, body := f.get(t, "/v1/books/"+bookID+"/cover", stranger)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("code = %d, want 404: %s", resp.StatusCode, body)
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
	f := newUploadFixture(t)
	bookID, digest := f.publish(t, "poisoned", coverEPUB(t, 300, 400, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	planted := jpegBytes(t, 7, 11)

	if err := f.cas.StoreCover(t.Context(), digest, "thumbnail", planted); err != nil {
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
	f := newUploadFixture(t)
	bookID, digest := f.publish(t, "marked", coverEPUB(t, 300, 400, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	if err := f.cas.MarkCoverAbsent(t.Context(), digest); err != nil {
		t.Fatal(err)
	}
	if resp, body := f.get(t, "/v1/books/"+bookID+"/cover", read); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("code = %d, want the remembered 404: %s", resp.StatusCode, body)
	}
}

// Rendering once and keeping nothing would make every request pay for the
// decode again.
func TestARenderedCoverIsWrittenToTheCache(t *testing.T) {
	f := newUploadFixture(t)
	bookID, digest := f.publish(t, "stored", coverEPUB(t, 300, 400, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, served := f.get(t, "/v1/books/"+bookID+"/cover", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code = %d: %s", resp.StatusCode, served)
	}
	cached, _, err := f.cas.OpenCover(t.Context(), digest, "thumbnail")
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
	f := newUploadFixture(t)
	bookID, digest := f.publish(t, "sniffed", coverEPUB(t, 300, 400, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	var planted bytes.Buffer
	if err := png.Encode(&planted, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	if err := f.cas.StoreCover(t.Context(), digest, "thumbnail", planted.Bytes()); err != nil {
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
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "validators", coverEPUB(t, 900, 1200, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	thumbnail, _ := f.get(t, "/v1/books/"+bookID+"/cover?size=thumbnail", read)
	full, _ := f.get(t, "/v1/books/"+bookID+"/cover?size=full", read)
	if thumbnail.Header.Get("ETag") == full.Header.Get("ETag") {
		t.Fatalf("both sizes answer with %s", thumbnail.Header.Get("ETag"))
	}
}

// A deleted book must not keep illustrating itself. The catalog decides
// what may be served, and asking the files alone would happily produce a
// picture of a book that is in the trash.
func TestCoverOfATrashedBookIsNotServed(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "trashed-cover", coverEPUB(t, 300, 400, "cover.png"))
	manage := f.mintToken(t, f.user.ID, store.ScopeLibraryManage)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	if resp, raw := f.req(
		t, http.MethodDelete, "/v1/books/"+bookID, manage,
	); resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := f.get(t, "/v1/books/"+bookID+"/cover", read); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("trashed book still has a cover: %d", resp.StatusCode)
	}
}

// A file reconciliation has marked missing is not a file. The blob is
// deliberately left on disk here: if the handler consulted the files
// without checking availability, it would find bytes and serve a cover
// for something the catalog has already withdrawn.
func TestCoverOfAnUnavailableFileReportsItGone(t *testing.T) {
	f := newUploadFixture(t)
	data := coverEPUB(t, 300, 400, "cover.png")
	bookID, digest := f.publish(t, "unavailable-cover", data)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	now := time.Now().UTC()
	if _, err := f.st.ReconcileBlob(t.Context(),
		store.BlobInfo{SHA256: digest, SizeBytes: int64(len(data))}, false, now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := content.ReconcileCatalogAvailability(
		t.Context(), f.st, now, 100); err != nil {
		t.Fatal(err)
	}
	resp, body := f.get(t, "/v1/books/"+bookID+"/cover", read)
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("code = %d, want 410: %s", resp.StatusCode, body)
	}
}

// A client cannot guess where a cover lives, so the book record has to
// say. It is advertised for every book: a client that asks and gets 404
// has learned the same thing as one told in advance, without the server
// having to record the answer for every book ever uploaded.
func TestBookRecordAdvertisesItsCover(t *testing.T) {
	f := newUploadFixture(t)
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
	code, page := getJSON(t, f.ts.URL+"/v1/libraries/"+f.library+"/books", read)
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
