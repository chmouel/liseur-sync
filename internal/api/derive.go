package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/xpointer"
)

// derivedJSON marks a locator the server computed at read time rather
// than one a client wrote (ADR-0059). A client never pushes it back.
type derivedJSON struct {
	From string `json:"from"`
}

// locatorDeriver fills kosync ops with the Liseur locator of the word
// their XPath names, for one response. It opens each book at most once
// and closes them all in close.
type locatorDeriver struct {
	s      *Server
	ctx    context.Context
	userID string
	books  map[string]*derivedEdition
	files  []*os.File
}

type derivedEdition struct {
	sha  string
	book *xpointer.Book
}

// newLocatorDeriver is nil when the credential may not read book text.
func (s *Server) newLocatorDeriver(r *http.Request) *locatorDeriver {
	tok, _ := auth.TokenFrom(r)
	if s.Files == nil || tok.UserID == "" || !tok.Scopes.Allows(store.ScopeLibraryRead) {
		return nil
	}
	return &locatorDeriver{s: s, ctx: r.Context(), userID: tok.UserID, books: map[string]*derivedEdition{}}
}

func (d *locatorDeriver) close() {
	if d == nil {
		return
	}
	for _, f := range d.files {
		f.Close()
	}
}

// op renders o, with a derived edition and locator when o is a kosync
// op whose XPath resolves in the one granted file behind its
// fingerprint. Anything short of that renders o as stored.
func (d *locatorDeriver) op(o store.Op) opJSON {
	out := opToJSON(o)
	if d == nil || o.Origin != store.OriginKosync || o.ForeignPos == nil ||
		o.EditionSHA != nil || len(o.LocatorJSON) > 0 || o.OriginAlias == nil {
		return out
	}
	md5, ok := strings.CutPrefix(*o.OriginAlias, "partial-md5:")
	if !ok {
		return out
	}
	ed := d.edition(md5)
	if ed == nil {
		return out
	}
	loc, err := ed.book.LocatorFor(d.ctx, *o.ForeignPos)
	if err != nil {
		return out
	}
	raw, err := json.Marshal(map[string]any{
		"href": loc.Href,
		"type": "application/xhtml+xml",
		"locations": map[string]any{
			"progression":      loc.Progression,
			"totalProgression": o.Progression,
			"cssSelector":      loc.Anchor.Selector,
			"liseurAnchor":     1,
		},
		"text": map[string]string{
			"before":    loc.Anchor.Before,
			"highlight": loc.Anchor.Highlight,
			"after":     loc.Anchor.After,
		},
	})
	if err != nil {
		return out
	}
	sha := ed.sha
	out.EditionSHA = &sha
	out.Locator = raw
	out.Derived = &derivedJSON{From: "kosync-xpath"}
	return out
}

func (d *locatorDeriver) edition(md5 string) *derivedEdition {
	if ed, seen := d.books[md5]; seen {
		return ed
	}
	d.books[md5] = nil
	book, err := d.s.St.CatalogBookByPartialMD5(d.ctx, d.userID, md5)
	if err != nil {
		return nil
	}
	f, size, err := d.s.Files.OpenBook(d.ctx, book)
	if err != nil {
		return nil
	}
	d.files = append(d.files, f)
	pub, err := epub.OpenPublication(d.ctx, f, size, epub.DefaultLimits())
	if err != nil {
		return nil
	}
	b, err := xpointer.Open(d.ctx, pub)
	if err != nil {
		return nil
	}
	ed := &derivedEdition{sha: book.ContentSHA256, book: b}
	d.books[md5] = ed
	return ed
}
