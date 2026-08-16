//go:build linux

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestCatalogRoundTripsAPublishedBook is the MVP in one test: a book a
// reconcile pass found under a folder's root can be found through the
// folder listing and downloaded byte-for-byte.
func TestCatalogRoundTripsAPublishedBook(t *testing.T) {
	f := newFolderFixture(t)
	body := bytes.Repeat([]byte("epub-bytes"), 100)
	bookID, digest := f.publish(t, "roundtrip", body)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	code, folders := getJSON(t, f.ts.URL+"/v1/folders", read)
	if code != http.StatusOK {
		t.Fatalf("folders: %d %v", code, folders)
	}
	entries := folders["folders"].([]any)
	if len(entries) != 1 {
		t.Fatalf("folders = %v", entries)
	}
	if got := entries[0].(map[string]any)["folder_id"]; got != f.folder.ID {
		t.Fatalf("folder_id = %v", got)
	}

	code, page := getJSON(t, f.ts.URL+"/v1/folders/"+f.folder.ID+"/books", read)
	if code != http.StatusOK {
		t.Fatalf("books: %d %v", code, page)
	}
	books := page["books"].([]any)
	if len(books) != 1 || books[0].(map[string]any)["book_id"] != bookID {
		t.Fatalf("books = %v", books)
	}
	if got := books[0].(map[string]any)["title"]; got != "roundtrip" {
		t.Fatalf("title = %v", got)
	}

	code, detail := getJSON(t, f.ts.URL+"/v1/books/"+bookID, read)
	if code != http.StatusOK {
		t.Fatalf("detail: %d %v", code, detail)
	}
	if detail["sha256"] != digest {
		t.Fatalf("sha256 = %v, want %s", detail["sha256"], digest)
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
	f := newFolderFixture(t)
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
	req.Header.Set("If-None-Match", `W/"`+digest+`"`)
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

// TestTheCatalogIsSharedAcrossEveryReader is ADR-0017's central claim
// checked over HTTP: every signed-in account sees the same folder's
// books, with no grant involved, while a reading position stays private
// to whoever set it.
func TestTheCatalogIsSharedAcrossEveryReader(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "shared", []byte("a shared book"))
	mine := f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)
	theirs := f.mintToken(t, f.other.ID, store.ScopeLibraryRead, store.ScopeSync)

	for _, tok := range []string{mine, theirs} {
		code, page := getJSON(t, f.ts.URL+"/v1/folders/"+f.folder.ID+"/books", tok)
		if code != http.StatusOK {
			t.Fatalf("books: %d %v", code, page)
		}
		books := page["books"].([]any)
		if len(books) != 1 || books[0].(map[string]any)["book_id"] != bookID {
			t.Fatalf("books = %v", books)
		}
		resp, _ := f.get(t, "/v1/books/"+bookID+"/download", tok)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("download: %d", resp.StatusCode)
		}
	}

	// Both readers can join the same catalog book to a work of their
	// own, and the two mappings must not collide.
	respA, a := f.resolveBook(t, bookID, mine)
	respB, b := f.resolveBook(t, bookID, theirs)
	if respA.StatusCode != http.StatusCreated || respB.StatusCode != http.StatusCreated {
		t.Fatalf("resolves: %d %d", respA.StatusCode, respB.StatusCode)
	}
	if a.WorkID == "" || a.WorkID == b.WorkID {
		t.Fatalf("two readers of a shared book got the same work: %q %q",
			a.WorkID, b.WorkID)
	}
	mapping, err := f.st.UserBookWork(t.Context(), f.user.ID, bookID)
	if err != nil || mapping.WorkID != a.WorkID {
		t.Fatalf("user's own mapping: %+v %v", mapping, err)
	}
	mapping, err = f.st.UserBookWork(t.Context(), f.other.ID, bookID)
	if err != nil || mapping.WorkID != b.WorkID {
		t.Fatalf("other's own mapping: %+v %v", mapping, err)
	}
}

// TestRootPathNeverReachesANonAdminResponse is the ADR-0017 obligation
// that a folder's root_path — a path on this machine's filesystem — is
// never handed to a reader. There is no admin-scoped HTTP route to
// compare against here (folder management is admin-CLI-only), so the
// only thing worth asserting is the negative: whatever a library-read
// token can reach, root_path is not in it. A substring search over the
// raw bytes is deliberate rather than decoding each shape and checking
// named fields, so a field added later that happens to carry the path
// cannot slip past this test unnoticed.
func TestRootPathNeverReachesANonAdminResponse(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishWithAuthor(t, "leaky", "Leaky Book", "Some Author", []byte("body"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	needle := []byte(f.folder.RootPath)

	assertClean := func(label string, raw []byte) {
		t.Helper()
		if bytes.Contains(raw, needle) {
			t.Fatalf("%s leaked the folder's root_path:\n%s", label, raw)
		}
	}

	_, raw := f.get(t, "/v1/folders", read)
	assertClean("folder list", raw)

	_, raw = f.get(t, "/v1/folders/"+f.folder.ID+"/books", read)
	assertClean("folder books", raw)

	_, raw = f.get(t, "/v1/books/"+bookID, read)
	assertClean("book detail", raw)

	code, entities := getJSON(t, f.ts.URL+"/v1/entities/contributors", read)
	if code != http.StatusOK {
		t.Fatalf("entities: %d %v", code, entities)
	}
	list := entities["entities"].([]any)
	if len(list) != 1 {
		t.Fatalf("contributors = %v", list)
	}
	entityID := list[0].(map[string]any)["id"].(string)
	_, raw = f.get(t, "/v1/entities/contributors/"+entityID+"/books", read)
	assertClean("entity books", raw)

	_, raw = f.opds(t, "/opds/v1.2", "token", read)
	assertClean("OPDS root feed", raw)

	_, raw = f.opds(t, "/opds/v1.2/folders/"+f.folder.ID, "token", read)
	assertClean("OPDS folder feed", raw)

	_, raw = f.opds(t, "/opds/v1.2/folders/"+f.folder.ID+"/contributors", "token", read)
	assertClean("OPDS entity-kind feed", raw)

	_, raw = f.opds(t, "/opds/v1.2/folders/"+f.folder.ID+"/contributors/"+entityID, "token", read)
	assertClean("OPDS entity-books feed", raw)
}

// TestCatalogRequiresTheLibraryReadScope keeps the capability check
// independent of anything else: a token without library-read cannot
// reach the catalog no matter whose folder it is.
func TestCatalogRequiresTheLibraryReadScope(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "scoped", []byte("scoped book"))
	syncOnly := f.mintToken(t, f.user.ID, store.ScopeSync)

	for _, path := range []string{
		"/v1/folders",
		"/v1/folders/" + f.folder.ID + "/books",
		"/v1/books/" + bookID,
		"/v1/books/" + bookID + "/download",
		"/v1/books/" + bookID + "/cover",
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

// TestCatalogPagesWithoutGapsOrRepeats walks the whole catalog one book
// at a time. Books published in a tight loop share a creation instant,
// which is exactly when a cursor on time alone loses or repeats rows.
func TestCatalogPagesWithoutGapsOrRepeats(t *testing.T) {
	f := newFolderFixture(t)
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
	url := "/v1/folders/" + f.folder.ID + "/books?limit=2"
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
		url = "/v1/folders/" + f.folder.ID + "/books?limit=2&cursor=" + next
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
	f := newFolderFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	base := "/v1/folders/" + f.folder.ID + "/books"

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
// arrived in an EPUB's own path on disk and is therefore whoever curates
// the folder's choice, not this server's. It must never act as a path,
// and never break out of the header it sits in.
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
			store.CatalogBook{OriginalFilename: raw}))
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
	f := newFolderFixture(t)
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
	f := newFolderFixture(t)
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

// TestAMissingBookIsListedButItsDownloadFailsCleanly is the third
// obligation of the redesign: a file a pass could not find is not
// deleted from the catalog (ADR-0017 draws that line at the disk, not at
// the server), so it must still be listed — but its download has to
// report the truth rather than serve stale bytes or panic.
func TestAMissingBookIsListedButItsDownloadFailsCleanly(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "vanishing", []byte("about to be removed"))
	// A folder emptied down to nothing is a zero-observation pass, and
	// rule 2 of ADR-0017 forbids marking anything missing from one — an
	// unmounted disk is usually still readable and empty. A surviving
	// book is what makes this pass see something and therefore be
	// allowed an opinion about the file that is gone.
	f.publish(t, "surviving", []byte("still here"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	if err := os.Remove(filepath.Join(f.root, "vanishing.epub")); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)

	code, detail := getJSON(t, f.ts.URL+"/v1/books/"+bookID, read)
	if code != http.StatusOK {
		t.Fatalf("a missing book must still be listed: %d %v", code, detail)
	}
	if detail["status"] != string(store.BookMissing) {
		t.Fatalf("status = %v, want missing", detail["status"])
	}

	resp, raw := f.get(t, "/v1/books/"+bookID+"/download", read)
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("download of a missing book = %d, want 410: %s", resp.StatusCode, raw)
	}
	resp, raw = f.get(t, "/v1/books/"+bookID+"/cover", read)
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("cover of a missing book = %d, want 410: %s", resp.StatusCode, raw)
	}
}
