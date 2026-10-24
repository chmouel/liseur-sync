package audiobookshelf

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSliceAudioSnippetM4BAndRoundTrip(t *testing.T) {
	// Build a synthetic M4B with 10 samples (each 1000 ticks at timescale 1000 = 1s each).
	stsd := makeFullBox("stsd", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 1)
		b.Write(makeBox("mp4a", func(m *bytes.Buffer) {
			m.Write(make([]byte, 28))
		}))
	})
	var spans []sampleSpan
	var mdatPayload bytes.Buffer
	for i := 0; i < 10; i++ {
		frame := bytes.Repeat([]byte{byte('A' + i)}, 32)
		spans = append(spans, sampleSpan{
			offset: uint64(i * 32),
			size:   32,
			delta:  1000,
		})
		mdatPayload.Write(frame)
	}
	fullM4B := buildSingleChunkM4A(1000, stsd, spans, mdatPayload.Bytes())

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

	// Verify the synthesized .m4a parses back as a valid ISO BMFF sound track with 3 samples ('C', 'D', 'E').
	moov, err := fetchMoovBox(context.Background(), func(_ context.Context, _, _ string, s, e int64) ([]byte, error) {
		return clip[s : e+1], nil
	}, "item-1", "ino-1", int64(len(clip)))
	if err != nil {
		t.Fatalf("re-parse moov failed: %v", err)
	}
	trk, err := parseSoundTrack(moov)
	if err != nil {
		t.Fatalf("re-parse sound track failed: %v", err)
	}
	if len(trk.sizes) != 3 {
		t.Fatalf("len(trk.sizes) = %d, want 3", len(trk.sizes))
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
