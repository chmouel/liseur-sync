package xpointer

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/epub"
)

// Fixtures and expectations from CrossPoint Reader's
// test/koreader_xpath_resolver/KOReaderXPathResolverTest.cpp: the
// device's own idea of where an offset is.
const (
	nestedFixture = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body><div><section><p>Alpha bravo</p><p>Second <em>nested</em> tail</p></section></div></body></html>`
	kindleFixture = "<html xmlns=\"http://www.w3.org/1999/xhtml\"><body class=\"calibre\">\n" +
		"<p id=\"filepos1\" class=\"calibre1\"></p>\n" +
		"<div class=\"calibre8\"><span class=\"calibre9\">3</span></div><div class=\"calibre10\"> </div>\n" +
		"<div class=\"calibre12\"><span class=\"calibre6\"><span class=\"bold\">She drove</span> back to " +
		"D.C.</span></div>\n" +
		"<div class=\"calibre19\"><span class=\"calibre6\">\u201cWho?\u201d</span></div>\n" +
		"</body></html>"
)

func TestCrossPointFixtures(t *testing.T) {
	const p = "/body/DocFragment[1]/body/"
	cases := []struct {
		doc    string
		offset int
		want   string // "" when CrossPoint resolves nothing
	}{
		{nestedFixture, 6, p + "div[1]/section[1]/p[1]/text()[1].6"},
		{nestedFixture, 26, p + "div[1]/section[1]/p[2]/text()[2].2"},
		{nestedFixture, 0, p + "div[1]/section[1]/p[1]/text()[1].0"},
		{`<html><body><p><RP><span>hidden</span></RP>Visible text</p></body></html>`, 0, p + "p[1]/text()[1].0"},
		{"<?xml version=\"1.0\" encoding=\"UTF-8\"?><html><body><p>A\u00e9\u4e2dB</p></body></html>", 3, p + "p[1]/text()[1].3"},
		{`<html><body><p>before<!--comment-->after</p></body></html>`, 6, p + "p[1]/text()[2].0"},
		{`<html><body><p>before<!--comment-->after</p></body></html>`, 7, p + "p[1]/text()[2].1"},
		{`<html><body><p>before<!--comment-->after</p></body></html>`, 11, ""},
		{`<html><body><p>before<?marker?>after</p></body></html>`, 7, p + "p[1]/text()[2].1"},
		{`<html><body><p>before<![CDATA[middle]]>after</p></body></html>`, 7, p + "p[1]/text()[2].1"},
		{`<html><body><p>before<![CDATA[middle]]>after</p></body></html>`, 13, p + "p[1]/text()[3].1"},
		{`<html><body><p><!--comment-->text</p></body></html>`, 0, p + "p[1]/text()[1].0"},
		{`<html><body><p><?marker?>text</p></body></html>`, 0, p + "p[1]/text()[1].0"},
		{`<html><body><p><![CDATA[text]]></p></body></html>`, 0, p + "p[1]/text()[1].0"},
		{`<html><body><p>before<rp><![CDATA[hidden]]></rp>after</p></body></html>`, 7, p + "p[1]/text()[2].1"},
		{kindleFixture, 2, p + "div[1]/span[1]/text()[1].0"},
		{kindleFixture, 5, p + "div[3]/span[1]/span[1]/text()[1].0"},
		{kindleFixture, 20, p + "div[3]/span[1]/text()[1].6"},
		{kindleFixture, 28, p + "div[4]/span[1]/text()[1].0"},
		{kindleFixture, 1, p + "div[1]/span[1]/text()[1].0"},
		{kindleFixture, 3, p + "div[3]/span[1]/span[1]/text()[1].0"},
		{kindleFixture, 4, p + "div[3]/span[1]/span[1]/text()[1].0"},
		{kindleFixture, 34, ""},
		{`<html><body><div>not a paragraph or list item</div></body></html>`, 0, p + "div[1]/text()[1].0"},
		{`<html><body>Hello world</body></html>`, 6, p + "text()[1].6"},
		{"<html><body><p>a</p>\nHello</body></html>", 2, p + "text()[1].1"},
		{"<html><body><p>broken", 100, ""},
		{"", 0, ""},
	}
	for _, c := range cases {
		got := ""
		if d, err := ParseDocument(t.Context(), []byte(c.doc)); err == nil {
			if pos, err := d.at(c.offset); err == nil {
				path, err := d.XPath(1, pos)
				if err != nil {
					t.Fatalf("XPath(%q @%d): %v", c.doc, c.offset, err)
				}
				got = path.String()
				// The path the device writes must lead back to the same place.
				back, err := d.Resolve(mustParse(t, got))
				if err != nil || back != pos {
					t.Errorf("Resolve(%s) = %v, %v; want %v", got, back, err, pos)
				}
			}
		}
		if got != c.want {
			t.Errorf("%q @%d = %q, want %q", c.doc, c.offset, got, c.want)
		}
	}
}

func mustParse(t *testing.T, s string) Path {
	t.Helper()
	p, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return p
}

func mustDoc(t *testing.T, s string) *Document {
	t.Helper()
	d, err := ParseDocument(t.Context(), []byte(s))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	return d
}

func TestParse(t *testing.T) {
	for in, want := range map[string]string{
		// KOReader's engine leaves out [1] on an only child.
		"/body/DocFragment[3]/body/div/p[42]/text().17":       "/body/DocFragment[3]/body/div[1]/p[42]/text()[1].17",
		"/body/DocFragment[3]/body/div[1]/p[2]/text()[3].0":   "/body/DocFragment[3]/body/div[1]/p[2]/text()[3].0",
		"/body/DocFragment[3]/body/div[2]/p[5].0":             "/body/DocFragment[3]/body/div[2]/p[5]",
		"/body/DocFragment[3]/body/div[2]/img":                "/body/DocFragment[3]/body/div[2]/img[1]",
		"/body/DocFragment[1]/body/text()[1].6":               "/body/DocFragment[1]/body/text()[1].6",
		"/body/DocFragment[7]/body":                           "/body/DocFragment[7]/body",
		"/body/DocFragment[7]/body.0":                         "/body/DocFragment[7]/body",
		"/body/DocFragment[2]/body/section[1]/h1[1]/text().0": "/body/DocFragment[2]/body/section[1]/h1[1]/text()[1].0",
		"/body/DocFragment[2]/body/a.b[1]/text().0":           "",
		"/body/DocFragment[0]/body/p[1]/text().0":             "",
		"/body/DocFragment[1]/body/p[0]/text().0":             "",
		"/body/DocFragment[1]/body/text()[1]/p[1].0":          "",
		"/body/DocFragment[1]/body/p[1]/text().x":             "",
		"/body/DocFragment[1]/html/p[1]/text().0":             "",
		"/body/DocFragment[1]/body//p[1]/text().0":            "",
		"0.42":                   "",
		"epubcfi(/6/4!/4/2/1:0)": "",
	} {
		p, err := Parse(in)
		got := ""
		if err == nil {
			got = p.String()
		}
		if got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestResolveKOReaderWhitespace(t *testing.T) {
	// CrossPoint counts the leading formatting run as text()[1]; KOReader's
	// engine drops a whitespace-only first child and calls the tail
	// text()[1]. Both have to land on "Tail".
	d := mustDoc(t, "<html><body><div>\n  <p>x</p>\n  Tail text</div></body></html>")
	for _, s := range []string{
		"/body/DocFragment[1]/body/div[1]/text()[2].3",
		"/body/DocFragment[1]/body/div/text().3",
	} {
		pos, err := d.Resolve(mustParse(t, s))
		if err != nil {
			t.Fatalf("Resolve(%s): %v", s, err)
		}
		a, err := d.AnchorAt(pos)
		if err != nil || a.Highlight != "Tail" {
			t.Errorf("Resolve(%s) anchors %+v, %v; want Tail", s, a, err)
		}
	}
	for _, s := range []string{
		"/body/DocFragment[1]/body/div[1]/text()[2].99",
		"/body/DocFragment[1]/body/div[2]/text()[1].0",
		"/body/DocFragment[1]/body/div[1]/span[1]/text()[1].0",
	} {
		if _, err := d.Resolve(mustParse(t, s)); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%s) = %v, want ErrNotFound", s, err)
		}
	}
}

func TestResolveElementPath(t *testing.T) {
	d := mustDoc(t, `<html><body><div><img src="a.png"/></div><div><p>  </p><p>First words</p></div></body></html>`)
	pos, err := d.Resolve(mustParse(t, "/body/DocFragment[1]/body/div[2].0"))
	if err != nil {
		t.Fatal(err)
	}
	path, _ := d.XPath(1, pos)
	if got := path.String(); got != "/body/DocFragment[1]/body/div[2]/p[2]/text()[1].0" {
		t.Errorf("element path resolves to %s", got)
	}
	// An image-only element holds no text to stand on.
	if _, err := d.Resolve(mustParse(t, "/body/DocFragment[1]/body/div[1]")); !errors.Is(err, ErrNotFound) {
		t.Errorf("image-only div: %v", err)
	}
}

func TestAnchorAt(t *testing.T) {
	cases := []struct {
		name, doc, xpath string
		want             Anchor
	}{
		{
			name: "word at offset", doc: nestedFixture,
			xpath: "/body/DocFragment[1]/body/div[1]/section[1]/p[1]/text()[1].6",
			want:  Anchor{"body > div:nth-of-type(1) > section:nth-of-type(1) > p:nth-of-type(1)", "Alpha ", "bravo", ""},
		},
		{
			// A page can start mid-word after a hyphenated break.
			name: "inside a word", doc: nestedFixture,
			xpath: "/body/DocFragment[1]/body/div[1]/section[1]/p[1]/text()[1].8",
			want:  Anchor{"body > div:nth-of-type(1) > section:nth-of-type(1) > p:nth-of-type(1)", "Alpha ", "bravo", ""},
		},
		{
			name: "inline formatting stays in its block", doc: nestedFixture,
			xpath: "/body/DocFragment[1]/body/div[1]/section[1]/p[2]/em[1]/text()[1].0",
			want:  Anchor{"body > div:nth-of-type(1) > section:nth-of-type(1) > p:nth-of-type(2)", "Second ", "nested", " tail"},
		},
		{
			name: "between words", doc: nestedFixture,
			xpath: "/body/DocFragment[1]/body/div[1]/section[1]/p[2]/text()[1].6",
			want:  Anchor{"body > div:nth-of-type(1) > section:nth-of-type(1) > p:nth-of-type(2)", "Second ", "nested", " tail"},
		},
		{
			name:  "unique id",
			doc:   `<html><body><p id="c1">One</p><p id="1st.para">Two words</p></body></html>`,
			xpath: "/body/DocFragment[1]/body/p[2]/text()[1].4",
			want:  Anchor{`#\31 st\.para`, "Two ", "words", ""},
		},
		{
			name:  "duplicate id falls back to a path",
			doc:   `<html><body><p id="x">One</p><p id="x">Two</p></body></html>`,
			xpath: "/body/DocFragment[1]/body/p[2]/text()[1].0",
			want:  Anchor{"body > p:nth-of-type(2)", "", "Two", ""},
		},
		{
			name:  "image before the text",
			doc:   `<html><body><div class="fig"><img src="a.png"><p>Caption here</p></div></body></html>`,
			xpath: "/body/DocFragment[1]/body/div[1]/p[1]/text()[1].0",
			want:  Anchor{"body > div:nth-of-type(1) > p:nth-of-type(1)", "", "Caption", " here"},
		},
		{
			name:  "context is code points and stays in the block",
			doc:   "<html><body><p>Chapter</p><p>\u00c9t\u00e9 \u00e0 Paris, l\u2019\u00e9t\u00e9 dernier, il pleuvait sans cesse sur les toits.</p></body></html>",
			xpath: "/body/DocFragment[1]/body/p[2]/text()[1].46",
			want:  Anchor{"body > p:nth-of-type(2)", "l\u2019\u00e9t\u00e9 dernier, il pleuvait sans ", "cesse", " sur les toits."},
		},
	}
	for _, c := range cases {
		d := mustDoc(t, c.doc)
		pos, err := d.Resolve(mustParse(t, c.xpath))
		if err != nil {
			t.Fatalf("%s: Resolve: %v", c.name, err)
		}
		got, err := d.AnchorAt(pos)
		if err != nil || got != c.want {
			t.Errorf("%s: AnchorAt = %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
}

func TestAnchorRefusals(t *testing.T) {
	d := mustDoc(t, "<html><body><p>"+strings.Repeat("echo ", 30)+"</p></body></html>")
	pos, _ := d.at(10)
	if _, err := d.AnchorAt(pos); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("repeated text: %v, want ErrAmbiguous", err)
	}
	if _, err := d.Find(Anchor{"body > p:nth-of-type(1)", "echo ", "echo", " "}); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("Find repeated: %v, want ErrAmbiguous", err)
	}

	d = mustDoc(t, `<html><body><p><ruby>漢<rp>(</rp><rt>kan</rt><rp>)</rp></ruby> word</p><p>x</p></body></html>`)
	for _, a := range []Anchor{
		{"body > p:nth-of-type(1)", "漢", "(", "kan)"}, // the quote is in a hidden <rp>
		{"body > p:nth-of-type(1)", "", "absent", ""},
		{"body > p:nth-of-type(3)", "", "x", ""},
		{"body > p:nth-of-type(1) > span:nth-of-type(1)", "", "word", ""},
		{"html > body", "", "word", ""},
		{"#missing", "", "word", ""},
		{"body > p:nth-of-type(1)", "", "", ""},
	} {
		if _, err := d.Find(a); !errors.Is(err, ErrNotFound) {
			t.Errorf("Find(%+v) = %v, want ErrNotFound", a, err)
		}
	}
}

// Every word in a resource has to survive XPath → anchor → XPath: the
// anchor the server derives for a CrossPoint position is the one a
// Liseur reader will hand back.
func TestRoundTrip(t *testing.T) {
	for _, doc := range []string{
		nestedFixture,
		kindleFixture,
		`<html><body><section id="s"><h1>Title <b>bold</b></h1><p>One, two; three.</p><blockquote><p>Quoted <i>words</i> here</p></blockquote><ul><li>Item one</li><li>Item <a href="#">two</a></li></ul></section><p>Tail</p></body></html>`,
		"<html><body><p>caf\u00e9 na\u00efve \U0001F600 emoji</p><p>A <![CDATA[cdata]]> bit<!-- c --> more</p></body></html>",
	} {
		d := mustDoc(t, doc)
		seen := 0
		for _, r := range d.runs {
			if r.hidden || r.blank {
				continue
			}
			for _, w := range wordPattern.FindAllStringIndex(r.text, -1) {
				pos := Position{run: r, offset: len([]rune(r.text[:w[0]]))}
				want, err := d.XPath(1, pos)
				if err != nil {
					t.Fatal(err)
				}
				a, err := d.AnchorAt(pos)
				if err != nil {
					t.Fatalf("AnchorAt(%s): %v", want, err)
				}
				back, err := d.Find(a)
				if err != nil {
					t.Fatalf("Find(%+v): %v", a, err)
				}
				got, _ := d.XPath(1, back)
				if got.String() != want.String() {
					t.Errorf("%s -> %+v -> %s", want, a, got)
				}
				seen++
			}
		}
		if seen == 0 {
			t.Errorf("no words in %q", doc)
		}
	}
}

func TestProgressionIsTextOffset(t *testing.T) {
	// A big image and a short caption: the reader's own progression is
	// mostly image, the text offset is not. Progression here is only a
	// hint beside the anchor and is never turned back into an XPath.
	d := mustDoc(t, `<html><body><p>Ab</p><div><img src="huge.png"/></div><p>Cd</p></body></html>`)
	pos, err := d.Resolve(mustParse(t, "/body/DocFragment[1]/body/p[2]/text()[1].0"))
	if err != nil {
		t.Fatal(err)
	}
	if got := pos.Progression(d); got != 0.5 {
		t.Errorf("Progression = %v, want 0.5", got)
	}
}

func TestCSSEscape(t *testing.T) {
	for in, want := range map[string]string{
		"c1":      "c1",
		"1st":     `\31 st`,
		"-2":      `-\32 `,
		"-":       `\-`,
		"a.b:c":   `a\.b\:c`,
		"été_x-y": "été_x-y",
		"a b":     `a\ b`,
	} {
		got := cssEscape(in)
		if got != want {
			t.Errorf("cssEscape(%q) = %q, want %q", in, got, want)
		}
		if back, ok := cssUnescape(got); !ok || back != in {
			t.Errorf("cssUnescape(%q) = %q", got, back)
		}
	}
	// The readers' fallback escaper writes a backslash before every
	// other character.
	if back, _ := cssUnescape(`1st\.para`); back != "1st.para" {
		t.Errorf("fallback form unescaped to %q", back)
	}
}

func TestParseSpine(t *testing.T) {
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
<manifest>
  <item id="cover" href="Text/cover.xhtml" media-type="application/xhtml+xml"/>
  <item id="c1" href="Text/chapter%201.xhtml" media-type="application/xhtml+xml"/>
  <item id="notes" href="../notes.xhtml" media-type="application/xhtml+xml"/>
  <item id="css" href="style.css" media-type="text/css"/>
</manifest>
<spine><itemref idref="cover" linear="no"/><itemref idref="c1"/><itemref idref="notes"/></spine>
</package>`
	got, err := parseSpine([]byte(opf), "OEBPS/content.opf")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"OEBPS/Text/cover.xhtml", "OEBPS/Text/chapter 1.xhtml", "notes.xhtml"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("spine = %q, want %q", got, want)
	}
}

func bookOf(files map[string]string, spine ...string) *Book {
	return &Book{spine: spine, read: func(_ context.Context, name string) ([]byte, error) {
		if s, ok := files[name]; ok {
			return []byte(s), nil
		}
		return nil, errors.New("no such resource")
	}}
}

func TestBook(t *testing.T) {
	ctx := context.Background()
	cover := `<html><body><p>Cover</p></body></html>`
	ch1 := `<html><body><h1>One</h1><p>It was a bright cold day in April, and the clocks were striking thirteen.</p></body></html>`
	a := bookOf(map[string]string{"OEBPS/cover.xhtml": cover, "OEBPS/Text/chapter 1.xhtml": ch1},
		"OEBPS/cover.xhtml", "OEBPS/Text/chapter 1.xhtml")

	xp := "/body/DocFragment[2]/body/p[1]/text()[1].25"
	loc, err := a.LocatorFor(ctx, xp)
	if err != nil {
		t.Fatal(err)
	}
	want := Locator{
		Href:   "OEBPS/Text/chapter%201.xhtml",
		Anchor: Anchor{"body > p:nth-of-type(1)", "It was a bright cold day ", "in", " April, and the clocks were stri"},
	}
	if loc.Href != want.Href || loc.Anchor != want.Anchor || loc.Progression <= 0.3 || loc.Progression >= 0.4 {
		t.Errorf("LocatorFor = %+v, want %+v", loc, want)
	}
	for _, href := range []string{loc.Href, "/OEBPS/Text/chapter%201.xhtml", "OEBPS/Text/chapter%201.xhtml#p1"} {
		back, err := a.XPathFor(ctx, href, loc.Anchor)
		if err != nil || back != xp {
			t.Errorf("XPathFor(%s) = %q, %v; want %q", href, back, err, xp)
		}
	}

	// Same file names, different text: another edition. Its anchor must
	// not land anywhere in this one.
	b := bookOf(map[string]string{"OEBPS/cover.xhtml": cover,
		"OEBPS/Text/chapter 1.xhtml": `<html><body><h1>One</h1><p>It was a bright cold morning in April, and the clocks struck one.</p></body></html>`},
		"OEBPS/cover.xhtml", "OEBPS/Text/chapter 1.xhtml")
	if _, err := b.XPathFor(ctx, loc.Href, loc.Anchor); !errors.Is(err, ErrNotFound) {
		t.Errorf("other edition: %v, want ErrNotFound", err)
	}

	for _, bad := range []string{"/body/DocFragment[3]/body/p[1]/text().0", "0.5"} {
		if _, err := a.LocatorFor(ctx, bad); err == nil {
			t.Errorf("LocatorFor(%s) succeeded", bad)
		}
	}
	if _, err := a.XPathFor(ctx, "OEBPS/Text/missing.xhtml", loc.Anchor); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown href: %v", err)
	}
}

// Open reads the spine from a real archive: a linear="no" cover still
// takes DocFragment[1], and a file name with a space is found.
func TestOpenPublication(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct{ name, body string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"OPS/book.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">x</dc:identifier><dc:title>Book</dc:title><dc:language>en</dc:language></metadata>
<manifest>
<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/>
<item id="c1" href="Text/chapter%201.xhtml" media-type="application/xhtml+xml"/>
</manifest>
<spine><itemref idref="cover" linear="no"/><itemref idref="c1"/></spine></package>`},
		{"OPS/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><nav epub:type="toc" xmlns:epub="http://www.idpf.org/2007/ops"><ol><li><a href="Text/chapter%201.xhtml">One</a></li></ol></nav></body></html>`},
		{"OPS/cover.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Cover</p></body></html>`},
		{"OPS/Text/chapter 1.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Call me Ishmael.</p></body></html>`},
	} {
		method := zip.Deflate
		if f.name == "mimetype" {
			method = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pub, err := epub.OpenPublication(ctx, bytes.NewReader(buf.Bytes()), int64(buf.Len()), epub.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(ctx, pub)
	if err != nil {
		t.Fatal(err)
	}
	loc, err := b.LocatorFor(ctx, "/body/DocFragment[2]/body/p/text().5")
	if err != nil {
		t.Fatal(err)
	}
	if loc.Href != "OPS/Text/chapter%201.xhtml" || loc.Anchor.Highlight != "me" {
		t.Errorf("LocatorFor = %+v", loc)
	}
}

func BenchmarkParseDocumentSiblings(b *testing.B) {
	for _, count := range []int{1000, 20000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			data := []byte("<html><body>" + strings.Repeat("<p>A paragraph with enough text to represent ordinary EPUB content.</p>\n", count) + "</body></html>")
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for b.Loop() {
				if _, err := ParseDocument(b.Context(), data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestDocumentManySiblingIndexes(t *testing.T) {
	const count = 20000
	d := mustDoc(t, "<html><body>\n"+strings.Repeat("<p>word</p>\n", count)+"</body></html>")
	pos, err := d.Resolve(mustParse(t, fmt.Sprintf("/body/DocFragment[1]/body/p[%d]/text()[1].2", count)))
	if err != nil {
		t.Fatal(err)
	}
	path, err := d.XPath(1, pos)
	if err != nil || path.Steps[0].Index != count || path.Text != 1 || path.Offset != 2 {
		t.Fatalf("last paragraph: %+v %v", path, err)
	}
	last := d.body.kids[len(d.body.kids)-1].run
	if last == nil || last.index != count+1 {
		t.Fatalf("body text count: %+v", last)
	}
}

func TestBookTranslationCanceledAfterResourceRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	book := &Book{spine: []string{"chapter.xhtml"}, read: func(context.Context, string) ([]byte, error) {
		cancel()
		return []byte("<html><body><p>word</p></body></html>"), nil
	}}
	if _, err := book.LocatorFor(ctx, "/body/DocFragment[1]/body/p[1]/text()[1].0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled translation: %v", err)
	}
}
