//go:build linux

package api

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// publishInPlace registers a library whose bytes stay where they are,
// writes one publication under its root, and catalogues it the way the
// sweep does. Nothing is copied anywhere, which is the point.
func (f *uploadFixture) publishInPlace(
	t *testing.T, name string, body []byte,
) (string, string, string) {
	t.Helper()
	shelf := filepath.Join(f.root, "shelf-"+name)
	if err := os.MkdirAll(shelf, 0o755); err != nil {
		t.Fatal(err)
	}
	relative := name + ".epub"
	full := filepath.Join(shelf, relative)
	if err := os.WriteFile(full, body, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}

	at := time.Now().UTC()
	libraryID := "lib-in-place-" + name
	if err := f.st.CreateLibrary(t.Context(), store.Library{
		ID: libraryID, OwnerUserID: f.user.ID, QuotaUserID: f.user.ID,
		Source:  store.LibraryDirectory,
		Storage: store.LibraryStorageInPlace,
		Refresh: store.LibraryRefreshInterval, Name: "Shelf " + name,
		RootPath: &shelf, CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	job, created, err := f.st.CreateIngestJob(t.Context(), f.user.ID,
		store.IngestJobRequest{
			ID: "job-in-place-" + name, LibraryID: libraryID,
			Source: store.IngestScanned, SourceRelativePath: &relative,
			RequestFingerprint: "scan-" + name, CreatedAt: at,
		})
	if err != nil || !created {
		t.Fatalf("create in-place job: %v %v", created, err)
	}
	modified := info.ModTime().UTC()
	bookID, fileID := "book-in-place-"+name, "file-in-place-"+name
	if _, err := f.st.CommitInPlaceBook(t.Context(), f.user.ID, job.ID,
		store.CommitInPlaceBookRequest{
			ExpectedRevision: job.Revision,
			Book: store.CatalogBook{
				ID: bookID, LibraryID: libraryID, Status: store.BookActive,
				Title: "In place " + name, TitleSource: store.MetadataEmbedded,
				CreatedAt: at, UpdatedAt: at,
			},
			File: store.BookFile{
				ID: fileID, LibraryID: libraryID, BookID: bookID,
				Storage:            store.LibraryStorageInPlace,
				ContentSHA256:      sha256Hex(body),
				ContentSizeBytes:   int64(len(body)),
				Source:             store.IngestScanned,
				SourceRelativePath: &relative,
				SourceModifiedAt:   &modified,
				OriginalFilename:   relative,
				MediaType:          "application/epub+zip",
				Availability:       store.BookFileAvailable,
				CreatedAt:          at, UpdatedAt: at,
			},
			UpdatedAt: at,
		}); err != nil {
		t.Fatal(err)
	}
	return bookID, libraryID, full
}

// A book the server never copied downloads exactly like one it did — with
// one difference the client can see: nothing here may be cached as
// immutable, because these bytes are not ours to promise.
func TestDownloadServesAnInPlaceBook(t *testing.T) {
	f := newUploadFixture(t)
	body := []byte("the bytes of a book on somebody else's disk")
	bookID, _, _ := f.publishInPlace(t, "served", body)

	resp, raw := f.get(t, "/v1/books/"+bookID+"/download", f.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: %d %s", resp.StatusCode, raw)
	}
	if !bytes.Equal(raw, body) {
		t.Errorf("downloaded %d bytes, the file on disk has %d",
			len(raw), len(body))
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, no-cache" {
		t.Errorf("Cache-Control %q, want private, no-cache", got)
	}
	if got := resp.Header.Get("ETag"); got != `W/"`+sha256Hex(body)+`"` {
		t.Errorf("ETag %q, want a weak validator", got)
	}
}

// The file changed underneath us, so the bytes at that path are not this
// book's bytes. Serving them would hand somebody a different publication
// under a title they chose, so the request is refused and the book is put
// in front of an administrator.
func TestDownloadRefusesAnInPlaceBookThatChangedOnDisk(t *testing.T) {
	f := newUploadFixture(t)
	bookID, libraryID, path := f.publishInPlace(t, "changed", []byte("the original bytes"))

	if err := os.WriteFile(path, []byte("something else entirely"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, raw := f.get(t, "/v1/books/"+bookID+"/download", f.token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("download: %d %s", resp.StatusCode, raw)
	}

	review, err := f.st.ListBooksInReview(t.Context(), f.user.ID, libraryID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(review) != 1 || review[0].ID != bookID {
		t.Fatalf("books in review: %+v", review)
	}
	if review[0].ReviewReason == "" {
		t.Error("a book in review must say why")
	}
}

// The digest a client matches its own copy against must be the digest of
// the bytes, not the address of the server's copy — an in-place library
// has no copy of anything, and its readers are exactly the migrating
// users who arrive holding the files already (ADR-0015).
func TestCatalogPayloadReportsAnInPlaceContentDigest(t *testing.T) {
	f := newUploadFixture(t)
	body := []byte("bytes the server never copied anywhere")
	bookID, libraryID, _ := f.publishInPlace(t, "digest", body)

	code, detail := getJSON(t, f.ts.URL+"/v1/books/"+bookID, f.token)
	if code != http.StatusOK {
		t.Fatalf("detail: %d %v", code, detail)
	}
	files, _ := detail["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files = %v", detail["files"])
	}
	file, _ := files[0].(map[string]any)
	if file["sha256"] != sha256Hex(body) {
		t.Errorf("sha256 = %v, want the content digest %s",
			file["sha256"], sha256Hex(body))
	}
	if file["size_bytes"] != float64(len(body)) {
		t.Errorf("size_bytes = %v, want %d", file["size_bytes"], len(body))
	}

	// The store agrees there is no blob here, so the payload above could
	// only have come from the content digest.
	stored, err := f.st.ListBookFiles(
		t.Context(), f.user.ID, bookID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	if stored[0].BlobSHA256 != "" {
		t.Fatalf("an in-place file claimed a blob: %+v", stored[0])
	}

	// The listing says the same thing, because it is the same shape.
	code, page := getJSON(t, f.ts.URL+"/v1/libraries/"+libraryID+"/books", f.token)
	if code != http.StatusOK {
		t.Fatalf("books: %d %v", code, page)
	}
	books, _ := page["books"].([]any)
	if len(books) != 1 {
		t.Fatalf("books = %v", page["books"])
	}
	if row, _ := books[0].(map[string]any); !reflect.DeepEqual(row, detail) {
		t.Errorf("listing row differs from detail:\n%v\n%v", row, detail)
	}
}
