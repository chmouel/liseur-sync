package audiobookshelf

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/m4b"
	"github.com/chmouel/liseur-sync/internal/store"
)

func TestRefinePositionWithSTTRequiresReadableEPUBText(t *testing.T) {
	const transcript = "the travelers reached the ancient bridge where the emerald river curved"
	for _, tc := range []struct {
		name    string
		href    string
		chapter string
		missing bool
		wantSTT bool
	}{
		{name: "missing file", href: "chapter.xhtml", missing: true},
		{name: "missing resource", href: "missing.xhtml", chapter: "<html><body>Some text</body></html>"},
		{name: "empty resource", href: "chapter.xhtml"},
		{name: "empty text", href: "chapter.xhtml", chapter: `<html><head><title>Chapter</title></head><body> </body></html>`},
		{name: "readable text", href: "chapter.xhtml", chapter: "<html><body><p>" + transcript + "</p><p>Evening fell.</p></body></html>", wantSTT: true},
		{name: "resource from position", chapter: "<html><body><p>" + transcript + "</p><p>Evening fell.</p></body></html>", wantSTT: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			folder := f.folder
			folder.ID, folder.RootPath = "f-stt", t.TempDir()
			if err := f.st.CreateFolder(t.Context(), folder); err != nil {
				t.Fatal(err)
			}
			if err := f.st.AssignUserFolder(t.Context(), f.user.ID, folder.ID); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			for name, body := range map[string]string{
				"mimetype":               "application/epub+zip",
				"META-INF/container.xml": `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
				"content.opf":            `<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">stt-test</dc:identifier><dc:title>Test</dc:title><dc:language>en</dc:language></metadata><manifest><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="chapter"/></spine></package>`,
				"chapter.xhtml":          tc.chapter,
			} {
				w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(w, body); err != nil {
					t.Fatal(err)
				}
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(folder.RootPath, "book.epub")
			if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.st.ReconcileFolder(t.Context(), folder.ID, []store.ObservedBook{{
				RelativePath: "book.epub", ContentSHA256: fmt.Sprintf("%064x", 1),
				SizeBytes: info.Size(), MTime: info.ModTime(), Title: "Test", MediaType: "application/epub+zip",
			}}, true, f.now); err != nil {
				t.Fatal(err)
			}
			books, err := f.st.ListCatalogBooks(t.Context(), f.user.ID, folder.ID, nil, 10)
			if err != nil || len(books) != 1 {
				t.Fatalf("catalog books: %v, %v", books, err)
			}
			if tc.missing {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			var sttCalls, audioCalls atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/audio/transcriptions" {
					sttCalls.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]string{"text": transcript})
					return
				}
				audioCalls.Add(1)
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(bytes.Repeat([]byte("a"), 100))
			})
			svc := &Service{Store: f.st, Client: client, Cfg: config.AudiobookshelfConfig{STTURL: client.baseURL}}
			item := Item{ID: "audio", Duration: 100, AudioFiles: []AudioFile{{Ino: "file", Duration: 100, Size: 1000, Ext: ".mp3"}}}
			seg := Segment{AudioStart: 0, AudioEnd: 100, BookStart: 0, BookEnd: 1, Href: tc.href}
			mapping := Mapping{Segments: []Segment{seg}, Resources: []Resource{{Href: "chapter.xhtml", Start: 0, End: 1}}}
			got := svc.refinePositionWithSTT(t.Context(), f.user.ID, store.AudiobookLink{BookID: books[0].ID}, item, mapping, 50, 0.5, seg)
			wantCalls := int32(0)
			if tc.wantSTT {
				wantCalls = 1
			}
			if sttCalls.Load() != wantCalls || audioCalls.Load() != wantCalls {
				t.Fatalf("STT calls = %d, audio calls = %d; want %d each", sttCalls.Load(), audioCalls.Load(), wantCalls)
			}
			if (got != 0.5) != tc.wantSTT {
				t.Fatalf("progression = %v, want refinement = %v", got, tc.wantSTT)
			}
		})
	}
}

func testMakeBox(typ string, fn func(*bytes.Buffer)) []byte {
	var body bytes.Buffer
	fn(&body)
	var out bytes.Buffer
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], uint32(8+body.Len()))
	out.Write(tmp[:])
	out.WriteString(typ)
	out.Write(body.Bytes())
	return out.Bytes()
}

func testMakeFullBox(typ string, version byte, flags uint32, fn func(*bytes.Buffer)) []byte {
	return testMakeBox(typ, func(b *bytes.Buffer) {
		b.WriteByte(version)
		b.WriteByte(byte(flags >> 16))
		b.WriteByte(byte(flags >> 8))
		b.WriteByte(byte(flags))
		fn(b)
	})
}

func testPutU32(b *bytes.Buffer, v uint32) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	b.Write(tmp[:])
}

func testPutU16(b *bytes.Buffer, v uint16) {
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], v)
	b.Write(tmp[:])
}

func TestSliceAudioSnippetM4BAndRoundTrip(t *testing.T) {
	stsd := testMakeFullBox("stsd", 0, 0, func(b *bytes.Buffer) {
		testPutU32(b, 1)
		b.Write(testMakeBox("mp4a", func(m *bytes.Buffer) {
			m.Write(make([]byte, 28))
		}))
	})
	var mdatPayload bytes.Buffer
	for i := 0; i < 10; i++ {
		mdatPayload.Write(bytes.Repeat([]byte{byte('A' + i)}, 32))
	}
	ftyp := testMakeBox("ftyp", func(b *bytes.Buffer) {
		b.WriteString("M4A ")
		testPutU32(b, 0x200)
		b.WriteString("M4A isommp42")
	})
	buildMoov := func(chunkOffset uint32) []byte {
		mvhd := testMakeFullBox("mvhd", 0, 0, func(b *bytes.Buffer) {
			testPutU32(b, 0)
			testPutU32(b, 0)
			testPutU32(b, 1000)
			testPutU32(b, 10000)
			testPutU32(b, 0x00010000)
			testPutU16(b, 0x0100)
			b.Write(make([]byte, 10))
			for _, v := range []uint32{0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000} {
				testPutU32(b, v)
			}
			b.Write(make([]byte, 24))
			testPutU32(b, 2)
		})
		mdhd := testMakeFullBox("mdhd", 0, 0, func(b *bytes.Buffer) {
			testPutU32(b, 0)
			testPutU32(b, 0)
			testPutU32(b, 1000)
			testPutU32(b, 10000)
			testPutU16(b, 0x55c4)
			testPutU16(b, 0)
		})
		hdlr := testMakeFullBox("hdlr", 0, 0, func(b *bytes.Buffer) {
			testPutU32(b, 0)
			b.WriteString("soun")
			b.Write(make([]byte, 12))
			b.WriteString("SoundHandler\x00")
		})
		stts := testMakeFullBox("stts", 0, 0, func(b *bytes.Buffer) {
			testPutU32(b, 1)
			testPutU32(b, 10)
			testPutU32(b, 1000)
		})
		stsc := testMakeFullBox("stsc", 0, 0, func(b *bytes.Buffer) {
			testPutU32(b, 1)
			testPutU32(b, 1)
			testPutU32(b, 10)
			testPutU32(b, 1)
		})
		stsz := testMakeFullBox("stsz", 0, 0, func(b *bytes.Buffer) {
			testPutU32(b, 0)
			testPutU32(b, 10)
			for i := 0; i < 10; i++ {
				testPutU32(b, 32)
			}
		})
		stco := testMakeFullBox("stco", 0, 0, func(b *bytes.Buffer) {
			testPutU32(b, 1)
			testPutU32(b, chunkOffset)
		})
		stbl := testMakeBox("stbl", func(b *bytes.Buffer) {
			b.Write(stsd)
			b.Write(stts)
			b.Write(stsc)
			b.Write(stsz)
			b.Write(stco)
		})
		minf := testMakeBox("minf", func(b *bytes.Buffer) { b.Write(stbl) })
		mdia := testMakeBox("mdia", func(b *bytes.Buffer) {
			b.Write(mdhd)
			b.Write(hdlr)
			b.Write(minf)
		})
		trak := testMakeBox("trak", func(b *bytes.Buffer) { b.Write(mdia) })
		return testMakeBox("moov", func(b *bytes.Buffer) {
			b.Write(mvhd)
			b.Write(trak)
		})
	}
	moov0 := buildMoov(0)
	moov := buildMoov(uint32(len(ftyp) + len(moov0) + 8))
	mdat := testMakeBox("mdat", func(b *bytes.Buffer) { b.Write(mdatPayload.Bytes()) })
	var fullBuf bytes.Buffer
	fullBuf.Write(ftyp)
	fullBuf.Write(moov)
	fullBuf.Write(mdat)
	fullM4B := fullBuf.Bytes()

	item := Item{
		ID:       "item-1",
		Duration: 10,
		AudioFiles: []AudioFile{{
			Ino:      "ino-1",
			Duration: 10,
			MimeType: "audio/mp4",
			Size:     int64(len(fullM4B)),
			Ext:      ".m4b",
		}},
	}
	reader := func(_ context.Context, itemID, ino string, start, end int64) ([]byte, error) {
		if itemID != "item-1" || ino != "ino-1" {
			t.Fatalf("unexpected target %s/%s", itemID, ino)
		}
		return fullM4B[start : end+1], nil
	}

	clip, name, err := SliceAudioSnippet(context.Background(), reader, item, 2.0, 5.0)
	if err != nil {
		t.Fatalf("SliceAudioSnippet failed: %v", err)
	}
	if name != "snippet.m4a" || len(clip) == 0 {
		t.Fatalf("unexpected clip %q (%d bytes)", name, len(clip))
	}

	meta, err := m4b.ReadMetadata(context.Background(), m4b.SeekerRangeReader(bytes.NewReader(clip), int64(len(clip))), int64(len(clip)))
	if err != nil {
		t.Fatalf("m4b.ReadMetadata(clip) failed: %v", err)
	}
	if meta.Duration != 3.0 {
		t.Fatalf("clip duration = %v, want 3.0", meta.Duration)
	}
}

func TestMatchTranscriptInXHTML(t *testing.T) {
	doc := []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter 37: Dissolution</title><style>body { color: black; }</style></head>
<body>
<h1>Chapitre 37</h1>
<p>Nous restons assis un long moment dans l'obscurité de la chapelle tandis que les cloches résonnent au loin.</p>
<p>Valka pose doucement sa main sur mon épaule et murmure que la flotte impériale approche enfin de l'orbite haute.</p>
<p>Les gardes ouvrent les portes de bronze et la lumière froide des étoiles inonde le couloir silencieux.</p>
</body>
</html>`)

	// Transcript matches the middle paragraph (with slight Whisper punctuation/accent differences).
	frac, conf, ok := MatchTranscriptInXHTML(doc, "valka pose doucement sa main sur mon epaule et murmure que la flotte imperiale approche enfin")
	if !ok {
		t.Fatalf("expected transcript match, got ok=false (conf=%v)", conf)
	}
	if frac < 0.45 || frac > 0.75 {
		t.Fatalf("expected fraction in middle paragraph (0.45..0.75), got %v (conf=%v)", frac, conf)
	}

	// Unrelated transcript should be rejected.
	if _, _, ok := MatchTranscriptInXHTML(doc, "completely unrelated words from another book entirely"); ok {
		t.Fatal("expected unrelated transcript to fail matching")
	}
}

func TestTranscribeAudioOpenAICompatible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("unexpected auth header %q", got)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		if got := r.FormValue("model"); got != "whisper-large-v3" {
			t.Errorf("model = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"text": "The ships hung in the sky in much the same way that bricks do not.",
		})
	}))
	defer srv.Close()

	txt, err := TranscribeAudio(context.Background(), srv.URL, "test-secret", "whisper-large-v3", []byte("fake-audio"), "snippet.m4a")
	if err != nil {
		t.Fatalf("TranscribeAudio failed: %v", err)
	}
	if txt != "The ships hung in the sky in much the same way that bricks do not." {
		t.Fatalf("unexpected transcript %q", txt)
	}
}

func TestToAudioLocatorWithXHTMLTextContext(t *testing.T) {
	doc := []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<body>
<p>I am all right, Selene, I said, not meaning to use her name. I am only tired.</p>
<p>I would have to tell them . . . somehow.</p>
<p>A small legion of identical women in black dresses waited in the corridor beyond the door, their hands folded in silence as the ship hummed around us.</p>
<p>We trust that everything is to your liking, asked the woman who was Naya and was not. If you require anything, you need only ring the bell beside the bed.</p>
</body>
</html>`)

	m := Mapping{
		Duration: 90000,
		Resources: []Resource{
			{Href: "text/part0060.html", Start: 0.7181818, End: 0.7350649},
		},
		Segments: []Segment{
			{
				Label:      "Chapter 51",
				Href:       "text/part0060.html",
				AudioStart: 83090.085,
				AudioEnd:   85154.55,
				BookStart:  0.7181818,
				BookEnd:    0.7350649,
			},
		},
	}

	locJSON := []byte(`{
		"href": "text/part0060.html",
		"locations": {"progression": 0.25, "totalProgression": 0.7220654},
		"text": {"before": "I would have to tell ", "highlight": "them", "after": " . . . somehow."}
	}`)

	at, mapped, usedLocator := m.ToAudioLocator(0.7220654, locJSON, func(href string) ([]byte, error) {
		if href == "text/part0060.html" {
			return doc, nil
		}
		return nil, nil
	})
	if !mapped || !usedLocator {
		t.Fatalf("ToAudioLocator: mapped=%v usedLocator=%v", mapped, usedLocator)
	}
	// The sentence is near the first third of the document (~0.24..0.32), not at 0.25*2064 if text refines it.
	if at <= 83090.085 || at >= 83800 {
		t.Fatalf("unexpected audio time %v", at)
	}
}
