//go:build linux

package content

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

func buildTestEPUBWithChapters(t *testing.T, title, author string, ch1Text, ch2Text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeFile := func(name, body string) {
		method := zip.Deflate
		if name == "mimetype" {
			method = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("mimetype", "application/epub+zip")
	writeFile("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)
	writeFile("OEBPS/content.opf", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:uuid:12345</dc:identifier>
    <dc:title>%s</dc:title>
    <dc:creator>%s</dc:creator>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>`, title, author))
	writeFile("OEBPS/nav.xhtml", `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body>
  <nav epub:type="toc">
    <ol>
      <li><a href="ch1.xhtml">Chapter 1</a></li>
      <li><a href="ch2.xhtml">Chapter 2</a></li>
    </ol>
  </nav>
</body>
</html>`)
	writeFile("OEBPS/ch1.xhtml", `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><body><p>`+ch1Text+`</p></body></html>`)
	writeFile("OEBPS/ch2.xhtml", `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><body><p>`+ch2Text+`</p></body></html>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildTestM4B(title, author string, durationSec uint32) []byte {
	box := func(typ string, payload []byte) []byte {
		out := make([]byte, 8+len(payload))
		binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
		copy(out[4:8], typ)
		copy(out[8:], payload)
		return out
	}
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)
	binary.BigEndian.PutUint32(mvhd[16:20], durationSec*1000)

	// chpl with 2 chapters: Chapter 1 (0..duration/2), Chapter 2 (duration/2..duration)
	var chpl bytes.Buffer
	chpl.Write([]byte{1, 0, 0, 0, 0, 0, 0, 0})
	chpl.WriteByte(2)
	writeCh := func(startSec uint32, name string) {
		var ts [8]byte
		binary.BigEndian.PutUint64(ts[:], uint64(startSec)*10_000_000)
		chpl.Write(ts[:])
		chpl.WriteByte(byte(len(name)))
		chpl.WriteString(name)
	}
	writeCh(0, "Chapter 1")
	writeCh(durationSec/2, "Chapter 2")

	textTag := func(tag, val string) []byte {
		dataPayload := append([]byte{0, 0, 0, 1, 0, 0, 0, 0}, []byte(val)...)
		return box(tag, box("data", dataPayload))
	}
	ilst := box("ilst", append(textTag("\xa9nam", title), textTag("\xa9ART", author)...))
	metaPayload := append([]byte{0, 0, 0, 0}, ilst...)
	udta := box("udta", append(box("meta", metaPayload), box("chpl", chpl.Bytes())...))
	moov := box("moov", append(box("mvhd", mvhd), udta...))
	return append(box("ftyp", []byte("M4A \x00\x00\x00\x00")), moov...)
}

func TestPlainAudiobookReconcileCueAndAutoAlign(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	booksDir := t.TempDir()
	audioDir := t.TempDir()
	now := time.Date(2026, time.April, 12, 10, 0, 0, 0, time.UTC)

	user := store.User{ID: "u1", Name: "reader", Argon2Hash: "x", CreatedAt: now}
	if err := st.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	fBooks := store.Folder{ID: "f-books", Name: "Books", RootPath: booksDir, Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now}
	fAudio := store.Folder{ID: "f-audio", Name: "Audio", RootPath: audioDir, Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateFolder(ctx, fBooks); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateFolder(ctx, fAudio); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignUserFolder(ctx, user.ID, fBooks.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignUserFolder(ctx, user.ID, fAudio.ID); err != nil {
		t.Fatal(err)
	}

	epubBytes := buildTestEPUBWithChapters(t, "Shadows Upon Time", "Christopher Ruocchio",
		"First chapter text of Shadows Upon Time.", "Second chapter text of Shadows Upon Time.")
	if err := os.WriteFile(filepath.Join(booksDir, "shadows.epub"), epubBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	m4bBytes := buildTestM4B("Sun Eater 07 - Shadows Upon Time", "Christopher Ruocchio", 1200)
	if err := os.WriteFile(filepath.Join(audioDir, "shadows.m4b"), m4bBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	rec := NewReconciler(st, ScanLimits{}, epub.DefaultLimits(), slog.Default())
	if _, err := rec.Reconcile(ctx, fBooks); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Reconcile(ctx, fAudio); err != nil {
		t.Fatal(err)
	}

	books, err := st.ListCatalogBooks(ctx, user.ID, fBooks.ID, nil, 10)
	if err != nil || len(books) != 1 {
		t.Fatalf("expected 1 book, got %+v %v", books, err)
	}
	link, ab, err := st.BookAudiobookByBookID(ctx, user.ID, books[0].ID)
	if err != nil {
		t.Fatalf("expected auto-linked audiobook, got err: %v", err)
	}
	if link.State != store.BookAudiobookActive || link.Source != "title_author" {
		t.Fatalf("expected active title_author link, got %+v", link)
	}
	if ab.DurationSeconds != 1200 {
		t.Fatalf("expected duration 1200, got %v", ab.DurationSeconds)
	}

	// Simulate a transient EPUB read failure during a subsequent audio folder scan:
	// the existing active book_audiobooks link must be preserved rather than downgraded to unmapped.
	epubPath := filepath.Join(booksDir, "shadows.epub")
	if err := os.Chmod(epubPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(epubPath, 0o644); err != nil {
			t.Error(err)
		}
	})

	// Touch the .cue so the mapping key changes while the EPUB is unreadable.
	cueContent := "FILE \"shadows.m4b\" MP3\n  TRACK 01 AUDIO\n    TITLE \"Chapter 1\"\n    INDEX 01 00:00:00\n  TRACK 02 AUDIO\n    TITLE \"Chapter 2\"\n    INDEX 01 08:00:00\n"
	if err := os.WriteFile(filepath.Join(audioDir, "shadows.cue"), []byte(cueContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Reconcile(ctx, fAudio); err != nil {
		t.Fatal(err)
	}
	linkAfterTransient, _, err := st.BookAudiobookByBookID(ctx, user.ID, books[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if linkAfterTransient.State != store.BookAudiobookActive {
		t.Fatalf("expected active mapping to survive transient EPUB read error, got %+v", linkAfterTransient)
	}

	// Restore EPUB readability and confirm the next pass updates the mapping key cleanly.
	if err := os.Chmod(epubPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Reconcile(ctx, fAudio); err != nil {
		t.Fatal(err)
	}
	linkRecovered, _, err := st.BookAudiobookByBookID(ctx, user.ID, books[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if linkRecovered.State != store.BookAudiobookActive || linkRecovered.MappingKey == link.MappingKey {
		t.Fatalf("expected mapping key to update after EPUB read recovery, got %+v (old key %q)", linkRecovered, link.MappingKey)
	}
}
