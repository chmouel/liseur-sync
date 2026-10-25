package api

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/audiomap"
	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

func buildTestAudioEPUB(t *testing.T, title, author, ch1XHTML, ch2XHTML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeEntry := func(name, body string, method uint16) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
	}
	writeEntry("mimetype", "application/epub+zip", zip.Store)
	writeEntry("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`, zip.Deflate)
	writeEntry("OEBPS/content.opf", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:uuid:audio-test-1</dc:identifier>
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
</package>`, title, author), zip.Deflate)
	writeEntry("OEBPS/nav.xhtml", `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body>
  <nav epub:type="toc">
    <ol>
      <li><a href="ch1.xhtml">Chapter 1</a></li>
      <li><a href="ch2.xhtml">Chapter 2</a></li>
    </ol>
  </nav>
</body>
</html>`, zip.Deflate)
	writeEntry("OEBPS/ch1.xhtml", ch1XHTML, zip.Deflate)
	writeEntry("OEBPS/ch2.xhtml", ch2XHTML, zip.Deflate)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildSliceableTestM4B(title, author string) []byte {
	box := func(typ string, payload []byte) []byte {
		out := make([]byte, 8+len(payload))
		binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
		copy(out[4:8], typ)
		copy(out[8:], payload)
		return out
	}

	// 120 samples, 1 sample/sec (timescale=1000, delta=1000), 64 bytes/sample.
	numSamples := 120
	sampleSize := 64
	mdatPayload := make([]byte, numSamples*sampleSize)
	for i := range mdatPayload {
		mdatPayload[i] = byte((i % 251) + 1)
	}
	ftyp := box("ftyp", []byte("M4A \x00\x00\x00\x00"))
	mdatOffset := 8 + len(ftyp)

	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)
	binary.BigEndian.PutUint32(mvhd[16:20], uint32(numSamples*1000))

	hdlr := make([]byte, 24)
	copy(hdlr[8:12], "soun")
	mdhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mdhd[12:16], 1000)
	binary.BigEndian.PutUint32(mdhd[16:20], uint32(numSamples*1000))

	stsd := make([]byte, 8+16)
	binary.BigEndian.PutUint32(stsd[4:8], 1)
	binary.BigEndian.PutUint32(stsd[8:12], 16)
	copy(stsd[12:16], "mp4a")

	stts := make([]byte, 16)
	binary.BigEndian.PutUint32(stts[4:8], 1)
	binary.BigEndian.PutUint32(stts[8:12], uint32(numSamples))
	binary.BigEndian.PutUint32(stts[12:16], 1000)

	stsc := make([]byte, 20)
	binary.BigEndian.PutUint32(stsc[4:8], 1)
	binary.BigEndian.PutUint32(stsc[8:12], 1)
	binary.BigEndian.PutUint32(stsc[12:16], uint32(numSamples))
	binary.BigEndian.PutUint32(stsc[16:20], 1)

	stsz := make([]byte, 12+numSamples*4)
	binary.BigEndian.PutUint32(stsz[4:8], 0)
	binary.BigEndian.PutUint32(stsz[8:12], uint32(numSamples))
	for i := 0; i < numSamples; i++ {
		binary.BigEndian.PutUint32(stsz[12+i*4:16+i*4], uint32(sampleSize))
	}

	stco := make([]byte, 12)
	binary.BigEndian.PutUint32(stco[4:8], 1)
	binary.BigEndian.PutUint32(stco[8:12], uint32(mdatOffset))

	stbl := box("stbl", bytes.Join([][]byte{
		box("stsd", stsd),
		box("stts", stts),
		box("stsc", stsc),
		box("stsz", stsz),
		box("stco", stco),
	}, nil))
	minf := box("minf", stbl)
	mdia := box("mdia", bytes.Join([][]byte{box("mdhd", mdhd), box("hdlr", hdlr), minf}, nil))
	trak := box("trak", mdia)

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
	writeCh(60, "Chapter 2")

	textTag := func(tag, val string) []byte {
		dataPayload := append([]byte{0, 0, 0, 1, 0, 0, 0, 0}, []byte(val)...)
		return box(tag, box("data", dataPayload))
	}
	ilst := box("ilst", append(textTag("\xa9nam", title), textTag("\xa9ART", author)...))
	metaPayload := append([]byte{0, 0, 0, 0}, ilst...)
	udta := box("udta", append(box("meta", metaPayload), box("chpl", chpl.Bytes())...))

	moov := box("moov", bytes.Join([][]byte{box("mvhd", mvhd), trak, udta}, nil))
	return bytes.Join([][]byte{ftyp, box("mdat", mdatPayload), moov}, nil)
}

func TestAudiobookEndpointsGrantsStreamingSTTAndSessions(t *testing.T) {
	ctx := t.Context()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// Set up mock Whisper STT server.
	var sttTranscript string
	var sttCalls atomic.Int32
	sttServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sttCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": sttTranscript})
	}))
	defer sttServer.Close()

	booksDir := t.TempDir()
	audioDir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)

	fBooks := store.Folder{ID: "f-books", Name: "Books", RootPath: booksDir, Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now}
	fAudio := store.Folder{ID: "f-audio", Name: "Audio", RootPath: audioDir, Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateFolder(ctx, fBooks); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateFolder(ctx, fAudio); err != nil {
		t.Fatal(err)
	}

	u1 := store.User{ID: "u-full", Name: "alice", Argon2Hash: "x", CreatedAt: now}
	uBookOnly := store.User{ID: "u-book-only", Name: "bob", Argon2Hash: "x", CreatedAt: now}
	if err := st.CreateUser(ctx, u1); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(ctx, uBookOnly); err != nil {
		t.Fatal(err)
	}
	// Alice has grants on both folders; Bob only has a grant on f-books.
	if err := st.AssignUserFolder(ctx, u1.ID, fBooks.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignUserFolder(ctx, u1.ID, fAudio.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignUserFolder(ctx, uBookOnly.ID, fBooks.ID); err != nil {
		t.Fatal(err)
	}

	ch1XHTML := `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><body>
<p>The morning sun rose slowly over the silent mountains and warmed the valley stones.</p>
<p>By noon the travelers reached the ancient bridge where the emerald river curved.</p>
<p>As evening fell the silver stars appeared above the quiet citadel.</p>
</body></html>`
	ch2XHTML := `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><body>
<p>In the second chapter the fleet departed from the orbital dock at dawn.</p>
</body></html>`

	epubBytes := buildTestAudioEPUB(t, "Shadows Upon Time", "Christopher Ruocchio", ch1XHTML, ch2XHTML)
	if err := os.WriteFile(filepath.Join(booksDir, "shadows.epub"), epubBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	m4bBytes := buildSliceableTestM4B("Shadows Upon Time", "Christopher Ruocchio")
	if err := os.WriteFile(filepath.Join(audioDir, "shadows.m4b"), m4bBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	rec := content.NewReconciler(st, content.ScanLimits{}, epub.DefaultLimits(), slog.New(slog.DiscardHandler))
	if _, err := rec.Reconcile(ctx, fBooks); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Reconcile(ctx, fAudio); err != nil {
		t.Fatal(err)
	}

	books, err := st.ListCatalogBooks(ctx, u1.ID, fBooks.ID, nil, 10)
	if err != nil || len(books) != 1 {
		t.Fatalf("expected 1 book, got %+v %v", books, err)
	}
	bookID := books[0].ID

	cfg := config.Default()
	cfg.InsecureHTTP = true
	cfg.ReaderOrigin = "http://reader.example.test"
	cfg.Audiobookshelf.STTURL = sttServer.URL
	cfg.Audiobookshelf.STTModel = "whisper-large-v3-turbo"

	authSvc := auth.NewService(st)
	srv := &Server{
		St:           st,
		Auth:         authSvc,
		Cfg:          cfg,
		Files:        content.NewFiles(st),
		LoginLimiter: auth.NewRateLimiter(100, time.Minute),
		OPDSLimiter:  auth.NewRateLimiter(100, time.Minute),
	}
	// Disable STT cooldown in test so consecutive locate calls can be tested deterministically.
	srv.audioState().cooldown = 0

	ts := httptest.NewServer(CORS(cfg, srv.Routes()))
	defer ts.Close()

	aliceSecret, aliceTok, err := authSvc.MintToken(ctx, u1.ID, "alice-reader",
		store.ScopeSet{store.ScopeLibraryRead, store.ScopeSync}, nil)
	if err != nil {
		t.Fatal(err)
	}
	bobSecret, _, err := authSvc.MintToken(ctx, uBookOnly.ID, "bob-reader",
		store.ScopeSet{store.ScopeLibraryRead, store.ScopeSync}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Dual folder-grant isolation: Bob can see the book, but not its audiobook in f-audio.
	if code, _ := get(t, ts.URL+"/v1/books/"+bookID+"/audio/info", bobSecret); code != http.StatusNotFound {
		t.Fatalf("expected 404 for user lacking audiobook folder grant, got %d", code)
	}
	if code, _ := post(t, ts.URL+"/v1/books/"+bookID+"/audio/ticket", bobSecret, map[string]any{}); code != http.StatusNotFound {
		t.Fatalf("expected 404 on ticket for user lacking audiobook folder grant, got %d", code)
	}

	// 2. Alice can fetch audio info.
	code, infoOut := get(t, ts.URL+"/v1/books/"+bookID+"/audio/info", aliceSecret)
	if code != http.StatusOK {
		t.Fatalf("audio/info: %d %v", code, infoOut)
	}
	if infoOut["stt_enabled"] != true {
		t.Fatalf("expected stt_enabled=true, got %v", infoOut)
	}

	// 3. Mint stream ticket & test HTTP Range (206 Partial Content), Strong ETag, If-Range, and CORS.
	code, ticketOut := post(t, ts.URL+"/v1/books/"+bookID+"/audio/ticket", aliceSecret, map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("audio/ticket: %d %v", code, ticketOut)
	}
	streamURL, _ := ticketOut["stream_url"].(string)
	if !strings.HasPrefix(streamURL, "v1/books/"+bookID+"/audio/stream/") {
		t.Fatalf("unexpected stream_url: %q", streamURL)
	}
	if got := RedactAudioStreamPath("/" + streamURL); !strings.HasSuffix(got, "/audio/stream/[REDACTED]") {
		t.Fatalf("expected RedactAudioStreamPath to redact ticket, got %q", got)
	}
	if got := RedactPath("/" + streamURL); !strings.HasSuffix(got, "/audio/stream/[REDACTED]") {
		t.Fatalf("expected RedactPath to redact stream ticket, got %q", got)
	}

	// Preflight OPTIONS from detached reader origin with Range and If-Range.
	optReq, _ := http.NewRequest(http.MethodOptions, ts.URL+"/"+streamURL, nil)
	optReq.Header.Set("Origin", "http://reader.example.test")
	optReq.Header.Set("Access-Control-Request-Method", "GET")
	optReq.Header.Set("Access-Control-Request-Headers", "Range, If-Range")
	optResp, err := http.DefaultClient.Do(optReq)
	if err != nil {
		t.Fatal(err)
	}
	optResp.Body.Close()
	if optResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 preflight, got %d", optResp.StatusCode)
	}
	if !strings.Contains(optResp.Header.Get("Access-Control-Allow-Headers"), "Range") {
		t.Fatalf("expected Range in Access-Control-Allow-Headers, got %q", optResp.Header.Get("Access-Control-Allow-Headers"))
	}

	// Range request for bytes 0-63.
	rangeReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/"+streamURL, nil)
	rangeReq.Header.Set("Origin", "http://reader.example.test")
	rangeReq.Header.Set("Range", "bytes=0-63")
	rangeResp, err := http.DefaultClient.Do(rangeReq)
	if err != nil {
		t.Fatal(err)
	}
	rangeBytes, _ := io.ReadAll(rangeResp.Body)
	rangeResp.Body.Close()
	if rangeResp.StatusCode != http.StatusPartialContent || len(rangeBytes) != 64 {
		t.Fatalf("expected 206 with 64 bytes, got %d (%d bytes)", rangeResp.StatusCode, len(rangeBytes))
	}
	etag := rangeResp.Header.Get("ETag")
	if strings.HasPrefix(etag, "W/") || etag == "" {
		t.Fatalf("expected strong ETag for If-Range support, got %q", etag)
	}
	if !strings.Contains(rangeResp.Header.Get("Access-Control-Expose-Headers"), "Content-Range") {
		t.Fatalf("expected Content-Range in Access-Control-Expose-Headers, got %q", rangeResp.Header.Get("Access-Control-Expose-Headers"))
	}

	// If-Range with strong ETag succeeds with 206.
	ifRangeReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/"+streamURL, nil)
	ifRangeReq.Header.Set("Range", "bytes=64-127")
	ifRangeReq.Header.Set("If-Range", etag)
	ifRangeResp, err := http.DefaultClient.Do(ifRangeReq)
	if err != nil {
		t.Fatal(err)
	}
	ifRangeResp.Body.Close()
	if ifRangeResp.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected 206 on matching strong If-Range, got %d", ifRangeResp.StatusCode)
	}

	// 4. Stateless STT on [t - 10s, t] locks onto spoken sentence and rejects ambiguous repeats.
	sttTranscript = "the travelers reached the ancient bridge where the emerald river curved"
	code, locOut := post(t, ts.URL+"/v1/books/"+bookID+"/audio/locate", aliceSecret, map[string]any{
		"audio_time": 25.0,
		"refine_stt": true,
	})
	if code != http.StatusOK {
		t.Fatalf("locate audio_to_book: %d %v", code, locOut)
	}
	if locOut["stt_matched"] != true {
		t.Fatalf("expected stt_matched=true, got %+v", locOut)
	}
	locMap, _ := locOut["locator"].(map[string]any)
	textMap, _ := locMap["text"].(map[string]any)
	if !strings.Contains(fmt.Sprint(textMap["highlight"]), "ancient bridge") {
		t.Fatalf("expected highlight to contain matched phrase, got %+v", textMap)
	}

	// Book -> Audio translation using locator.
	code, revOut := post(t, ts.URL+"/v1/books/"+bookID+"/audio/locate", aliceSecret, map[string]any{
		"locator": locMap,
	})
	if code != http.StatusOK || revOut["direction"] != "book_to_audio" {
		t.Fatalf("locate book_to_audio: %d %v", code, revOut)
	}

	t.Run("resource coordinate round trips", func(t *testing.T) {
		link, _, err := st.BookAudiobookByBookID(ctx, u1.ID, bookID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := st.UpsertBookAudiobook(ctx, link, now); err != nil {
				t.Error(err)
			}
		})
		for _, tc := range []struct {
			name      string
			segments  []audiomap.Segment
			resources []audiomap.Resource
			wantHref  string
			wantTotal float64
		}{
			{
				name: "two chapters in one document",
				segments: []audiomap.Segment{
					{AudioStart: 0, AudioEnd: 60, BookStart: 0, BookEnd: 0.25, Href: "OEBPS/ch1.xhtml"},
					{AudioStart: 60, AudioEnd: 120, BookStart: 0.25, BookEnd: 0.5, Href: "OEBPS/ch1.xhtml"},
				},
				resources: []audiomap.Resource{{Href: "OEBPS/ch1.xhtml", Start: 0, End: 0.5}},
				wantHref:  "OEBPS/ch1.xhtml", wantTotal: 0.375,
			},
			{
				name: "one chapter spanning documents",
				segments: []audiomap.Segment{
					{AudioStart: 0, AudioEnd: 120, BookStart: 0, BookEnd: 1, Href: "OEBPS/ch1.xhtml"},
				},
				resources: []audiomap.Resource{
					{Href: "OEBPS/ch1.xhtml", Start: 0, End: 0.6},
					{Href: "OEBPS/ch2.xhtml", Start: 0.6, End: 1},
				},
				wantHref: "OEBPS/ch2.xhtml", wantTotal: 0.75,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				mapping := audiomap.Mapping{Segments: tc.segments, Resources: tc.resources, FullNarrative: true}
				changed := link
				changed.MappingJSON, err = json.Marshal(mapping)
				if err != nil {
					t.Fatal(err)
				}
				if err := st.UpsertBookAudiobook(ctx, changed, now); err != nil {
					t.Fatal(err)
				}
				code, out := post(t, ts.URL+"/v1/books/"+bookID+"/audio/locate", aliceSecret,
					map[string]any{"audio_time": 90})
				if code != http.StatusOK {
					t.Fatalf("audio locate: %d %v", code, out)
				}
				loc := out["locator"].(map[string]any)
				if loc["href"] != tc.wantHref || math.Abs(out["progression"].(float64)-tc.wantTotal) > 1e-9 {
					t.Fatalf("wrong resource: %v", out)
				}
				for _, withTotal := range []bool{true, false} {
					if !withTotal {
						delete(loc["locations"].(map[string]any), "totalProgression")
					}
					code, reversed := post(t, ts.URL+"/v1/books/"+bookID+"/audio/locate", aliceSecret,
						map[string]any{"locator": loc})
					if code != http.StatusOK || math.Abs(reversed["audio_time"].(float64)-90) > 1e-9 ||
						math.Abs(reversed["progression"].(float64)-tc.wantTotal) > 1e-9 {
						t.Fatalf("round trip changed position: %d %v", code, reversed)
					}
				}
			})
		}

		mapping := audiomap.Mapping{
			Segments:  []audiomap.Segment{{AudioStart: 0, AudioEnd: 120, BookStart: 0.1, BookEnd: 0.5, Href: "OEBPS/ch1.xhtml"}},
			Resources: []audiomap.Resource{{Href: "OEBPS/ch1.xhtml", Start: 0, End: 0.5}},
		}
		changed := link
		changed.MappingJSON, err = json.Marshal(mapping)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertBookAudiobook(ctx, changed, now); err != nil {
			t.Fatal(err)
		}
		frac, _, _, _, _, _, matched := audiomap.MatchTranscriptDetailInXHTML([]byte(ch1XHTML), sttTranscript)
		if !matched {
			t.Fatal("transcript fixture did not match")
		}
		code, refined := post(t, ts.URL+"/v1/books/"+bookID+"/audio/locate", aliceSecret,
			map[string]any{"audio_time": 90, "refine_stt": true})
		if code != http.StatusOK || refined["stt_matched"] != true {
			t.Fatalf("STT locate: %d %v", code, refined)
		}
		if math.Abs(refined["progression"].(float64)-frac*0.5) > 1e-9 {
			t.Fatalf("STT used segment coordinates instead of resource coordinates: %v", refined)
		}
		locations := refined["locator"].(map[string]any)["locations"].(map[string]any)
		if math.Abs(locations["progression"].(float64)-frac) > 1e-9 {
			t.Fatalf("wrong STT resource progression: %v", locations)
		}
	})

	t.Run("early STT never transcribes future audio", func(t *testing.T) {
		before := sttCalls.Load()
		for _, at := range []float64{0, 0.5, 60, 60.5} {
			code, out := post(t, ts.URL+"/v1/books/"+bookID+"/audio/locate", aliceSecret,
				map[string]any{"audio_time": at, "refine_stt": true})
			if code != http.StatusOK || out["stt_matched"] != false {
				t.Fatalf("early locate: %d %v", code, out)
			}
		}
		if sttCalls.Load() != before {
			t.Fatal("early refinement called STT with speech ahead of the playhead")
		}
	})

	t.Run("STT requires readable EPUB text", func(t *testing.T) {
		link, ab, err := st.BookAudiobookByBookID(ctx, u1.ID, bookID)
		if err != nil {
			t.Fatal(err)
		}
		var mapping audiomap.Mapping
		if err := json.Unmarshal(link.MappingJSON, &mapping); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name    string
			href    string
			chapter string
			missing bool
			wantSTT bool
		}{
			{name: "missing file", href: "OEBPS/ch1.xhtml", missing: true},
			{name: "missing resource", href: "OEBPS/missing.xhtml", chapter: ch1XHTML},
			{name: "empty resource", href: "OEBPS/ch1.xhtml"},
			{name: "empty text", href: "OEBPS/ch1.xhtml", chapter: `<html><head><title>Chapter</title></head><body> </body></html>`},
			{name: "readable text", href: "OEBPS/ch1.xhtml", chapter: ch1XHTML, wantSTT: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				book := books[0]
				book.RelativePath = tc.name + ".epub"
				if !tc.missing {
					path := filepath.Join(booksDir, book.RelativePath)
					data := buildTestAudioEPUB(t, book.Title, "Christopher Ruocchio", tc.chapter, ch2XHTML)
					if err := os.WriteFile(path, data, 0o644); err != nil {
						t.Fatal(err)
					}
					info, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					book.SizeBytes, book.MTime = info.Size(), info.ModTime()
				}
				before := sttCalls.Load()
				_, _, _, _, _, _, matched := srv.refineAudioTimeWithSTT(
					ctx, u1.ID, book, ab, mapping, mapping.Segments[0], tc.href, 25,
				)
				wantCalls := int32(0)
				if tc.wantSTT {
					wantCalls = 1
				}
				if calls := sttCalls.Load() - before; calls != wantCalls || matched != tc.wantSTT {
					t.Fatalf("STT calls = %d, matched = %v; want %d, %v", calls, matched, wantCalls, tc.wantSTT)
				}
			})
		}
	})

	t.Run("unusable locators never seek to the start", func(t *testing.T) {
		for _, tc := range []struct {
			locator any
			code    int
		}{
			{"not a locator", http.StatusBadRequest},
			{nil, http.StatusBadRequest},
			{map[string]any{}, http.StatusNotFound},
			{map[string]any{"href": "unknown.xhtml"}, http.StatusNotFound},
		} {
			code, out := post(t, ts.URL+"/v1/books/"+bookID+"/audio/locate", aliceSecret,
				map[string]any{"locator": tc.locator})
			if code != tc.code {
				t.Fatalf("locator %v: %d %v", tc.locator, code, out)
			}
		}
	})

	t.Run("stale alignment is not advertised or served", func(t *testing.T) {
		link, _, err := st.BookAudiobookByBookID(ctx, u1.ID, bookID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := st.UpsertBookAudiobook(ctx, link, now); err != nil {
				t.Error(err)
			}
		})
		for _, field := range []string{"edition", "audio", "chapters"} {
			changed := link
			switch field {
			case "edition":
				changed.EditionSHA = "stale"
			case "audio":
				changed.AudiobookSHA = "stale"
			case "chapters":
				changed.MappingKey = "stale"
			}
			if err := st.UpsertBookAudiobook(ctx, changed, now); err != nil {
				t.Fatal(err)
			}
			base := ts.URL + "/v1/books/" + bookID
			if code, out := get(t, base+"/audio/info", aliceSecret); code != http.StatusNotFound {
				t.Fatalf("%s info: %d %v", field, code, out)
			}
			for _, path := range []string{"/audio/ticket", "/audio/locate"} {
				if code, out := post(t, base+path, aliceSecret, map[string]any{"audio_time": 25}); code != http.StatusNotFound {
					t.Fatalf("%s %s: %d %v", field, path, code, out)
				}
			}
			code, out := post(t, base+"/resolve", aliceSecret, map[string]any{})
			if (code != http.StatusOK && code != http.StatusCreated) || out["has_audio"] == true {
				t.Fatalf("%s advertised stale audio: %d %v", field, code, out)
			}
		}
	})

	// 5. Server restart (ticket key rotation) and token revocation immediately invalidate stream tickets.
	srv.RotateAudioTicketKey()
	rotResp, err := http.Get(ts.URL + "/" + streamURL)
	if err != nil {
		t.Fatal(err)
	}
	rotResp.Body.Close()
	if rotResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after server key rotation, got %d", rotResp.StatusCode)
	}

	// Mint a fresh ticket, then revoke the underlying token -> stream must immediately return 401.
	code, ticketOut2 := post(t, ts.URL+"/v1/books/"+bookID+"/audio/ticket", aliceSecret, map[string]any{})
	if code != http.StatusOK {
		t.Fatal(code)
	}
	streamURL2 := ticketOut2["stream_url"].(string)
	if err := st.RevokeToken(ctx, u1.ID, aliceTok.ID); err != nil {
		t.Fatal(err)
	}
	revResp, err := http.Get(ts.URL + "/" + streamURL2)
	if err != nil {
		t.Fatal(err)
	}
	revResp.Body.Close()
	if revResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after token revocation, got %d", revResp.StatusCode)
	}

	// 6. Listening session mode ("listening" -> OriginAudiobook) on POST /v1/sessions,
	// plus effective ticket expiry capped by short-lived reader token expiry.
	shortExp := time.Now().UTC().Add(30 * time.Minute)
	shortSecret, _, err := authSvc.MintToken(ctx, u1.ID, "alice-reader-short",
		store.ScopeSet{store.ScopeLibraryRead, store.ScopeSync}, &shortExp)
	if err != nil {
		t.Fatal(err)
	}
	code, shortTicket := post(t, ts.URL+"/v1/books/"+bookID+"/audio/ticket", shortSecret, map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("short token ticket: %d %v", code, shortTicket)
	}
	parsedExp, err := time.Parse(time.RFC3339Nano, fmt.Sprint(shortTicket["expires_at"]))
	if err != nil || parsedExp.After(shortExp.Add(2*time.Second)) {
		t.Fatalf("expected ticket expires_at <= short token expiry (%v), got %v (%v)", shortExp, shortTicket["expires_at"], err)
	}

	syncSecret, _, err := authSvc.MintToken(ctx, u1.ID, "alice-sync",
		store.ScopeSet{store.ScopeLibraryRead, store.ScopeSync}, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, resWork := post(t, ts.URL+"/v1/books/"+bookID+"/resolve", syncSecret, map[string]any{})
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("resolve book work: %d %v", code, resWork)
	}
	workID := resWork["work_id"].(string)

	code, sesOut := post(t, ts.URL+"/v1/sessions", syncSecret, map[string]any{
		"sessions": []map[string]any{
			{
				"session_id":        "listen-ses-1",
				"work_id":           workID,
				"started_at":        "2026-04-12T10:00:00Z",
				"ended_at":          "2026-04-12T10:15:00Z",
				"start_progression": 0.10,
				"end_progression":   0.25,
				"idle_ms":           0,
				"active_ms":         900000,
				"mode":              "listening",
			},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("push listening session: %d %v", code, sesOut)
	}

	// Idempotent replay with identical mode succeeds.
	code, _ = post(t, ts.URL+"/v1/sessions", syncSecret, map[string]any{
		"sessions": []map[string]any{
			{
				"session_id":        "listen-ses-1",
				"work_id":           workID,
				"started_at":        "2026-04-12T10:00:00Z",
				"ended_at":          "2026-04-12T10:15:00Z",
				"start_progression": 0.10,
				"end_progression":   0.25,
				"idle_ms":           0,
				"active_ms":         900000,
				"mode":              "listening",
			},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("expected idempotent replay 200, got %d", code)
	}

	// Replay with conflicting mode ("reading") is rejected with 409 Conflict.
	code, _ = post(t, ts.URL+"/v1/sessions", syncSecret, map[string]any{
		"sessions": []map[string]any{
			{
				"session_id":        "listen-ses-1",
				"work_id":           workID,
				"started_at":        "2026-04-12T10:00:00Z",
				"ended_at":          "2026-04-12T10:15:00Z",
				"start_progression": 0.10,
				"end_progression":   0.25,
				"idle_ms":           0,
				"active_ms":         900000,
				"mode":              "reading",
			},
		},
	})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 on mode mismatch for same session_id, got %d", code)
	}

	// Invalid mode is rejected with 400 Bad Request (code=bad_mode).
	code, badOut := post(t, ts.URL+"/v1/sessions", syncSecret, map[string]any{
		"sessions": []map[string]any{
			{
				"session_id":        "listen-ses-2",
				"work_id":           workID,
				"started_at":        "2026-04-12T10:00:00Z",
				"ended_at":          "2026-04-12T10:15:00Z",
				"start_progression": 0.10,
				"end_progression":   0.25,
				"idle_ms":           0,
				"mode":              "podcasts",
			},
		},
	})
	if code != http.StatusBadRequest || badOut["code"] != "bad_mode" {
		t.Fatalf("expected 400 bad_mode, got %d %v", code, badOut)
	}
}
