package audiobookshelf

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/audiomap"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

// TranscribeAudio posts a short audio snippet (.m4a or .mp3) to an
// OpenAI-compatible /v1/audio/transcriptions endpoint and returns the
// recognized text.
func TranscribeAudio(ctx context.Context, baseURL, apiKey, model string, audioBytes []byte, filename string) (string, error) {
	return audiomap.TranscribeAudio(ctx, baseURL, apiKey, model, audioBytes, filename)
}

// MatchTranscriptInXHTML locates a spoken transcript snippet inside an EPUB
// XHTML document's visible text, returning the character fraction [0, 1] at
// the end of the matched passage and a confidence score in [0, 1].
func MatchTranscriptInXHTML(doc []byte, transcript string) (fraction float64, confidence float64, ok bool) {
	return audiomap.MatchTranscriptInXHTML(doc, transcript)
}

// MatchLocatorTextInXHTML locates a Readium locator text context (before +
// highlight + after) inside an EPUB XHTML document's visible text, returning
// the character fraction [0, 1] at the highlighted word (or end of the snippet)
// and a confidence score in [0, 1].
func MatchLocatorTextInXHTML(doc []byte, before, highlight, after string) (fraction float64, confidence float64, ok bool) {
	return audiomap.MatchLocatorTextInXHTML(doc, before, highlight, after)
}

// refinePositionWithSTT attempts to refine a chapter-interpolated book
// progression using a 12-second audio snippet around remoteTime when stt_url
// is configured. Any failure falls back cleanly to linearP.
func (s *Service) refinePositionWithSTT(ctx context.Context, userID string, l store.AudiobookLink, item Item, m Mapping, remoteTime, linearP float64, seg Segment) float64 {
	if s.Cfg.STTURL == "" || s.Client == nil || len(item.AudioFiles) == 0 || remoteTime <= 0 {
		return linearP
	}
	if strings.Contains(s.Cfg.STTURL, "api.groq.com") && s.Cfg.STTAPIKey == "" {
		return linearP
	}
	book, err := s.Store.CatalogBookByID(ctx, userID, l.BookID)
	if err != nil || book.Status != store.BookActive {
		return linearP
	}

	t0 := math.Max(seg.AudioStart, remoteTime-10)
	t1 := math.Min(seg.AudioEnd, remoteTime+2)
	if t1-t0 < 3 {
		t0 = math.Max(0, remoteTime-10)
		t1 = math.Min(item.Duration, remoteTime+2)
	}
	if t1-t0 < 3 {
		return linearP
	}

	sttCtx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()

	clip, filename, err := SliceAudioSnippet(sttCtx, s.Client.FileRange, item, t0, t1)
	if err != nil || len(clip) == 0 {
		return linearP
	}
	transcript, err := TranscribeAudio(sttCtx, s.Cfg.STTURL, s.Cfg.STTAPIKey, s.Cfg.STTModel, clip, filename)
	if err != nil || transcript == "" {
		return linearP
	}

	f, size, err := content.NewFiles(s.Store).OpenBook(sttCtx, book)
	if err != nil {
		return linearP
	}
	defer f.Close()
	pub, err := epub.OpenPublication(sttCtx, f, size, epub.DefaultLimits())
	if err != nil {
		return linearP
	}

	// Locate the spine resource covering seg.Href (or linearP).
	targetHref := seg.Href
	resStart, resEnd := seg.BookStart, seg.BookEnd
	for _, r := range m.Resources {
		if r.Href == seg.Href {
			resStart, resEnd = r.Start, r.End
			break
		}
	}
	if targetHref == "" {
		for _, r := range m.Resources {
			if linearP >= r.Start && linearP <= r.End {
				targetHref = r.Href
				resStart, resEnd = r.Start, r.End
				break
			}
		}
	}
	if targetHref == "" || resEnd <= resStart {
		return linearP
	}

	doc, err := pub.Resource(sttCtx, targetHref)
	if err != nil {
		return linearP
	}
	frac, _, ok := MatchTranscriptInXHTML(doc, transcript)
	if !ok {
		return linearP
	}
	refined := resStart + frac*(resEnd-resStart)
	if refined < seg.BookStart || refined > seg.BookEnd {
		return linearP
	}
	return refined
}
