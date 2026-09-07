package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

func publicationEPUB(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	files := map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OPS/book.opf":           `<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Publication</dc:title><dc:language>en</dc:language></metadata><manifest><item id="one" href="one.xhtml" media-type="application/xhtml+xml"/><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest><spine><itemref idref="one"/></spine></package>`,
		"OPS/nav.xhtml":          `<html xmlns="http://www.w3.org/1999/xhtml"><body><nav/></body></html>`,
		"OPS/one.xhtml":          `<html xmlns="http://www.w3.org/1999/xhtml"><head/><body><p>Publication text</p><script>alert(1)</script></body></html>`,
		"private.txt":            "not a publication resource",
	}
	for _, name := range []string{"mimetype", "META-INF/container.xml", "OPS/book.opf", "OPS/nav.xhtml", "OPS/one.xhtml", "private.txt"} {
		entry, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestPublicationRoutesServeReadiumAndIsolateResourceBytes(t *testing.T) {
	f := newFolderFixture(t)
	id, digest := f.publish(t, "publication", publicationEPUB(t))
	base := "/v1/books/" + id + "/publication/"
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	resp, raw := f.get(t, base+"manifest.json", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("manifest: %d %s", resp.StatusCode, raw)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), f.root) {
		t.Fatal("manifest exposed the watched path")
	}
	if got := manifest["readingOrder"].([]any)[0].(map[string]any)["href"]; got != base+digest+"/resources/OPS/one.xhtml" {
		t.Fatalf("resource link = %v", got)
	}
	for _, path := range []string{base + "manifest.json", base + digest + "/positions.json", base + digest + "/resources/OPS/one.xhtml"} {
		resp, raw := f.get(t, path, read)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, raw)
		}
		head, body := f.req(t, http.MethodHead, path, read)
		if head.StatusCode != http.StatusOK || len(body) != 0 || head.Header.Get("Content-Length") != resp.Header.Get("Content-Length") {
			t.Fatalf("HEAD %s: %d %s", path, head.StatusCode, body)
		}
		if resp.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("publication may be cached across credentials")
		}
	}
	resp, raw = f.get(t, base+digest+"/resources/OPS/one.xhtml", read)
	if !bytes.Contains(raw, []byte("Publication text")) || resp.Header.Get("Content-Type") != "application/octet-stream" || resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("resource protection: %v %s", resp.Header, raw)
	}
}

func TestPublicationRoutesRecheckGrantsAndVersion(t *testing.T) {
	f := newFolderFixture(t)
	id, digest := f.publish(t, "publication", publicationEPUB(t))
	base := "/v1/books/" + id + "/publication/"
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	other := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)
	if err := f.st.UnassignUserFolder(t.Context(), f.other.ID, f.folder.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base + "manifest.json", base + digest + "/positions.json", base + digest + "/resources/OPS/one.xhtml"} {
		for _, tt := range []struct {
			token  string
			status int
		}{{"", 401}, {other, 404}} {
			resp, _ := f.get(t, path, tt.token)
			if resp.StatusCode != tt.status {
				t.Fatalf("%s: got %d, want %d", path, resp.StatusCode, tt.status)
			}
		}
	}
	for _, path := range []string{base + "stale/positions.json", base + "stale/resources/OPS/one.xhtml"} {
		resp, _ := f.get(t, path, read)
		if resp.StatusCode != 409 {
			t.Fatalf("stale version: %d", resp.StatusCode)
		}
	}
	for _, path := range []string{"private.txt", "OPS/missing.xhtml", "%2e%2e/private.txt", "OPS%5cone.xhtml"} {
		resp, _ := f.get(t, base+digest+"/resources/"+path, read)
		if resp.StatusCode != 404 {
			t.Fatalf("unsafe/undeclared %s: %d", path, resp.StatusCode)
		}
	}
	book, err := f.st.CatalogBookByID(t.Context(), f.user.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, book.RelativePath), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp, _ := f.get(t, base+digest+"/resources/OPS/one.xhtml", read)
	if resp.StatusCode != 409 {
		t.Fatalf("changed file: %d", resp.StatusCode)
	}
}
