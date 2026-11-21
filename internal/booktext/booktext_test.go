package booktext

import (
	"strings"
	"testing"
)

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

func TestFindMentions(t *testing.T) {
	b := testBook()
	terms := []string{FoldQuery("Raskolnikov"), FoldQuery("Rodya"), FoldQuery("Rodion  Romanovich")}
	hits, more := b.Find(terms, true, 0, nil, 100)
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

	hits, more = b.Find(terms, true, 0, nil, 2)
	if len(hits) != 2 || !more {
		t.Fatalf("limit: %d hits, more=%v", len(hits), more)
	}
	hits, _ = b.Find(terms, true, 0, &Position{Chapter: 1}, 100)
	if len(hits) != 3 {
		t.Fatalf("stop at chapter 2: %d hits", len(hits))
	}
	hits, _ = b.Find([]string{FoldQuery("emile")}, true, 0, nil, 10)
	if len(hits) != 0 {
		t.Fatal("phantom hit")
	}
}

func TestFindFoldsDiacritics(t *testing.T) {
	b := &Book{Chapters: []Chapter{{Text: "Hier, Émile a vu ÉMILE."}}, bytes: 1}
	b.fillSpans()
	hits, _ := b.Find([]string{FoldQuery("emile")}, true, 0, nil, 10)
	if len(hits) != 2 || b.Chapters[0].Text[hits[0].Offset:hits[0].Offset+hits[0].Length] != "Émile" {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestFindStopsAtMatchEnd(t *testing.T) {
	b := &Book{Chapters: []Chapter{{Text: "Émile spoke"}}}
	terms := []string{FoldQuery("Émile"), FoldQuery("Ém")}
	hits, _ := b.Find(terms, false, 0, &Position{Offset: len("Ém")}, 10)
	if len(hits) != 1 || hits[0].Length != len("Ém") {
		t.Fatalf("matches crossing the stop: %+v", hits)
	}
	hits, _ = b.Find(terms[:1], true, 0, &Position{Offset: len("Émile")}, 10)
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
	if ch, _ := b.AtProgression(0.99); ch != 2 {
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
