// Package workident derives the identifiers that join a catalog book to a
// reader's sync work.
//
// It exists so that the HTTP resolve route and the maintenance backfill
// agree on that evidence. If they disagreed, a book resolved by an
// operator in advance and the same book resolved by a client on first
// read could land on two different works, and the reader's position would
// silently stop following them between devices.
package workident

import (
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// AliasOrder resolves in decreasing strength. "source" is the catalog
// server's own id for the book (e.g. "komga:<id>"): two devices browsing
// the same catalog hold the same one, so it identifies the book without
// either of them having downloaded the file.
var AliasOrder = []string{"sha256", "partial-md5", "source", "dc", "ta"}

// Order sorts identifiers strongest first and drops duplicates. Callers
// rely on both: resolution walks the list in order, and a repeated alias
// would otherwise be registered twice.
func Order(ids []store.Identifier) []store.Identifier {
	ordered := make([]store.Identifier, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, kind := range AliasOrder {
		for _, id := range ids {
			key := id.Kind + ":" + id.Value
			if id.Kind == kind && !seen[key] {
				ordered = append(ordered, id)
				seen[key] = true
			}
		}
	}
	return ordered
}

// ForCatalogBook is the evidence the catalog holds about one book,
// strongest first.
//
// Only a book whose file the last pass could find contributes a digest:
// a missing file cannot vouch for one, and registering an alias from it
// would attach the reader's work graph to bytes nobody can produce.
//
// The stable "source:liseur-sync:<book_id>" alias is not added here. The
// store appends it inside the resolution transaction, so it is present
// even for a book with nothing else to go on.
func ForCatalogBook(
	book store.CatalogBook, ids []store.BookIdentifier, author string,
) []store.Identifier {
	var out []store.Identifier
	// Duplicates are not filtered here: Order, which every return path
	// goes through, already collapses them. Empty values are, because
	// nothing downstream does and "sha256:" would alias every book whose
	// digest the catalog happens not to know.
	add := func(kind, value string) {
		if value == "" {
			return
		}
		out = append(out, store.Identifier{Kind: kind, Value: value})
	}
	if book.Status == store.BookActive {
		add("sha256", book.ContentSHA256)
	}
	for _, id := range ids {
		add("dc", id.Value)
	}
	if fingerprint := TitleAuthorFingerprint(book.Title, author); fingerprint != "" {
		add("ta", fingerprint)
	}
	return Order(out)
}

// TitleAuthorFingerprint is the fuzzy fallback alias. It must fold exactly
// the way a client's does or the two never meet, so it reuses the same
// normalization the catalog uses to match contributor names.
func TitleAuthorFingerprint(title, author string) string {
	folded := metadata.NormalizeName(title)
	if folded == "" {
		return ""
	}
	return folded + "|" + metadata.NormalizeName(author)
}

// Plan is everything ResolveCatalogBookWork needs for one catalog book,
// derived from the catalog alone. The resolve route and the backfill both
// go through it: the identifiers are only half the story, and a work
// proposed with a different title or an edition list built from different
// digests would resolve differently even from identical evidence.
//
// workID is the id to propose. It is used only if no identifier matches an
// existing work, so callers mint one per book and let the store discard it.
func Plan(
	userID, workID string,
	book store.CatalogBook,
	ids []store.BookIdentifier,
	author string,
) (store.Work, []store.Edition, []store.Identifier) {
	aliases := ForCatalogBook(book, ids, author)
	proposed := store.Work{
		ID: workID, UserID: userID,
		Title: book.Title, Author: author,
	}
	var editions []store.Edition
	for _, id := range aliases {
		if id.Kind == "sha256" {
			editions = append(editions, store.Edition{
				UserID: userID, SHA256: id.Value, WorkID: workID,
			})
		}
	}
	return proposed, editions, aliases
}
