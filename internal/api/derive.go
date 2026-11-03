package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
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

const maxDerivedEditions = 4

// locatorDeriver owns a bounded, request-local cache of open editions.
type locatorDeriver struct {
	s      *Server
	ctx    context.Context
	userID string
	books  []*derivedEdition
}

type derivedEdition struct {
	md5  string
	sha  string
	book *xpointer.Book
	file *os.File
}

// newLocatorDeriver is nil when the credential may not read book text.
func (s *Server) newLocatorDeriver(r *http.Request) *locatorDeriver {
	tok, _ := auth.TokenFrom(r)
	if s.Files == nil || tok.UserID == "" || !tok.Scopes.Allows(store.ScopeLibraryRead) {
		return nil
	}
	return &locatorDeriver{s: s, ctx: r.Context(), userID: tok.UserID}
}

func (d *locatorDeriver) close() {
	if d == nil {
		return
	}
	for _, ed := range d.books {
		if ed.file != nil {
			ed.file.Close()
		}
	}
	d.books = nil
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
	for i, ed := range d.books {
		if ed.md5 == md5 {
			d.books = append(slices.Delete(d.books, i, i+1), ed)
			if ed.book == nil {
				return nil
			}
			return ed
		}
	}
	// Evict before opening, so even a cache miss stays within the descriptor budget
	if len(d.books) == maxDerivedEditions {
		if f := d.books[0].file; f != nil {
			f.Close()
		}
		d.books = slices.Delete(d.books, 0, 1)
	}
	ed := &derivedEdition{md5: md5}
	d.books = append(d.books, ed)
	book, err := d.s.St.CatalogBookByPartialMD5(d.ctx, d.userID, md5)
	if err != nil {
		return nil
	}
	f, size, err := d.s.Files.OpenBook(d.ctx, book)
	if err != nil {
		return nil
	}
	pub, err := epub.OpenPublication(d.ctx, f, size, epub.DefaultLimits())
	if err != nil {
		f.Close()
		return nil
	}
	b, err := xpointer.Open(d.ctx, pub)
	if err != nil {
		f.Close()
		return nil
	}
	ed.sha, ed.book, ed.file = book.ContentSHA256, b, f
	return ed
}
