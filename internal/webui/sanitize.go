package webui

import (
	"strings"

	"github.com/a-h/templ"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Book descriptions come from the publication, from a Calibre export or
// from a third-party metadata provider, so they are markup written by
// somebody we do not trust, and they have to reach the page as HTML
// anyway — Calibre wraps every blurb in <div align="justify"> and the
// alternative is printing the tags at the reader.
//
// The sanitizer below parses the description with golang.org/x/net/html
// and re-serialises the tree it got, element by element, against an
// allowlist. Nothing from the input is ever copied through verbatim:
// tag names are matched, attributes are rebuilt, text is escaped again
// on the way out. That is the only shape of sanitizer worth having —
// a regex or a string replacement sanitizes the markup somebody wrote,
// not the markup the browser will parse, and those differ precisely
// where the attack lives.
//
// x/net/html is a new direct dependency, but it was already in the
// module graph as an indirect one and it is a golang.org/x module like
// the x/crypto and x/text this binary already needs, so it buys the
// real HTML5 tree builder — the same algorithm the browser runs — for
// no new supply-chain surface. A third-party sanitizer (bluemonday)
// would have been a genuinely new dependency for a policy we can state
// in fifty lines, and Go's stdlib `html` package only escapes strings;
// it cannot tell an element from a text node.

const (
	// maxSanitizedBytes caps what one description can add to a page, so
	// a hostile or merely deranged blurb cannot make every listing that
	// mentions the book unusable.
	maxSanitizedBytes = 64 << 10

	// maxSanitizedDepth is how deeply the output may nest. Beyond it
	// elements are unwrapped: the text survives, the pyramid does not.
	maxSanitizedDepth = 24

	// maxSanitizedRecursion stops us walking an input nested deeper
	// than any real document, which would otherwise cost us stack
	// rather than output bytes.
	maxSanitizedRecursion = 256
)

// allowedElements is the whole vocabulary a description may use.
//
// <img> is deliberately absent: a description that loads a remote image
// is a tracking pixel, and rendering one would hand the reader's IP
// address and User-Agent to whoever wrote the blurb, on a page they
// asked their own server for.
var allowedElements = map[string]bool{
	"p": true, "br": true, "div": true, "span": true,
	"em": true, "i": true, "strong": true, "b": true,
	"u": true, "s": true, "sub": true, "sup": true,
	"blockquote": true, "ul": true, "ol": true, "li": true,
	"h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true, "a": true, "code": true,
	"pre": true, "hr": true, "small": true,
}

// droppedSubtrees are the elements whose content is not text that lost
// its tags but a payload in its own right, so the element goes and
// takes its children with it.
var droppedSubtrees = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true,
	"embed": true, "svg": true, "math": true, "form": true,
	"input": true, "template": true, "noscript": true,
}

// voidElements have no closing tag.
var voidElements = map[string]bool{"br": true, "hr": true}

// blockElements decides whether the input already lays itself out. Its
// membership is wider than the allowlist on purpose: an input made of
// <table> rows has structure even though we unwrap all of it, and
// turning its newlines into <br> would only double the gaps.
var blockElements = map[string]bool{
	"p": true, "div": true, "br": true, "ul": true, "ol": true,
	"li": true, "blockquote": true, "pre": true, "hr": true,
	"table": true, "tr": true, "td": true, "th": true, "section": true,
	"article": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true,
}

// descriptionHTML renders a publication-supplied description as the
// sanitized HTML it is meant to be. Templates call this instead of
// printing the string, which would show the reader the tags.
func descriptionHTML(raw string) templ.Component {
	return templ.Raw(sanitizeDescription(raw))
}

// sanitizeDescription turns untrusted description markup into HTML that
// is safe to embed in a page. The result is always well balanced: every
// element it opens it closes, so a truncated or malformed description
// cannot swallow the rest of the document.
func sanitizeDescription(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	body := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(raw), body)
	if err != nil {
		// The tree builder recovers from anything a browser recovers
		// from, so this is close to unreachable; if it ever fires we
		// still owe the reader the text, escaped.
		return html.EscapeString(raw)
	}
	s := &sanitizer{breakLines: !hasBlockMarkup(nodes)}
	for _, n := range nodes {
		s.walk(n, 0, 0)
	}
	return strings.TrimSpace(s.out.String())
}

// hasBlockMarkup reports whether the input already carries its own
// layout. When it does not — the common case of a hand-typed blurb —
// the sanitizer turns newlines into <br> so the paragraphs the author
// typed survive HTML's whitespace collapsing.
func hasBlockMarkup(nodes []*html.Node) bool {
	for _, n := range nodes {
		if n.Type == html.ElementNode && blockElements[n.Data] {
			return true
		}
		if hasBlockMarkup(children(n)) {
			return true
		}
	}
	return false
}

func children(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return out
}

type sanitizer struct {
	out        strings.Builder
	breakLines bool
	full       bool
}

// write appends to the output unless the cap has been reached. Closing
// tags go through closeTag instead, so the output stays balanced even
// after truncation.
func (s *sanitizer) write(str string) {
	if s.full {
		return
	}
	if s.out.Len()+len(str) > maxSanitizedBytes {
		s.full = true
		return
	}
	s.out.WriteString(str)
}

// closeTag writes past the cap, by the length of a tag name, because an
// unbalanced document is worse than a slightly overlong one.
func (s *sanitizer) closeTag(name string) {
	s.out.WriteString("</" + name + ">")
}

// walk renders one node. depth counts the elements actually emitted,
// nest counts the input levels descended, and the two differ whenever
// something is unwrapped.
func (s *sanitizer) walk(n *html.Node, depth, nest int) {
	if nest > maxSanitizedRecursion {
		return
	}
	switch n.Type {
	case html.TextNode:
		s.text(n.Data)
		return
	case html.ElementNode:
		// fall through
	default:
		// Comments, doctypes and processing instructions carry nothing
		// a reader can see and plenty a parser can be confused by.
		return
	}
	name := strings.ToLower(n.Data)
	if droppedSubtrees[name] {
		return
	}
	if !allowedElements[name] || depth >= maxSanitizedDepth {
		s.walkChildren(n, depth, nest+1)
		return
	}
	if name == "a" {
		href, ok := safeHref(attr(n, "href"))
		if !ok {
			// A link nobody may follow is just its text.
			s.walkChildren(n, depth, nest+1)
			return
		}
		s.write(`<a href="` + html.EscapeString(href) +
			`" rel="nofollow noopener noreferrer" target="_blank">`)
		s.walkChildren(n, depth+1, nest+1)
		s.closeTag("a")
		return
	}
	if voidElements[name] {
		s.write("<" + name + ">")
		return
	}
	// Every other allowed element is emitted bare: no attribute of any
	// kind survives, which is what disposes of on* handlers, style,
	// class, id, align, srcset and everything invented since.
	s.write("<" + name + ">")
	s.walkChildren(n, depth+1, nest+1)
	s.closeTag(name)
}

func (s *sanitizer) walkChildren(n *html.Node, depth, nest int) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		s.walk(c, depth, nest)
	}
}

func (s *sanitizer) text(t string) {
	if t == "" {
		return
	}
	escaped := html.EscapeString(t)
	if !s.breakLines {
		s.write(escaped)
		return
	}
	lines := strings.Split(escaped, "\n")
	for i, line := range lines {
		if i > 0 {
			s.write("<br>")
		}
		s.write(line)
	}
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}

// safeHref decides whether a link may be rendered, and returns the
// value to render.
//
// The parser has already decoded entities, so `java&#09;script:` lands
// here as "java\tscript:" — which is why the scheme is checked after
// every ASCII control character has been removed, not before. Anything
// that is not http, https or mailto is refused, including the relative
// and scheme-relative forms' near misses.
func safeHref(v string) (string, bool) {
	var b strings.Builder
	for _, r := range v {
		if r <= 0x20 || r == 0x7f {
			// Space included: a leading or embedded space is how the
			// classic scheme-smuggling payloads are written, and no
			// legitimate href needs a literal one.
			continue
		}
		b.WriteRune(r)
	}
	cleaned := b.String()
	if cleaned == "" {
		return "", false
	}
	colon := strings.IndexByte(cleaned, ':')
	if colon < 0 {
		// No scheme at all: relative, fragment or query. Fine.
		return cleaned, true
	}
	// A ':' after a path separator is part of the path, not a scheme.
	if cut := strings.IndexAny(cleaned, "/?#"); cut >= 0 && cut < colon {
		return cleaned, true
	}
	switch strings.ToLower(cleaned[:colon]) {
	case "http", "https", "mailto":
		return cleaned, true
	}
	return "", false
}
