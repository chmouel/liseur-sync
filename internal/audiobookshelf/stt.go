package audiobookshelf

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

// TranscribeAudio posts a short audio snippet (.m4a or .mp3) to an
// OpenAI-compatible /v1/audio/transcriptions endpoint and returns the
// recognized text.
func TranscribeAudio(ctx context.Context, baseURL, apiKey, model string, audioBytes []byte, filename string) (string, error) {
	base := strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return "", errors.New("audiobookshelf: stt_url is not configured")
	}
	endpoint := base + "/audio/transcriptions"
	if !strings.HasSuffix(base, "/v1") && !strings.Contains(base, "/v1/") {
		endpoint = base + "/v1/audio/transcriptions"
	}
	if model == "" {
		model = "whisper-1"
	}
	if filename == "" {
		filename = "snippet.m4a"
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(audioBytes); err != nil {
		return "", err
	}
	if err := mw.WriteField("model", model); err != nil {
		return "", err
	}
	if err := mw.WriteField("response_format", "json"); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "liseur-sync")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("audiobookshelf: stt server answered %s", cleanText(resp.Status))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Text), nil
}

type docWord struct {
	norm    string
	endRune int
}

// MatchTranscriptInXHTML locates a spoken transcript snippet inside an EPUB
// XHTML document's visible text, returning the character fraction [0, 1] at
// the end of the matched passage and a confidence score in [0, 1].
func MatchTranscriptInXHTML(doc []byte, transcript string) (fraction float64, confidence float64, ok bool) {
	qWords := tokenizeWords(transcript)
	if len(qWords) < 4 {
		return 0, 0, false
	}
	words, totalRunes := extractXHTMLWords(doc)
	if len(words) < len(qWords) || totalRunes <= 0 {
		return 0, 0, false
	}

	qLen := len(qWords)
	qSet := make(map[string]int, qLen)
	qBigrams := make(map[string]int, qLen)
	for i, w := range qWords {
		qSet[w]++
		if i+1 < qLen {
			qBigrams[w+" "+qWords[i+1]]++
		}
	}

	winSize := qLen + qLen/3
	if winSize > len(words) {
		winSize = len(words)
	}

	bestScore := 0.0
	bestEndRune := 0

	for start := 0; start <= len(words)-qLen; start++ {
		end := start + winSize
		if end > len(words) {
			end = len(words)
		}
		// Quick pre-filter: require at least 2 words in the first 4 of the window to appear in qSet.
		hits := 0
		seen := make(map[string]int, qLen)
		bigramHits := 0
		seenBigrams := make(map[string]int, qLen)
		lastHitOffset := 0
		for i := start; i < end; i++ {
			w := words[i].norm
			if maxCnt, exists := qSet[w]; exists && seen[w] < maxCnt {
				seen[w]++
				hits++
				lastHitOffset = i
			}
			if i+1 < end {
				bg := w + " " + words[i+1].norm
				if maxBg, exists := qBigrams[bg]; exists && seenBigrams[bg] < maxBg {
					seenBigrams[bg]++
					bigramHits++
				}
			}
		}
		wordRecall := float64(hits) / float64(qLen)
		bigramRecall := 0.0
		if qLen > 1 {
			bigramRecall = float64(bigramHits) / float64(qLen-1)
		}
		score := 0.65*wordRecall + 0.35*bigramRecall
		if score > bestScore {
			bestScore = score
			bestEndRune = words[lastHitOffset].endRune
		}
	}

	if bestScore < 0.55 {
		return 0, bestScore, false
	}
	return clamp01(float64(bestEndRune) / float64(totalRunes)), bestScore, true
}

func extractXHTMLWords(doc []byte) ([]docWord, int) {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	var (
		words     []docWord
		total     int
		skipDepth int
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if skipDepth > 0 {
				skipDepth++
				continue
			}
			switch strings.ToLower(t.Name.Local) {
			case "head", "style", "script":
				skipDepth = 1
			}
		case xml.EndElement:
			if skipDepth > 0 {
				skipDepth--
			}
		case xml.CharData:
			if skipDepth == 0 {
				s := string(t)
				var cur strings.Builder
				for _, r := range s {
					total++
					if unicode.IsLetter(r) || unicode.IsDigit(r) {
						cur.WriteRune(foldRune(unicode.ToLower(r)))
					} else if cur.Len() > 0 {
						words = append(words, docWord{norm: cur.String(), endRune: total})
						cur.Reset()
					}
				}
				if cur.Len() > 0 {
					words = append(words, docWord{norm: cur.String(), endRune: total})
				}
			}
		}
	}
	return words, total
}

func tokenizeWords(s string) []string {
	var out []string
	var cur strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(foldRune(unicode.ToLower(r)))
		} else if cur.Len() > 0 {
			if utf8.RuneCountInString(cur.String()) > 0 {
				out = append(out, cur.String())
			}
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func foldRune(r rune) rune {
	switch r {
	case 'à', 'á', 'â', 'ã', 'ä', 'å':
		return 'a'
	case 'ç':
		return 'c'
	case 'è', 'é', 'ê', 'ë':
		return 'e'
	case 'ì', 'í', 'î', 'ï':
		return 'i'
	case 'ñ':
		return 'n'
	case 'ò', 'ó', 'ô', 'õ', 'ö', 'ø':
		return 'o'
	case 'ù', 'ú', 'û', 'ü':
		return 'u'
	case 'ý', 'ÿ':
		return 'y'
	}
	return r
}

// refinePositionWithSTT attempts to refine a chapter-interpolated book
// progression using a 12-second audio snippet around remoteTime when stt_url
// is configured. Any failure falls back cleanly to linearP.
func (s *Service) refinePositionWithSTT(ctx context.Context, userID string, l store.AudiobookLink, item Item, m Mapping, remoteTime, linearP float64, seg Segment) float64 {
	if s.Cfg.STTURL == "" || s.Client == nil || len(item.AudioFiles) == 0 || remoteTime <= 0 {
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
