//go:build linux

package api

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

// folderFixture is the replacement for the deleted uploadFixture. Books
// no longer arrive through an upload endpoint (ADR-0017): a test that
// wants a catalog row writes real bytes under a real folder root and
// drives the actual reconciler over it, exactly as the watcher in
// cmd/liseur-sync/main.go does. Nothing here builds an ObservedBook by
// hand, so a bug in the pass itself would show up here rather than only
// in internal/content.
type folderFixture struct {
	ts    *httptest.Server
	st    store.Store
	srv   *Server
	cache *content.Cache

	// user and other are two distinct accounts. The fixture grants its
	// default folder to both; isolation tests explicitly revoke one grant.
	user  store.User
	other store.User
	// token is user's everyday credential: library-read and sync, the
	// combination an ordinary client holds.
	token string

	folder store.Folder
	root   string
	rec    *content.Reconciler
}

func newFolderFixture(t *testing.T) *folderFixture {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	cacheRoot := t.TempDir()
	// t.TempDir creates its directory 0777&^umask (testing.go's
	// makeTempDir), which is usually 0755 — world-readable. The cache
	// stores rendered covers, which are pictures of what somebody is
	// reading, so OpenCache refuses anything looser than 0700. Every
	// real deployment's cache root is created by an operator who chose
	// its permissions; a test has to choose them itself.
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	cache, err := content.OpenCache(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	cfg := config.Default()
	cfg.InsecureHTTP = true // httptest is plain HTTP
	cfg.Content.CacheDir = cacheRoot

	srv := &Server{
		St:           st,
		Auth:         auth.NewService(st),
		Cfg:          cfg,
		LoginLimiter: auth.NewRateLimiter(1000, time.Minute),
		OPDSLimiter:  auth.NewRateLimiter(1000, time.Minute),
		Files:        content.NewFiles(st),
		Covers:       cache,
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	f := &folderFixture{ts: ts, st: st, srv: srv, cache: cache}
	f.user = f.createUser(t, "reader")
	f.other = f.createUser(t, "stranger")
	f.token = f.mintToken(t, f.user.ID, store.ScopeLibraryRead, store.ScopeSync)

	f.root = t.TempDir()
	f.folder = store.Folder{
		ID: store.NewID(), Name: "Books", RootPath: f.root,
		Kind: store.FolderPlain,
	}
	if err := st.CreateFolder(t.Context(), f.folder); err != nil {
		t.Fatal(err)
	}
	for _, user := range []store.User{f.user, f.other} {
		if err := st.AssignUserFolder(t.Context(), user.ID, f.folder.ID); err != nil {
			t.Fatal(err)
		}
	}
	f.rec = content.NewReconciler(st, content.ScanLimits{}, epub.DefaultLimits(),
		slog.New(slog.DiscardHandler))
	// The upload route hands its bytes to the same reconciler the tests
	// drive by hand, so an uploaded book and a book written under the
	// root go through exactly one pass implementation (ADR-0023).
	ingester := content.NewIngester(f.rec)
	srv.Ingest = ingester
	srv.Removal = ingester
	return f
}

func (f *folderFixture) createUser(t *testing.T, name string) store.User {
	t.Helper()
	hash, err := auth.HashPassword("hunter2hunter")
	if err != nil {
		t.Fatal(err)
	}
	u := store.User{
		ID: store.NewID(), Name: name + "-" + store.NewID()[:8],
		Argon2Hash: hash, Timezone: "UTC", CreatedAt: time.Now().UTC(),
	}
	if err := f.st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	var after string
	for {
		folders, err := f.st.ListFolders(t.Context(), "", after, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, folder := range folders {
			if err := f.st.AssignUserFolder(t.Context(), u.ID, folder.ID); err != nil {
				t.Fatal(err)
			}
		}
		if len(folders) < 100 {
			break
		}
		after = store.FolderCursor(folders[len(folders)-1])
	}
	return u
}

func (f *folderFixture) mintToken(t *testing.T, userID string, scopes ...store.Scope) string {
	t.Helper()
	set, err := store.NormalizeScopes(scopes)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := f.srv.Auth.MintToken(t.Context(), userID, "test-device", set, nil)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

// reconcile runs one complete pass over the fixture's folder, the same
// call the watcher makes. Tests that need a second, incomplete or
// otherwise unusual pass call f.rec.Reconcile or the store directly.
func (f *folderFixture) reconcile(t *testing.T) store.ReconcileResult {
	t.Helper()
	return f.reconcileFolder(t, f.folder)
}

// reconcileFolder is reconcile generalized to an arbitrary folder, for
// the tests that need a second folder beside the fixture's default one
// — a Calibre library, most often, since it is never the same kind as
// the plain folder every other test uses.
func (f *folderFixture) reconcileFolder(t *testing.T, folder store.Folder) store.ReconcileResult {
	t.Helper()
	for _, user := range []store.User{f.user, f.other} {
		if err := f.st.AssignUserFolder(t.Context(), user.ID, folder.ID); err != nil {
			t.Fatal(err)
		}
	}
	result, err := f.rec.Reconcile(t.Context(), folder)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// writeBook puts name.epub under the folder root and reconciles, then
// returns the catalog id the store minted for it and the content
// digest. The bytes need not be a valid EPUB — an unparseable file is
// still catalogued, titled from its filename (internal/content's own
// rule) — so tests that only care about byte-for-byte transport can pass
// arbitrary content.
func (f *folderFixture) writeBook(t *testing.T, name string, body []byte) (bookID, digest string) {
	t.Helper()
	full := filepath.Join(f.root, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	sum := sha256.Sum256(body)
	digest = hex.EncodeToString(sum[:])

	known, err := f.st.BooksInFolder(t.Context(), f.folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	relative := name
	for _, b := range known {
		if b.RelativePath == relative {
			return b.ID, digest
		}
	}
	t.Fatalf("writeBook: %q was not catalogued after a reconcile pass", name)
	return "", ""
}

// publish is writeBook with a filename derived from the title, matching
// the style most tests want: a book called "one" produces "one.epub"
// with a title readable from the filename by default, or from a real
// dc:title when the caller hands it valid EPUB bytes.
func (f *folderFixture) publish(t *testing.T, name string, body []byte) (bookID, digest string) {
	t.Helper()
	return f.writeBook(t, name+".epub", body)
}

// publishAs writes a real, parseable EPUB carrying the given title, so a
// test can assert on metadata rather than only on bytes.
func (f *folderFixture) publishAs(t *testing.T, name, title string, body []byte) (bookID, digest string) {
	t.Helper()
	return f.writeBook(t, name+".epub", makeEPUB(t, title, "", body))
}

// publishWithAuthor is publishAs plus a dc:creator, for the tests that
// browse by contributor.
func (f *folderFixture) publishWithAuthor(
	t *testing.T, name, title, author string, body []byte,
) (bookID, digest string) {
	t.Helper()
	return f.writeBook(t, name+".epub", makeEPUB(t, title, author, body))
}

// makeEPUB builds the smallest publication epub.Validate accepts, with a
// dc:title (and, optionally, a dc:creator) in the Dublin Core namespace:
// without one the title falls back to the filename, which is correct
// production behaviour but not what a metadata test is asking about.
// extra is folded into the manifest's spine content so two books built
// from different extra bytes still hash differently.
func makeEPUB(t *testing.T, title, author string, extra []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name, body string, method uint16) {
		t.Helper()
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, body); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", "application/epub+zip", zip.Store)
	add("META-INF/container.xml", `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OPS/book.opf"
 media-type="application/oebps-package+xml"/></rootfiles></container>`, zip.Deflate)
	creator := ""
	if author != "" {
		creator = `<dc:creator>` + xmlEscape(author) + `</dc:creator>`
	}
	add("OPS/book.opf", `<package xmlns="http://www.idpf.org/2007/opf">`+
		`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">`+
		`<dc:title>`+xmlEscape(title)+`</dc:title>`+creator+`</metadata>`+
		`<manifest><item href="nav.xhtml" media-type="application/xhtml+xml"`+
		` properties="nav"/></manifest></package>`, zip.Deflate)
	add("OPS/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml">`+
		`<body><nav/><!-- `+string(extra)+` --></body></html>`, zip.Deflate)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// xmlEscape is used for title and author strings so a metadata test can
// hand makeEPUB attacker-shaped text (angle brackets, ampersands) and get
// back a well-formed OPF where that text round-trips as a literal title —
// exactly what a real EPUB with a hostile title would look like on disk.
func xmlEscape(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		panic(err)
	}
	return buf.String()
}

func (f *folderFixture) get(t *testing.T, path, token string) (*http.Response, []byte) {
	t.Helper()
	return f.req(t, http.MethodGet, path, token)
}

func (f *folderFixture) req(t *testing.T, method, path, token string) (*http.Response, []byte) {
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

// getJSON is the JSON-decoding sibling of get, used by every test that
// wants the body rather than the raw response.
func getJSON(t *testing.T, url, token string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}
