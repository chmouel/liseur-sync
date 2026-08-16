package workident

import (
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestForCatalogBookOrdersAndDeduplicates: a book contributes its
// identifiers in the order they were recorded, which is not the order
// resolution walks them in. The list has to come back strongest first,
// and a repeated Dublin Core identifier must not be registered twice.
func TestForCatalogBookOrdersAndDeduplicates(t *testing.T) {
	book := store.CatalogBook{
		ID: "b1", Title: "  The   Title ", Status: store.BookActive,
		ContentSHA256: "here",
	}
	ids := []store.BookIdentifier{
		{Scheme: "isbn", Value: "urn:isbn:9780000000001"},
		{Scheme: "isbn", Value: "urn:isbn:9780000000001"},
		{Scheme: "uuid", Value: "urn:uuid:abc"},
	}
	got := ForCatalogBook(book, ids, "Ada Author")
	want := []store.Identifier{
		{Kind: "sha256", Value: "here"},
		{Kind: "dc", Value: "urn:isbn:9780000000001"},
		{Kind: "dc", Value: "urn:uuid:abc"},
		{Kind: "ta", Value: "the title|ada author"},
	}
	assertIdentifiers(t, got, want)
}

// TestForCatalogBookIgnoresAMissingFile: a missing file's digest is a
// claim about bytes the server cannot produce. Registering it as an
// alias would point the reader's work graph at nothing.
func TestForCatalogBookIgnoresAMissingFile(t *testing.T) {
	book := store.CatalogBook{
		ID: "b1", Title: "Title", Status: store.BookMissing,
		ContentSHA256: "gone",
	}
	got := ForCatalogBook(book, nil, "Ada")
	assertIdentifiers(t, got, []store.Identifier{
		{Kind: "ta", Value: "title|ada"},
	})
}

// TestForCatalogBookDropsEmptyValues: a book the catalog holds no digest
// for must not contribute "sha256:", which would otherwise alias every
// such book to a single work.
func TestForCatalogBookDropsEmptyValues(t *testing.T) {
	book := store.CatalogBook{ID: "b1", Title: "T", Status: store.BookActive}
	got := ForCatalogBook(book, []store.BookIdentifier{{Scheme: "isbn"}}, "")
	assertIdentifiers(t, got, []store.Identifier{{Kind: "ta", Value: "t|"}})
}

// TestTitleAuthorFingerprintNeedsATitle: an untitled book would otherwise
// fingerprint as "|author" and collide with every other untitled book by
// that author.
func TestTitleAuthorFingerprintNeedsATitle(t *testing.T) {
	if got := TitleAuthorFingerprint("", "Ada"); got != "" {
		t.Fatalf("fingerprint without a title = %q, want empty", got)
	}
	if got := TitleAuthorFingerprint("Title", "Ada"); got != "title|ada" {
		t.Fatalf("fingerprint = %q", got)
	}
}

// TestPlanProposesAnEditionPerDigest: the work is only half of what the
// store needs; without an edition row the digest identifies nothing on
// the next device to present the same file.
func TestPlanProposesAnEditionPerDigest(t *testing.T) {
	book := store.CatalogBook{
		ID: "b1", Title: "Dune", Status: store.BookActive,
		ContentSHA256: "digest",
	}
	work, editions, aliases := Plan("u1", "w1", book, nil, "Frank Herbert")
	if work.ID != "w1" || work.UserID != "u1" || work.Title != "Dune" ||
		work.Author != "Frank Herbert" {
		t.Fatalf("work = %+v", work)
	}
	if len(editions) != 1 || editions[0].SHA256 != "digest" ||
		editions[0].WorkID != "w1" || editions[0].UserID != "u1" {
		t.Fatalf("editions = %+v", editions)
	}
	assertIdentifiers(t, aliases, []store.Identifier{
		{Kind: "sha256", Value: "digest"},
		{Kind: "ta", Value: "dune|frank herbert"},
	})
}

func assertIdentifiers(t *testing.T, got, want []store.Identifier) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("identifiers = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("identifier %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
