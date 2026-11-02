package xpointer

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// ErrNotFound reports a path, selector or quote that names nothing in
// the document, or names something that cannot carry a position.
var ErrNotFound = errors.New("xpointer: position not in document")

// ErrNoBody reports a resource with no <body> to count from.
var ErrNoBody = errors.New("xpointer: document has no body")

// CrossPoint skips the text of these and of everything inside them; the
// element itself still takes its place among its siblings.
var hiddenNames = map[string]bool{"head": true, "style": true, "script": true, "title": true, "rp": true}

type element struct {
	name   string
	id     string
	index  int // 1-based among element siblings of the same name
	parent *element
	kids   []child
	hidden bool
}

// child is one entry in an element's content: an element or a text run.
type child struct {
	el  *element
	run *run
}

// run is one text node as CrossPoint counts them: the character data
// between two pieces of markup.
type run struct {
	parent *element
	text   string
	// index is the 1-based text() index among the parent's text nodes;
	// a hidden run is not counted and keeps 0.
	index int
	// start is the run's first code point in the document's visible text.
	start  int
	length int // in code points
	blank  bool
	hidden bool
}

// Document is one parsed XHTML resource.
type Document struct {
	body    *element
	runs    []*run // every run under body, in document order
	ids     map[string]int
	visible int // visible code points up to the end of the last non-blank run
}

// ParseDocument reads an XHTML resource leniently, the way a reader has to take
// an EPUB that is not quite well-formed.
func ParseDocument(data []byte) (*Document, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		switch strings.ToLower(label) {
		case "utf-8", "utf8", "us-ascii", "ascii":
			return input, nil
		}
		return nil, errors.New("xpointer: unsupported charset " + label)
	}

	d := &Document{ids: map[string]int{}}
	var cur *element // innermost open element under body, nil outside it
	done := false
	visible := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			id := attr(t, "id")
			if id != "" {
				d.ids[id]++
			}
			if done {
				continue
			}
			if cur == nil {
				if !strings.EqualFold(t.Name.Local, "body") {
					continue
				}
				d.body = &element{name: t.Name.Local, id: id}
				cur = d.body
				continue
			}
			el := &element{name: t.Name.Local, id: id, parent: cur, hidden: cur.hidden || hiddenNames[strings.ToLower(t.Name.Local)]}
			for _, k := range cur.kids {
				if k.el != nil && k.el.name == el.name {
					el.index++
				}
			}
			el.index++
			cur.kids = append(cur.kids, child{el: el})
			cur = el
		case xml.EndElement:
			if cur == nil {
				continue
			}
			cur = cur.parent
			if cur == nil {
				done = true
			}
		case xml.CharData:
			if cur == nil || len(t) == 0 {
				continue
			}
			r := &run{parent: cur, text: string(t), length: utf8.RuneCount(t), blank: blank(string(t)), hidden: cur.hidden}
			if !r.hidden {
				for _, k := range cur.kids {
					if k.run != nil && !k.run.hidden {
						r.index++
					}
				}
				r.index++
				r.start = visible
				visible += r.length
				if !r.blank {
					d.visible = r.start + len([]rune(strings.TrimRight(r.text, " \r\n\t")))
				}
			}
			cur.kids = append(cur.kids, child{run: r})
			d.runs = append(d.runs, r)
		}
	}
	if d.body == nil {
		return nil, ErrNoBody
	}
	return d, nil
}

func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name && (a.Name.Space == "" || a.Name.Space == "xml") {
			return a.Value
		}
	}
	return ""
}

// blank mirrors CRe's IsEmptySpace: only these four count.
func blank(s string) bool {
	return strings.Trim(s, " \r\n\t") == ""
}

// Position is a place in a Document: a text run and a code point offset
// into it.
type Position struct {
	run    *run
	offset int
}

// Progression is the position's fraction of the resource's visible text.
func (p Position) Progression(d *Document) float64 {
	if d.visible == 0 {
		return 0
	}
	return min(float64(p.run.start+p.offset)/float64(d.visible), 1)
}

func (d *Document) usable(i int) bool {
	r := d.runs[i]
	return !r.hidden && !r.blank
}

// next returns the first usable run at or after the given one, which is
// where a position in formatting whitespace is read.
func (d *Document) next(r *run) (*run, bool) {
	at := -1
	for i, x := range d.runs {
		if x == r {
			at = i
			break
		}
	}
	for i := max(at, 0); i < len(d.runs); i++ {
		if d.usable(i) {
			return d.runs[i], true
		}
	}
	return nil, false
}

// Resolve finds a parsed path in the document. Element steps are matched
// strictly. The text node is first counted CrossPoint's way; if that
// lands in formatting whitespace or the offset overruns it, it is
// counted again skipping whitespace-only nodes, the way KOReader's
// engine drops them.
func (d *Document) Resolve(p Path) (Position, error) {
	el := d.body
	for _, s := range p.Steps {
		want := max(s.Index, 1)
		var found *element
		for _, k := range el.kids {
			if k.el != nil && k.el.name == s.Name && k.el.index == want {
				found = k.el
				break
			}
		}
		if found == nil {
			return Position{}, ErrNotFound
		}
		el = found
	}
	if el.hidden {
		return Position{}, ErrNotFound
	}
	if p.Text == 0 {
		return d.firstIn(el)
	}
	var strict, loose *run
	n := 0
	for _, k := range el.kids {
		if k.run == nil || k.run.hidden {
			continue
		}
		if k.run.index == p.Text {
			strict = k.run
		}
		if !k.run.blank {
			n++
			if n == p.Text {
				loose = k.run
			}
		}
	}
	if strict != nil && !strict.blank && p.Offset <= strict.length {
		return Position{run: strict, offset: p.Offset}, nil
	}
	if loose != nil && p.Offset <= loose.length {
		return Position{run: loose, offset: p.Offset}, nil
	}
	return Position{}, ErrNotFound
}

// firstIn is the start of the first usable text inside an element.
func (d *Document) firstIn(el *element) (Position, error) {
	for i, r := range d.runs {
		if d.usable(i) && within(r.parent, el) {
			return Position{run: r}, nil
		}
	}
	return Position{}, ErrNotFound
}

func within(el, ancestor *element) bool {
	for ; el != nil; el = el.parent {
		if el == ancestor {
			return true
		}
	}
	return false
}

// XPath writes a position as CrossPoint would, for the 1-based spine
// index fragment. A position in formatting whitespace moves to the next
// text, since neither engine anchors there.
func (d *Document) XPath(fragment int, pos Position) (Path, error) {
	r := pos.run
	off := pos.offset
	if r == nil || r.hidden {
		return Path{}, ErrNotFound
	}
	if r.blank {
		next, ok := d.next(r)
		if !ok {
			return Path{}, ErrNotFound
		}
		r, off = next, 0
	}
	var steps []Step
	for el := r.parent; el != d.body; el = el.parent {
		steps = append(steps, Step{Name: el.name, Index: el.index})
	}
	for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
		steps[i], steps[j] = steps[j], steps[i]
	}
	return Path{Fragment: fragment, Steps: steps, Text: r.index, Offset: off}, nil
}

// at maps a visible code point offset to a position, the way
// CrossPoint's findXPathForVisibleTextOffset does: the offset falls in
// the run that holds it, and an offset in formatting whitespace moves
// to the next text.
func (d *Document) at(offset int) (Position, error) {
	for i, r := range d.runs {
		if r.hidden || offset >= r.start+r.length {
			continue
		}
		if r.blank || offset < r.start {
			for j := i; j < len(d.runs); j++ {
				if d.usable(j) {
					return Position{run: d.runs[j]}, nil
				}
			}
			return Position{}, ErrNotFound
		}
		return Position{run: r, offset: offset - r.start}, nil
	}
	return Position{}, ErrNotFound
}
