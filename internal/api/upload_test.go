//go:build linux

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// The upload route is ADR-0023, and what these tests are mostly about is
// what it refuses. A folder nobody marked, a token without the scope, a
// file that is not an EPUB and a body over the bound each have to be
// turned away without leaving anything behind — because the one thing
// this route has that nothing else in the server has is permission to
// create a file under somebody's library.

func (f *folderFixture) allowUploads(t *testing.T) {
	t.Helper()
	if err := f.st.SetFolderUploads(
		t.Context(), f.folder.ID, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	f.folder.AcceptsUploads = true
}

// upload posts one publication the way a client does.
func (f *folderFixture) upload(
	t *testing.T, token, filename string, body []byte,
) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost,
		f.ts.URL+"/v1/folders/"+f.folder.ID+"/books", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
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
	return resp, decodeJSON(t, raw)
}

func decodeJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	out := map[string]any{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding %q: %v", raw, err)
	}
	return out
}

func TestUploadCataloguesTheBook(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)

	body := makeEPUB(t, "Ancillary Justice", "Ann Leckie", []byte("one"))
	resp, got := f.upload(t, token, "whatever.epub", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%v)", resp.StatusCode, got)
	}
	if got["book_id"] == "" || got["book_id"] == nil {
		t.Fatalf("no book id in %v", got)
	}
	sum := sha256.Sum256(body)
	if got["content_sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest = %v, want the uploaded bytes' digest", got["content_sha256"])
	}
	if got["duplicate"] != false {
		t.Fatalf("duplicate = %v, want false", got["duplicate"])
	}

	// The filename comes from the publication, not from the upload: the
	// catalog knows what the book calls itself and a client should not
	// have to.
	if got["relative_path"] != "Ann Leckie - Ancillary Justice.epub" {
		t.Fatalf("relative_path = %v", got["relative_path"])
	}
	if _, err := os.Stat(filepath.Join(
		f.root, "Ann Leckie - Ancillary Justice.epub")); err != nil {
		t.Fatalf("the book is not on the disk: %v", err)
	}

	book, err := f.st.CatalogBookByID(t.Context(), got["book_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Ancillary Justice" {
		t.Fatalf("title = %q, want the one in the publication", book.Title)
	}
}

// The bytes are the idempotency key, which is the whole reason this
// route needs neither a job table nor a client-supplied key.
func TestUploadTwiceMakesOneBook(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)
	body := makeEPUB(t, "Ancillary Sword", "Ann Leckie", []byte("two"))

	first, got := f.upload(t, token, "a.epub", body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d (%v)", first.StatusCode, got)
	}
	second, again := f.upload(t, token, "a.epub", body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d, want 200 (%v)", second.StatusCode, again)
	}
	if again["duplicate"] != true {
		t.Fatalf("duplicate = %v, want true", again["duplicate"])
	}
	if again["book_id"] != got["book_id"] {
		t.Fatalf("book id changed: %v then %v", got["book_id"], again["book_id"])
	}
	books, err := f.st.BooksInFolder(t.Context(), f.folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 {
		t.Fatalf("folder holds %d books, want 1", len(books))
	}
}

// Two different books whose metadata produces the same filename must
// both survive, and neither may overwrite the other.
func TestUploadSuffixesACollidingName(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)

	_, first := f.upload(t, token, "a.epub",
		makeEPUB(t, "Provenance", "Ann Leckie", []byte("first")))
	resp, second := f.upload(t, token, "b.epub",
		makeEPUB(t, "Provenance", "Ann Leckie", []byte("second")))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d (%v)", resp.StatusCode, second)
	}
	if first["relative_path"] != "Ann Leckie - Provenance.epub" {
		t.Fatalf("first path = %v", first["relative_path"])
	}
	if second["relative_path"] != "Ann Leckie - Provenance (2).epub" {
		t.Fatalf("second path = %v", second["relative_path"])
	}
	if first["book_id"] == second["book_id"] {
		t.Fatal("the second upload replaced the first")
	}
}

func TestUploadRefusedWhereTheFolderDidNotAsk(t *testing.T) {
	f := newFolderFixture(t)
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)

	resp, _ := f.upload(t, token, "a.epub",
		makeEPUB(t, "Nope", "Nobody", []byte("x")))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	assertRootIsEmpty(t, f.root)
}

func TestUploadRefusedWithoutTheScope(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	// library-manage is deliberately not enough: ADR-0018 gave it series
	// claims and nothing else.
	token := f.mintToken(t, f.user.ID,
		store.ScopeLibraryRead, store.ScopeLibraryManage)

	resp, _ := f.upload(t, token, "a.epub",
		makeEPUB(t, "Nope", "Nobody", []byte("x")))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	assertRootIsEmpty(t, f.root)
}

func TestUploadRefusesWhatIsNotAnEPUB(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)

	resp, got := f.upload(t, token, "a.epub", []byte("this is not a zip"))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%v)", resp.StatusCode, got)
	}
	assertRootIsEmpty(t, f.root)
	assertNoSpool(t, f.srv.Cfg.Content.CacheDir)
}

func TestUploadRefusesAnEmptyBody(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)

	resp, _ := f.upload(t, token, "a.epub", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertRootIsEmpty(t, f.root)
	assertNoSpool(t, f.srv.Cfg.Content.CacheDir)
}

func TestUploadRefusesABodyOverTheBound(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	f.srv.Cfg.Content.MaxUploadBytes = 64
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)

	resp, _ := f.upload(t, token, "a.epub",
		makeEPUB(t, "Too Big", "Somebody", bytes.Repeat([]byte("x"), 4096)))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	assertRootIsEmpty(t, f.root)
	assertNoSpool(t, f.srv.Cfg.Content.CacheDir)
}

// A Calibre library is read from metadata.db, so a file dropped into its
// tree is litter rather than a book. Phase 3 of ADR-0023 writes Calibre
// properly; until then the honest answer is that this is not supported.
func TestUploadIntoACalibreFolderIsRefused(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	if _, err := f.st.FolderByID(t.Context(), f.folder.ID); err != nil {
		t.Fatal(err)
	}
	calibre := store.Folder{
		ID: store.NewID(), Name: "Calibre", RootPath: t.TempDir(),
		Kind: store.FolderCalibre, AcceptsUploads: true,
	}
	if err := f.st.CreateFolder(t.Context(), calibre); err != nil {
		t.Fatal(err)
	}
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)
	saved := f.folder
	f.folder = calibre
	resp, _ := f.upload(t, token, "a.epub",
		makeEPUB(t, "Nope", "Nobody", []byte("x")))
	f.folder = saved
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
	assertRootIsEmpty(t, calibre.RootPath)
}

// GET /v1/folders has to say which folders take uploads, or a client
// cannot offer the action only where it applies.
func TestFolderListReportsUploadPermission(t *testing.T) {
	f := newFolderFixture(t)
	_, body := getJSON(t, f.ts.URL+"/v1/folders", f.token)
	folders := body["folders"].([]any)
	if got := folders[0].(map[string]any)["accepts_uploads"]; got != false {
		t.Fatalf("accepts_uploads = %v, want false before anybody asked", got)
	}

	f.allowUploads(t)
	_, body = getJSON(t, f.ts.URL+"/v1/folders", f.token)
	folders = body["folders"].([]any)
	if got := folders[0].(map[string]any)["accepts_uploads"]; got != true {
		t.Fatalf("accepts_uploads = %v, want true", got)
	}
}

func assertRootIsEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the folder root holds %d entries, want none", len(entries))
	}
}

// A refused upload must not leave its spool behind either. The spool
// lives outside every folder root on purpose, but a cache that grows by
// one file per rejected request is still a leak.
func assertNoSpool(t *testing.T, cacheDir string) {
	t.Helper()
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".epub" {
			t.Fatalf("a spooled upload was left behind: %s", e.Name())
		}
	}
}
