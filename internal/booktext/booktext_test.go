package booktext

import (
	"context"
	"strings"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/readium/go-toolkit/pkg/manifest"
	readiumurl "github.com/readium/go-toolkit/pkg/util/url"
	"testing"
	"unicode/utf8"
)

func TestExtractPreservesTruncatedProgression(t *testing.T) {
	href := func(name string) manifest.HREF { return manifest.NewHREF(readiumurl.MustURLFromString(name)) }
	first, second := href("one.xhtml"), href("two.xhtml")
	idx := &epub.Index{Entries: map[string]epub.IndexEntry{"one.xhtml": {}, "two.xhtml": {}}}
	idx.Manifest.ReadingOrder = manifest.LinkList{{Href: first}, {Href: second}}
	start, end := 0.0, 0.4
	idx.Positions = []manifest.Locator{{Href: readiumurl.MustURLFromString("one.xhtml")}, {Href: readiumurl.MustURLFromString("two.xhtml")}}
	idx.Positions[0].Locations.TotalProgression = &start
	idx.Positions[1].Locations.TotalProgression = &end
	read := func(name string) ([]byte, error) {
		if name == "one.xhtml" {
			return []byte("<p>first chapter</p>"), nil
		}
		return []byte(strings.Repeat("x", MaxBookBytes)), nil
	}
	b, err := Extract(context.Background(), idx, read)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Truncated || b.Chapters[0].EndProg != 0.4 {
		t.Fatalf("extraction: %+v", b)
	}
	if _, _, ok := b.AtProgression(0.2); !ok {
		t.Fatal("retained progression rejected")
	}
	if _, _, ok := b.AtProgression(0.9); ok {
		t.Fatal("unextracted progression accepted")
	}
	idx.Positions = nil
	b, err = Extract(context.Background(), idx, read)
	if err != nil {
		t.Fatal(err)
	}
	if b.ProgressionKnown() {
		t.Fatal("unknown truncated range advertised")
	}
	if _, _, ok := b.AtProgression(0.2); ok {
		t.Fatal("unknown truncated range accepted")
	}
}

func TestHTMLText(t *testing.T) {
	doc := `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>x</title><style>p{}</style></head>
<body><h1>Chapter <em>One</em></h1><p>It was   a <b>dark</b>
night.</p><script>alert(1)</script><p hidden="">secret</p><div style="display: none">gone</div><p>Second<br/>line</p>
<span aria-hidden="true">aria</span><p>Émile</p></body></html>`
	text, heading := htmlText([]byte(doc))
	want := "Chapter One\nIt was a dark night.\nSecond\nline\nÉmile"
	if text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
	if heading != "Chapter One" {
		t.Fatalf("heading = %q", heading)
	}
}

func TestHTMLTextKeepsHiddenSubtreeClosed(t *testing.T) {
	doc := `<html><body><div hidden><br></br>Hidden ending <span>nested</span></div><p>Visible</p></body></html>`
	text, _ := htmlText([]byte(doc))
	if text != "Visible" {
		t.Fatalf("hidden subtree: %q", text)
	}
	deep := "<div hidden>" + strings.Repeat("<span>", 2000) + strings.Repeat("</br></other>", 2000) + "Hidden</div><p>Visible</p>"
	text, _ = htmlText([]byte(deep))
	if text != "Visible" {
		t.Fatalf("deep hidden subtree: %q", text)
	}
}

func TestHTMLTextChecksActualHiddenStyleProperties(t *testing.T) {
	doc := `<html><body><p style="display:&#9;none">Hidden display</p><p style="visibility: &#10;hidden !important">Hidden visibility</p><p style="--display:none">Visible custom property</p><p style="display:block">Visible text</p></body></html>`
	text, _ := htmlText([]byte(doc))
	if strings.Contains(text, "Hidden") || !strings.Contains(text, "Visible custom property") || !strings.Contains(text, "Visible text") {
		t.Fatalf("extracted text: %q", text)
	}
}

func TestHTMLTextRespectsStyleDeclarationOrder(t *testing.T) {
	doc := `<html><body><p style="display:none; display:block">Visible override</p><p style="--label:'x;display:none;y'">Visible quoted value</p><p style="display:none!important; display:block">Hidden priority</p><p style="visibility:hidden; visibility:visible">Visible visibility</p><p style="display:none; display:bogus">Hidden invalid override</p><p style="display:none; display:initial">Visible display reset</p><p style="visibility:hidden; visibility:initial">Visible visibility reset</p><p style="display:none; display:table-caption">Visible table caption</p><p style="display:none; display:inline flow-root">Visible multi-keyword</p><p style="display:no/**/ne">Visible separated identifier</p><p style="display:none; display:inline/**/flow-root">Visible comment boundary</p></body></html>`
	text, _ := htmlText([]byte(doc))
	for _, visible := range []string{"Visible override", "Visible quoted value", "Visible visibility", "Visible display reset", "Visible visibility reset", "Visible table caption", "Visible multi-keyword", "Visible separated identifier", "Visible comment boundary"} {
		if !strings.Contains(text, visible) {
			t.Errorf("missing %q: %q", visible, text)
		}
	}
	if strings.Contains(text, "Hidden") {
		t.Fatalf("hidden text: %q", text)
	}
}

func TestHTMLTextAfterSelfClosingRawTextTags(t *testing.T) {
	for _, tag := range []string{"style", "title", "script"} {
		doc := "<html><head><" + tag + "/></head><body><p>Readable chapter.</p></body></html>"
		text, _ := htmlText([]byte(doc))
		if text != "Readable chapter." {
			t.Errorf("%s: %q", tag, text)
		}
	}
}

func TestInlineHeadingKeepsTextAndSkipsOpening(t *testing.T) {
	text, title := htmlText([]byte(`<html><body><h1>Un<em>break</em>able &amp; fine</h1><p>It began with a long journey. The end.</p></body></html>`))
	if title != "Unbreakable & fine" || !strings.HasPrefix(text, title+"\n") {
		t.Fatalf("heading %q, text %q", title, text)
	}
	chapter := Chapter{Title: title, Text: text}
	if got := chapter.FirstSentence(200); got != "It began with a long journey." {
		t.Fatalf("opening: %q", got)
	}
}

func TestMultilineHeadingKeepsTitleAndSkipsOpening(t *testing.T) {
	text, title := htmlText([]byte(`<html><body><h1>Chapter 1<br/>Arrival</h1><p>It began with a long journey. The end.</p></body></html>`))
	if title != "Chapter 1 Arrival" {
		t.Fatalf("heading %q, text %q", title, text)
	}
	chapter := Chapter{Title: title, Text: text}
	if got := chapter.FirstSentence(200); got != "It began with a long journey." {
		t.Fatalf("opening: %q", got)
	}
}

func TestChapterTitleBounds(t *testing.T) {
	long := strings.Repeat("é", MaxTitleBytes)
	_, heading := htmlText([]byte("<html><body><h1>" + long + "</h1></body></html>"))
	for _, title := range []string{heading, boundedTitle(long), boundedTitle("  one  two  ")} {
		if len(title) > MaxTitleBytes || !utf8.ValidString(title) {
			t.Fatalf("unbounded or invalid title: %q", title)
		}
	}
	if heading != strings.Repeat("é", MaxTitleBytes/2) || boundedTitle("  one  two  ") != "one two" {
		t.Fatalf("unexpected bounded heading: %q", heading)
	}
}

func testBook() *Book {
	b := &Book{Chapters: []Chapter{
		{Index: 0, Href: "one.xhtml", Title: "One", Text: "Rodion Romanovich Raskolnikov left his room. Rodya walked.", StartProg: -1},
		{Index: 1, Href: "Text/two%20b.xhtml", Title: "Two", Text: "Sonya met Rodya. Then Raskolnikov's sister, Dunya, arrived. Raskolnikovs are many.", StartProg: -1},
		{Index: 2, Href: "three.xhtml", Title: "Three", Text: strings.Repeat("Pyotr Petrovich spoke at length. ", 40), StartProg: -1},
	}}
	for _, c := range b.Chapters {
		b.bytes += len(c.Text)
	}
	b.fillSpans()
	return b
}

type cancelAfterChecks struct {
	context.Context
	checks int
	after  int
}

func (c *cancelAfterChecks) Err() error {
	c.checks++
	if c.checks >= c.after {
		return context.Canceled
	}
	return nil
}

func TestFindStopsOnCancellation(t *testing.T) {
	b := &Book{Chapters: []Chapter{{Text: strings.Repeat("a", 100000)}}}
	for _, after := range []int{3, 30} {
		ctx := &cancelAfterChecks{Context: context.Background(), after: after}
		hits, more := b.Find(ctx, []string{"b"}, false, 0, nil, 10)
		if ctx.checks < after || len(hits) != 0 || more {
			t.Fatalf("cancellation after %d checks: %d checks, %v, more %v", after, ctx.checks, hits, more)
		}
	}
}

func TestFindMentions(t *testing.T) {
	b := testBook()
	terms := []string{FoldQuery("Raskolnikov"), FoldQuery("Rodya"), FoldQuery("Rodion  Romanovich")}
	hits, more := b.Find(context.Background(), terms, true, 0, nil, 100)
	if more {
		t.Fatal("more reported")
	}
	var got []string
	for _, h := range hits {
		got = append(got, b.Chapters[h.Chapter].Text[h.Offset:h.Offset+h.Length])
	}
	// "Raskolnikovs" is another word; "Rodion" alone is not a term.
	want := []string{"Rodion Romanovich", "Raskolnikov", "Rodya", "Rodya", "Raskolnikov"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("hits = %q, want %q", got, want)
	}

	hits, more = b.Find(context.Background(), terms, true, 0, nil, 2)
	if len(hits) != 2 || !more {
		t.Fatalf("limit: %d hits, more=%v", len(hits), more)
	}
	hits, _ = b.Find(context.Background(), terms, true, 0, &Position{Chapter: 1}, 100)
	if len(hits) != 3 {
		t.Fatalf("stop at chapter 2: %d hits", len(hits))
	}
	hits, _ = b.Find(context.Background(), []string{FoldQuery("emile")}, true, 0, nil, 10)
	if len(hits) != 0 {
		t.Fatal("phantom hit")
	}
}

func TestFindLimitsDistinctHits(t *testing.T) {
	b := &Book{Chapters: []Chapter{{Text: "aaaa ... aa"}}}
	hits, more := b.Find(context.Background(), []string{"aa"}, false, 0, nil, 2)
	if len(hits) != 2 || hits[0].Offset != 0 || hits[1].Offset != 2 || !more {
		t.Fatalf("overlapping matches: %+v, more=%v", hits, more)
	}
	hits, more = b.Find(context.Background(), []string{"aa", "aaaa"}, false, 0, nil, 2)
	if len(hits) != 2 || hits[0].Term != 1 || hits[1].Offset != 9 || more {
		t.Fatalf("overlapping terms: %+v, more=%v", hits, more)
	}
}

func TestFindFoldsDiacritics(t *testing.T) {
	b := &Book{Chapters: []Chapter{{Text: "Hier, Émile a vu ÉMILE."}}, bytes: 1}
	b.fillSpans()
	hits, _ := b.Find(context.Background(), []string{FoldQuery("emile")}, true, 0, nil, 10)
	if len(hits) != 2 || b.Chapters[0].Text[hits[0].Offset:hits[0].Offset+hits[0].Length] != "Émile" {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestFindStopsAtMatchEnd(t *testing.T) {
	b := &Book{Chapters: []Chapter{{Text: "Émile spoke"}}}
	terms := []string{FoldQuery("Émile"), FoldQuery("Ém")}
	hits, _ := b.Find(context.Background(), terms, false, 0, &Position{Offset: len("Ém")}, 10)
	if len(hits) != 1 || hits[0].Length != len("Ém") {
		t.Fatalf("matches crossing the stop: %+v", hits)
	}
	hits, _ = b.Find(context.Background(), terms[:1], true, 0, &Position{Offset: len("Émile")}, 10)
	if len(hits) != 1 || hits[0].Length != len("Émile") {
		t.Fatalf("match ending at the stop: %+v", hits)
	}
}

func TestReadCoversChapterWithoutGaps(t *testing.T) {
	b := testBook()
	c := b.Chapters[2]
	var sb strings.Builder
	for at := 0; at < len(c.Text); {
		p := b.Read(2, at, 100)
		if p.Start != at || p.End <= at || p.End-p.Start > 100 {
			t.Fatalf("read at %d: %d..%d", at, p.Start, p.End)
		}
		sb.WriteString(p.Text)
		at = p.End
	}
	if sb.String() != c.Text {
		t.Fatal("pages do not reassemble the chapter")
	}
}

func TestNearestIndex(t *testing.T) {
	for _, tc := range []struct {
		text, quote string
		guess, want int
	}{
		{"aaaaaa", "aaaa", 1, 1},
		{"abc abc", "abc", 2, 0},
		{"abc abc", "abc", 3, 4},
		{"abc", "x", 1, -1},
	} {
		if got := nearestIndex(tc.text, tc.quote, tc.guess); got != tc.want {
			t.Errorf("nearestIndex(%q, %q, %d) = %d, want %d", tc.text, tc.quote, tc.guess, got, tc.want)
		}
	}
}

func TestLocateRepeatedQuoteNearEnd(t *testing.T) {
	text := strings.Repeat("dit Émile. ", 100)
	b := &Book{Chapters: []Chapter{{Href: "one.xhtml", Text: text}}}
	end := 1.0
	for _, before := range []string{"", "dit"} {
		_, off, exact, ok := b.Locate("one.xhtml", &end, before, "Émile")
		if !ok || !exact || off != strings.LastIndex(text, "Émile") {
			t.Fatalf("before=%q: offset %d exact=%v ok=%v", before, off, exact, ok)
		}
	}
}

func TestLocateMidWordQuoteContext(t *testing.T) {
	text := "hello there. Later lo appears again."
	b := &Book{Chapters: []Chapter{{Href: "chapter.xhtml", Text: text}}}
	end := 1.0
	_, off, exact, ok := b.Locate("chapter.xhtml", &end, "hel", "lo")
	if !ok || !exact || off != strings.Index(text, "lo") {
		t.Fatalf("mid-word anchor: offset %d, exact %v, ok %v", off, exact, ok)
	}
	text = "préfix, puis pré fix"
	b.Chapters[0].Text = text
	start := 0.0
	_, off, exact, ok = b.Locate("chapter.xhtml", &start, "pré", "fix")
	if !ok || !exact || off != len("pré") {
		t.Fatalf("unicode mid-word anchor: offset %d, exact %v, ok %v", off, exact, ok)
	}
	_, off, exact, ok = b.Locate("chapter.xhtml", &start, "pré", " fix")
	if !ok || !exact || off != strings.LastIndex(text, "fix") {
		t.Fatalf("leading highlight whitespace: offset %d, exact %v, ok %v", off, exact, ok)
	}
	text = "hello there. hel lo appears later."
	b.Chapters[0].Text = text
	_, off, exact, ok = b.Locate("chapter.xhtml", &end, "hel ", "lo")
	if !ok || !exact || off != strings.LastIndex(text, "lo") {
		t.Fatalf("spaced anchor: offset %d, exact %v, ok %v", off, exact, ok)
	}
}

func TestLocateQuoteAcrossParagraphs(t *testing.T) {
	text := "Bob left.\nLater that day\nBob returned."
	b := &Book{Chapters: []Chapter{{Href: "chapter.xhtml", Text: text}}}
	start := 0.0
	_, off, exact, ok := b.Locate("chapter.xhtml", &start, "Later that day", "Bob")
	if !ok || !exact || off != strings.LastIndex(text, "Bob") {
		t.Fatalf("paragraph anchor: offset %d, exact %v, ok %v", off, exact, ok)
	}
	_, off, exact, ok = b.Locate("chapter.xhtml", &start, "", "day Bob")
	if !ok || !exact || off != strings.Index(text, "day") {
		t.Fatalf("cross-paragraph quote: offset %d, exact %v, ok %v", off, exact, ok)
	}
}

func TestLocate(t *testing.T) {
	b := testBook()
	half := 0.5
	ch, off, exact, ok := b.Locate("/Text/two b.xhtml", &half, "met ", "Rodya")
	if !ok || !exact || ch != 1 || !strings.HasPrefix(b.Chapters[1].Text[off:], "Rodya.") {
		t.Fatalf("anchored: ch=%d off=%d exact=%v ok=%v", ch, off, exact, ok)
	}
	ch, off, exact, ok = b.Locate("three.xhtml#x", &half, "", "")
	if !ok || exact || ch != 2 || off == 0 {
		t.Fatalf("fraction: ch=%d off=%d exact=%v ok=%v", ch, off, exact, ok)
	}
	if _, _, _, ok := b.Locate("missing.xhtml", nil, "", ""); ok {
		t.Fatal("missing resource located")
	}
	if _, _, _, ok := b.Locate("one.xhtml", nil, "", "missing"); ok {
		t.Fatal("unmatched quote without resource progression was located")
	}
	if ch, _, _ := b.AtProgression(0.99); ch != 2 {
		t.Fatalf("AtProgression = %d", ch)
	}
}

func TestWindowAndFirstSentence(t *testing.T) {
	b := testBook()
	p := b.Window(1, strings.Index(b.Chapters[1].Text, "Dunya"), 20, 20)
	if !strings.Contains(p.Text, "Dunya") || strings.HasPrefix(p.Text, " ") {
		t.Fatalf("window = %q", p.Text)
	}
	c := Chapter{Title: "One", Text: "One\nIt began. Then more."}
	if got := c.FirstSentence(200); got != "It began. Then more." && got != "It began." {
		t.Fatalf("first sentence = %q", got)
	}
}

func TestWindowDoesNotExceedByteAllowance(t *testing.T) {
	b := &Book{Chapters: []Chapter{{Text: "é" + strings.Repeat("a", 2048), StartProg: 0, EndProg: 1}}}
	p := b.Window(0, len("é"), 1, 1023)
	if len(p.Text) > 1024 || !utf8.ValidString(p.Text) {
		t.Fatalf("window returned %d bytes: %q", len(p.Text), p.Text)
	}
}
