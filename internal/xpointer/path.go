// Package xpointer translates a reading position between the two exact
// forms the server sees (ADR-0059): the CRe XPath a KOReader-engine
// device sends through kosync, and the text anchor the Liseur readers
// write into a Readium locator.
//
// Both name a place in one XHTML resource of one EPUB. An XPath walks
// elements below <body> by name and same-name index, picks a text node
// and counts code points into it. A Liseur anchor names a block by CSS
// selector and quotes the words around the first visible one. The
// element indexes of the two agree (an XPath step index is a CSS
// :nth-of-type), so translating is a walk of the same tree.
//
// Text nodes and offsets are counted the way CrossPoint Reader counts
// them: character data between any two pieces of markup (an element
// boundary, a comment, a processing instruction, a CDATA boundary) is
// one node, empty runs make no node, content of non-visible elements is
// not counted, and offsets are raw Unicode code points. KOReader's own
// engine may discard whitespace-only nodes and collapses whitespace runs
// before counting. Outgoing derivation declines paths when discarded sibling
// whitespace could change the text-node index; offsets within a wrapped node
// can still differ between engines.
package xpointer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrSyntax reports a string that is not a CRe XPath this package reads.
var ErrSyntax = errors.New("xpointer: not a document-fragment xpath")

// Step is one element step below <body>. Index is the 1-based position
// among same-name siblings; 0 means the path left it out, which CRe
// does when the element is the only one of its name.
type Step struct {
	Name  string
	Index int
}

// Path is a parsed CRe XPath:
// /body/DocFragment[F]/body/div[2]/p[4]/text()[1].17
type Path struct {
	// Fragment is the 1-based spine index.
	Fragment int
	Steps    []Step
	// Text is the 1-based text node index under the last step, or 0 when
	// the path ends at an element.
	Text   int
	Offset int
}

const maxSteps = 64

// Parse reads a CRe XPath. It accepts both CRe's spelling, which leaves
// out [1] on an only child, and CrossPoint's, which always writes it.
func Parse(s string) (Path, error) {
	const prefix = "/body/DocFragment["
	if !strings.HasPrefix(s, prefix) {
		return Path{}, ErrSyntax
	}
	rest := s[len(prefix):]
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return Path{}, ErrSyntax
	}
	frag, err := strconv.Atoi(rest[:end])
	if err != nil || frag < 1 {
		return Path{}, ErrSyntax
	}
	rest = rest[end+1:]
	if !strings.HasPrefix(rest, "/body") {
		return Path{}, ErrSyntax
	}
	rest = rest[len("/body"):]
	p := Path{Fragment: frag}

	// A path may end in ".N". After text() it is the code point offset;
	// after an element it is CRe's child offset, and a position at an
	// element is read as its start, so the number is dropped.
	if dot := strings.LastIndexByte(rest, '.'); dot >= 0 && !strings.Contains(rest[dot:], "/") {
		off, err := strconv.Atoi(rest[dot+1:])
		if err != nil || off < 0 {
			return Path{}, ErrSyntax
		}
		rest = rest[:dot]
		p.Offset = off
	}
	if rest != "" {
		if rest[0] != '/' {
			return Path{}, ErrSyntax
		}
		parts := strings.Split(rest[1:], "/")
		if len(parts) > maxSteps {
			return Path{}, ErrSyntax
		}
		for i, part := range parts {
			name, idx, err := splitStep(part)
			if err != nil {
				return Path{}, err
			}
			if name == "text()" {
				if i != len(parts)-1 {
					return Path{}, ErrSyntax
				}
				p.Text = max(idx, 1)
				continue
			}
			if strings.ContainsAny(name, "()[]. \t\r\n") {
				return Path{}, ErrSyntax
			}
			p.Steps = append(p.Steps, Step{Name: name, Index: idx})
		}
	}
	if p.Text == 0 {
		p.Offset = 0
	}
	return p, nil
}

func splitStep(part string) (string, int, error) {
	open := strings.IndexByte(part, '[')
	if open < 0 {
		if part == "" {
			return "", 0, ErrSyntax
		}
		return part, 0, nil
	}
	if open == 0 || !strings.HasSuffix(part, "]") {
		return "", 0, ErrSyntax
	}
	idx, err := strconv.Atoi(part[open+1 : len(part)-1])
	if err != nil || idx < 1 {
		return "", 0, ErrSyntax
	}
	return part[:open], idx, nil
}

// String writes the path the way CrossPoint does, with every index
// spelled out; CRe reads that spelling too.
func (p Path) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "/body/DocFragment[%d]/body", p.Fragment)
	for _, s := range p.Steps {
		fmt.Fprintf(&b, "/%s[%d]", s.Name, max(s.Index, 1))
	}
	if p.Text > 0 {
		fmt.Fprintf(&b, "/text()[%d].%d", p.Text, p.Offset)
	}
	return b.String()
}
