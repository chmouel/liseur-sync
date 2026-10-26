//go:build linux

package api

import (
	"bytes"
	"encoding/binary"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

const audioChapter = `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><body>
<p>The morning sun rose slowly over the silent mountains and warmed the valley stones.</p>
</body></html>`

// bookAt writes a publication under the fixture's root and catalogs it.
func (f *folderFixture) bookAt(t *testing.T, relative string) store.CatalogBook {
	t.Helper()
	body := buildTestAudioEPUB(t, "Shadows Upon Time", "Christopher Ruocchio", audioChapter, audioChapter)
	if err := os.WriteFile(filepath.Join(f.root, relative), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rec.Reconcile(t.Context(), f.folder); err != nil {
		t.Fatal(err)
	}
	books, err := f.st.ListCatalogBooks(t.Context(), "", f.folder.ID, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range books {
		if b.RelativePath == relative {
			return b
		}
	}
	t.Fatalf("%s was not catalogued", relative)
	return store.CatalogBook{}
}

func (f *folderFixture) uploadAudiobook(
	t *testing.T, token, bookID string, body []byte,
) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "whatever.m4b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/v1/books/"+bookID+"/audiobook", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
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

// The audiobook lands beside its book under the book's own name, the
// pass pairs it by that name, and a second one is refused rather than
// replacing it.
func TestUploadAudiobookLandsBesideItsBookAndPairs(t *testing.T) {
	f := newFolderFixture(t)
	f.allowUploads(t)
	book := f.bookAt(t, "shadows.epub")
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)

	m4b := buildSliceableTestM4B("Something Else Entirely", "Nobody")
	resp, got := f.uploadAudiobook(t, token, book.ID, m4b)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%v)", resp.StatusCode, got)
	}
	if got["audiobook_id"] == nil || got["audiobook_id"] == "" {
		t.Fatalf("no audiobook_id in %v", got)
	}
	if state := got["state"]; state != "active" && state != "unmapped" {
		t.Fatalf("state = %v, want the pairing's state", state)
	}
	if onDisk, err := os.ReadFile(filepath.Join(f.root, "shadows.m4b")); err != nil || !bytes.Equal(onDisk, m4b) {
		t.Fatalf("the audiobook is not beside the book: %v", err)
	}
	link, _, err := f.st.BookAudiobookByBookID(t.Context(), "", book.ID)
	if err != nil || link.Source != "filename" {
		t.Fatalf("link = %+v, %v; want a filename pairing", link, err)
	}

	resp, got = f.uploadAudiobook(t, token, book.ID, buildSliceableTestM4B("Another", "Somebody"))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second upload status = %d, want 409 (%v)", resp.StatusCode, got)
	}
	if onDisk, _ := os.ReadFile(filepath.Join(f.root, "shadows.m4b")); !bytes.Equal(onDisk, m4b) {
		t.Fatal("the first audiobook was replaced")
	}
	assertNoSpool(t, f.srv.Cfg.Content.CacheDir)
}

func TestUploadAudiobookRefusals(t *testing.T) {
	f := newFolderFixture(t)
	book := f.bookAt(t, "shadows.epub")
	token := f.mintToken(t, f.user.ID, store.ScopeLibraryUpload)
	m4b := buildSliceableTestM4B("Shadows Upon Time", "Christopher Ruocchio")

	// Nobody marked this folder writable.
	if resp, got := f.uploadAudiobook(t, token, book.ID, m4b); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unmarked folder: status = %d (%v)", resp.StatusCode, got)
	}
	f.allowUploads(t)

	stranger := f.createUser(t, "ungranted")
	if err := f.st.UnassignUserFolder(t.Context(), stranger.ID, f.folder.ID); err != nil {
		t.Fatal(err)
	}
	if resp, _ := f.uploadAudiobook(t, f.mintToken(t, stranger.ID, store.ScopeLibraryUpload),
		book.ID, m4b); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ungranted reader: status = %d, want 404", resp.StatusCode)
	}
	if resp, _ := f.uploadAudiobook(t, token, book.ID, []byte("not an audiobook")); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("not an M4B: status = %d, want 422", resp.StatusCode)
	}
	// A movie header with a duration and no track at all: metadata
	// reads fine, and there is nothing to play.
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)
	binary.BigEndian.PutUint32(mvhd[16:20], 60000)
	bare := append(binary.BigEndian.AppendUint32(nil, 36), "moov"...)
	bare = append(append(binary.BigEndian.AppendUint32(bare, 28), "mvhd"...), mvhd...)
	if resp, _ := f.uploadAudiobook(t, token, book.ID, bare); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("no sound track: status = %d, want 422", resp.StatusCode)
	}
	f.srv.Cfg.Content.MaxAudiobookUploadBytes = 64
	if resp, _ := f.uploadAudiobook(t, token, book.ID, m4b); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the bound: status = %d, want 413", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(f.root, "shadows.m4b")); !os.IsNotExist(err) {
		t.Fatalf("a refused upload left a file: %v", err)
	}
	assertNoSpool(t, f.srv.Cfg.Content.CacheDir)
}

// In a Calibre folder the audiobook becomes the book's M4B format, and
// the pass pairs it by Calibre id.
func TestUploadAudiobookIntoCalibreAddsAFormat(t *testing.T) {
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
	resp, body := f.upload(t, token, "a.epub",
		buildTestAudioEPUB(t, "Shadows Upon Time", "Christopher Ruocchio", audioChapter, audioChapter))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("book upload = %d: %v", resp.StatusCode, body)
	}
	bookID := body["book_id"].(string)
	epubPath := body["relative_path"].(string)

	resp, got := f.uploadAudiobook(t, token, bookID, buildSliceableTestM4B("Shadows Upon Time", "Christopher Ruocchio"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%v)", resp.StatusCode, got)
	}
	m4bPath := strings.TrimSuffix(epubPath, ".epub") + ".m4b"
	if _, err := os.Stat(filepath.Join(library.RootPath, filepath.FromSlash(m4bPath))); err != nil {
		t.Fatalf("no M4B beside the Calibre book: %v", err)
	}
	link, _, err := f.st.BookAudiobookByBookID(t.Context(), "", bookID)
	if err != nil || link.Source != "calibre" {
		t.Fatalf("link = %+v, %v; want a calibre pairing", link, err)
	}
}
