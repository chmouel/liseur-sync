package audiobookshelf

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/readium/go-toolkit/pkg/manifest"
	"golang.org/x/net/html"

	"github.com/chmouel/liseur-sync/internal/epub"
)

// BookAnchors places an EPUB's table of contents on its totalProgression
// axis, using the same positions a reader computes. An entry naming a
// whole document starts where that document starts, exactly. An entry
// naming an element inside one is placed by its share of the document's
// text, read through resource.
func BookAnchors(idx *epub.Index, resource func(href string) ([]byte, error)) ([]BookChapter, []Resource, error) {
	resources := readingOrderSpans(idx.Positions)
	if len(resources) == 0 {
		return nil, nil, errors.New("book has no positions")
	}
	byHref := make(map[string]Resource, len(resources))
	for _, r := range resources {
		byHref[r.Href] = r
	}
	var flat []manifest.Link
	var walk func(manifest.LinkList)
	walk = func(list manifest.LinkList) {
		for _, l := range list {
			flat = append(flat, l)
			walk(l.Children)
		}
	}
	walk(idx.Manifest.TableOfContents)

	texts := map[string][]byte{}
	var out []BookChapter
	for _, l := range flat {
		href, frag, _ := strings.Cut(l.Href.String(), "#")
		r, ok := byHref[href]
		if !ok {
			continue // a TOC entry outside the reading order is not a place a reader can be
		}
		c := BookChapter{Title: strings.TrimSpace(l.Title), Href: href, Start: r.Start}
		if frag != "" {
			raw, seen := texts[href]
			if !seen {
				var err error
				if raw, err = resource(href); err != nil {
					return nil, nil, fmt.Errorf("reading %s: %w", href, err)
				}
				texts[href] = raw
			}
			f, found := textFractionBefore(raw, frag)
			if !found {
				return nil, nil, fmt.Errorf("%s has no element %q", href, frag)
			}
			if f > 0 {
				c.Start = r.Start + f*(r.End-r.Start)
				c.Fragment, c.Approx = frag, true
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	for i := range out {
		out[i].End = 1
		for j := i + 1; j < len(out); j++ {
			if out[j].Start > out[i].Start {
				out[i].End = out[j].Start
				break
			}
		}
	}
	return out, resources, nil
}

// readingOrderSpans reads each document's span off the positions list:
// a document starts at its first position and ends where the next
// starts.
func readingOrderSpans(positions []manifest.Locator) []Resource {
	var out []Resource
	for _, p := range positions {
		href := p.Href.String()
		if len(out) > 0 && out[len(out)-1].Href == href {
			continue
		}
		start := 0.0
		if p.Locations.TotalProgression != nil {
			start = *p.Locations.TotalProgression
		}
		if len(out) > 0 {
			out[len(out)-1].End = start
		}
		out = append(out, Resource{Href: href, Start: start, End: 1})
	}
	return out
}

// textFractionBefore is the share of a document's visible text that
// comes before the element whose id (or anchor name) is id.
func textFractionBefore(doc []byte, id string) (float64, bool) {
	z := html.NewTokenizer(bytes.NewReader(doc))
	var total, before int
	found := false
	skip := 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			// EOF, or a document that stops parsing: either way the text
			// read so far is all there is.
			if !found || total == 0 {
				return 0, found
			}
			return float64(before) / float64(total), true
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			if tt == html.StartTagToken && hiddenText(string(name)) {
				skip++
			}
			for hasAttr && !found {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				if (string(k) == "id" || string(k) == "name") && string(v) == id {
					found, before = true, total
				}
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); hiddenText(string(name)) && skip > 0 {
				skip--
			}
		case html.TextToken:
			if skip == 0 {
				total += visibleRunes(z.Text())
			}
		}
	}
}

func hiddenText(tag string) bool { return tag == "script" || tag == "style" || tag == "head" }

func visibleRunes(b []byte) int {
	n := 0
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if !unicode.IsSpace(r) {
			n++
		}
		b = b[size:]
	}
	return n
}
