package webui_test

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReaderAudioPanel(t *testing.T) {
	runNodeTests(t, "readeraudiopanel.test.mjs")
}

func TestReaderAudioPanelInARealBrowser(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("no chromium; set LISEUR_CHROME to run the browser check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node to drive the browser with")
	}
	parallelBrowser(t)
	f := newBooksFixture(t)
	bookID := f.addBook(t, "novel", browserTestEPUB(t))
	ts := httptest.NewUnstartedServer(nil)
	wholeServer(t, f, ts, "")
	cookie := f.loginTo(t, ts, "alice")
	cmd := exec.Command(node, filepath.Join("testdata", "readerbrowser.mjs"))
	cmd.Env = append(os.Environ(),
		"SMOKE_CHROME="+chrome,
		"SMOKE_URL="+ts.URL+"/ui/books/"+bookID+"/read",
		"SMOKE_COOKIE="+cookie.Name+"="+cookie.Value,
		"SMOKE_HOST="+strings.TrimPrefix(ts.URL, "http://"),
		"SMOKE_AUDIO_PANEL=1",
		"SMOKE_SHOT="+os.Getenv("LISEUR_READER_SCREENSHOT"),
	)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("audiobook panel browser check: %v", err)
	}
}

func TestReaderNarrationFollowsInARealBrowser(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("no chromium; set LISEUR_CHROME to run the browser check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node to drive the browser with")
	}
	for _, tc := range []struct {
		name, direction, lang, body string
	}{
		{"rtl", "rtl", "ar", strings.Repeat("<p>هذا نص عربي طويل للقراءة مع الكتاب الصوتي.</p>", 40)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parallelBrowser(t)
			var buf bytes.Buffer
			w := zip.NewWriter(&buf)
			mimetype, err := w.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(mimetype, "application/epub+zip"); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{
				"META-INF/container.xml": `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles>` +
					`<rootfile full-path="book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
				"book.opf": `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">` +
					`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">narration</dc:identifier>` +
					`<dc:title>Narration</dc:title><dc:language>` + tc.lang + `</dc:language></metadata>` +
					`<manifest><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest>` +
					`<spine page-progression-direction="` + tc.direction + `"><itemref idref="chapter"/></spine></package>`,
				"chapter.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml" dir="` + tc.direction + `" lang="` + tc.lang + `">` +
					`<head><title>Narration</title></head><body>` + tc.body + `</body></html>`,
			} {
				part, err := w.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(part, body); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			f := newBooksFixture(t)
			bookID := f.addBook(t, tc.name, buf.Bytes())
			ts := httptest.NewUnstartedServer(nil)
			wholeServer(t, f, ts, "")
			cookie := f.loginTo(t, ts, "alice")
			cmd := exec.Command(node, filepath.Join("testdata", "readerbrowser.mjs"))
			cmd.Env = append(os.Environ(),
				"SMOKE_CHROME="+chrome,
				"SMOKE_URL="+ts.URL+"/ui/books/"+bookID+"/read",
				"SMOKE_COOKIE="+cookie.Name+"="+cookie.Value,
				"SMOKE_HOST="+strings.TrimPrefix(ts.URL, "http://"),
				"SMOKE_NARRATION="+tc.name,
			)
			out, err := cmd.CombinedOutput()
			t.Logf("%s", out)
			if err != nil {
				t.Fatalf("narration following browser check: %v", err)
			}
		})
	}
}
