package booktext

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Passage is a run of one chapter's text. Start and End are byte
// offsets into the chapter's text, so End is where the next read
// starts.
type Passage struct {
	Chapter     int
	Title       string
	Start, End  int
	Progression float64
	Text        string
}

// Chapter returns chapter i, or false.
func (b *Book) Chapter(i int) (*Chapter, bool) {
	if i < 0 || i >= len(b.Chapters) {
		return nil, false
	}
	return &b.Chapters[i], true
}

// Progression is the whole-book fraction of an offset in a chapter.
func (b *Book) Progression(ch, off int) float64 {
	c := &b.Chapters[ch]
	if len(c.Text) == 0 {
		return c.StartProg
	}
	return c.StartProg + (c.EndProg-c.StartProg)*float64(off)/float64(len(c.Text))
}

// AtProgression finds the chapter and offset of a whole-book fraction.
func (b *Book) AtProgression(p float64) (int, int) {
	p = min(max(p, 0), 1)
	for i, c := range b.Chapters {
		if p < c.EndProg || i == len(b.Chapters)-1 {
			return i, offsetIn(c, p)
		}
	}
	return 0, 0
}

func offsetIn(c Chapter, p float64) int {
	span := c.EndProg - c.StartProg
	if span <= 0 || len(c.Text) == 0 {
		return 0
	}
	f := min(max((p-c.StartProg)/span, 0), 1)
	return snapBack(c.Text, int(f*float64(len(c.Text))))
}

// Read returns up to max bytes of chapter ch from start, ending on a
// word boundary when one is near.
func (b *Book) Read(ch, start, maxBytes int) Passage {
	c := &b.Chapters[ch]
	start = snapBack(c.Text, min(max(start, 0), len(c.Text)))
	end := min(start+maxBytes, len(c.Text))
	if end < len(c.Text) {
		end = snapBack(c.Text, end)
		if i := strings.LastIndexAny(c.Text[start:end], " \n"); i > (end-start)/2 {
			end = start + i + 1
		}
	}
	return b.passage(ch, start, end)
}

// Window returns the text around an offset: before bytes ahead of it
// and after bytes from it, trimmed to whole words.
func (b *Book) Window(ch, off, before, after int) Passage {
	c := &b.Chapters[ch]
	off = snapBack(c.Text, min(max(off, 0), len(c.Text)))
	start := snapBack(c.Text, max(off-before, 0))
	end := snapBack(c.Text, min(off+after, len(c.Text)))
	if start > 0 {
		if i := strings.IndexAny(c.Text[start:off], " \n"); i >= 0 {
			start += i + 1
		}
	}
	if end < len(c.Text) {
		if i := strings.LastIndexAny(c.Text[off:end], " \n"); i >= 0 {
			end = off + i
		}
	}
	return b.passage(ch, start, end)
}

func (b *Book) passage(ch, start, end int) Passage {
	c := &b.Chapters[ch]
	return Passage{
		Chapter:     ch,
		Title:       c.Title,
		Start:       start,
		End:         end,
		Progression: b.Progression(ch, start),
		Text:        c.Text[start:end],
	}
}

func snapBack(s string, i int) int {
	for i > 0 && i < len(s) && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

// FirstSentence is the opening of a chapter, at most maxBytes long.
func (c *Chapter) FirstSentence(maxBytes int) string {
	t := c.Text
	if c.Title != "" {
		// The heading is usually the chapter's first line; skip it.
		if line, rest, ok := strings.Cut(t, "\n"); ok && strings.EqualFold(collapse(line), c.Title) {
			t = rest
		}
	}
	end := len(t)
	for i, r := range t {
		if r == '\n' && i > 0 {
			end = i
			break
		}
		if (r == '.' || r == '!' || r == '?') && i > 20 {
			end = i + 1
			break
		}
	}
	if end > maxBytes {
		end = snapBack(t, maxBytes)
		return strings.TrimSpace(t[:end]) + "…"
	}
	return strings.TrimSpace(t[:end])
}

// Locate finds an offset in the chapter a locator names. href is the
// reading-order resource, resProg the fraction within it, and quote the
// text the locator was anchored on (before + highlight). It returns
// exact when the quote was found, so the caller can say whether the
// position is the reader's paragraph or an estimate.
func (b *Book) Locate(href string, resProg *float64, before, highlight string) (ch, off int, exact, ok bool) {
	want, okp := archivePath(href)
	if !okp {
		return 0, 0, false, false
	}
	ch = -1
	for i, c := range b.Chapters {
		if name, _ := archivePath(c.Href); name == want {
			ch = i
			break
		}
	}
	if ch < 0 {
		return 0, 0, false, false
	}
	c := b.Chapters[ch]
	guess := 0
	if resProg != nil {
		guess = snapBack(c.Text, int(min(max(*resProg, 0), 1)*float64(len(c.Text))))
	}
	if h := collapse(highlight); h != "" {
		quote := h
		if bq := collapse(before); bq != "" {
			quote = bq + " " + h
		}
		for _, q := range []string{quote, h} {
			if hits := allIndexes(c.Text, q); len(hits) > 0 {
				best := hits[0]
				for _, h := range hits {
					if abs(h-guess) < abs(best-guess) {
						best = h
					}
				}
				if q == quote && q != h {
					best += len(quote) - len(h)
				}
				return ch, best, true, true
			}
		}
	}
	return ch, guess, false, true
}

func allIndexes(s, sub string) []int {
	var out []int
	for at := 0; len(out) < 64; {
		i := strings.Index(s[at:], sub)
		if i < 0 {
			break
		}
		out = append(out, at+i)
		at += i + 1
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// folded is a chapter's text lowercased, stripped of diacritics, with
// every run of anything but letters and digits made one space, and the
// original byte offset of every folded byte.
type folded struct {
	text string
	orig []int32
}

func fold(s string, cache map[rune]string) folded {
	var b strings.Builder
	orig := make([]int32, 0, len(s)+1)
	b.Grow(len(s))
	space := true
	for i, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			if !unicode.IsMark(r) {
				space = true
			}
			continue
		}
		var f string
		if r < utf8.RuneSelf {
			f = string(unicode.ToLower(r))
		} else if v, ok := cache[r]; ok {
			f = v
		} else {
			f = store.FoldForSearch(string(r))
			cache[r] = f
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
			orig = append(orig, int32(i))
		}
		space = false
		for k := 0; k < len(f); k++ {
			orig = append(orig, int32(i))
		}
		b.WriteString(f)
	}
	orig = append(orig, int32(len(s)))
	return folded{text: b.String(), orig: orig}
}

// FoldQuery normalizes a query or a name the way chapter text is folded
// for matching.
func FoldQuery(s string) string { return fold(s, map[rune]string{}).text }

// Hit is one match: where it is and which term matched.
type Hit struct {
	Chapter int
	Offset  int
	Length  int
	Term    int
}

type Position struct {
	Chapter int
	Offset  int
}

// Find returns the matches of terms (already folded with FoldQuery), in
// reading order, on word boundaries when words is set. It stops after
// limit hits and reports whether there were more. Chapters before
// fromChapter are skipped, and matches must end at or before stop.
func (b *Book) Find(terms []string, words bool, fromChapter int, stop *Position, limit int) ([]Hit, bool) {
	cache := map[rune]string{}
	var hits []Hit
	for ch := max(fromChapter, 0); ch < len(b.Chapters); ch++ {
		c := &b.Chapters[ch]
		if stop != nil && ch > stop.Chapter {
			break
		}
		f := fold(c.Text, cache)
		var chapterHits []Hit
		for ti, term := range terms {
			if term == "" {
				continue
			}
			termHits := 0
			for at := 0; at <= len(f.text); {
				i := strings.Index(f.text[at:], term)
				if i < 0 {
					break
				}
				s, e := at+i, at+i+len(term)
				at = s + 1
				if words && ((s > 0 && f.text[s-1] != ' ') || (e < len(f.text) && f.text[e] != ' ')) {
					continue
				}
				off := int(f.orig[s])
				last := int(f.orig[e-1])
				_, size := utf8.DecodeRuneInString(c.Text[last:])
				if stop != nil && ch == stop.Chapter && last+size > stop.Offset {
					if off >= stop.Offset {
						break
					}
					continue
				}
				chapterHits = append(chapterHits, Hit{Chapter: ch, Offset: off, Length: last + size - off, Term: ti})
				termHits++
				if termHits > limit {
					// Later hits of this term cannot be among the
					// first limit+1 of the chapter.
					break
				}
			}
		}
		sort.SliceStable(chapterHits, func(i, j int) bool { return chapterHits[i].Offset < chapterHits[j].Offset })
		chapterHits = dedupeOverlaps(chapterHits)
		for _, h := range chapterHits {
			if len(hits) == limit {
				return hits, true
			}
			hits = append(hits, h)
		}
	}
	return hits, false
}

// dedupeOverlaps keeps the longest match where two terms hit the same
// place ("Rodion" inside "Rodion Romanovich").
func dedupeOverlaps(h []Hit) []Hit {
	out := h[:0]
	for _, x := range h {
		if n := len(out); n > 0 && x.Offset < out[n-1].Offset+out[n-1].Length {
			if x.Length > out[n-1].Length && x.Offset == out[n-1].Offset {
				out[n-1] = x
			}
			continue
		}
		out = append(out, x)
	}
	return out
}
