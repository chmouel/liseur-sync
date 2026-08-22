//go:build linux

package api

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	_ "modernc.org/sqlite"
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

	book, err := f.st.CatalogBookByID(t.Context(), "", got["book_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Ancillary Justice" {
		t.Fatalf("title = %q, want the one in the publication", book.Title)
	}
}

// TestUploadJoinsTheWorkTheReaderWasAlreadySyncing is the bug this
// exists for. A book added on a device syncs its position long before it
// is ever sent anywhere, so the server ends up holding a work with the
// reader's place in it and no file. Uploading the file used to add a
// catalog entry beside that work and join neither to the other, and the
// library drew the same book twice: one card with the reading, one with
// the file.
func TestUploadJoinsTheWorkTheReaderWasAlreadySyncing(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)

	body := makeEPUB(t, "Ancillary Justice", "Ann Leckie", []byte("one"))
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	// The device, weeks earlier: it has the file, so it names the book
	// on the file's digest and syncs a position under that name.
	sync := f.mintToken(t, f.user.ID, store.ScopeSync)
	code, out := post(t, f.ts.URL+"/v1/works/resolve", sync, map[string]any{
		"identifiers": []map[string]string{{"kind": "sha256", "value": digest}},
		"title":       "Ancillary Justice",
		"author":      "Ann Leckie",
	})
	if code != http.StatusCreated {
		t.Fatalf("resolve: %d %v", code, out)
	}
	workID := out["work_id"].(string)

	upload := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)
	resp, got := f.upload(t, upload, "whatever.epub", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%v)", resp.StatusCode, got)
	}
	bookID := got["book_id"].(string)

	link, err := f.st.UserBookWork(t.Context(), f.user.ID, bookID)
	if err != nil {
		t.Fatalf("the uploaded book was joined to no work at all: %v", err)
	}
	if link.WorkID != workID {
		t.Fatalf("book joined work %q, want %q — the reading it already had",
			link.WorkID, workID)
	}
}

// A retry over a bad connection must settle the work as surely as the
// first attempt, or whether the reading is joined depends on the network.
func TestUploadingTwiceStillJoinsTheWork(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)

	body := makeEPUB(t, "Ancillary Justice", "Ann Leckie", []byte("one"))
	if _, got := f.upload(t, token, "whatever.epub", body); got["book_id"] == nil {
		t.Fatalf("no book id in %v", got)
	}
	resp, got := f.upload(t, token, "whatever.epub", body)
	if resp.StatusCode != http.StatusOK || got["duplicate"] != true {
		t.Fatalf("second upload = %d %v, want 200 and duplicate", resp.StatusCode, got)
	}
	if _, err := f.st.UserBookWork(
		t.Context(), f.user.ID, got["book_id"].(string),
	); err != nil {
		t.Fatalf("a re-sent book was joined to no work: %v", err)
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

func TestUploadDoesNotDeduplicateAgainstAnInaccessibleBook(t *testing.T) {
	f := newFolderFixture(t)
	body := makeEPUB(t, "Private Copy", "A Reader", []byte("same bytes"))
	hiddenBookID, _ := f.writeBook(t, "hidden.epub", body)
	if err := f.st.UnassignUserFolder(t.Context(), f.user.ID, f.folder.ID); err != nil {
		t.Fatal(err)
	}

	targetRoot := t.TempDir()
	target := store.Folder{
		ID: store.NewID(), Name: "Uploads", RootPath: targetRoot,
		Kind: store.FolderPlain,
	}
	if err := f.st.CreateFolder(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AssignUserFolder(t.Context(), f.user.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	f.folder, f.root = target, targetRoot
	f.allowUploads(t)
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)

	resp, got := f.upload(t, token, "copy.epub", body)
	if resp.StatusCode != http.StatusCreated || got["duplicate"] != false {
		t.Fatalf("upload = %d %v, want a new visible copy", resp.StatusCode, got)
	}
	if got["book_id"] == hiddenBookID {
		t.Fatal("upload exposed the inaccessible book through digest deduplication")
	}
	books, err := f.st.BooksInFolder(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 || books[0].ID != got["book_id"] {
		t.Fatalf("target books = %+v, response = %v", books, got)
	}
}

func TestUploadAnswers404WithoutAFolderGrant(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	if err := f.st.UnassignUserFolder(t.Context(), f.user.ID, f.folder.ID); err != nil {
		t.Fatal(err)
	}
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)

	resp, got := f.upload(t, token, "private.epub",
		makeEPUB(t, "Private", "", []byte("bytes")))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("upload = %d %v, want 404", resp.StatusCode, got)
	}
	entries, err := os.ReadDir(f.root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("inaccessible upload changed the folder: %+v, %v", entries, err)
	}
}

// The same book sent twice at once is the case the digest check cannot
// answer on its own: without the lock both requests look, both find
// nothing, and both write.
func TestUploadTwiceAtOnceMakesOneBook(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)
	body := makeEPUB(t, "Ancillary Mercy", "Ann Leckie", []byte("three"))

	var wg sync.WaitGroup
	codes := make([]int, 2)
	ids := make([]any, 2)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, got := f.upload(t, token, "a.epub", body)
			codes[i] = resp.StatusCode
			ids[i] = got["book_id"]
		}()
	}
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusCreated && code != http.StatusOK {
			t.Fatalf("request %d = %d, want 201 or 200", i, code)
		}
	}
	if ids[0] != ids[1] {
		t.Fatalf("two book ids for one book: %v and %v", ids[0], ids[1])
	}
	books, err := f.st.BooksInFolder(t.Context(), f.folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 {
		t.Fatalf("folder holds %d books, want 1", len(books))
	}
	entries, err := os.ReadDir(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("folder holds %d files, want 1", len(entries))
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

// A Calibre library is read from metadata.db, so a file dropped into
// its tree would be litter rather than a book. Phase 3 of ADR-0023
// writes Calibre properly: rows, the layout Calibre names, a cover and
// an OPF. The assertion that matters is that the pass then catalogs it,
// because that is the difference between a book and a file.
func TestUploadIntoACalibreFolderAddsACalibreBook(t *testing.T) {
	f := newFolderFixture(t)
	root := newCalibreLibrary(t)
	library := store.Folder{
		ID: store.NewID(), Name: "Calibre", RootPath: root,
		Kind: store.FolderCalibre, AcceptsUploads: true,
	}
	if err := f.st.CreateFolder(t.Context(), library); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AssignUserFolder(t.Context(), f.user.ID, library.ID); err != nil {
		t.Fatal(err)
	}
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)
	saved := f.folder
	f.folder = library
	resp, body := f.upload(t, token, "whatever.epub",
		makeEPUB(t, "The Dispossessed", "Ursula K. Le Guin", []byte("chapter")))
	f.folder = saved
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.StatusCode, body)
	}
	got := body
	want := "Ursula K. Le Guin/The Dispossessed (1)/" +
		"The Dispossessed - Ursula K. Le Guin.epub"
	if got["relative_path"] != want {
		t.Errorf("relative_path = %v, want %q", got["relative_path"], want)
	}
	if got["book_id"] == nil || got["book_id"] == "" {
		t.Errorf("no book_id, so the pass did not catalog it: %v", got)
	}
	if got["duplicate"] != false {
		t.Errorf("duplicate = %v, want false", got["duplicate"])
	}
	// No cover.jpg: this publication declares no cover art, and
	// inventing one would be editing metadata. Calibre draws a
	// placeholder for a book whose has_cover is 0.
	for _, name := range []string{
		want,
		"Ursula K. Le Guin/The Dispossessed (1)/metadata.opf",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
}

// The digest is the idempotency key everywhere, and a Calibre folder is
// not an exception: a client retrying a transfer must not leave the
// library with the book in it twice.
func TestUploadingTheSameBookIntoCalibreTwiceMakesOneBook(t *testing.T) {
	f := newFolderFixture(t)
	library := store.Folder{
		ID: store.NewID(), Name: "Calibre", RootPath: newCalibreLibrary(t),
		Kind: store.FolderCalibre, AcceptsUploads: true,
	}
	if err := f.st.CreateFolder(t.Context(), library); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AssignUserFolder(t.Context(), f.user.ID, library.ID); err != nil {
		t.Fatal(err)
	}
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)
	f.folder = library
	payload := makeEPUB(t, "Once", "Only", []byte("chapter"))
	first, firstBody := f.upload(t, token, "a.epub", payload)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d: %s", first.StatusCode, firstBody)
	}
	second, secondBody := f.upload(t, token, "a.epub", payload)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d, want 200: %s", second.StatusCode, secondBody)
	}
	one, two := firstBody, secondBody
	if two["duplicate"] != true || two["book_id"] != one["book_id"] {
		t.Fatalf("second upload = %v, want the first book back", two)
	}
}

// newCalibreLibrary builds an empty library from a real Calibre
// schema — triggers included, which is where writing to Calibre goes
// wrong.
func newCalibreLibrary(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	schema, err := os.ReadFile(filepath.Join(
		"..", "calibre", "testdata", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("apply Calibre schema: %v", err)
	}
	return root
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
