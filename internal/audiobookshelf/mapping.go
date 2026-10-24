package audiobookshelf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// BookChapter is one table-of-contents entry of an EPUB, placed on the
// book's totalProgression axis. End is the next entry's start, so back
// matter ends the last chapter rather than being swallowed by it.
type BookChapter struct {
	Title    string
	Href     string
	Fragment string
	Start    float64
	End      float64
	// Approx is an entry inside a resource, placed by its share of the
	// resource's text rather than by a position the reader computes.
	Approx bool
}

// Resource is one reading-order document on the totalProgression axis.
type Resource struct {
	Href  string  `json:"href"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Segment pairs one stretch of audio with one stretch of the book.
type Segment struct {
	Label      string  `json:"label"`
	AudioStart float64 `json:"a0"`
	AudioEnd   float64 `json:"a1"`
	BookStart  float64 `json:"b0"`
	BookEnd    float64 `json:"b1"`
	Href       string  `json:"href,omitempty"`
	Fragment   string  `json:"frag,omitempty"`
	Approx     bool    `json:"approx,omitempty"`
}

// Mapping is a link's alignment. Positions outside its segments are not
// exchanged: nothing is clamped onto the nearest chapter.
type Mapping struct {
	Duration      float64    `json:"duration"`
	Segments      []Segment  `json:"segments"`
	Resources     []Resource `json:"resources"`
	CoversFirst   bool       `json:"covers_first,omitempty"`
	CoversLast    bool       `json:"covers_last,omitempty"`
	FullNarrative bool       `json:"full_narrative,omitempty"`
	Linear        bool       `json:"linear,omitempty"`
}

// ErrNoAlignment is a book and an audiobook whose chapters could not be
// paired with full confidence.
var ErrNoAlignment = errors.New("chapters could not be aligned")

// Align pairs an audiobook's chapters with a book's table of contents by
// chapter number. It succeeds only with full confidence: every numbered
// audio chapter matched, numbers without gaps, both axes strictly
// increasing. Unnumbered audio chapters before the first match and
// after the last (an introduction, the credits) are left outside
// coverage.
func Align(book []BookChapter, resources []Resource, audio []Chapter, duration float64) (Mapping, error) {
	if err := validateAudio(audio, duration); err != nil {
		return Mapping{}, err
	}
	audioNums := make([]int, len(audio))
	bookNums := make([]int, len(book))
	for i, c := range audio {
		audioNums[i] = chapterNumber(c.Title)
	}
	for i, c := range book {
		bookNums[i] = chapterNumber(c.Title)
	}
	type pair struct{ a, b int }
	var pairs []pair
	audioByNum, bookByNum := occurrences(audioNums), occurrences(bookNums)
	for n, as := range audioByNum {
		bs := bookByNum[n]
		switch {
		case len(bs) == 0:
			return Mapping{}, fmt.Errorf("%w: audio chapter %d is not in the book", ErrNoAlignment, n)
		case len(as) != len(bs):
			return Mapping{}, fmt.Errorf("%w: chapter %d appears %d times in the audio and %d in the book",
				ErrNoAlignment, n, len(as), len(bs))
		}
		for k := range as {
			pairs = append(pairs, pair{as[k], bs[k]})
		}
	}
	if len(pairs) == 0 {
		return Mapping{}, fmt.Errorf("%w: no numbered audio chapter", ErrNoAlignment)
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].a < pairs[j].a })

	for k, p := range pairs {
		a, b := audio[p.a], book[p.b]
		if !finite(b.Start) || !finite(b.End) || b.Start < 0 || b.End > 1 || !(b.Start < b.End) {
			return Mapping{}, fmt.Errorf("%w: chapter %q is empty", ErrNoAlignment, a.Title)
		}
		if k == 0 {
			continue
		}
		prev := pairs[k-1]
		if p.b <= prev.b || !(audio[prev.a].Start < a.Start) || !(book[prev.b].Start < b.Start) {
			return Mapping{}, fmt.Errorf("%w: chapter %q is out of order", ErrNoAlignment, a.Title)
		}
		if n, pn := audioNums[p.a], audioNums[prev.a]; n != pn+1 && n != 1 {
			return Mapping{}, fmt.Errorf("%w: chapter %d follows %d", ErrNoAlignment, n, pn)
		}
		// An unnumbered audio chapter between two matches (an interlude)
		// is absorbed only when the book has an unnumbered entry there too.
		if p.a > prev.a+1 && !hasUnnumbered(bookNums, prev.b+1, p.b) {
			return Mapping{}, fmt.Errorf("%w: %q has no counterpart in the book", ErrNoAlignment, audio[prev.a+1].Title)
		}
		// A numbered book entry between two matches that the audio lacks.
		for i := prev.b + 1; i < p.b; i++ {
			if bookNums[i] > 0 && book[i].Start > book[prev.b].Start && book[i].Start < b.Start {
				return Mapping{}, fmt.Errorf("%w: book chapter %q is not in the audio", ErrNoAlignment, book[i].Title)
			}
		}
	}

	m := Mapping{Duration: duration, Resources: resources}
	for k, p := range pairs {
		a, b := audio[p.a], book[p.b]
		seg := Segment{
			Label: a.Title, AudioStart: a.Start, AudioEnd: a.End, BookStart: b.Start, BookEnd: b.End,
			Href: b.Href, Fragment: b.Fragment, Approx: b.Approx,
		}
		if k+1 < len(pairs) {
			seg.AudioEnd = audio[pairs[k+1].a].Start
			seg.BookEnd = book[pairs[k+1].b].Start
		}
		m.Segments = append(m.Segments, seg)
	}
	first, last := pairs[0].b, pairs[len(pairs)-1].b
	m.CoversFirst, m.CoversLast = true, true
	for i, n := range bookNums {
		if n > 0 && book[i].Start < book[first].Start {
			m.CoversFirst = false
		}
		if n > 0 && book[i].Start > book[last].Start {
			m.CoversLast = false
		}
	}
	m.FullNarrative = true
	matched := make(map[int]bool, len(pairs))
	for _, p := range pairs {
		matched[p.b] = true
	}
	for i, n := range bookNums {
		if n > 0 && !matched[i] {
			m.FullNarrative = false
		}
	}
	return m, nil
}

// LinearMapping is the whole audiobook against the whole book, for a
// reader who confirmed the audiobook covers it all.
func LinearMapping(resources []Resource, duration float64) Mapping {
	return Mapping{
		Duration: duration, Resources: resources, CoversFirst: true, CoversLast: true, FullNarrative: true, Linear: true,
		Segments: []Segment{{Label: "whole book", AudioStart: 0, AudioEnd: duration, BookStart: 0, BookEnd: 1}},
	}
}

func occurrences(nums []int) map[int][]int {
	out := map[int][]int{}
	for i, n := range nums {
		if n > 0 {
			out[n] = append(out[n], i)
		}
	}
	return out
}

func hasUnnumbered(nums []int, from, to int) bool {
	for i := from; i < to; i++ {
		if nums[i] == 0 {
			return true
		}
	}
	return false
}

// Coverage is the stretch of each axis the mapping speaks for.
func (m Mapping) Coverage() (audioStart, audioEnd, bookStart, bookEnd float64) {
	if len(m.Segments) == 0 {
		return 0, 0, 0, 0
	}
	first, last := m.Segments[0], m.Segments[len(m.Segments)-1]
	return first.AudioStart, last.AudioEnd, first.BookStart, last.BookEnd
}

// segmentAt finds the segment holding v on one axis. The end of the
// last segment belongs to it; every other end belongs to the next.
func (m Mapping) segmentAt(v float64, audio bool) (Segment, bool) {
	for i, s := range m.Segments {
		lo, hi := s.BookStart, s.BookEnd
		if audio {
			lo, hi = s.AudioStart, s.AudioEnd
		}
		if v >= lo && (v < hi || (i == len(m.Segments)-1 && v <= hi)) {
			return s, true
		}
	}
	// A player's duration and its last chapter's end can disagree by a
	// rounding; a time within a second past the end is still the end.
	if n := len(m.Segments); audio && n > 0 {
		if last := m.Segments[n-1]; v > last.AudioEnd && v <= last.AudioEnd+1 {
			return last, true
		}
	}
	return Segment{}, false
}

// ToBook maps an audio time to a book totalProgression.
func (m Mapping) ToBook(t float64) (float64, Segment, bool) {
	s, ok := m.segmentAt(t, true)
	if !ok {
		return 0, Segment{}, false
	}
	t = math.Min(t, s.AudioEnd)
	p := lerp(t, s.AudioStart, s.AudioEnd, s.BookStart, s.BookEnd)
	if !m.FullNarrative {
		// Reading completion starts at 0.999, even without an explicit finish
		p = math.Min(p, 0.998)
	}
	return p, s, true
}

// ToAudio maps a book totalProgression to an audio time.
func (m Mapping) ToAudio(p float64) (float64, bool) {
	s, ok := m.segmentAt(p, false)
	if !ok {
		return 0, false
	}
	return lerp(p, s.BookStart, s.BookEnd, s.AudioStart, s.AudioEnd), true
}

func lerp(v, x0, x1, y0, y1 float64) float64 {
	if x1 <= x0 {
		return y0
	}
	return y0 + (v-x0)/(x1-x0)*(y1-y0)
}

// locator is the readium locator an inbound op carries.
type locator struct {
	Href      string           `json:"href"`
	Type      string           `json:"type"`
	Locations locatorLocations `json:"locations"`
}

type locatorLocations struct {
	Fragments        []string `json:"fragments,omitempty"`
	Progression      float64  `json:"progression"`
	TotalProgression float64  `json:"totalProgression"`
}

// LocatorAt names the document holding totalProgression p and where in
// it, so a reader opens the right chapter rather than estimating from a
// whole-book fraction. atHead adds the chapter's fragment, for a
// position at the very start of a chapter that shares its document.
func (m Mapping) LocatorAt(p float64, seg Segment, atHead bool) ([]byte, error) {
	if len(m.Resources) == 0 {
		return nil, errors.New("mapping has no reading order")
	}
	r := m.Resources[len(m.Resources)-1]
	for _, c := range m.Resources {
		if p >= c.Start && p < c.End {
			r = c
			break
		}
	}
	prog := clamp01(lerp(p, r.Start, r.End, 0, 1))
	loc := locator{Href: r.Href, Type: "application/xhtml+xml",
		Locations: locatorLocations{Progression: prog, TotalProgression: clamp01(p)}}
	if atHead && seg.Fragment != "" && seg.Href == r.Href {
		loc.Locations.Fragments = []string{seg.Fragment}
	}
	return json.Marshal(loc)
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// ChaptersKey names an audiobook's chapter list, so a re-cut is noticed.
func ChaptersKey(bookSHA string, chapters []Chapter, duration float64, linear bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%.17g\n%t\n", bookSHA, duration, linear)
	for _, c := range chapters {
		fmt.Fprintf(h, "%.3f\t%.3f\t%s\n", c.Start, c.End, c.Title)
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

var (
	prefixedNumber = regexp.MustCompile(`^(?:chapter|chapitre|ch\.?)\s*[-:.]?\s*([0-9]+|[ivxlcdm]+)\b`)
	leadingDigits  = regexp.MustCompile(`^([0-9]+)(?:$|[\s.:)\-–—])`)
	bareRoman      = regexp.MustCompile(`^([ivxlcdm]+)(?:$|\s*[.:\-–—]\s)|^([ivxlcdm]+)\.$`)
)

// chapterNumber reads the chapter number a title starts with, 0 when
// there is none. Number words ("Chapter One") are not read: a title
// they would misread is worse than a chapter left for the reader.
func chapterNumber(title string) int {
	t := strings.ToLower(strings.TrimSpace(title))
	if m := prefixedNumber.FindStringSubmatch(t); m != nil {
		return parseNumber(m[1])
	}
	if m := leadingDigits.FindStringSubmatch(t); m != nil {
		return parseNumber(m[1])
	}
	if m := bareRoman.FindStringSubmatch(t); m != nil {
		return parseNumber(m[1] + m[2])
	}
	return 0
}

func parseNumber(s string) int {
	if n, err := strconv.Atoi(s); err == nil {
		if n > 0 && n < 10000 {
			return n
		}
		return 0
	}
	return roman(s)
}

// roman reads a well-formed roman numeral, 0 for anything else.
func roman(s string) int {
	vals := map[byte]int{'i': 1, 'v': 5, 'x': 10, 'l': 50, 'c': 100, 'd': 500, 'm': 1000}
	total := 0
	for i := 0; i < len(s); i++ {
		v := vals[s[i]]
		if i+1 < len(s) && vals[s[i+1]] > v {
			total -= v
		} else {
			total += v
		}
	}
	if total <= 0 || toRoman(total) != s {
		return 0
	}
	return total
}

func toRoman(n int) string {
	syms := []struct {
		v int
		s string
	}{{1000, "m"}, {900, "cm"}, {500, "d"}, {400, "cd"}, {100, "c"}, {90, "xc"}, {50, "l"}, {40, "xl"}, {10, "x"}, {9, "ix"}, {5, "v"}, {4, "iv"}, {1, "i"}}
	var b strings.Builder
	for _, x := range syms {
		for n >= x.v {
			b.WriteString(x.s)
			n -= x.v
		}
	}
	return b.String()
}

func validateAudio(audio []Chapter, duration float64) error {
	if !finite(duration) || duration <= 0 {
		return fmt.Errorf("%w: invalid audio duration", ErrNoAlignment)
	}
	for i, c := range audio {
		if !finite(c.Start) || !finite(c.End) || c.Start < 0 || c.End <= c.Start || c.End > duration+1 ||
			(i > 0 && c.Start < audio[i-1].End) {
			return fmt.Errorf("%w: invalid times for audio chapter %q", ErrNoAlignment, c.Title)
		}
	}
	return nil
}
