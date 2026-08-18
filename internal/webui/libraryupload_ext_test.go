//go:build linux

package webui_test

// The browser's way of sending a book (ADR-0023). What matters here is
// not the transfer — that is the API's, tested there — but the two
// things only this surface can get wrong: showing the form where it
// would be refused, and accepting a post that no form of ours made.

import (
	"archive/zip"
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// allowUploads marks the fixture's folder the way an administrator does.
func allowUploads(t *testing.T, f *booksFixture) {
	t.Helper()
	if err := f.st.SetFolderUploads(
		t.Context(), f.folder, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

// send posts one publication the way the form on the page does: the
// token in the query string, the file in the body.
func send(
	t *testing.T, f *booksFixture, csrf, filename string, body []byte,
) *http.Response {
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
		f.ts.URL+"/ui/library/upload?folder="+f.folder+"&csrf="+csrf, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(f.cookie)
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// csrfOnLibrary reads the token out of the page, which is the only
// place a browser could have got it.
func csrfOnLibrary(t *testing.T, f *booksFixture) string {
	t.Helper()
	_, body := f.get(t, "/ui/library", f.cookie)
	return csrfFrom(t, body)
}

func TestLibraryOffersNoFormUntilTheFolderAsks(t *testing.T) {
	f := newBooksFixture(t)

	if _, body := f.get(t, "/ui/library", f.cookie); strings.Contains(body, "Send a book") {
		t.Fatal("a folder that never asked for uploads offered the form")
	}

	allowUploads(t, f)
	if _, body := f.get(t, "/ui/library", f.cookie); !strings.Contains(body, "Send a book") {
		t.Fatal("a folder that accepts uploads did not offer the form")
	}
}

func TestSendingABookFromTheBrowserAddsIt(t *testing.T) {
	f := newBooksFixture(t)
	allowUploads(t, f)

	resp := send(t, f, csrfOnLibrary(t, f), "whatever.epub",
		browserEPUB(t, "Ancillary Justice", "Ann Leckie"))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); !strings.Contains(got, "notice=") {
		t.Fatalf("location = %q, want a notice saying what happened", got)
	}
	// The Location is relative and the browser resolves it against the
	// URL it posted to, which is a directory deeper than the page the
	// form is on. "library" alone lands on /ui/library/library.
	if got := landsOn(t, f, resp); got != "/ui/library" {
		t.Fatalf("the reader ends up at %q, want the library page", got)
	}
	if _, err := os.Stat(filepath.Join(
		f.root, "Ann Leckie - Ancillary Justice.epub")); err != nil {
		t.Fatalf("the book is not in the folder: %v", err)
	}
}

// The same book twice is a success that has to read differently, or the
// second send looks like it added a second copy.
func TestSendingTheSameBookTwiceSaysSo(t *testing.T) {
	f := newBooksFixture(t)
	allowUploads(t, f)
	body := browserEPUB(t, "Ancillary Justice", "Ann Leckie")
	csrf := csrfOnLibrary(t, f)

	send(t, f, csrf, "whatever.epub", body)
	resp := send(t, f, csrf, "whatever.epub", body)
	if got := resp.Header.Get("Location"); !strings.Contains(got, "already") {
		t.Fatalf("location = %q, want it to say the book was already here", got)
	}

	found, err := filepath.Glob(filepath.Join(f.root, "*.epub"))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("the folder holds %d books, want 1: %v", len(found), found)
	}
}

func TestSendingABookNeedsTheFormsToken(t *testing.T) {
	f := newBooksFixture(t)
	allowUploads(t, f)

	resp := send(t, f, "not-the-token", "whatever.epub",
		browserEPUB(t, "Ancillary Justice", "Ann Leckie"))
	if got := resp.Header.Get("Location"); !strings.Contains(got, "problem=") {
		t.Fatalf("location = %q, want a refusal", got)
	}
	found, _ := filepath.Glob(filepath.Join(f.root, "*.epub"))
	if len(found) != 0 {
		t.Fatalf("a post with no token left %v behind", found)
	}
}

func TestSendingABookIntoAFolderThatDidNotAskIsRefused(t *testing.T) {
	f := newBooksFixture(t)

	resp := send(t, f, csrfOnLibrary(t, f), "whatever.epub",
		browserEPUB(t, "Ancillary Justice", "Ann Leckie"))
	if got := resp.Header.Get("Location"); !strings.Contains(got, "problem=") {
		t.Fatalf("location = %q, want a refusal", got)
	}
	found, _ := filepath.Glob(filepath.Join(f.root, "*.epub"))
	if len(found) != 0 {
		t.Fatalf("an unmarked folder was written into: %v", found)
	}
}

// browserEPUB is the smallest publication the validator accepts.
func browserEPUB(t *testing.T, title, author string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name, body string, method uint16) {
		t.Helper()
		part, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, body); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", "application/epub+zip", zip.Store)
	add("META-INF/container.xml", `<?xml version="1.0"?>`+
		`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">`+
		`<rootfiles><rootfile full-path="OPS/book.opf"`+
		` media-type="application/oebps-package+xml"/></rootfiles></container>`,
		zip.Deflate)
	add("OPS/book.opf", `<package xmlns="http://www.idpf.org/2007/opf">`+
		`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">`+
		`<dc:title>`+title+`</dc:title><dc:creator>`+author+`</dc:creator>`+
		`</metadata><manifest><item href="nav.xhtml"`+
		` media-type="application/xhtml+xml" properties="nav"/></manifest>`+
		`</package>`, zip.Deflate)
	add("OPS/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml">`+
		`<body><nav/></body></html>`, zip.Deflate)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// landsOn is where a browser would go next: the relative Location header
// resolved against the URL the form was posted to.
func landsOn(t *testing.T, f *booksFixture, resp *http.Response) string {
	t.Helper()
	from, err := url.Parse(f.ts.URL + "/ui/library/upload")
	if err != nil {
		t.Fatal(err)
	}
	to, err := from.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return to.Path
}
