//go:build linux

// Package webui_test exercises the books UI against the real content
// server rather than against fakes. It lives outside the package because
// the API server imports this one, and a browser upload is only worth
// anything if it reaches the same store and CAS a token upload does.
package webui_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/api"
	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
	"github.com/chmouel/liseur-sync/internal/webui"
)

type booksFixture struct {
	ts      *httptest.Server
	st      store.Store
	cas     *content.CAS
	api     *api.Server
	cookie  *http.Cookie
	library string
}

func newBooksFixture(t *testing.T) *booksFixture {
	t.Helper()
	root := t.TempDir()
	st, err := sqlite.Open(filepath.Join(root, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	cas, err := content.Open(filepath.Join(root, "content"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cas.Close() })

	hash, _ := auth.HashPassword("hunter2hunter")
	for _, u := range []store.User{
		{ID: "u1", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now()},
		{ID: "u2", Name: "bob", Argon2Hash: hash, CreatedAt: time.Now()},
	} {
		if err := st.CreateUser(t.Context(), u); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default()
	cfg.InsecureHTTP = true
	apiSrv := &api.Server{
		St: st, Auth: auth.NewService(st), Cfg: cfg,
		LoginLimiter: auth.NewRateLimiter(100, time.Minute),
		Content:      cas, Blobs: cas,
	}
	ui := &webui.Server{
		St: st, Auth: auth.NewService(st), Cfg: cfg,
		LoginLimiter: auth.NewRateLimiter(100, time.Minute),
		Uploads:      apiSrv, Downloads: apiSrv,
	}
	mux := http.NewServeMux()
	ui.Mount(mux, func(h http.Handler) http.Handler { return h })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	f := &booksFixture{ts: ts, st: st, cas: cas, api: apiSrv, library: "lib-web"}
	if err := st.CreateLibrary(t.Context(), store.Library{
		ID: f.library, OwnerUserID: "u1", QuotaUserID: "u1",
		Kind: store.LibraryManaged, Name: "Alice's Books", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	f.cookie = f.login(t, "alice")
	return f
}

func (f *booksFixture) login(t *testing.T, user string) *http.Cookie {
	t.Helper()
	resp, err := noRedirectClient().PostForm(f.ts.URL+"/ui/login", url.Values{
		"username": {user}, "password": {"hunter2hunter"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if strings.Contains(c.Name, "session") {
			return c
		}
	}
	t.Fatalf("no session cookie for %s", user)
	return nil
}

func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func (f *booksFixture) get(t *testing.T, path string, cookie *http.Cookie) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.ts.URL+path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func csrfFrom(t *testing.T, html string) string {
	t.Helper()
	const marker = `name="csrf" value="`
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatal("no csrf field in page")
	}
	rest := html[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

// uploadForm posts the multipart body the books page's form produces.
// The csrf field comes first on purpose: the handler must be able to
// reject a forged request before it streams the file anywhere.
func (f *booksFixture) uploadForm(
	t *testing.T, cookie *http.Cookie, csrf, library, filename string, body []byte,
) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if csrf != "" {
		mw.WriteField("csrf", csrf)
	}
	if library != "" {
		mw.WriteField("library", library)
	}
	if filename != "" {
		part, err := mw.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		part.Write(body)
	}
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/ui/books/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// promote finishes the ingest the upload started, so the browser test
// can see a real book. The background worker does this in production;
// running it inline keeps the test about the UI.
func (f *booksFixture) promote(t *testing.T, name string) string {
	t.Helper()
	jobs, err := f.st.ListIngestJobs(t.Context(), "u1", f.library, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) == 0 {
		t.Fatal("no ingest job to promote")
	}
	job := jobs[len(jobs)-1]
	blob, err := f.cas.Promote(t.Context(), *job.StagingPath, *job.ContentSHA256, job.BytesReceived)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	job, err = f.st.TransitionIngestJob(t.Context(), "u1", job.ID, store.IngestJobTransition{
		ExpectedState: job.State, ExpectedRevision: job.Revision,
		NextState: store.IngestValidated, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.st.TransitionIngestJob(t.Context(), "u1", job.ID, store.IngestJobTransition{
		ExpectedState: job.State, ExpectedRevision: job.Revision,
		NextState:                     store.IngestExtracted,
		ExtractedEmbeddedMetadataJSON: []byte(`{"title":"x"}`),
		UpdatedAt:                     at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	bookID := "book-" + name
	if _, err := f.st.CommitNewBookPromotion(t.Context(), "u1", job.ID,
		store.CommitNewBookPromotionRequest{
			ExpectedRevision: job.Revision,
			Blob:             store.BlobInfo{SHA256: blob.SHA256, SizeBytes: blob.Size},
			Book: store.CatalogBook{
				ID: bookID, LibraryID: f.library, Status: store.BookActive,
				Title: name, TitleSource: store.MetadataEmbedded,
				CreatedAt: at, UpdatedAt: at,
			},
			File: store.BookFile{
				ID: "file-" + name, LibraryID: f.library, BookID: bookID,
				BlobSHA256: blob.SHA256, Source: store.IngestUpload,
				OriginalFilename: name + ".epub", MediaType: "application/epub+zip",
				Availability: store.BookFileAvailable,
				CreatedAt:    at, UpdatedAt: at,
			},
			UpdatedAt: at.Add(2 * time.Second),
		}); err != nil {
		t.Fatal(err)
	}
	return bookID
}

// TestBooksUIUploadsAndServesABook is the browser half of the MVP: sign
// in, upload a file with the form on the page, then find and download
// the book that results.
func TestBooksUIUploadsAndServesABook(t *testing.T) {
	f := newBooksFixture(t)
	resp, html := f.get(t, "/ui/books", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("books page: %d", resp.StatusCode)
	}
	// The apostrophe arrives escaped, which is the point of rendering
	// through templ rather than concatenating strings.
	if !strings.Contains(html, "Alice&#39;s Books") {
		t.Fatalf("library not offered:\n%s", html)
	}
	csrf := csrfFrom(t, html)

	body := bytes.Repeat([]byte("web-epub"), 50)
	up := f.uploadForm(t, f.cookie, csrf, f.library, "novel.epub", body)
	if up.StatusCode != http.StatusSeeOther {
		t.Fatalf("upload: %d", up.StatusCode)
	}
	if loc := up.Header.Get("Location"); strings.Contains(loc, "problem=") {
		t.Fatalf("upload reported a problem: %s", loc)
	}

	bookID := f.promote(t, "novel")

	_, html = f.get(t, "/ui/books?library="+f.library, f.cookie)
	if !strings.Contains(html, "novel") {
		t.Fatalf("book missing from the list:\n%s", html)
	}
	_, html = f.get(t, "/ui/books/"+bookID, f.cookie)
	if !strings.Contains(html, "novel.epub") {
		t.Fatalf("file missing from the detail page:\n%s", html)
	}

	resp, got := f.get(t, "/ui/books/"+bookID+"/download", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: %d", resp.StatusCode)
	}
	if got != string(body) {
		t.Fatalf("downloaded %d bytes, want %d", len(got), len(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/epub+zip" {
		t.Fatalf("content-type = %q", ct)
	}
}

// TestBooksUIUploadRequiresCSRF: the upload is a mutation, and the form
// carries the token in the multipart body. A missing or wrong token must
// be refused, and refused before any file is stored — otherwise a
// cross-site form could fill someone's quota even if the page never
// renders.
func TestBooksUIUploadRequiresCSRF(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	good := csrfFrom(t, html)

	for _, csrf := range []string{"", "not-the-token", good + "x"} {
		resp := f.uploadForm(t, f.cookie, csrf, f.library, "x.epub", []byte("data"))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("csrf %q: status %d, want 403", csrf, resp.StatusCode)
		}
	}
	jobs, err := f.st.ListIngestJobs(t.Context(), "u1", f.library, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("a forged upload created %d ingest jobs", len(jobs))
	}
}

// TestBooksUIIsScopedToTheSignedInUser: bob has no libraries, cannot see
// alice's books, and cannot download one by guessing its id.
func TestBooksUIIsScopedToTheSignedInUser(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	f.uploadForm(t, f.cookie, csrfFrom(t, html), f.library, "private.epub", []byte("secret"))
	bookID := f.promote(t, "private")

	bob := f.login(t, "bob")
	_, html = f.get(t, "/ui/books", bob)
	if strings.Contains(html, "Alice&#39;s Books") || strings.Contains(html, "private") {
		t.Fatalf("cross-user leak on the books page:\n%s", html)
	}
	if resp, _ := f.get(t, "/ui/books/"+bookID, bob); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("book detail for a stranger: %d", resp.StatusCode)
	}
	if resp, body := f.get(t, "/ui/books/"+bookID+"/download", bob); resp.StatusCode == http.StatusOK {
		t.Fatalf("stranger downloaded the book: %s", body)
	}
	// Naming alice's library in the query string must not reveal it
	// either — the id is guessable in a way the ACL is not.
	if _, html := f.get(t, "/ui/books?library="+f.library, bob); strings.Contains(html, "private") {
		t.Fatalf("library query string bypassed the ACL:\n%s", html)
	}

	resp := f.uploadForm(t, f.cookie, "", f.library, "x.epub", []byte("x"))
	_ = resp
	// A read-only grantee sees the books but is offered no upload form.
	if err := f.st.GrantLibraryAccess(t.Context(), "u1", f.library, "u2",
		store.LibraryRoleRead, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_, html = f.get(t, "/ui/books", bob)
	if !strings.Contains(html, "private") {
		t.Fatalf("grantee cannot see the shared books:\n%s", html)
	}
	if strings.Contains(html, `action="books/upload"`) {
		t.Fatalf("read-only grantee was offered an upload form:\n%s", html)
	}
}

// TestBooksUIRejectsAnEmptyOrLibrarylessUpload: the browser can submit a
// form with nothing chosen, and that must be a message rather than a
// stack trace or a stored empty file.
func TestBooksUIRejectsAnEmptyOrLibrarylessUpload(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	csrf := csrfFrom(t, html)

	for _, tc := range []struct{ library, filename string }{
		{f.library, ""},
		{"", "x.epub"},
		{"lib-that-does-not-exist", "x.epub"},
	} {
		resp := f.uploadForm(t, f.cookie, csrf, tc.library, tc.filename, []byte("x"))
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("library=%q file=%q: status %d", tc.library, tc.filename, resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); !strings.Contains(loc, "problem=") {
			t.Fatalf("library=%q file=%q: no problem reported (%s)",
				tc.library, tc.filename, loc)
		}
	}
}

// TestBooksUIRequiresASession: every books route is behind the session,
// including the download, which would otherwise be an unauthenticated
// read of the whole library.
func TestBooksUIRequiresASession(t *testing.T) {
	f := newBooksFixture(t)
	for _, path := range []string{
		"/ui/books", "/ui/books/anything", "/ui/books/anything/download",
	} {
		resp, _ := f.get(t, path, nil)
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("%s served without a session", path)
		}
	}
	// The upload redirects to the login page like every other route, so
	// the status alone proves nothing: what matters is that no bytes
	// were taken.
	f.uploadForm(t, nil, "x", f.library, "x.epub", []byte("x"))
	jobs, err := f.st.ListIngestJobs(t.Context(), "u1", f.library, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("an unauthenticated upload created %d ingest jobs", len(jobs))
	}
}

// TestBooksUIUploadHonoursTheACLNotJustTheForm: the upload form is hidden
// from a read-only grantee, but hiding it is presentation. Someone who
// posts the form anyway — a stale page, a hand-built request — must still
// be refused, and refused by the library ACL rather than by the UI.
func TestBooksUIUploadHonoursTheACLNotJustTheForm(t *testing.T) {
	f := newBooksFixture(t)
	if err := f.st.GrantLibraryAccess(t.Context(), "u1", f.library, "u2",
		store.LibraryRoleRead, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	bob := f.login(t, "bob")
	_, html := f.get(t, "/ui/books", bob)

	resp := f.uploadForm(t, bob, csrfFrom(t, html), f.library, "sneaky.epub", []byte("data"))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "problem=") {
		t.Fatalf("read-only grantee's upload was accepted: %s", loc)
	}
	jobs, err := f.st.ListIngestJobs(t.Context(), "u1", f.library, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("read-only grantee created %d ingest jobs", len(jobs))
	}
}

// TestBooksUIExplainsAnUploadThatNeverBecameABook is the regression for
// the reported bug: a file was accepted, quarantined by validation, and
// the page then showed nothing at all — indistinguishable from the server
// losing it. The upload must be listed with a reason a person can act on.
func TestBooksUIExplainsAnUploadThatNeverBecameABook(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	csrf := csrfFrom(t, html)
	if up := f.uploadForm(
		t, f.cookie, csrf, f.library, "broken.epub", []byte("not an epub"),
	); up.StatusCode != http.StatusSeeOther {
		t.Fatalf("upload: %d", up.StatusCode)
	}

	// While it is working, the page says so rather than staying silent.
	_, html = f.get(t, "/ui/books?library="+f.library, f.cookie)
	if !strings.Contains(html, "Uploads in progress") {
		t.Fatalf("an upload in flight is invisible:\n%s", html)
	}

	f.quarantine(t, "invalid_epub")

	_, html = f.get(t, "/ui/books?library="+f.library, f.cookie)
	if !strings.Contains(html, "Not a readable EPUB") {
		t.Fatalf("a rejected upload is invisible:\n%s", html)
	}
	// The reason is for the librarian who can do something about it, not
	// for everyone who can read the library.
	_, readerHTML := f.get(t, "/ui/books?library="+f.library, f.readerCookie(t))
	if strings.Contains(readerHTML, "Not a readable EPUB") ||
		strings.Contains(readerHTML, "Uploads in progress") {
		t.Fatalf("a reader was shown ingest internals:\n%s", readerHTML)
	}
}

// quarantine drives the newest job to quarantined, the way the validation
// worker does when a file turns out not to be an EPUB.
func (f *booksFixture) quarantine(t *testing.T, code string) {
	t.Helper()
	jobs, err := f.st.ListIngestActivity(t.Context(), "u1", f.library, 100)
	if err != nil || len(jobs) == 0 {
		t.Fatalf("no job to quarantine: %v %v", jobs, err)
	}
	job := jobs[0]
	at := time.Now().UTC()
	expires := at.Add(72 * time.Hour)
	if _, err := f.st.TransitionIngestJob(t.Context(), "u1", job.ID,
		store.IngestJobTransition{
			ExpectedState: job.State, ExpectedRevision: job.Revision,
			NextState: store.IngestQuarantined, ErrorCode: code,
			ErrorDetail: "validation refused it", ExpiresAt: &expires,
			UpdatedAt: at,
		}); err != nil {
		t.Fatal(err)
	}
}

// readerCookie signs in a user granted read access to the fixture library.
func (f *booksFixture) readerCookie(t *testing.T) *http.Cookie {
	t.Helper()
	if err := f.st.GrantLibraryAccess(t.Context(), "u1", f.library, "u2",
		store.LibraryRoleRead, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return f.login(t, "bob")
}

// postForm posts a urlencoded form the way the page's buttons do.
func (f *booksFixture) postForm(
	t *testing.T, path string, cookie *http.Cookie, form url.Values,
) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+path,
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// TestBooksUIDeletesAndRestoresABook walks the deletion path the way a
// person does: press Delete, see the book gone and its download refused,
// find it under "Recently deleted", press Put back, download it again.
func TestBooksUIDeletesAndRestoresABook(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	csrf := csrfFrom(t, html)
	body := bytes.Repeat([]byte("deletable"), 40)
	f.uploadForm(t, f.cookie, csrf, f.library, "regret.epub", body)
	bookID := f.promote(t, "regret")

	form := url.Values{"csrf": {csrf}, "library": {f.library}}
	if resp := f.postForm(
		t, "/ui/books/"+bookID+"/delete", f.cookie, form,
	); resp.StatusCode != http.StatusSeeOther ||
		strings.Contains(resp.Header.Get("Location"), "problem=") {
		t.Fatalf("delete: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	_, html = f.get(t, "/ui/books?library="+f.library, f.cookie)
	if strings.Contains(html, "books/"+bookID+"/download") {
		t.Fatalf("deleted book still offered for download:\n%s", html)
	}
	if !strings.Contains(html, "Recently deleted") ||
		!strings.Contains(html, "books/"+bookID+"/restore") {
		t.Fatalf("deleted book is not restorable from the page:\n%s", html)
	}
	if resp, _ := f.get(
		t, "/ui/books/"+bookID+"/download", f.cookie,
	); resp.StatusCode == http.StatusOK {
		t.Fatal("deleted book is still downloadable")
	}

	if resp := f.postForm(
		t, "/ui/books/"+bookID+"/restore", f.cookie, form,
	); resp.StatusCode != http.StatusSeeOther ||
		strings.Contains(resp.Header.Get("Location"), "problem=") {
		t.Fatalf("restore: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, got := f.get(t, "/ui/books/"+bookID+"/download", f.cookie)
	if resp.StatusCode != http.StatusOK || got != string(body) {
		t.Fatalf("restored download: %d, %d bytes", resp.StatusCode, len(got))
	}
	_, html = f.get(t, "/ui/books?library="+f.library, f.cookie)
	if strings.Contains(html, "Recently deleted") {
		t.Fatalf("restored book still in the trash:\n%s", html)
	}
}

// TestBooksUIDeleteRequiresCSRFAndAccess: deletion is the most damaging
// button on the page, so a forged form and another user's cookie must
// both fail, and the book must survive both.
func TestBooksUIDeleteRequiresCSRFAndAccess(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	csrf := csrfFrom(t, html)
	body := []byte("protected bytes")
	f.uploadForm(t, f.cookie, csrf, f.library, "safe.epub", body)
	bookID := f.promote(t, "safe")

	for _, bad := range []string{"", "not-the-token"} {
		if resp := f.postForm(t, "/ui/books/"+bookID+"/delete", f.cookie,
			url.Values{"csrf": {bad}, "library": {f.library}},
		); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("csrf %q: %d, want 403", bad, resp.StatusCode)
		}
	}

	// Bob has a valid session and his own token, but no access to the
	// book. He must not be able to delete it.
	bob := f.login(t, "bob")
	_, bobHTML := f.get(t, "/ui/books", bob)
	resp := f.postForm(t, "/ui/books/"+bookID+"/delete", bob,
		url.Values{"csrf": {csrfFrom(t, bobHTML)}})
	if resp.StatusCode == http.StatusOK ||
		!strings.Contains(resp.Header.Get("Location"), "problem=") {
		t.Fatalf("stranger's delete was not refused: %d %s",
			resp.StatusCode, resp.Header.Get("Location"))
	}

	if resp, got := f.get(
		t, "/ui/books/"+bookID+"/download", f.cookie,
	); resp.StatusCode != http.StatusOK || got != string(body) {
		t.Fatalf("book damaged by refused deletes: %d", resp.StatusCode)
	}
}

// TestBooksUIRestoreSaysWhenTheFileIsGone covers the corner where the
// undo cannot fully undo: the bytes were lost while the book sat in the
// trash. The entry comes back, but calling that "restored" and leaving a
// dead Download link would be a lie.
func TestBooksUIRestoreSaysWhenTheFileIsGone(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	csrf := csrfFrom(t, html)
	body := []byte("bytes that will vanish")
	f.uploadForm(t, f.cookie, csrf, f.library, "doomed.epub", body)
	bookID := f.promote(t, "doomed")

	files, err := f.st.ListBookFiles(t.Context(), "u1", bookID, store.LibraryRoleRead)
	if err != nil || len(files) == 0 {
		t.Fatalf("no file to lose: %+v %v", files, err)
	}
	form := url.Values{"csrf": {csrf}, "library": {f.library}}
	f.postForm(t, "/ui/books/"+bookID+"/delete", f.cookie, form)

	// The bytes go missing while the book is in the trash.
	if _, err := f.st.ReconcileBlob(t.Context(), store.BlobInfo{
		SHA256: files[0].BlobSHA256, SizeBytes: int64(len(body)),
	}, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ReconcileCatalogAvailability(
		t.Context(), time.Now().UTC(), 100,
	); err != nil {
		t.Fatal(err)
	}

	resp := f.postForm(t, "/ui/books/"+bookID+"/restore", f.cookie, form)
	loc := resp.Header.Get("Location")
	if strings.Contains(loc, "problem=") {
		t.Fatalf("restore refused: %s", loc)
	}
	if !strings.Contains(loc, "upload+it+again") {
		t.Fatalf("restore claimed success with no file: %s", loc)
	}
	_, html = f.get(t, "/ui/books?library="+f.library, f.cookie)
	if strings.Contains(html, "books/"+bookID+"/download") {
		t.Fatalf("download offered for a book with no bytes:\n%s", html)
	}
}

// TestBooksUIPointsOutTheSameFileTwice: uploading one file twice is
// allowed on purpose — a second catalog entry for deduplicated bytes may
// be exactly what somebody meant — so the page owes the user the fact
// that it happened rather than a silent pair of identical rows. Deleting
// one settles it.
func TestBooksUIPointsOutTheSameFileTwice(t *testing.T) {
	f := newBooksFixture(t)
	_, html := f.get(t, "/ui/books", f.cookie)
	csrf := csrfFrom(t, html)

	body := bytes.Repeat([]byte("same-bytes"), 40)
	for _, name := range []string{"first", "second"} {
		if up := f.uploadForm(
			t, f.cookie, csrf, f.library, name+".epub", body,
		); up.StatusCode != http.StatusSeeOther {
			t.Fatalf("upload %s: %d", name, up.StatusCode)
		}
		f.promote(t, name)
	}
	// A book nothing else shares must not be dragged into the report.
	if up := f.uploadForm(t, f.cookie, csrf, f.library, "alone.epub",
		bytes.Repeat([]byte("different"), 40),
	); up.StatusCode != http.StatusSeeOther {
		t.Fatalf("upload alone: %d", up.StatusCode)
	}
	f.promote(t, "alone")

	_, html = f.get(t, "/ui/books?library="+f.library, f.cookie)
	if !strings.Contains(html, "The same file, more than once") {
		t.Fatalf("duplicates not reported:\n%s", html)
	}
	section := html[strings.Index(html, "The same file, more than once"):]
	section = section[:strings.Index(section, "In this library")]
	for _, want := range []string{"first", "second"} {
		if !strings.Contains(section, want) {
			t.Fatalf("%q missing from the duplicate report:\n%s", want, section)
		}
	}
	if strings.Contains(section, "alone") {
		t.Fatalf("a book with unique bytes was reported:\n%s", section)
	}

	// A reader may see the library but is shown nothing to act on, since
	// acting on it is a deletion they cannot perform. Checked while the
	// duplicate is still there, or this proves nothing.
	_, readerHTML := f.get(t, "/ui/books?library="+f.library, f.readerCookie(t))
	if strings.Contains(readerHTML, "The same file, more than once") {
		t.Fatalf("a reader was shown librarian work:\n%s", readerHTML)
	}

	// Deleting the spare resolves it, and the page stops nagging.
	resp := f.postForm(t, "/ui/books/book-second/delete", f.cookie, url.Values{
		"csrf": {csrf}, "library": {f.library},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	_, html = f.get(t, "/ui/books?library="+f.library, f.cookie)
	if strings.Contains(html, "The same file, more than once") {
		t.Fatalf("duplicates still reported after deleting one:\n%s", html)
	}
}
