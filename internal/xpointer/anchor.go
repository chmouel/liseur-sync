package xpointer

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ErrAmbiguous reports a quote that occurs more than once in its block.
// Taking the first match would be a guess, and a wrong anchor costs the
// reader their place where no anchor only costs precision.
var ErrAmbiguous = errors.New("xpointer: quote is not unique in its block")

// The capture lengths and the block and blocked tag sets are the Liseur
// readers' (reader-anchor.js on the web, ExactLocatorAnchor.kt on
// Android); an anchor written here has to read as one of theirs.
const (
	maxBefore    = 32
	maxHighlight = 64
	maxAfter     = 32
	maxSelector  = 2048
)

var (
	blockNames   = map[string]bool{"p": true, "li": true, "blockquote": true, "pre": true, "td": true, "th": true, "figcaption": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "div": true}
	blockedNames = map[string]bool{"script": true, "style": true, "noscript": true, "template": true}
	wordPattern  = regexp.MustCompile(`[\p{L}\p{N}\p{M}]+(?:['’][\p{L}\p{N}\p{M}]+)*`)
)

// Anchor is a Liseur exact anchor: the block a word is in, named by CSS
// selector, and the word with the text either side of it.
type Anchor struct {
	Selector  string
	Before    string
	Highlight string
	After     string
}

func (a Anchor) quote() string { return a.Before + a.Highlight + a.After }

// AnchorAt describes the first word at or after a position the way the
// Liseur readers describe the first word on screen.
func (d *Document) AnchorAt(pos Position) (Anchor, error) {
	if pos.run == nil {
		return Anchor{}, ErrNotFound
	}
	from := -1
	for i, r := range d.runs {
		if r == pos.run {
			from = i
			break
		}
	}
	if from < 0 {
		return Anchor{}, ErrNotFound
	}
	for i := from; i < len(d.runs); i++ {
		r := d.runs[i]
		if r.hidden || r.blank || blockedNames[strings.ToLower(r.parent.name)] {
			continue
		}
		skip := 0
		if i == from {
			skip = pos.offset
		}
		// Words arrive in byte order, so the rune count up to each word's
		// end is accumulated from the previous one rather than recounted
		// from the start of the run.
		prev, before := 0, 0
		for _, w := range wordPattern.FindAllStringIndex(r.text, -1) {
			length := utf8.RuneCountInString(r.text[w[0]:w[1]])
			before += utf8.RuneCountInString(r.text[prev:w[1]])
			prev = w[1]
			if before <= skip {
				continue
			}
			// A word the readers cannot quote whole cannot carry an offset
			// inside it: the anchor would name the word's first rune, which
			// in a run with no spaces (a CJK or Thai paragraph) is far from
			// the position asked for.
			if length > maxHighlight {
				return Anchor{}, ErrNotFound
			}
			return d.anchorFor(r, w[0], w[1])
		}
	}
	return Anchor{}, ErrNotFound
}

func (d *Document) anchorFor(r *run, start, end int) (Anchor, error) {
	block := r.parent
	for block != d.body && !blockNames[strings.ToLower(block.name)] {
		block = block.parent
	}
	text, at := blockText(block, r)
	a := Anchor{
		Selector:  d.selector(block),
		Before:    lastRunes(text[:at+start], maxBefore),
		Highlight: firstRunes(r.text[start:end], maxHighlight),
		After:     firstRunes(text[at+end:], maxAfter),
	}
	if len(a.Selector) > maxSelector {
		return Anchor{}, ErrNotFound
	}
	if n := occurrences(text, a.quote()); n != 1 {
		return Anchor{}, ErrAmbiguous
	}
	return a, nil
}

// blockText is the block's textContent, every run under it hidden or
// not, and the byte offset at which run r starts in it.
func blockText(block *element, r *run) (string, int) {
	var b strings.Builder
	at := -1
	var walk func(*element)
	walk = func(el *element) {
		for _, k := range el.kids {
			if k.el != nil {
				walk(k.el)
				continue
			}
			if k.run == r {
				at = b.Len()
			}
			b.WriteString(k.run.text)
		}
	}
	walk(block)
	return b.String(), at
}

func occurrences(text, quote string) int {
	if quote == "" {
		return 0
	}
	n := 0
	for at := 0; n < 2; n++ {
		i := strings.Index(text[at:], quote)
		if i < 0 {
			return n
		}
		_, size := utf8.DecodeRuneInString(text[at+i:])
		at += i + size
	}
	return n
}

func firstRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}

func lastRunes(s string, n int) string {
	i := len(s)
	for ; n > 0 && i > 0; n-- {
		_, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
	}
	return s[i:]
}

// selector names an element the way the readers' capture scripts do:
// its id when no other element in the document has it, otherwise
// :nth-of-type steps down from body.
func (d *Document) selector(el *element) string {
	if el.id != "" && d.ids[el.id] == 1 {
		return "#" + cssEscape(el.id)
	}
	var parts []string
	for ; el != d.body; el = el.parent {
		parts = append(parts, fmt.Sprintf("%s:nth-of-type(%d)", el.name, el.index))
	}
	if len(parts) == 0 {
		return "body"
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "body > " + strings.Join(parts, " > ")
}

// Find locates an anchor's word. The selector has to name an element,
// the quote has to occur in its text exactly once, and the word has to
// be in text CrossPoint counts.
func (d *Document) Find(a Anchor) (Position, error) {
	if a.Highlight == "" {
		return Position{}, ErrNotFound
	}
	block, err := d.lookup(a.Selector)
	if err != nil {
		return Position{}, err
	}
	text, _ := blockText(block, nil)
	q := a.quote()
	switch occurrences(text, q) {
	case 0:
		return Position{}, ErrNotFound
	case 1:
	default:
		return Position{}, ErrAmbiguous
	}
	want := strings.Index(text, q) + len(a.Before)
	at := 0
	var found *run
	var walk func(*element) bool
	walk = func(el *element) bool {
		for _, k := range el.kids {
			if k.el != nil {
				if walk(k.el) {
					return true
				}
				continue
			}
			if want < at+len(k.run.text) {
				found = k.run
				return true
			}
			at += len(k.run.text)
		}
		return false
	}
	if !walk(block) || found.hidden || found.blank {
		return Position{}, ErrNotFound
	}
	return Position{run: found, offset: utf8.RuneCountInString(found.text[:want-at])}, nil
}

func (d *Document) lookup(sel string) (*element, error) {
	sel = strings.TrimSpace(sel)
	if strings.HasPrefix(sel, "#") {
		id, ok := cssUnescape(sel[1:])
		if !ok || d.ids[id] != 1 {
			return nil, ErrNotFound
		}
		var found *element
		var walk func(*element)
		walk = func(el *element) {
			if found != nil {
				return
			}
			if el.id == id {
				found = el
				return
			}
			for _, k := range el.kids {
				if k.el != nil {
					walk(k.el)
				}
			}
		}
		walk(d.body)
		if found == nil {
			return nil, ErrNotFound
		}
		return found, nil
	}
	parts := strings.Split(sel, ">")
	if strings.TrimSpace(parts[0]) != "body" {
		return nil, ErrNotFound
	}
	el := d.body
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		name, rest, ok := strings.Cut(part, ":nth-of-type(")
		if !ok || name == "" || !strings.HasSuffix(rest, ")") {
			return nil, ErrNotFound
		}
		idx, err := strconv.Atoi(rest[:len(rest)-1])
		if err != nil {
			return nil, ErrNotFound
		}
		var next *element
		for _, k := range el.kids {
			if k.el != nil && k.el.name == name && k.el.index == idx {
				next = k.el
				break
			}
		}
		if next == nil {
			return nil, ErrNotFound
		}
		el = next
	}
	return el, nil
}

// cssEscape is CSSOM's CSS.escape, which is what both readers use.
func cssEscape(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == 0:
			b.WriteRune('\uFFFD')
		case r >= 1 && r <= 0x1f || r == 0x7f,
			i == 0 && r >= '0' && r <= '9',
			i == 1 && r >= '0' && r <= '9' && runes[0] == '-':
			fmt.Fprintf(&b, "\\%x ", r)
		case i == 0 && r == '-' && len(runes) == 1:
			b.WriteString("\\-")
		case r >= 0x80 || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		default:
			b.WriteByte('\\')
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cssUnescape reads an escaped CSS identifier back.
func cssUnescape(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			r, size := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += size
			continue
		}
		i++
		if i >= len(s) {
			return "", false
		}
		j := i
		for j < len(s) && j-i < 6 && isHex(s[j]) {
			j++
		}
		if j == i {
			r, size := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += size
			continue
		}
		v, _ := strconv.ParseUint(s[i:j], 16, 32)
		if v == 0 || v > utf8.MaxRune || v >= 0xd800 && v <= 0xdfff {
			v = 0xfffd
		}
		b.WriteRune(rune(v))
		i = j
		if i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
			i++
		}
	}
	return b.String(), true
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
