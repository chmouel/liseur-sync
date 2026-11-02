package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

const crosspointXPath = "/body/DocFragment[2]/body/p[2]/text().8"

// chapterEPUB has a linear="no" cover ahead of the chapter, so the
// chapter is DocFragment[2] the way CrossPoint counts it.
func chapterEPUB(t *testing.T) []byte {
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
		{"OEBPS/chapter1.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>Loomings</h1><p>Chapter one.</p><p>Call me Ishmael. Some years ago, never mind how long.</p></body></html>`},
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

// publishForCrossPoint catalogues the chapter book and returns its
// digest and KOReader fingerprint.
func (f *folderFixture) publishForCrossPoint(t *testing.T) (sha, partialMD5 string) {
	t.Helper()
	bookID, sha := f.writeBook(t, "moby.epub", chapterEPUB(t))
	book, err := f.st.CatalogBookByID(t.Context(), "", bookID)
	if err != nil || book.PartialMD5 == "" {
		t.Fatalf("catalogued book = %+v, %v", book, err)
	}
	return sha, book.PartialMD5
}

// readOnX3 appends what the kosync adapter stores for a CrossPoint PUT.
func (f *folderFixture) readOnX3(t *testing.T, userID, partialMD5 string) {
	t.Helper()
	xpath, alias := crosspointXPath, "partial-md5:"+partialMD5
	if _, err := f.st.AppendKosyncOp(t.Context(), userID, partialMD5, "kosync:x3", store.Op{
		OpID: store.NewID(), DeviceID: "kosync:x3", ClientTS: time.Now(), Progression: 0.42,
		ForeignPos: &xpath, Origin: store.OriginKosync, OriginAlias: &alias,
	}); err != nil {
		t.Fatal(err)
	}
}

// opsAt collects every op a native read returns, newest positions and
// furthest alike.
func opsAt(t *testing.T, f *folderFixture, path, token string) []map[string]any {
	t.Helper()
	resp, body := f.get(t, path, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, resp.StatusCode, body)
	}
	var out struct {
		Ops      []map[string]any `json:"ops"`
		Furthest []map[string]any `json:"furthest"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return append(out.Ops, out.Furthest...)
}

func nativeReads(t *testing.T, f *folderFixture, userID, partialMD5 string) []string {
	t.Helper()
	workID, err := f.st.WorkIDByAlias(t.Context(), userID, "partial-md5", partialMD5)
	if err != nil {
		t.Fatal(err)
	}
	return []string{"/v1/changes", "/v1/heads", "/v1/works/" + workID + "/positions"}
}

// A CrossPoint position comes back to a Liseur reader as the anchored
// locator of the same word, marked as derived, with the op otherwise as
// stored (ADR-0059).
func TestNativeReadsDeriveLocatorFromKosyncXPath(t *testing.T) {
	f := newFolderFixture(t)
	sha, md5 := f.publishForCrossPoint(t)
	f.readOnX3(t, f.user.ID, md5)

	for _, path := range nativeReads(t, f, f.user.ID, md5) {
		ops := opsAt(t, f, path, f.token)
		if len(ops) == 0 {
			t.Fatalf("%s: no ops", path)
		}
		for _, op := range ops {
			loc, _ := op["locator"].(map[string]any)
			locations, _ := loc["locations"].(map[string]any)
			text, _ := loc["text"].(map[string]any)
			derived, _ := op["derived"].(map[string]any)
			if op["edition_sha"] != sha || derived["from"] != "kosync-xpath" ||
				op["foreign_pos"] != crosspointXPath || op["device_id"] != "kosync:x3" || op["origin"] != "kosync" {
				t.Fatalf("%s: op = %v", path, op)
			}
			if loc["href"] != "OEBPS/chapter1.xhtml" || loc["type"] != "application/xhtml+xml" ||
				locations["liseurAnchor"] != float64(1) || locations["cssSelector"] != "body > p:nth-of-type(2)" ||
				locations["totalProgression"] != 0.42 ||
				text["before"] != "Call me " || text["highlight"] != "Ishmael" ||
				!strings.HasPrefix(text["after"].(string), ". Some years ago") {
				t.Fatalf("%s: locator = %v", path, loc)
			}
		}
	}
}

// Book text reaches only a credential that may read the library, and
// only through a grant the owner holds now.
func TestNativeReadsDeriveOnlyWithinTheCatalogBoundary(t *testing.T) {
	underived := func(t *testing.T, f *folderFixture, userID, md5, token string) {
		t.Helper()
		for _, path := range nativeReads(t, f, userID, md5) {
			for _, op := range opsAt(t, f, path, token) {
				if op["derived"] != nil || op["locator"] != nil || op["edition_sha"] != nil {
					t.Fatalf("%s: derived without a grant or scope: %v", path, op)
				}
				if op["foreign_pos"] != crosspointXPath {
					t.Fatalf("%s: op not as stored: %v", path, op)
				}
			}
		}
	}

	t.Run("sync-only credential", func(t *testing.T) {
		f := newFolderFixture(t)
		_, md5 := f.publishForCrossPoint(t)
		f.readOnX3(t, f.user.ID, md5)
		underived(t, f, f.user.ID, md5, f.mintToken(t, f.user.ID, store.ScopeSync))
	})
	t.Run("revoked grant", func(t *testing.T) {
		f := newFolderFixture(t)
		_, md5 := f.publishForCrossPoint(t)
		f.readOnX3(t, f.user.ID, md5)
		if err := f.st.UnassignUserFolder(t.Context(), f.user.ID, f.folder.ID); err != nil {
			t.Fatal(err)
		}
		underived(t, f, f.user.ID, md5, f.token)
	})
	t.Run("fingerprint of a folder the reader cannot see", func(t *testing.T) {
		f := newFolderFixture(t)
		_, md5 := f.publishForCrossPoint(t)
		if err := f.st.UnassignUserFolder(t.Context(), f.other.ID, f.folder.ID); err != nil {
			t.Fatal(err)
		}
		f.readOnX3(t, f.other.ID, md5)
		f.readOnX3(t, f.user.ID, md5)
		underived(t, f, f.other.ID, md5, f.mintToken(t, f.other.ID, store.ScopeLibraryRead, store.ScopeSync))

		// Tenant isolation: the reader's own derivation holds, and
		// neither account sees the other's op.
		for _, op := range opsAt(t, f, "/v1/changes", f.token) {
			if op["derived"] == nil {
				t.Fatalf("granted reader lost derivation: %v", op)
			}
		}
		if n := len(opsAt(t, f, "/v1/changes", f.token)); n != 1 {
			t.Fatalf("reader sees %d ops, want only their own", n)
		}
	})
}
