package kosync

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

const (
	deriveDoc  = "0123456789abcdef0123456789abcdef"
	deriveSHA  = "sha-moby"
	deriveWant = "/body/DocFragment[2]/body/p[2]/text()[1].8"
)

// mobyEPUB has a linear="no" cover ahead of the chapter, so the
// chapter is DocFragment[2] the way CrossPoint counts it.
func mobyEPUB(t *testing.T) []byte {
	t.Helper()
	return mobyEPUBWithChapter(t, `<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>Loomings</h1><p>Chapter one.</p><p>Call me Ishmael. Some years ago, never mind how long.</p></body></html>`)
}

func mobyEPUBWithChapter(t *testing.T, chapter string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct{ name, body string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OEBPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"OEBPS/book.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">moby</dc:identifier><dc:title>Moby-Dick</dc:title><dc:language>en</dc:language></metadata>
<manifest>
<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/>
<item id="c1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
</manifest>
<spine><itemref idref="cover" linear="no"/><itemref idref="c1"/></spine></package>`},
		{"OEBPS/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><nav epub:type="toc" xmlns:epub="http://www.idpf.org/2007/ops"><ol><li><a href="chapter1.xhtml">Loomings</a></li></ol></nav></body></html>`},
		{"OEBPS/cover.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Moby-Dick</p></body></html>`},
		{"OEBPS/chapter1.xhtml", chapter},
	} {
		method := zip.Deflate
		if f.name == "mimetype" {
			method = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type deriveFixture struct {
	ts       *httptest.Server
	st       store.Store
	user     store.User
	slot     string
	key      string
	pairCode string
	workID   string
}

// newDeriveFixture holds one EPUB in a granted folder, a paired kosync
// slot, and a work the reader opened as editionSHA and the X3 then read
// under its fingerprint. The test writes the newest op.
func newDeriveFixture(t *testing.T, editionSHA string) *deriveFixture {
	t.Helper()
	ctx := t.Context()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.InsecureHTTP = true
	s := &Server{St: st, Cfg: cfg, AuthRateLim: auth.NewRateLimiter(1000, time.Minute), Files: content.NewFiles(st)}
	mux := http.NewServeMux()
	s.Mount(mux, func(h http.Handler) http.Handler { return auth.RequireSecureTransport(cfg, h) })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, KosyncEnabled: true, KopluginEnabled: true, CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	f := &deriveFixture{ts: ts, st: st, user: u}
	f.addBook(t, "lib", "moby.epub", deriveSHA, mobyEPUB(t))
	if err := st.AssignUserFolder(ctx, u.ID, "lib"); err != nil {
		t.Fatal(err)
	}
	f.workID = "w-moby"
	if err := st.CreateWork(ctx, store.Work{ID: f.workID, UserID: u.ID, Title: "Moby-Dick", CreatedAt: time.Now()},
		&store.Edition{UserID: u.ID, SHA256: editionSHA, WorkID: f.workID},
		[]store.Identifier{{Kind: "sha256", Value: editionSHA}, {Kind: "partial-md5", Value: deriveDoc}}); err != nil {
		t.Fatal(err)
	}
	f.slot, f.key, f.pairCode = pairDevice(t, ts, st, u.ID, "x3")

	code, _ := kreq(t, "PUT", ts.URL+"/adapter/kosync/syncs/progress", f.slot, f.key, map[string]any{
		"document": deriveDoc, "progress": "/body/DocFragment[2]/body/p[1]/text()[1].0",
		"percentage": 0.1, "device": "x3", "timestamp": time.Now().Add(-time.Hour).Unix(),
	})
	if code != 200 {
		t.Fatalf("put progress: %d", code)
	}
	return f
}

func (f *deriveFixture) addBook(t *testing.T, folderID, name, sha string, body []byte) {
	t.Helper()
	ctx := t.Context()
	root := t.TempDir()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := f.st.CreateFolder(ctx, store.Folder{
		ID: folderID, Name: folderID, RootPath: root, Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ReconcileFolder(ctx, folderID, []store.ObservedBook{{
		RelativePath: name, SizeBytes: info.Size(), MTime: info.ModTime(), ContentSHA256: sha,
		PartialMD5: deriveDoc, OriginalFilename: name, MediaType: "application/epub+zip", Title: name,
	}}, true, now); err != nil {
		t.Fatal(err)
	}
}

// readOnWeb appends what the web reader or the app writes on a page turn.
func (f *deriveFixture) readOnWeb(t *testing.T, editionSHA string, locator map[string]any) {
	t.Helper()
	raw, err := json.Marshal(locator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.AppendOps(t.Context(), f.user.ID, "web-1", []store.Op{{
		OpID: "web-op-1", WorkID: f.workID, EditionSHA: &editionSHA, ClientTS: time.Now(),
		Progression: 0.42, LocatorJSON: raw, Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}
}

func anchoredAtIshmael() map[string]any {
	return map[string]any{
		"href": "/OEBPS/chapter1.xhtml", "type": "application/xhtml+xml",
		"locations": map[string]any{
			"progression": 0.3, "totalProgression": 0.42,
			"liseurAnchor": 1, "cssSelector": "body > p:nth-of-type(2)",
		},
		"text": map[string]any{"before": "Call me ", "highlight": "Ishmael", "after": ". Some years ago, never mind"},
	}
}

// A Liseur anchor written against the very file the X3 named comes back
// to CrossPoint and KOReader as the XPath of the same word (ADR-0059).
func TestGetProgressDerivesXPathFromLiseurAnchor(t *testing.T) {
	f := newDeriveFixture(t, deriveSHA)
	f.readOnWeb(t, deriveSHA, anchoredAtIshmael())

	_, out := kreq(t, "GET", f.ts.URL+"/adapter/kosync/syncs/progress/"+deriveDoc, f.slot, f.key, nil)
	if out["progress"] != deriveWant || out["percentage"] != 0.42 || out["device"] != "web-1" {
		t.Fatalf("KOReader GET = %v, want progress %q", out, deriveWant)
	}
	code, out := crosspointDo(t, "GET", f.ts.URL+"/adapter/kosync/syncs/progress/"+deriveDoc+"?position_kinds=crosspoint",
		f.slot, f.key, f.pairCode, nil)
	if code != 200 || out["progress"] != deriveWant {
		t.Fatalf("CrossPoint GET = %d %v, want progress %q", code, out, deriveWant)
	}
}

// Every case short of an exact anchor in the same granted file keeps
// answering with the percentage, as before.
func TestGetProgressFallsBackToPercentage(t *testing.T) {
	progressionOnly := anchoredAtIshmael()
	progressionOnly["locations"] = map[string]any{"progression": 0.3, "totalProgression": 0.42}
	delete(progressionOnly, "text")
	moved := anchoredAtIshmael()
	moved["text"] = map[string]any{"before": "", "highlight": "Queequeg", "after": ""}

	for name, tc := range map[string]struct {
		edition string
		locator map[string]any
		setup   func(t *testing.T, f *deriveFixture)
	}{
		"another edition":  {edition: "sha-other", locator: anchoredAtIshmael()},
		"progression only": {edition: deriveSHA, locator: progressionOnly},
		"quote not found":  {edition: deriveSHA, locator: moved},
		"revoked grant": {edition: deriveSHA, locator: anchoredAtIshmael(), setup: func(t *testing.T, f *deriveFixture) {
			if err := f.st.UnassignUserFolder(t.Context(), f.user.ID, "lib"); err != nil {
				t.Fatal(err)
			}
		}},
		"fingerprint shared by two files": {edition: deriveSHA, locator: anchoredAtIshmael(), setup: func(t *testing.T, f *deriveFixture) {
			f.addBook(t, "other", "moby2.epub", "sha-moby-2", append(mobyEPUB(t), 0))
			if err := f.st.AssignUserFolder(t.Context(), f.user.ID, "other"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newDeriveFixture(t, tc.edition)
			if tc.setup != nil {
				tc.setup(t, f)
			}
			f.readOnWeb(t, tc.edition, tc.locator)
			_, out := kreq(t, "GET", f.ts.URL+"/adapter/kosync/syncs/progress/"+deriveDoc, f.slot, f.key, nil)
			if out["progress"] != "0.42" {
				t.Fatalf("GET = %v, want the percentage", out)
			}
		})
	}
}

func TestGetProgressDeclinesReceiverSpecificTextIndex(t *testing.T) {
	f := newDeriveFixture(t, deriveSHA)
	if err := f.st.UnassignUserFolder(t.Context(), f.user.ID, "lib"); err != nil {
		t.Fatal(err)
	}
	f.addBook(t, "whitespace", "whitespace.epub", deriveSHA, mobyEPUBWithChapter(t, "<html><body><div>\n  <p>x</p>\n  Tail text</div></body></html>"))
	if err := f.st.AssignUserFolder(t.Context(), f.user.ID, "whitespace"); err != nil {
		t.Fatal(err)
	}
	f.readOnWeb(t, deriveSHA, map[string]any{
		"href":      "OEBPS/chapter1.xhtml",
		"locations": map[string]any{"liseurAnchor": 1, "cssSelector": "body > div:nth-of-type(1)"},
		"text":      map[string]any{"before": "\n  ", "highlight": "Tail", "after": " text"},
	})
	_, out := kreq(t, "GET", f.ts.URL+"/adapter/kosync/syncs/progress/"+deriveDoc, f.slot, f.key, nil)
	if out["progress"] != "0.42" {
		t.Fatalf("KOReader received a receiver-specific XPath: %v", out)
	}
}
