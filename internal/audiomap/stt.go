package audiomap

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// TranscribeAudio posts a short audio snippet (.m4a or .mp3) to an
// OpenAI-compatible /v1/audio/transcriptions endpoint and returns the
// recognized text.
func TranscribeAudio(ctx context.Context, baseURL, apiKey, model string, audioBytes []byte, filename string) (string, error) {
	base := strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return "", errors.New("audiomap: stt_url is not configured")
	}
	endpoint := base + "/audio/transcriptions"
	if !strings.HasSuffix(base, "/v1") && !strings.Contains(base, "/v1/") {
		endpoint = base + "/v1/audio/transcriptions"
	}
	if model == "" {
		model = "whisper-large-v3-turbo"
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
		return "", fmt.Errorf("audiomap: stt server answered %s", strings.TrimSpace(resp.Status))
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
	norm      string
	startRune int
	endRune   int
}

// MatchTranscriptInXHTML locates a spoken transcript snippet inside an EPUB
// XHTML document's visible text, returning the character fraction [0, 1] at
// the end of the matched passage and a confidence score in [0, 1].
func MatchTranscriptInXHTML(doc []byte, transcript string) (fraction float64, confidence float64, ok bool) {
	frac, conf, _, _, _, _, matched := MatchTranscriptDetailInXHTML(doc, transcript)
	return frac, conf, matched
}

// MatchTranscriptDetailInXHTML locates a spoken transcript snippet inside an
// EPUB XHTML document's visible text, returning the character fraction [0, 1]
// at the end of the matched passage, the confidence score, the runner-up margin
// against non-overlapping windows, and surrounding text context (before, highlight, after).
func MatchTranscriptDetailInXHTML(doc []byte, transcript string) (fraction float64, confidence float64, margin float64, before, highlight, after string, ok bool) {
	qWords := tokenizeWords(transcript)
	if len(qWords) < 4 {
		return 0, 0, 0, "", "", "", false
	}
	words, runes := extractXHTMLWords(doc)
	totalRunes := len(runes)
	if len(words) < len(qWords) || totalRunes <= 0 {
		return 0, 0, 0, "", "", "", false
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

	type winScore struct {
		start    int
		firstHit int
		lastHit  int
		score    float64
	}
	scores := make([]winScore, 0, len(words)-qLen+1)

	bestScore := 0.0
	bestIdx := -1

	for start := 0; start <= len(words)-qLen; start++ {
		end := start + winSize
		if end > len(words) {
			end = len(words)
		}
		hits := 0
		seen := make(map[string]int, qLen)
		bigramHits := 0
		seenBigrams := make(map[string]int, qLen)
		firstHitOffset := -1
		lastHitOffset := start
		for i := start; i < end; i++ {
			w := words[i].norm
			if maxCnt, exists := qSet[w]; exists && seen[w] < maxCnt {
				seen[w]++
				hits++
				if firstHitOffset < 0 {
					firstHitOffset = i
				}
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
		if firstHitOffset < 0 {
			firstHitOffset = start
		}
		wordRecall := float64(hits) / float64(qLen)
		bigramRecall := 0.0
		if qLen > 1 {
			bigramRecall = float64(bigramHits) / float64(qLen-1)
		}
		score := 0.65*wordRecall + 0.35*bigramRecall
		scores = append(scores, winScore{
			start:    start,
			firstHit: firstHitOffset,
			lastHit:  lastHitOffset,
			score:    score,
		})
		if score > bestScore {
			bestScore = score
			bestIdx = len(scores) - 1
		}
	}

	if bestIdx < 0 || bestScore < 0.55 {
		return 0, bestScore, 0, "", "", "", false
	}

	best := scores[bestIdx]
	secondBest := 0.0
	for _, s := range scores {
		// Non-overlapping matched passage span: the matched word boundaries
		// [s.firstHit, s.lastHit] must be disjoint from [best.firstHit, best.lastHit].
		if s.lastHit < best.firstHit || s.firstHit > best.lastHit {
			if s.score > secondBest {
				secondBest = s.score
			}
		}
	}
	margin = bestScore - secondBest
	before, highlight, after = buildWordContext(words, runes, best.firstHit, best.lastHit)
	return clamp01(float64(words[best.lastHit].endRune) / float64(totalRunes)), bestScore, margin, before, highlight, after, true
}

// ExtractTextContextAtFraction returns a short (before, highlight, after) text
// snippet around a character fraction in [0, 1] of an XHTML document.
func ExtractTextContextAtFraction(doc []byte, fraction float64) (before, highlight, after string) {
	words, runes := extractXHTMLWords(doc)
	totalRunes := len(runes)
	if len(words) == 0 || totalRunes <= 0 {
		return "", "", ""
	}
	targetRune := int(clamp01(fraction) * float64(totalRunes))
	targetIdx := 0
	for i, w := range words {
		if w.endRune >= targetRune {
			targetIdx = i
			break
		}
		targetIdx = i
	}
	startIdx := targetIdx - 4
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := targetIdx + 4
	if endIdx >= len(words) {
		endIdx = len(words) - 1
	}
	return buildWordContext(words, runes, startIdx, endIdx)
}

func buildWordContext(words []docWord, runes []rune, startIdx, endIdx int) (before, highlight, after string) {
	if len(words) == 0 || len(runes) == 0 {
		return "", "", ""
	}
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx < startIdx {
		endIdx = startIdx
	}
	if endIdx >= len(words) {
		endIdx = len(words) - 1
	}
	if endIdx-startIdx > 14 {
		startIdx = endIdx - 14
	}
	preStart := startIdx - 6
	if preStart < 0 {
		preStart = 0
	}
	postEnd := endIdx + 6
	if postEnd >= len(words) {
		postEnd = len(words) - 1
	}

	sliceRunes := func(loRune, hiRune int) string {
		if loRune < 0 {
			loRune = 0
		}
		if hiRune > len(runes) {
			hiRune = len(runes)
		}
		if loRune >= hiRune {
			return ""
		}
		var b strings.Builder
		b.Grow(hiRune - loRune)
		inSpace := false
		for _, r := range runes[loRune:hiRune] {
			if unicode.IsSpace(r) {
				if !inSpace {
					b.WriteByte(' ')
					inSpace = true
				}
				continue
			}
			inSpace = false
			b.WriteRune(r)
		}
		return b.String()
	}

	hiStart := words[startIdx].startRune
	hiEnd := words[endIdx].endRune
	if preStart < startIdx {
		before = sliceRunes(words[preStart].startRune, hiStart)
	}
	highlight = sliceRunes(hiStart, hiEnd)
	if endIdx < postEnd {
		after = sliceRunes(hiEnd, words[postEnd].endRune)
	}
	return before, highlight, after
}

// MatchLocatorTextInXHTML locates a Readium locator text context (before +
// highlight + after) inside an EPUB XHTML document's visible text.
func MatchLocatorTextInXHTML(doc []byte, before, highlight, after string) (fraction float64, confidence float64, ok bool) {
	full := strings.TrimSpace(before + highlight + after)
	qWords := tokenizeWords(full)
	if len(qWords) < 4 {
		return 0, 0, false
	}
	words, runes := extractXHTMLWords(doc)
	totalRunes := len(runes)
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
	bestStart := 0
	bestLastHit := 0

	for start := 0; start <= len(words)-qLen; start++ {
		end := start + winSize
		if end > len(words) {
			end = len(words)
		}
		hits := 0
		seen := make(map[string]int, qLen)
		bigramHits := 0
		seenBigrams := make(map[string]int, qLen)
		firstHit := -1
		lastHit := start
		for i := start; i < end; i++ {
			w := words[i].norm
			if maxCnt, exists := qSet[w]; exists && seen[w] < maxCnt {
				seen[w]++
				hits++
				if firstHit < 0 {
					firstHit = i
				}
				lastHit = i
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
			if firstHit >= 0 {
				bestStart = firstHit
			} else {
				bestStart = start
			}
			bestLastHit = lastHit
		}
	}

	if bestScore < 0.65 {
		return 0, bestScore, false
	}
	targetIdx := bestLastHit
	if prefixWords := len(tokenizeWords(before + highlight)); prefixWords > 0 {
		if idx := bestStart + prefixWords - 1; idx >= bestStart && idx <= bestLastHit {
			targetIdx = idx
		}
	}
	return clamp01(float64(words[targetIdx].endRune) / float64(totalRunes)), bestScore, true
}

func extractXHTMLWords(doc []byte) ([]docWord, []rune) {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	var (
		words     []docWord
		runes     []rune
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
				var normBuf strings.Builder
				wordStart := 0
				for _, r := range s {
					runes = append(runes, r)
					total := len(runes)
					if unicode.IsLetter(r) || unicode.IsDigit(r) {
						if normBuf.Len() == 0 {
							wordStart = total - 1
						}
						normBuf.WriteRune(foldRune(unicode.ToLower(r)))
					} else if normBuf.Len() > 0 {
						words = append(words, docWord{
							norm:      normBuf.String(),
							startRune: wordStart,
							endRune:   total - 1,
						})
						normBuf.Reset()
					}
				}
				if normBuf.Len() > 0 {
					words = append(words, docWord{
						norm:      normBuf.String(),
						startRune: wordStart,
						endRune:   len(runes),
					})
				}
			}
		}
	}
	return words, runes
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
