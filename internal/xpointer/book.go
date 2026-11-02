package xpointer

import (
	"context"
	"encoding/xml"
	"errors"
	"net/url"
	"path"
	"strings"

	"github.com/chmouel/liseur-sync/internal/epub"
)

// Book is the part of an EPUB a translation needs: the spine, and the
// bytes of a resource in it.
type Book struct {
	// spine holds archive paths, unescaped, in package order. It keeps
	// linear="no" items, which Readium's reading order drops: a
	// DocFragment index counts every itemref.
	spine []string
	read  func(ctx context.Context, name string) ([]byte, error)
}

// Locator is the part of a Readium locator a translation fills in.
type Locator struct {
	Href        string
	Progression float64
	Anchor      Anchor
}

// Open reads a publication's spine from its package document.
func Open(ctx context.Context, pub *epub.Publication) (*Book, error) {
	opf, err := pub.Resource(ctx, pub.PackagePath)
	if err != nil {
		return nil, err
	}
	spine, err := parseSpine(opf, pub.PackagePath)
	if err != nil {
		return nil, err
	}
	return &Book{spine: spine, read: pub.Resource}, nil
}

func parseSpine(opf []byte, opfPath string) ([]string, error) {
	var pkg struct {
		Items []struct {
			ID   string `xml:"id,attr"`
			Href string `xml:"href,attr"`
		} `xml:"manifest>item"`
		Refs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"spine>itemref"`
	}
	if err := xml.Unmarshal(opf, &pkg); err != nil {
		return nil, err
	}
	hrefs := make(map[string]string, len(pkg.Items))
	for _, it := range pkg.Items {
		hrefs[it.ID] = it.Href
	}
	base := path.Dir(opfPath)
	spine := make([]string, 0, len(pkg.Refs))
	for _, ref := range pkg.Refs {
		u, err := url.Parse(hrefs[ref.IDRef])
		if err != nil || u.Path == "" {
			return nil, errors.New("xpointer: spine item with no usable href")
		}
		spine = append(spine, path.Join(base, u.Path))
	}
	if len(spine) == 0 {
		return nil, errors.New("xpointer: empty spine")
	}
	return spine, nil
}

func (b *Book) document(ctx context.Context, i int) (*Document, error) {
	data, err := b.read(ctx, b.spine[i])
	if err != nil {
		return nil, err
	}
	return ParseDocument(data)
}

// LocatorFor translates a CRe XPath into a Liseur anchored locator.
func (b *Book) LocatorFor(ctx context.Context, xpath string) (Locator, error) {
	p, err := Parse(xpath)
	if err != nil {
		return Locator{}, err
	}
	if p.Fragment > len(b.spine) {
		return Locator{}, ErrNotFound
	}
	doc, err := b.document(ctx, p.Fragment-1)
	if err != nil {
		return Locator{}, err
	}
	pos, err := doc.Resolve(p)
	if err != nil {
		return Locator{}, err
	}
	a, err := doc.AnchorAt(pos)
	if err != nil {
		return Locator{}, err
	}
	return Locator{
		Href:        (&url.URL{Path: b.spine[p.Fragment-1]}).EscapedPath(),
		Progression: pos.Progression(doc),
		Anchor:      a,
	}, nil
}

// XPathFor translates a Liseur anchor in the resource at href into the
// XPath CrossPoint would write for the same word.
func (b *Book) XPathFor(ctx context.Context, href string, a Anchor) (string, error) {
	want, ok := normalizeHref(href)
	if !ok {
		return "", ErrNotFound
	}
	for i, name := range b.spine {
		if name != want {
			continue
		}
		doc, err := b.document(ctx, i)
		if err != nil {
			return "", err
		}
		pos, err := doc.Find(a)
		if err != nil {
			return "", err
		}
		p, err := doc.XPath(i+1, pos)
		if err != nil {
			return "", err
		}
		return p.String(), nil
	}
	return "", ErrNotFound
}

// normalizeHref turns a locator href into an archive path. Readium
// Kotlin 2 wrote hrefs with a leading slash; later readers do not.
func normalizeHref(href string) (string, bool) {
	u, err := url.Parse(href)
	if err != nil || u.IsAbs() || u.Host != "" {
		return "", false
	}
	p := strings.TrimPrefix(path.Clean("/"+u.Path), "/")
	return p, p != ""
}
