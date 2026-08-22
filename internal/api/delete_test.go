//go:build linux

package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// The delete route is ADR-0025, and like the upload route it is mostly
// about what it refuses. It is the only thing in this server that
// destroys a reader's bytes and there is no trash behind it, so a
// folder nobody marked, a token without the scope and a file that is
// not the one the catalog described each have to be turned away with
// the file still there.

func (f *folderFixture) deleteBook(
	t *testing.T, token, bookID, query string,
) *http.Response {
	t.Helper()
	resp, _ := f.req(t, http.MethodDelete, "/v1/books/"+bookID+query, token)
	return resp
}

// pushPosition gives a work some reading, the way a client does.
func (f *folderFixture) pushPosition(
	t *testing.T, token, workID, editionSHA string,
) {
	t.Helper()
	resp, raw := f.postRaw(t, "/v1/ops", token, `{"ops":[{
		"op_id":"018e6f1a-0000-7000-8000-0000000000d1",
		"work_id":"`+workID+`","edition_sha":"`+editionSHA+`",
		"client_ts":"2024-01-01T00:00:00Z","progression":0.4,
		"locator":{"href":"/text/ch1.xhtml"}}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("push a position: %d (%s)", resp.StatusCode, raw)
	}
}

func TestDeleteBookRemovesTheFileAndTheRow(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	bookID, _ := f.publishAs(t, "gone", "Gone", []byte("bytes"))
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryDelete)

	if resp := f.deleteBook(t, token, bookID, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(f.root, "gone.epub")); !os.IsNotExist(err) {
		t.Errorf("the file survived: %v", err)
	}
	if _, err := f.st.CatalogBookByID(t.Context(), "", bookID); err == nil {
		t.Error("the catalog row survived")
	}
}

// The flag that bounds writing bounds unwriting, and it is answered in
// the same words an upload into the same folder would be.
func TestDeleteBookRefusesAFolderThatDoesNotAcceptUploads(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "kept", "Kept", []byte("bytes"))
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryDelete)

	if resp := f.deleteBook(t, token, bookID, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(f.root, "kept.epub")); err != nil {
		t.Errorf("the file was deleted from a folder nobody marked: %v", err)
	}
	if _, err := f.st.CatalogBookByID(t.Context(), "", bookID); err != nil {
		t.Errorf("the row went even though the file stayed: %v", err)
	}
}

// There is no trash behind this. A file replaced since the last pass is
// not the book the reader asked to delete.
func TestDeleteBookRefusesAFileThatChanged(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	bookID, _ := f.publishAs(t, "swapped", "Swapped", []byte("bytes"))
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryDelete)
	if err := os.WriteFile(filepath.Join(f.root, "swapped.epub"),
		[]byte("a different book entirely"), 0o644); err != nil {
		t.Fatal(err)
	}

	if resp := f.deleteBook(t, token, bookID, ""); resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(f.root, "swapped.epub")); err != nil {
		t.Errorf("the replacement was deleted: %v", err)
	}
}

func TestDeleteBookAnswers404ForABookThatIsNotThere(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryDelete)

	if resp := f.deleteBook(t, token, "no-such-book", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDeleteBookAnswers404WithoutAFolderGrant(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	bookID, _ := f.publishAs(t, "private", "Private", []byte("bytes"))
	if err := f.st.UnassignUserFolder(t.Context(), f.user.ID, f.folder.ID); err != nil {
		t.Fatal(err)
	}
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryDelete)

	if resp := f.deleteBook(t, token, bookID, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(f.root, "private.epub")); err != nil {
		t.Fatalf("the inaccessible file was changed: %v", err)
	}
}

// A server with no watcher has no folder to delete from, and says so
// rather than panicking — the same answer the upload route gives.
func TestDeleteBookIsUnavailableWithoutAWatcher(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	bookID, _ := f.publishAs(t, "orphan", "Orphan", []byte("bytes"))
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryDelete)
	f.srv.Removal = nil

	if resp := f.deleteBook(t, token, bookID, ""); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// The reading is a separate question, asked separately (ADR-0024). By
// default the work survives its book, and only the caller's own goes
// when they ask.
func TestDeleteBookLeavesTheReadingUnlessAsked(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  bool
	}{
		{"kept by default", "", true},
		{"forgotten when asked", "?forget_reading=true", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFolderFixture(t)
			f.allowUploads(t)
			bookID, digest := f.publishAs(t, "read", "Read", []byte("bytes"))
			// Resolving is how a reader comes to have a work behind a
			// catalog book at all, so the reading here is made the way
			// a real one is.
			resolveTok := f.mintToken(t, f.user.ID,
				store.ScopeLibraryRead, store.ScopeSync)
			resp, out := f.resolveBook(t, bookID, resolveTok)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("resolve: %d", resp.StatusCode)
			}
			workID := out.WorkID
			// And a position, because a work with no reading behind it
			// is collected as empty whatever this route does — ADR-0024
			// only promises to keep a work that is worth keeping.
			f.pushPosition(t, resolveTok, workID, digest)
			token := f.mintToken(t, f.user.ID, store.ScopeLibraryDelete)

			if resp := f.deleteBook(t, token, bookID, tc.query); resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", resp.StatusCode)
			}
			_, err := f.st.WorkByID(t.Context(), f.user.ID, workID)
			if survived := err == nil; survived != tc.want {
				t.Fatalf("work survived = %v, want %v (err %v)",
					survived, tc.want, err)
			}
		})
	}
}
