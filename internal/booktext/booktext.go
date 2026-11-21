// Package booktext turns an EPUB's reading order into plain text an
// assistant can read (ADR-0069): one string per reading-order resource,
// with the chapter titles the table of contents gives them and the
// whole-book progression range each one covers, so a passage can be
// addressed by chapter, by fraction, by a reader's locator or by what
// it says.
package booktext

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/readium/go-toolkit/pkg/manifest"
	"golang.org/x/net/html"

	"github.com/chmouel/liseur-sync/internal/epub"
)

// MaxBookBytes bounds the extracted text of one book. A publication
// past it is cut off there, which the result reports.
const MaxBookBytes = 24 << 20

const MaxTitleBytes = 256

// ErrNoText is returned for a publication with no readable text, such
// as a comic or a fixed-layout book made of images.
var ErrNoText = errors.New("the book has no readable text")

// Chapter is one reading-order resource as text.
type Chapter struct {
	Index int
	Href  string
	Title string
	Text  string
	// StartProg and EndProg are the whole-book progression range this
	// resource covers, on the same axis readers record positions on.
	StartProg float64
	EndProg   float64
}

// Book is a publication's text.
type Book struct {
	Chapters     []Chapter
	Truncated    bool
	bytes        int
	unknownRange bool
}

// Bytes is the size of the text held.
func (b *Book) Bytes() int { return b.bytes }

func (b *Book) ProgressionKnown() bool { return !b.unknownRange }

// Extract builds a book's text from its index. read returns the bytes
// of one archive entry.
func Extract(ctx context.Context, idx *epub.Index, read func(name string) ([]byte, error)) (*Book, error) {
	titles := tocTitles(idx.Manifest.TableOfContents)
	spans := progressionSpans(idx.Positions)
	b := &Book{}
	for _, link := range idx.Manifest.ReadingOrder {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		href := link.Href.String()
		name, ok := archivePath(href)
		if !ok || !idx.HasResource(name) {
			continue
		}
		raw, err := read(name)
		if err != nil {
			return nil, err
		}
		text, heading := htmlText(raw)
		if b.bytes+len(text) > MaxBookBytes {
			b.Truncated = true
			end, ok := spans[href]
			if !ok || len(b.Chapters) == 0 || b.Chapters[len(b.Chapters)-1].EndProg != end[0] {
				b.unknownRange = true
			}
			break
		}
		title := titles[name]
		if title == "" {
			title = heading
		}
		ch := Chapter{Index: len(b.Chapters), Href: href, Title: title, Text: text, StartProg: -1, EndProg: -1}
		if s, ok := spans[href]; ok {
			ch.StartProg, ch.EndProg = s[0], s[1]
		}
		b.Chapters = append(b.Chapters, ch)
		b.bytes += len(text)
	}
	if b.bytes == 0 {
		return nil, ErrNoText
	}
	b.fillSpans()
	return b, nil
}

// fillSpans falls back to each chapter's share of the text when the
// positions list did not place every chapter, so the axis stays
// monotonic.
func (b *Book) fillSpans() {
	for _, c := range b.Chapters {
		if c.StartProg < 0 {
			if b.Truncated {
				b.unknownRange = true
			}
			goto fill
		}
	}
	return
fill:
	at := 0
	for i := range b.Chapters {
		c := &b.Chapters[i]
		c.StartProg = float64(at) / float64(b.bytes)
		at += len(c.Text)
		c.EndProg = float64(at) / float64(b.bytes)
	}
}

func archivePath(href string) (string, bool) {
	u, err := url.Parse(href)
	if err != nil || u.IsAbs() || u.Host != "" {
		return "", false
	}
	p := strings.TrimPrefix(u.Path, "/")
	return p, p != ""
}

// tocTitles maps each resource to the first table-of-contents title
// that points into it.
func tocTitles(list manifest.LinkList) map[string]string {
	out := map[string]string{}
	var walk func(manifest.LinkList)
	walk = func(l manifest.LinkList) {
		for _, link := range l {
			if name, ok := archivePath(link.Href.String()); ok {
				if _, seen := out[name]; !seen {
					if t := boundedTitle(link.Title); t != "" {
						out[name] = t
					}
				}
			}
			walk(link.Children)
		}
	}
	walk(list)
	return out
}

func progressionSpans(positions []manifest.Locator) map[string][2]float64 {
	out := map[string][2]float64{}
	last := ""
	for _, p := range positions {
		href := p.Href.String()
		if _, seen := out[href]; seen {
			continue
		}
		start := 0.0
		if p.Locations.TotalProgression != nil {
			start = *p.Locations.TotalProgression
		}
		if last != "" {
			prev := out[last]
			prev[1] = start
			out[last] = prev
		}
		out[href] = [2]float64{start, 1}
		last = href
	}
	return out
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func boundedTitle(s string) string {
	var out strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = out.Len() > 0
			continue
		}
		needed := utf8.RuneLen(r)
		if space {
			needed++
		}
		if out.Len()+needed > MaxTitleBytes {
			break
		}
		if space {
			out.WriteByte(' ')
		}
		out.WriteRune(r)
		space = false
	}
	return out.String()
}

var blockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true, "br": true,
	"dd": true, "div": true, "dl": true, "dt": true, "figcaption": true, "figure": true,
	"footer": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"header": true, "hr": true, "li": true, "main": true, "nav": true, "ol": true, "p": true,
	"pre": true, "section": true, "table": true, "td": true, "th": true, "tr": true, "ul": true,
	"body": true,
}

// skipTags hold nothing a reader sees as text.
var skipTags = map[string]bool{
	"head": true, "script": true, "style": true, "noscript": true, "template": true,
	"svg": true, "math": true, "object": true, "iframe": true, "rt": true, "rp": true,
}

var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true,
	"input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true,
}

func localName(raw []byte) string {
	tag := strings.ToLower(string(raw))
	if i := strings.IndexByte(tag, ':'); i >= 0 {
		tag = tag[i+1:]
	}
	return tag
}

// htmlText renders a chapter document as plain text: one line per
// block, whitespace collapsed, scripts, styles and hidden elements
// dropped. It also returns the first heading, for a chapter the table
// of contents does not name.
func htmlText(doc []byte) (string, string) {
	z := html.NewTokenizer(bytes.NewReader(doc))
	var w textWriter
	var heading strings.Builder
	skip, inHeading, headingDone := 0, 0, false
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return strings.TrimSpace(w.b.String()), boundedTitle(heading.String())
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := localName(name)
			opens := tt == html.StartTagToken && !voidTags[tag]
			if skip > 0 {
				if opens {
					skip++
				}
				continue
			}
			if skipTags[tag] || (hasAttr && hidden(z)) {
				if opens {
					skip = 1
				}
				continue
			}
			if blockTags[tag] {
				w.newline()
			}
			if !opens {
				continue
			}
			switch {
			case inHeading > 0:
				inHeading++
			case !headingDone && (tag == "h1" || tag == "h2" || tag == "h3"):
				inHeading = 1
			}
		case html.EndTagToken:
			if skip > 0 {
				skip--
				continue
			}
			name, _ := z.TagName()
			if blockTags[localName(name)] {
				w.newline()
			}
			if inHeading > 0 {
				inHeading--
				if inHeading == 0 && heading.Len() > 0 {
					headingDone = true
				}
			}
		case html.TextToken:
			if skip > 0 {
				continue
			}
			t := string(z.Text())
			w.text(t)
			if inHeading > 0 && heading.Len() < MaxTitleBytes {
				end := snapBack(t, min(len(t), MaxTitleBytes-heading.Len()))
				heading.WriteString(t[:end])
				if heading.Len() < MaxTitleBytes {
					heading.WriteByte(' ')
				}
			}
		}
	}
}

func hidden(z *html.Tokenizer) bool {
	for {
		key, val, more := z.TagAttr()
		k := strings.ToLower(string(key))
		v := strings.ToLower(strings.ReplaceAll(string(val), " ", ""))
		switch {
		case k == "hidden",
			k == "aria-hidden" && v == "true",
			k == "style" && (strings.Contains(v, "display:none") || strings.Contains(v, "visibility:hidden")):
			return true
		}
		if !more {
			return false
		}
	}
}

type textWriter struct {
	b         strings.Builder
	lineStart bool
	space     bool
}

func (w *textWriter) newline() {
	if w.b.Len() > 0 && !w.lineStart {
		w.b.WriteByte('\n')
	}
	w.lineStart, w.space = true, false
}

func (w *textWriter) text(s string) {
	for _, r := range s {
		if r == utf8.RuneError || r == 0 || (unicode.IsControl(r) && !unicode.IsSpace(r)) {
			continue
		}
		if unicode.IsSpace(r) {
			w.space = true
			continue
		}
		if w.space && !w.lineStart && w.b.Len() > 0 {
			w.b.WriteByte(' ')
		}
		w.b.WriteRune(r)
		w.space, w.lineStart = false, false
	}
}
