package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
)

const coverManifest = `
<package xmlns="http://www.idpf.org/2007/opf"
 xmlns:dc="http://purl.org/dc/elements/1.1/">
 <metadata><dc:title>Book</dc:title></metadata>
 <manifest>
  <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"
    properties="nav"/>
  <item id="cover" href="cover.jpg" media-type="image/jpeg"
    properties="cover-image"/>
 </manifest>
</package>`

func readCoverBytes(t *testing.T, data []byte, maxBytes int64) (CoverImage, error) {
	t.Helper()
	return ReadCover(context.Background(), bytes.NewReader(data),
		int64(len(data)), DefaultLimits(), maxBytes)
}

func TestReadCoverReturnsTheDeclaredImage(t *testing.T) {
	data := epubWithPackage(t, coverManifest, zipEntry{
		name: "OPS/cover.jpg", body: "the-jpeg-bytes", method: zip.Deflate,
	})
	cover, err := readCoverBytes(t, data, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if cover.Path != "OPS/cover.jpg" || cover.MediaType != "image/jpeg" ||
		string(cover.Data) != "the-jpeg-bytes" {
		t.Fatalf("cover = %+v", cover)
	}
}

// TestReadCoverIsNotAValidationPass: the caller is serving a thumbnail for
// a blob that was validated before it was promoted. Re-reading every entry
// to produce one image would make a cover cost as much as the book.
func TestReadCoverDoesNotReadTheWholeArchive(t *testing.T) {
	// An entry that would fail Validate's ratio and size checks, but that
	// a cover read has no reason to touch.
	data := epubWithPackage(t, coverManifest,
		zipEntry{name: "OPS/cover.jpg", body: "jpeg", method: zip.Deflate},
		zipEntry{
			name:   "OPS/huge.txt",
			body:   strings.Repeat("a", 4<<20),
			method: zip.Deflate,
		})
	limits := DefaultLimits()
	limits.MaxUncompressedBytes = 64 << 10
	cover, err := ReadCover(context.Background(), bytes.NewReader(data),
		int64(len(data)), limits, 1<<20)
	if err != nil {
		t.Fatalf("reading a cover next to a large entry: %v", err)
	}
	if string(cover.Data) != "jpeg" {
		t.Fatalf("cover data = %q", cover.Data)
	}
}

// TestReadCoverWithoutACover: a book with no cover is a perfectly good
// book. Reporting it as a validation failure would make the caller
// quarantine content that is fine.
func TestReadCoverWithoutACover(t *testing.T) {
	data := makeEPUB(t, validEntries()...)
	if _, err := readCoverBytes(t, data, 1<<20); !errors.Is(err, ErrNoCover) {
		t.Fatalf("err = %v, want ErrNoCover", err)
	}
}

// TestReadCoverWhenTheCoverIsMissing: a manifest may name an entry the
// archive does not contain. That is the same outcome as having no cover,
// not a reason to refuse the book.
func TestReadCoverWhenTheCoverIsMissing(t *testing.T) {
	data := epubWithPackage(t, coverManifest)
	if _, err := readCoverBytes(t, data, 1<<20); !errors.Is(err, ErrNoCover) {
		t.Fatalf("err = %v, want ErrNoCover", err)
	}
}

// TestReadCoverRefusesAnOversizedCover: the cap is what stops one archive
// entry from deciding how much memory the server spends on a thumbnail.
func TestReadCoverRefusesAnOversizedCover(t *testing.T) {
	data := epubWithPackage(t, coverManifest, zipEntry{
		name: "OPS/cover.jpg", body: strings.Repeat("x", 4096),
		method: zip.Deflate,
	})
	_, err := readCoverBytes(t, data, 1024)
	requireCode(t, err, CodeArchiveLimits)
}

// TestReadCoverRefusesAnUnsafePath: the cover path comes from the archive,
// so it is exactly as trustworthy as the rest of it.
func TestReadCoverRefusesAnUnsafePath(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range validEntries() {
		header := &zip.FileHeader{Name: entry.name, Method: entry.method}
		target, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	header := &zip.FileHeader{Name: "../escape.jpg", Method: zip.Store}
	target, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Write([]byte("jpeg")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = readCoverBytes(t, buffer.Bytes(), 1<<20)
	if err == nil {
		t.Fatal("an escaping entry was accepted")
	}
	if code, ok := ErrorCode(err); !ok || code != CodeUnsafeArchive {
		t.Fatalf("err = %v, want an unsafe-archive failure", err)
	}
}

// TestReadCoverRefusesTooManyEntries: the entry map is built from the
// archive's own directory, so its size is chosen by whoever wrote the
// file. The bound is what stops one upload from deciding how much memory
// a thumbnail request costs.
func TestReadCoverRefusesTooManyEntries(t *testing.T) {
	entries := validEntries()
	for i := range 64 {
		entries = append(entries, zipEntry{
			name: fmt.Sprintf("OPS/pad-%03d.txt", i), body: "x", method: zip.Store,
		})
	}
	data := makeEPUB(t, entries...)
	limits := DefaultLimits()
	limits.MaxEntries = 8
	_, err := ReadCover(context.Background(), bytes.NewReader(data),
		int64(len(data)), limits, 1<<20)
	requireCode(t, err, CodeArchiveLimits)
}

func TestReadCoverRejectsInvalidInput(t *testing.T) {
	data := makeEPUB(t, validEntries()...)
	for _, tc := range []struct {
		name string
		max  int64
	}{{"zero", 0}, {"negative", -1}} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readCoverBytes(t, data, tc.max)
			if !errors.Is(err, ErrInvalidValidationInput) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if _, err := ReadCover(context.Background(), nil, 0,
		DefaultLimits(), 1<<20); !errors.Is(err, ErrInvalidValidationInput) {
		t.Fatalf("nil reader: %v", err)
	}
}

// cancellingReaderAt cancels once a read reaches a given offset, which is
// how the test gets between the archive's small control documents and the
// cover entry itself. Without it, cancellation is always caught earlier,
// while reading container.xml, and the read of the cover — the only part
// that can be large — would go untested.
type cancellingReaderAt struct {
	inner      io.ReaderAt
	start, end int64
	cancel     func()
}

func (c cancellingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	// Only the first bytes of the cover. The window has to be this
	// narrow: zip.NewReader scans the last 64 KiB of the file for the
	// central directory, so anything wider fires during parsing and the
	// test would pass without the read below ever being reached.
	if off >= c.start && off < c.end {
		c.cancel()
	}
	return c.inner.ReadAt(p, off)
}

// TestReadCoverStopsWhileReadingTheCover: the cover is the one entry that
// can be megabytes, so it is the one read that has to notice a client
// that has gone away.
func TestReadCoverStopsWhileReadingTheCover(t *testing.T) {
	data := epubWithPackage(t, coverManifest, zipEntry{
		name: "OPS/cover.jpg", body: strings.Repeat("j", 1<<20), method: zip.Store,
	})
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var coverOffset int64
	for _, file := range archive.File {
		if file.Name == "OPS/cover.jpg" {
			if coverOffset, err = file.DataOffset(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if coverOffset == 0 {
		t.Fatal("cover entry not found")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := cancellingReaderAt{
		inner: bytes.NewReader(data), start: coverOffset,
		end: coverOffset + 4096, cancel: cancel,
	}
	_, err = ReadCover(ctx, reader, int64(len(data)), DefaultLimits(), 4<<20)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestReadCoverBoundsWhatItReadsIntoMemory: the cap has to apply to the
// bytes coming out of the decompressor, not to the size the archive
// claims. A refused cover that was fully expanded first has already cost
// the memory the cap exists to deny.
func TestReadCoverBoundsWhatItReadsIntoMemory(t *testing.T) {
	const huge = 64 << 20
	data := epubWithPackage(t, coverManifest, zipEntry{
		// Zeros deflate to almost nothing, so the archive stays small
		// while the entry expands to 64 MiB.
		name: "OPS/cover.jpg", body: strings.Repeat("\x00", huge),
		method: zip.Deflate,
	})
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := readCoverBytes(t, data, 1<<20); err == nil {
		t.Fatal("an oversized cover was accepted")
	}
	runtime.ReadMemStats(&after)
	// TotalAlloc only grows, so this is not a statement about the
	// collector: reading the whole entry would have to allocate its
	// 64 MiB somewhere.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > huge/2 {
		t.Fatalf("refusing a %d-byte cover allocated %d bytes", huge, allocated)
	}
}

// TestReadCoverStopsOnACancelledContext: a client that gave up should not
// keep the server reading an archive on its behalf.
func TestReadCoverStopsOnACancelledContext(t *testing.T) {
	data := epubWithPackage(t, coverManifest, zipEntry{
		name: "OPS/cover.jpg", body: "jpeg", method: zip.Deflate,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReadCover(ctx, bytes.NewReader(data), int64(len(data)),
		DefaultLimits(), 1<<20)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
