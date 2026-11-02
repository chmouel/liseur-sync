package kosync

import (
	"context"
	"encoding/json"
	"os"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/xpointer"
)

// BookOpener opens the file behind a catalog book. The caller owns the file.
type BookOpener interface {
	OpenBook(ctx context.Context, book store.CatalogBook) (*os.File, int64, error)
}

type anchoredLocator struct {
	Href      string `json:"href"`
	Locations struct {
		LiseurAnchor int    `json:"liseurAnchor"`
		CSSSelector  string `json:"cssSelector"`
	} `json:"locations"`
	Text struct {
		Before    string `json:"before"`
		Highlight string `json:"highlight"`
		After     string `json:"after"`
	} `json:"text"`
}

// derivedXPath answers a KOReader-speaking client with an XPath when
// the newest op is a Liseur exact anchor written against the very file
// the client named (ADR-0059). Anything short of that is "", and the
// caller falls back to the percentage.
func (s *Server) derivedXPath(ctx context.Context, userID, partialMD5 string, o store.Op) string {
	if s.Files == nil || o.ForeignPos != nil || o.EditionSHA == nil || len(o.LocatorJSON) == 0 {
		return ""
	}
	var loc anchoredLocator
	if json.Unmarshal(o.LocatorJSON, &loc) != nil ||
		loc.Locations.LiseurAnchor != 1 || loc.Href == "" || loc.Text.Highlight == "" {
		return ""
	}
	book, err := s.St.CatalogBookByPartialMD5(ctx, userID, partialMD5)
	if err != nil || book.ContentSHA256 != *o.EditionSHA {
		return ""
	}
	f, size, err := s.Files.OpenBook(ctx, book)
	if err != nil {
		return ""
	}
	defer f.Close()
	pub, err := epub.OpenPublication(ctx, f, size, epub.DefaultLimits())
	if err != nil {
		return ""
	}
	b, err := xpointer.Open(ctx, pub)
	if err != nil {
		return ""
	}
	xp, err := b.XPathFor(ctx, loc.Href, xpointer.Anchor{
		Selector:  loc.Locations.CSSSelector,
		Before:    loc.Text.Before,
		Highlight: loc.Text.Highlight,
		After:     loc.Text.After,
	})
	if err != nil {
		return ""
	}
	return xp
}
