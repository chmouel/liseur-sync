package webui

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReaderJavaScript runs the reader's own test suite.
//
// The renderer is written by hand precisely so that the repository
// needs no JavaScript build step, and testing it must not smuggle one
// back in: this drives node over a plain script, against an archive
// built here, and skips when node is absent.
func TestReaderJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; skipping the reader's JavaScript tests")
	}
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, readerTestEPUB(t), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, filepath.Join("testdata", "reader_test.cjs"), path).CombinedOutput()
	t.Logf("reader.js:\n%s", out)
	if err != nil {
		t.Fatalf("reader JavaScript tests failed: %v", err)
	}
	if !strings.Contains(string(out), "ok   - ") {
		t.Fatal("reader JavaScript tests reported nothing; did the suite run?")
	}
}

// readerTestEPUB builds an archive shaped like a real EPUB: an
// uncompressed mimetype entry first, everything else deflated. The two
// storage methods are the point — a reader that only handles one opens
// half the books in the world.
func readerTestEPUB(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	stored, err := w.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stored.Write([]byte("application/epub+zip")); err != nil {
		t.Fatal(err)
	}

	// Long enough that deflate actually compresses, so the test would
	// notice an inflate that silently returned the first block only.
	body := strings.Repeat(
		"<p>Call me Ishmael. Some years ago—never mind how long precisely—"+
			"having little or no money in my purse, I thought I would sail about.</p>\n", 40)

	for name, content := range map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Moby-Dick</dc:title></metadata>
  <manifest>
    <item id="c1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="chapter2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`,
		"OEBPS/chapter1.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body>` + body + `</body></html>`,
		"OEBPS/chapter2.xhtml": `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body><p>The Carpet-Bag.</p></body></html>`,
	} {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
