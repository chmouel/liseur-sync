//go:build linux

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
)

// publish drives a real upload all the way to a catalog book: the bytes go
// through the upload endpoint into the CAS, get promoted into the final
// tree, and the book and its file are committed together. Faking any of
// that would let a download test pass against content that was never
// really stored.
func (f *uploadFixture) publish(t *testing.T, name string, body []byte) (string, string) {
	t.Helper()
	return f.publishAs(t, name, "Title of "+name, body)
}

// publishAs is publish with the title spelled out, for tests that care
// what the metadata says rather than only that a book exists.
func (f *uploadFixture) publishAs(
	t *testing.T, name, title string, body []byte,
) (string, string) {
	t.Helper()
	code, out := f.upload(t, f.token, f.library, "publish-"+name, body)
	if code != http.StatusAccepted {
		t.Fatalf("upload %s: %d %v", name, code, out)
	}
	jobID := out["job_id"].(string)
	job, err := f.st.IngestJobByID(t.Context(), f.user.ID, jobID)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := f.cas.Promote(t.Context(), *job.StagingPath,
		*job.ContentSHA256, job.BytesReceived)
	if err != nil {
		t.Fatal(err)
	}

	at := time.Now().UTC()
	job, err = f.st.TransitionIngestJob(t.Context(), f.user.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: job.State, ExpectedRevision: job.Revision,
			NextState: store.IngestValidated, UpdatedAt: at,
		})
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.st.TransitionIngestJob(t.Context(), f.user.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: job.State, ExpectedRevision: job.Revision,
			NextState:                     store.IngestExtracted,
			ExtractedEmbeddedMetadataJSON: []byte(`{"title":"Extracted"}`),
			UpdatedAt:                     at.Add(time.Second),
		})
	if err != nil {
		t.Fatal(err)
	}

	bookID, fileID := "book-"+name, "file-"+name
	if _, err := f.st.CommitNewBookPromotion(t.Context(), f.user.ID, job.ID,
		store.CommitNewBookPromotionRequest{
			ExpectedRevision: job.Revision,
			Blob:             store.BlobInfo{SHA256: blob.SHA256, SizeBytes: blob.Size},
			Book: store.CatalogBook{
				ID: bookID, LibraryID: f.library, Status: store.BookActive,
				Title: title, TitleSource: store.MetadataEmbedded,
				Publisher: "A Publisher", PublisherSource: store.MetadataEmbedded,
				CreatedAt: at, UpdatedAt: at,
			},
			File: store.BookFile{
				ID: fileID, LibraryID: f.library, BookID: bookID,
				BlobSHA256: blob.SHA256, Source: store.IngestUpload,
				OriginalFilename: name + ".epub",
				MediaType:        "application/epub+zip",
				Availability:     store.BookFileAvailable,
				CreatedAt:        at, UpdatedAt: at,
			},
			UpdatedAt: at.Add(2 * time.Second),
		}); err != nil {
		t.Fatal(err)
	}
	return bookID, blob.SHA256
}

func (f *uploadFixture) get(t *testing.T, path, token string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.ts.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

// TestCatalogRoundTripsAnUploadedBook is the MVP in one test: a book that
// was uploaded can be found through the library listing and downloaded
// byte-for-byte.
func TestCatalogRoundTripsAnUploadedBook(t *testing.T) {
	f := newUploadFixture(t)
	body := bytes.Repeat([]byte("epub-bytes"), 100)
	bookID, digest := f.publish(t, "roundtrip", body)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	code, libs := getJSON(t, f.ts.URL+"/v1/libraries", read)
	if code != http.StatusOK {
		t.Fatalf("libraries: %d %v", code, libs)
	}
	entries := libs["libraries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("libraries = %v", entries)
	}
	if got := entries[0].(map[string]any)["library_id"]; got != f.library {
		t.Fatalf("library_id = %v", got)
	}

	code, page := getJSON(t, f.ts.URL+"/v1/libraries/"+f.library+"/books", read)
	if code != http.StatusOK {
		t.Fatalf("books: %d %v", code, page)
	}
	books := page["books"].([]any)
	if len(books) != 1 || books[0].(map[string]any)["book_id"] != bookID {
		t.Fatalf("books = %v", books)
	}
	if got := books[0].(map[string]any)["title"]; got != "Title of roundtrip" {
		t.Fatalf("title = %v", got)
	}

	code, detail := getJSON(t, f.ts.URL+"/v1/books/"+bookID, read)
	if code != http.StatusOK {
		t.Fatalf("detail: %d %v", code, detail)
	}
	files := detail["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["sha256"] != digest {
		t.Fatalf("files = %v", files)
	}

	resp, raw := f.get(t, "/v1/books/"+bookID+"/download", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: %d %s", resp.StatusCode, raw)
	}
	if !bytes.Equal(raw, body) {
		t.Fatalf("downloaded %d bytes, uploaded %d", len(raw), len(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/epub+zip" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if etag := resp.Header.Get("ETag"); etag != `"`+digest+`"` {
		t.Fatalf("ETag = %q, want the content digest", etag)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "roundtrip.epub") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("download did not send nosniff")
	}
}

// TestDownloadServesRangesAndConditionalRequests covers what an ebook
// reader actually does: resume an interrupted transfer, and skip a
// re-download it already has.
func TestDownloadServesRangesAndConditionalRequests(t *testing.T) {
	f := newUploadFixture(t)
	body := []byte("0123456789abcdefghij")
	bookID, digest := f.publish(t, "ranges", body)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	path := f.ts.URL + "/v1/books/" + bookID + "/download"

	req, _ := http.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+read)
	req.Header.Set("Range", "bytes=5-9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("range request: %d", resp.StatusCode)
	}
	if string(raw) != "56789" {
		t.Fatalf("range body = %q", raw)
	}

	req, _ = http.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+read)
	req.Header.Set("If-None-Match", `"`+digest+`"`)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional request: %d, want 304", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodHead, path, nil)
	req.Header.Set("Authorization", "Bearer "+read)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD: %d", resp.StatusCode)
	}
	if len(raw) != 0 {
		t.Fatalf("HEAD returned a body of %d bytes", len(raw))
	}
	if got := resp.Header.Get("Content-Length"); got != "20" {
		t.Fatalf("HEAD Content-Length = %q, want 20", got)
	}
}

// TestCatalogIsScopedToTheCallersLibraries is the tenant boundary. A
// stranger holding a perfectly valid library-read token must not see, read
// or download anything in a library they were never granted.
func TestCatalogIsScopedToTheCallersLibraries(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "private", []byte("secret book"))
	stranger := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)

	code, libs := getJSON(t, f.ts.URL+"/v1/libraries", stranger)
	if code != http.StatusOK {
		t.Fatalf("libraries: %d", code)
	}
	if entries := libs["libraries"].([]any); len(entries) != 0 {
		t.Fatalf("stranger sees %v", entries)
	}
	for _, path := range []string{
		"/v1/libraries/" + f.library + "/books",
		"/v1/books/" + bookID,
		"/v1/books/" + bookID + "/download",
	} {
		t.Run(path, func(t *testing.T) {
			resp, raw := f.get(t, path, stranger)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("code = %d, want 404: %s", resp.StatusCode, raw)
			}
		})
	}

	// And a grant is what changes that, so the 404 above is really the ACL
	// and not something incidental.
	if err := f.st.GrantLibraryAccess(t.Context(), f.user.ID, f.library,
		f.other.ID, store.LibraryRoleRead, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// Read is genuinely enough: every catalog route must work for someone
	// holding the read role, not merely for the owner.
	code, page := getJSON(t, f.ts.URL+"/v1/libraries/"+f.library+"/books", stranger)
	if code != http.StatusOK {
		t.Fatalf("listing after read grant: %d %v", code, page)
	}
	if books := page["books"].([]any); len(books) != 1 {
		t.Fatalf("read-only user sees %v", books)
	}
	code, detail := getJSON(t, f.ts.URL+"/v1/books/"+bookID, stranger)
	if code != http.StatusOK {
		t.Fatalf("detail after read grant: %d %v", code, detail)
	}
	// The detail view must list the file too, or a read-only user has no
	// way to know there is anything to download.
	if files, _ := detail["files"].([]any); len(files) != 1 {
		t.Fatalf("read-only user sees files %v", detail["files"])
	}
	resp, raw := f.get(t, "/v1/books/"+bookID+"/download", stranger)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("after grant: %d %s", resp.StatusCode, raw)
	}
	if string(raw) != "secret book" {
		t.Fatalf("body = %q", raw)
	}
}

// TestDownloadReportsContentThatIsNoLongerStored separates "no such book"
// from "the bytes are gone". A client that gets 404 retries elsewhere; one
// that gets 410 knows the catalog entry is real and the file is not.
func TestDownloadReportsContentThatIsNoLongerStored(t *testing.T) {
	f := newUploadFixture(t)
	body := []byte("about to vanish")
	bookID, digest := f.publish(t, "vanishing", body)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	removed, err := f.cas.RemoveBlob(t.Context(), digest, int64(len(body)))
	if err != nil || !removed {
		t.Fatalf("remove blob: %v %v", removed, err)
	}

	// The catalog entry still exists...
	code, detail := getJSON(t, f.ts.URL+"/v1/books/"+bookID, read)
	if code != http.StatusOK {
		t.Fatalf("detail: %d %v", code, detail)
	}
	// ...but its content does not.
	resp, raw := f.get(t, "/v1/books/"+bookID+"/download", read)
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("code = %d, want 410: %s", resp.StatusCode, raw)
	}
}

// TestCatalogRequiresTheLibraryReadScope keeps the capability check
// independent of the ACL: owning the library is not enough if the token
// cannot read catalogs.
func TestCatalogRequiresTheLibraryReadScope(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "scoped", []byte("scoped book"))
	syncOnly := f.mintToken(t, f.user.ID, store.ScopeSync)

	for _, path := range []string{
		"/v1/libraries",
		"/v1/libraries/" + f.library + "/books",
		"/v1/books/" + bookID,
		"/v1/books/" + bookID + "/download",
	} {
		t.Run(path, func(t *testing.T) {
			resp, _ := f.get(t, path, syncOnly)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("code = %d, want 403", resp.StatusCode)
			}
			resp, _ = f.get(t, path, "")
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("unauthenticated code = %d, want 401", resp.StatusCode)
			}
		})
	}
}

// TestCatalogPagesWithoutGapsOrRepeats walks the whole catalog one book at
// a time. Books published in a tight loop share a creation instant, which
// is exactly when a cursor on time alone loses or repeats rows.
func TestCatalogPagesWithoutGapsOrRepeats(t *testing.T) {
	f := newUploadFixture(t)
	const total = 7
	want := map[string]bool{}
	for i := range total {
		id, _ := f.publish(t, fmt.Sprintf("page-%d", i),
			[]byte(fmt.Sprintf("book number %d", i)))
		want[id] = true
	}
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	seen := map[string]int{}
	pages := 0
	lastPageHadCursor := false
	url := "/v1/libraries/" + f.library + "/books?limit=2"
	for range total + 3 {
		resp, raw := f.get(t, url, read)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("page: %d %s", resp.StatusCode, raw)
		}
		var page map[string]any
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		got := page["books"].([]any)
		if len(got) == 0 {
			t.Fatal("a next_cursor led to an empty page, so the server " +
				"advertised more results than it had")
		}
		for _, b := range got {
			seen[b.(map[string]any)["book_id"].(string)]++
		}
		pages++
		next, ok := page["next_cursor"].(string)
		lastPageHadCursor = ok
		if !ok {
			break
		}
		url = "/v1/libraries/" + f.library + "/books?limit=2&cursor=" + next
	}

	if pages == 0 || lastPageHadCursor {
		t.Fatal("the final short page still advertised a cursor, so a " +
			"client following cursors would never stop")
	}
	if len(seen) != total {
		t.Fatalf("saw %d distinct books, published %d", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("book %s appeared %d times", id, n)
		}
		if !want[id] {
			t.Fatalf("unexpected book %s", id)
		}
	}
}

func TestCatalogRejectsMalformedPagingWithout5xx(t *testing.T) {
	f := newUploadFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	base := "/v1/libraries/" + f.library + "/books"

	for _, tc := range []struct{ name, query string }{
		{"zero limit", "?limit=0"},
		{"negative limit", "?limit=-5"},
		{"limit over the cap", "?limit=100000"},
		{"limit is not a number", "?limit=lots"},
		{"cursor is not base64", "?cursor=!!!!"},
		{"cursor is not json", "?cursor=aGVsbG8"},
		{"cursor has no id", "?cursor=" + encodeCatalogCursor(store.CatalogBookCursor{CreatedAt: time.Now()})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, raw := f.get(t, base+tc.query, read)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400: %s", resp.StatusCode, raw)
			}
		})
	}
}

// TestDownloadFilenameCannotEscapeOrForgeHeaders covers a filename that
// arrived in a multipart header and is therefore attacker-controlled. It
// must never act as a path, and never break out of the header it sits in.
func TestDownloadFilenameCannotEscapeOrForgeHeaders(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"book.epub", "book.epub"},
		{"../../../etc/passwd", "passwd"},
		{`..\..\windows\system32`, "system32"},
		{`evil".epub`, "evil_.epub"},
		{"line\r\nX-Injected: yes", "lineX-Injected: yes"},
		{"", ""},
		{"..", ""},
		{"   ", ""},
		{strings.Repeat("a", 400), strings.Repeat("a", 200)},
	} {
		if got := sanitizeFilename(tc.raw); got != tc.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	// Whatever survives sanitizing must still produce a single header line.
	for _, raw := range []string{
		"book.epub", "../../etc/passwd", "quote\".epub", "héllo.epub",
		"line\r\nX-Injected: yes", "semi;colon.epub",
	} {
		header := contentDisposition(downloadFilename(
			store.BookFile{OriginalFilename: raw}))
		if strings.ContainsAny(header, "\r\n") {
			t.Errorf("header for %q contains a newline: %q", raw, header)
		}
		if strings.Count(header, `"`) != 2 {
			t.Errorf("header for %q has unbalanced quotes: %q", raw, header)
		}
	}
}

// TestDownloadRefusesAMisleadingMediaType stops a stored media type from
// making a browser run the download as something else.
func TestDownloadRefusesAMisleadingMediaType(t *testing.T) {
	for _, tc := range []struct{ stored, want string }{
		{"application/epub+zip", "application/epub+zip"},
		{"application/octet-stream", "application/octet-stream"},
		{"", "application/epub+zip"},
		{"text/html", "application/octet-stream"},
		{"application/javascript", "application/octet-stream"},
		{"image/svg+xml", "application/octet-stream"},
		{"not a media type", "application/epub+zip"},
	} {
		if got := downloadMediaType(tc.stored); got != tc.want {
			t.Errorf("downloadMediaType(%q) = %q, want %q", tc.stored, got, tc.want)
		}
	}
}

// TestDownloadRejectsABadRangeInJSON: a malformed or unsatisfiable Range
// is client input, and this package's contract is that client input never
// produces a body outside the JSON error shape. http.ServeContent would
// answer with plain text and leave the attachment headers on, so the
// wrapper that corrects it needs pinning.
func TestDownloadRejectsABadRangeInJSON(t *testing.T) {
	f := newUploadFixture(t)
	body := bytes.Repeat([]byte("x"), 64)
	bookID, _ := f.publish(t, "badrange", body)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	for _, rng := range []string{"bytes=abc", "chunks=0-1", "bytes=999-1200", "bytes=-"} {
		req, _ := http.NewRequest(http.MethodGet,
			f.ts.URL+"/v1/books/"+bookID+"/download", nil)
		req.Header.Set("Authorization", "Bearer "+read)
		req.Header.Set("Range", rng)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Fatalf("range %q: status %d", rng, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("range %q: content-type %q body %s", rng, ct, raw)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("range %q: body is not JSON: %s", rng, raw)
		}
		if out["error"] == nil || out["error"] == "" {
			t.Fatalf("range %q: no error message: %s", rng, raw)
		}
		if got := resp.Header.Get("Content-Disposition"); got != "" {
			t.Fatalf("range %q: failed request still an attachment: %q", rng, got)
		}
		if bytes.Contains(raw, body[:8]) {
			t.Fatalf("range %q: content leaked into the error", rng)
		}
	}
}

// TestDownloadStillServesGoodRanges guards the wrapper from over-reaching:
// resuming a partial download is the normal case and must keep working.
func TestDownloadStillServesGoodRanges(t *testing.T) {
	f := newUploadFixture(t)
	body := []byte("0123456789abcdefghij")
	bookID, _ := f.publish(t, "goodrange", body)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	req, _ := http.NewRequest(http.MethodGet,
		f.ts.URL+"/v1/books/"+bookID+"/download", nil)
	req.Header.Set("Authorization", "Bearer "+read)
	req.Header.Set("Range", "bytes=4-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 (%s)", resp.StatusCode, raw)
	}
	if string(raw) != "456789"[:5] {
		t.Fatalf("range body = %q", raw)
	}
	if got := resp.Header.Get("Content-Disposition"); got == "" {
		t.Fatal("206 lost its attachment header")
	}
}

// TestReconciliationHidesABookWhoseBytesAreGone is the end of the chain
// the store and content passes start: losing a blob must take the book
// out of the catalog a reader browses, not merely fail at download time.
func TestReconciliationHidesABookWhoseBytesAreGone(t *testing.T) {
	f := newUploadFixture(t)
	body := []byte("about to vanish under reconciliation")
	bookID, digest := f.publish(t, "vanishing", body)
	keptID, _ := f.publish(t, "surviving", []byte("still here"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	removed, err := f.cas.RemoveBlob(t.Context(), digest, int64(len(body)))
	if err != nil || !removed {
		t.Fatalf("remove blob: %v %v", removed, err)
	}

	// Before reconciliation the catalog still advertises a file it can no
	// longer serve.
	code, detail := getJSON(t, f.ts.URL+"/v1/books/"+bookID, read)
	if code != http.StatusOK {
		t.Fatalf("detail before: %d %v", code, detail)
	}
	if files, _ := detail["files"].([]any); len(files) != 1 {
		t.Fatalf("expected a stale file entry before reconciliation: %v", detail)
	}

	now := time.Now().UTC()
	if _, err := content.ReconcileBlobInventory(
		t.Context(), f.st, f.cas, now, 100); err != nil {
		t.Fatal(err)
	}
	report, err := content.ReconcileCatalogAvailability(
		t.Context(), f.st, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesMarkedMissing != 1 || report.BooksMarkedMissing != 1 {
		t.Fatalf("reconciliation report: %+v", report)
	}

	code, detail = getJSON(t, f.ts.URL+"/v1/books/"+bookID, read)
	if code != http.StatusOK {
		t.Fatalf("detail after: %d %v", code, detail)
	}
	if files, _ := detail["files"].([]any); len(files) != 0 {
		t.Fatalf("catalog still offers a file it cannot serve: %v", detail)
	}
	if detail["status"] != string(store.BookMissing) {
		t.Fatalf("status after reconciliation: %v", detail["status"])
	}

	resp, raw := f.get(t, "/v1/books/"+bookID+"/download", read)
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("download = %d, want 410: %s", resp.StatusCode, raw)
	}

	// The book that kept its bytes must be untouched.
	code, kept := getJSON(t, f.ts.URL+"/v1/books/"+keptID, read)
	if code != http.StatusOK {
		t.Fatalf("kept detail: %d %v", code, kept)
	}
	if files, _ := kept["files"].([]any); len(files) != 1 {
		t.Fatalf("reconciliation hid a book that was fine: %v", kept)
	}
	if kept["status"] != string(store.BookActive) {
		t.Fatalf("kept status: %v", kept["status"])
	}
	resp, raw = f.get(t, "/v1/books/"+keptID+"/download", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("kept download = %d: %s", resp.StatusCode, raw)
	}
}

func (f *uploadFixture) req(
	t *testing.T, method, path, token string,
) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, f.ts.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

// TestDeleteTakesABookOutOfTheCatalogAndRestorePutsItBack is the deletion
// path as a caller sees it: gone from the catalog and undownloadable
// immediately, and recoverable until retention closes.
func TestDeleteTakesABookOutOfTheCatalogAndRestorePutsItBack(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "doomed", []byte("doomed book"))
	manage := f.mintToken(t, f.user.ID, store.ScopeLibraryManage)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	if resp, raw := f.req(t, http.MethodDelete, "/v1/books/"+bookID, manage); resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := f.get(t, "/v1/books/"+bookID, read); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted book still readable: %d", resp.StatusCode)
	}
	if resp, _ := f.get(t, "/v1/books/"+bookID+"/download", read); resp.StatusCode == http.StatusOK {
		t.Fatal("deleted book is still downloadable")
	}
	// Deleting twice would silently extend the retention window.
	if resp, _ := f.req(t, http.MethodDelete, "/v1/books/"+bookID, manage); resp.StatusCode != http.StatusConflict {
		t.Fatalf("double delete: %d", resp.StatusCode)
	}

	if resp, raw := f.req(
		t, http.MethodPost, "/v1/books/"+bookID+"/restore", manage,
	); resp.StatusCode != http.StatusOK {
		t.Fatalf("restore: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := f.get(t, "/v1/books/"+bookID+"/download", read); resp.StatusCode != http.StatusOK {
		t.Fatalf("restored book is not downloadable: %d", resp.StatusCode)
	}
	// Restoring what is not in the trash is not an undo.
	if resp, _ := f.req(
		t, http.MethodPost, "/v1/books/"+bookID+"/restore", manage,
	); resp.StatusCode != http.StatusConflict {
		t.Fatalf("restore of a live book: %d", resp.StatusCode)
	}
}

// TestDeleteRequiresTheManageScope: reading a library must never be
// enough to destroy what is in it, and an anonymous caller must not learn
// whether the book exists.
func TestDeleteRequiresTheManageScope(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "protected", []byte("protected book"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodDelete, "/v1/books/" + bookID},
		{http.MethodPost, "/v1/books/" + bookID + "/restore"},
	} {
		if resp, _ := f.req(t, tc.method, tc.path, read); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s with read scope: %d", tc.method, resp.StatusCode)
		}
		if resp, _ := f.req(t, tc.method, tc.path, ""); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated: %d", tc.method, resp.StatusCode)
		}
	}
	// The book is untouched by all that.
	if resp, _ := f.get(t, "/v1/books/"+bookID, read); resp.StatusCode != http.StatusOK {
		t.Fatalf("book damaged by refused deletes: %d", resp.StatusCode)
	}
}
