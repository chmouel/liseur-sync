package audiobookshelf

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/readium/go-toolkit/pkg/manifest"
	"github.com/readium/go-toolkit/pkg/util/url"

	"github.com/chmouel/liseur-sync/internal/epub"
)

// evenBook is a book whose TOC is titles, one document each, spread
// evenly over the totalProgression axis.
func evenBook(titles ...string) ([]BookChapter, []Resource) {
	n := float64(len(titles))
	var book []BookChapter
	var res []Resource
	for i, t := range titles {
		href := fmt.Sprintf("c%02d.xhtml", i)
		s, e := float64(i)/n, float64(i+1)/n
		book = append(book, BookChapter{Title: t, Href: href, Start: s, End: e})
		res = append(res, Resource{Href: href, Start: s, End: e})
	}
	return book, res
}

// evenAudio is chapters of 100 seconds each.
func evenAudio(titles ...string) []Chapter {
	var out []Chapter
	for i, t := range titles {
		out = append(out, Chapter{Title: t, Start: float64(i * 100), End: float64(i*100 + 100)})
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestAlignWholeBookWithFrontAndBackMatter(t *testing.T) {
	book, res := evenBook("Dedication", "Maps", "CHAPTER 1", "CHAPTER 2", "CHAPTER 3", "About the author")
	audio := evenAudio("Chapter 01", "Chapter 02", "Chapter 03")
	m, err := Align(book, res, audio, 300)
	if err != nil {
		t.Fatal(err)
	}
	if (!m.CoversFirst || !m.CoversLast || !m.FullNarrative) || len(m.Segments) != 3 {
		t.Fatalf("mapping %+v", m)
	}
	a0, a1, b0, b1 := m.Coverage()
	if a0 != 0 || a1 != 300 || !near(b0, 2.0/6) || !near(b1, 5.0/6) {
		t.Fatalf("coverage %v %v %v %v", a0, a1, b0, b1)
	}
	// Halfway through the second audio chapter is halfway through the
	// second book chapter, and back.
	p, seg, ok := m.ToBook(150)
	if !ok || !near(p, 3.5/6) || seg.Href != "c03.xhtml" {
		t.Fatalf("ToBook(150) = %v %+v %v", p, seg, ok)
	}
	if back, ok := m.ToAudio(p); !ok || !near(back, 150) {
		t.Fatalf("ToAudio = %v %v", back, ok)
	}
	// Front and back matter are not covered.
	if _, ok := m.ToAudio(0.1); ok {
		t.Fatal("front matter mapped to audio")
	}
	if _, ok := m.ToAudio(0.95); ok {
		t.Fatal("back matter mapped to audio")
	}
	// A time a rounding past the last chapter is still the end.
	if p, _, ok := m.ToBook(300.5); !ok || !near(p, 5.0/6) {
		t.Fatalf("ToBook(300.5) = %v %v", p, ok)
	}
}

func TestAlignPartialAudiobookWithIntroduction(t *testing.T) {
	var titles []string
	for i := 1; i <= 10; i++ {
		titles = append(titles, fmt.Sprintf("CHAPTER %d", i))
	}
	book, res := evenBook(titles...)
	audio := evenAudio("Introduction", "CHAPITRE 6: Dissolution", "CHAPITRE 7", "CHAPITRE 8", "Chapitre 9", "CHAPITRE 10: LE DIABLE", "Crédits")
	m, err := Align(book, res, audio, 700)
	if err != nil {
		t.Fatal(err)
	}
	if m.CoversFirst || !m.CoversLast || m.FullNarrative {
		t.Fatalf("partial recording coverage: %+v", m)
	}
	if p, _, ok := m.ToBook(600); !ok || !near(p, 0.998) {
		t.Fatalf("partial recording end = %v %v", p, ok)
	}
	if _, _, ok := m.ToBook(50); ok {
		t.Fatal("the introduction was mapped")
	}
	if _, _, ok := m.ToBook(650); ok {
		t.Fatal("the credits were mapped")
	}
	if p, _, ok := m.ToBook(100); !ok || !near(p, 0.5) {
		t.Fatalf("chapter 6 starts at %v %v", p, ok)
	}
	if _, ok := m.ToAudio(0.2); ok {
		t.Fatal("a chapter the audiobook lacks was mapped")
	}

	// The first half only: finishing the audiobook is not finishing the book.
	audio = evenAudio("Chapter 1", "Chapter 2", "Chapter 3")
	if m, err = Align(book, res, audio, 300); err != nil || !m.CoversFirst || m.CoversLast || m.FullNarrative {
		t.Fatalf("first half: %+v %v", m, err)
	}
}

func TestAlignNestedPartsWithRepeatedNumbers(t *testing.T) {
	book, res := evenBook("Part One", "Chapter 1", "Chapter 2", "Part Two", "Chapter 1", "Chapter 2")
	audio := evenAudio("Part One", "Chapter 1", "Chapter 2", "Part Two", "Chapter 1", "Chapter 2")
	m, err := Align(book, res, audio, 600)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Segments) != 4 || m.Segments[2].Href != "c04.xhtml" {
		t.Fatalf("segments %+v", m.Segments)
	}
	// "Part Two" sits between two matches on both sides and is absorbed.
	if p, _, ok := m.ToBook(350); !ok || p < 2.0/6 || p > 4.0/6 {
		t.Fatalf("ToBook(350) = %v %v", p, ok)
	}
}

func TestAlignRefusesWhatItCannotVouchFor(t *testing.T) {
	for name, tc := range map[string]struct {
		book  []string
		audio []string
	}{
		"no numbers":           {[]string{"Chapter 1", "Chapter 2"}, []string{"Track A", "Track B"}},
		"audio chapter absent": {[]string{"Chapter 1", "Chapter 2"}, []string{"Chapter 1", "Chapter 2", "Chapter 3"}},
		"gap in numbers":       {[]string{"Chapter 1", "Chapter 3"}, []string{"Chapter 1", "Chapter 3"}},
		"out of order":         {[]string{"Chapter 2", "Chapter 1"}, []string{"Chapter 1", "Chapter 2"}},
		"book chapter missing from audio": {
			[]string{"Chapter 1", "Chapter 2", "Chapter 3"}, []string{"Chapter 1", "Chapter 3"}},
		"duplicate on one side": {
			[]string{"Chapter 1", "Chapter 1", "Chapter 2"}, []string{"Chapter 1", "Chapter 2"}},
		"interlude only in audio": {
			[]string{"Chapter 1", "Chapter 2"}, []string{"Chapter 1", "Interlude", "Chapter 2"}},
	} {
		book, res := evenBook(tc.book...)
		if _, err := Align(book, res, evenAudio(tc.audio...), 1000); !errors.Is(err, ErrNoAlignment) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAlignRejectsInvalidChapterTimes(t *testing.T) {
	book, res := evenBook("Chapter 1", "Chapter 2")
	for name, mutate := range map[string]func([]Chapter, *float64){
		"zero duration":        func(_ []Chapter, d *float64) { *d = 0 },
		"infinite duration":    func(_ []Chapter, d *float64) { *d = math.Inf(1) },
		"nan duration":         func(_ []Chapter, d *float64) { *d = math.NaN() },
		"negative start":       func(a []Chapter, _ *float64) { a[0].Start = -1 },
		"infinite end":         func(a []Chapter, _ *float64) { a[1].End = math.Inf(1) },
		"nan start":            func(a []Chapter, _ *float64) { a[0].Start = math.NaN() },
		"empty chapter":        func(a []Chapter, _ *float64) { a[1].End = a[1].Start },
		"past duration":        func(a []Chapter, _ *float64) { a[1].End = 202 },
		"overlap":              func(a []Chapter, _ *float64) { a[1].Start = 99 },
		"invalid introduction": func(a []Chapter, _ *float64) { a[0].Title, a[0].Start = "Introduction", -1 },
	} {
		t.Run(name, func(t *testing.T) {
			audio, duration := evenAudio("Chapter 1", "Chapter 2"), float64(200)
			mutate(audio, &duration)
			if _, err := Align(book, res, audio, duration); !errors.Is(err, ErrNoAlignment) {
				t.Fatalf("Align = %v", err)
			}
		})
	}
}

func TestLocatorNamesTheDocument(t *testing.T) {
	// Two chapters share one document; the second starts at 60% of it.
	res := []Resource{{Href: "front.xhtml", Start: 0, End: 0.2}, {Href: "body.xhtml", Start: 0.2, End: 1}}
	book := []BookChapter{
		{Title: "Chapter 1", Href: "body.xhtml", Start: 0.2, End: 0.68},
		{Title: "Chapter 2", Href: "body.xhtml", Fragment: "ch2", Start: 0.68, End: 1, Approx: true},
	}
	m, err := Align(book, res, evenAudio("Chapter 1", "Chapter 2"), 200)
	if err != nil {
		t.Fatal(err)
	}
	p, seg, _ := m.ToBook(100)
	raw, err := m.LocatorAt(p, seg, true)
	if err != nil {
		t.Fatal(err)
	}
	var loc locator
	if err := json.Unmarshal(raw, &loc); err != nil {
		t.Fatal(err)
	}
	if loc.Href != "body.xhtml" || !near(loc.Locations.Progression, 0.6) || !near(loc.Locations.TotalProgression, 0.68) ||
		len(loc.Locations.Fragments) != 1 || loc.Locations.Fragments[0] != "ch2" {
		t.Fatalf("locator %s", raw)
	}
	// Mid-chapter, the fragment would send a reader back to the heading.
	p, seg, _ = m.ToBook(150)
	var mid locator
	if raw, _ = m.LocatorAt(p, seg, false); json.Unmarshal(raw, &mid) != nil || len(mid.Locations.Fragments) != 0 {
		t.Fatalf("mid-chapter locator %s", raw)
	}
}

func TestLinearMappingCoversEverything(t *testing.T) {
	m := LinearMapping([]Resource{{Href: "a", Start: 0, End: 1}}, 1000)
	if p, _, ok := m.ToBook(250); !ok || !near(p, 0.25) {
		t.Fatalf("ToBook = %v %v", p, ok)
	}
	if a, ok := m.ToAudio(1); !ok || a != 1000 || (!m.CoversFirst || !m.CoversLast || !m.FullNarrative) {
		t.Fatalf("ToAudio(1) = %v %v", a, ok)
	}
}

func TestChapterNumber(t *testing.T) {
	for title, want := range map[string]int{
		"Chapter 01":               1,
		"CHAPTER 68":               68,
		"CHAPITRE 37: Dissolution": 37,
		"Ch. 4":                    4,
		"Chapter XII":              12,
		"chapter iv - the road":    4,
		"12":                       12,
		"3. The Fall":              3,
		"IX":                       9,
		"XI.":                      11,
		"I Am Legend":              0,
		"Introduction":             0,
		"Chapter One":              0,
		"1984":                     1984,
		"Part Two":                 0,
		"Chapter IIII":             0,
		"Mix 3":                    0,
	} {
		if got := chapterNumber(title); got != want {
			t.Errorf("chapterNumber(%q) = %d, want %d", title, got, want)
		}
	}
}

func TestTextFractionBefore(t *testing.T) {
	doc := []byte(`<?xml version="1.0"?><html><head><title>Long title text here</title><style>p{}</style></head>
<body><h1 id="c1">AAAA</h1><p>AAAA</p><script>var x = 1;</script><h1 id="c2">BBBB</h1><p>BBBB<a name="old"/></p>CC</body></html>`)
	for id, want := range map[string]float64{"c1": 0, "c2": 8.0 / 18, "old": 16.0 / 18} {
		if got, ok := textFractionBefore(doc, id); !ok || !near(got, want) {
			t.Errorf("%s: %v %v, want %v", id, got, ok, want)
		}
	}
	if _, ok := textFractionBefore(doc, "missing"); ok {
		t.Error("a missing id was found")
	}
}

func TestChaptersKeyNoticesARecut(t *testing.T) {
	a := evenAudio("Chapter 1", "Chapter 2")
	b := evenAudio("Chapter 1", "Chapter 2")
	b[1].Start = 101
	if ChaptersKey("sha", a, 200, false) == ChaptersKey("sha", b, 200, false) ||
		ChaptersKey("sha", a, 200, false) == ChaptersKey("sha2", a, 200, false) ||
		ChaptersKey("sha", a, 200, false) == ChaptersKey("sha", a, 200, true) {
		t.Fatal("key ignores a change")
	}
}

func TestBookAnchorsPlacesFragmentsInsideADocument(t *testing.T) {
	pos := func(href string, tp float64) manifest.Locator {
		u, err := url.URLFromString(href)
		if err != nil {
			t.Fatal(err)
		}
		return manifest.Locator{Href: u, Locations: manifest.Locations{TotalProgression: &tp}}
	}
	link := func(title, href string, children ...manifest.Link) manifest.Link {
		return manifest.Link{Title: title, Href: manifest.MustNewHREFFromString(href, false), Children: children}
	}
	idx := &epub.Index{
		Positions: []manifest.Locator{pos("text/front.xhtml", 0), pos("text/body.xhtml", 0.2), pos("text/body.xhtml", 0.6)},
		Manifest: manifest.Manifest{TableOfContents: manifest.LinkList{
			link("Contents", "text/front.xhtml"),
			link("Part One", "text/body.xhtml",
				link("Chapter 1", "text/body.xhtml#c1"),
				link("Chapter 2", "text/body.xhtml#c2")),
			link("Elsewhere", "text/not-in-spine.xhtml"),
		}},
	}
	doc := []byte(`<html><body><h1 id="c1">AAAA</h1><p>AAAA</p><h1 id="c2">BBBB</h1><p>BBBBBBBB</p></body></html>`)
	book, res, err := BookAnchors(idx, func(href string) ([]byte, error) {
		if href != "text/body.xhtml" {
			t.Errorf("read %s", href)
		}
		return doc, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[1].Start != 0.2 || res[1].End != 1 {
		t.Fatalf("resources %+v", res)
	}
	got := map[string]BookChapter{}
	for _, c := range book {
		got[c.Title] = c
	}
	if len(book) != 4 || got["Elsewhere"].Title != "" {
		t.Fatalf("entries %+v", book)
	}
	// Chapter 1 opens the document, so it is exact and shares Part One's start.
	if c := got["Chapter 1"]; c.Approx || c.Start != 0.2 || !near(c.End, 0.2+0.8*8/20) {
		t.Fatalf("chapter 1 %+v", c)
	}
	if c := got["Chapter 2"]; !c.Approx || c.Fragment != "c2" || !near(c.Start, 0.2+0.8*8/20) || c.End != 1 {
		t.Fatalf("chapter 2 %+v", c)
	}

	idx.Manifest.TableOfContents = manifest.LinkList{link("Chapter 9", "text/body.xhtml#nowhere")}
	if _, _, err := BookAnchors(idx, func(string) ([]byte, error) { return doc, nil }); err == nil {
		t.Fatal("an entry naming a missing element was placed")
	}
}
