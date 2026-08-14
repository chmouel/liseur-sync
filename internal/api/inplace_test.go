//go:build linux

package api

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
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
