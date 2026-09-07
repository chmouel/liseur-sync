package epub

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	readium "github.com/readium/go-toolkit/pkg/parser/epub"
)

const readerPackage = `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Reader</dc:title><dc:identifier id="uid">urn:uuid:test</dc:identifier><dc:language>en</dc:language></metadata>
<manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="one" href="one.xhtml" media-type="application/xhtml+xml"/>
<item id="two" href="two.xhtml" media-type="application/xhtml+xml"/>
<item id="late" href="late.bin" media-type="image/png"/></manifest>
<spine><itemref idref="one"/><itemref idref="nav" linear="no"/><itemref idref="two"/></spine></package>`

func readerArchive(t *testing.T, pkg string, extra ...zipEntry) []byte {
	t.Helper()
	entries := validEntries()[:4]
	entries[2].body = pkg
	entries = append(entries,
		zipEntry{name: "OPS/one.xhtml", body: `<html xmlns="http://www.w3.org/1999/xhtml"><head/><body>First chapter</body></html>`, method: zip.Deflate},
		zipEntry{name: "OPS/two.xhtml", body: `<html xmlns="http://www.w3.org/1999/xhtml"><head/><body>Second chapter</body></html>`, method: zip.Deflate},
		zipEntry{name: "OPS/late.bin", body: strings.Repeat("x", 8<<20), method: zip.Store})
	return makeEPUB(t, append(entries, extra...)...)
}

// Forbid reads into the late resource. The ZIP directory and local headers
// remain readable, so this detects eager decompression rather than ZIP indexing.
type guardedArchive struct {
	*bytes.Reader
	start, end int64
}

func (r guardedArchive) ReadAt(p []byte, off int64) (int, error) {
	if off < r.end && off+int64(len(p)) > r.start {
		return 0, fmt.Errorf("eager read of late resource at %d", off)
	}
	return r.Reader.ReadAt(p, off)
}

func TestPublicationOpensWithoutReadingChapterBytes(t *testing.T) {
	data := readerArchive(t, readerPackage)
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var late *zip.File
	for _, f := range z.File {
		if f.Name == "OPS/late.bin" {
			late = f
		}
	}
	start, err := late.DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	// zip.NewReader probes the last 65 KiB for EOCD; leave that tail readable.
	guard := guardedArchive{bytes.NewReader(data), start, start + int64(late.CompressedSize64) - (128 << 10)}
	p, err := OpenPublication(t.Context(), guard, int64(len(data)), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	positions := p.Positions(t.Context())
	if len(positions) != 2 || positions[1].Href.String() != "OPS/two.xhtml" {
		t.Fatalf("positions = %+v", positions)
	}
	first, err := p.Resource(t.Context(), "OPS/one.xhtml")
	if err != nil || !bytes.Contains(first, []byte("First chapter")) {
		t.Fatalf("first chapter: %s %v", first, err)
	}
	if _, err := p.Resource(t.Context(), "OPS/late.bin"); err == nil {
		t.Fatal("late resource was not read on demand")
	}
}

func TestPublicationResourcesAreBoundedAndDeclared(t *testing.T) {
	data := readerArchive(t, readerPackage, zipEntry{name: "private.txt", body: "not declared"})
	p, err := OpenPublication(t.Context(), bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, name := range []string{"../OPS/one.xhtml", "/OPS/one.xhtml", "private.txt", "https://example.org/one.xhtml"} {
		if _, err := p.Resource(t.Context(), name); err == nil {
			t.Errorf("served %q", name)
		}
	}
	limits := DefaultLimits()
	limits.MaxEntryBytes = 1 << 20
	if _, err := OpenPublication(t.Context(), bytes.NewReader(data), int64(len(data)), limits); err == nil {
		t.Fatal("oversized entry accepted")
	}
}

func TestPublicationRefusesRemoteAndDuplicateEntries(t *testing.T) {
	for _, tt := range []struct {
		name, pkg string
		extra     []zipEntry
	}{
		{"remote", strings.ReplaceAll(readerPackage, `href="late.bin"`, `href="https://example.org/late.bin"`), nil},
		{"duplicate", readerPackage, []zipEntry{{name: "OPS/one.xhtml", body: "duplicate"}}},
		{"drm", readerPackage, []zipEntry{{name: "META-INF/rights.xml", body: "<rights/>"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := readerArchive(t, tt.pkg, tt.extra...)
			if _, err := OpenPublication(t.Context(), bytes.NewReader(data), int64(len(data)), DefaultLimits()); err == nil {
				t.Fatal("unsafe publication accepted")
			}
		})
	}
}

func TestPublicationPositionsUseStoredLengths(t *testing.T) {
	// Feed the same fourteen stored lengths as ADR-0032 to the toolkit's
	// strategy without manufacturing or decompressing a publication.
	lengths := []uint64{392, 1456, 325, 858, 321, 117517, 134039, 321, 72116, 108814, 119685, 26728, 1270, 521}
	var total uint
	for _, length := range lengths {
		r := &publicationResource{file: &zip.File{FileHeader: zip.FileHeader{CompressedSize64: length}}}
		total += readium.ArchiveEntryLength{PageLength: 1024}.PositionCount(r)
	}
	if total != 578 {
		t.Fatalf("positions = %d, want 578", total)
	}

}

var _ io.ReaderAt = guardedArchive{}
