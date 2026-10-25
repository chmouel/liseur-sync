//go:build linux

package content

import (
	"context"
	"fmt"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

type batchingAuthorCatalog struct {
	AudiobookCatalog
	batchSizes []int
}

func (c *batchingAuthorCatalog) CatalogAuthorsForBooks(
	_ context.Context, _ string, bookIDs []string,
) (map[string][]string, error) {
	c.batchSizes = append(c.batchSizes, len(bookIDs))
	out := make(map[string][]string, len(bookIDs))
	for _, id := range bookIDs {
		out[id] = []string{"Author"}
	}
	return out, nil
}

func TestCatalogAuthorsByBookBatchesLargeCatalog(t *testing.T) {
	const books = catalogAuthorBatchSize*2 + 201
	bookIDs := make([]string, books)
	for i := range bookIDs {
		bookIDs[i] = fmt.Sprintf("book-%d", i)
	}
	cat := &batchingAuthorCatalog{}

	authors, err := catalogAuthorsByBook(context.Background(), cat, bookIDs)
	if err != nil {
		t.Fatal(err)
	}
	if len(authors) != books {
		t.Fatalf("got %d author entries, want %d", len(authors), books)
	}
	wantBatches := []int{catalogAuthorBatchSize, catalogAuthorBatchSize, 201}
	if fmt.Sprint(cat.batchSizes) != fmt.Sprint(wantBatches) {
		t.Fatalf("batch sizes = %v, want %v", cat.batchSizes, wantBatches)
	}
}

func TestIdentifierMatchRejectsConflictingCandidates(t *testing.T) {
	book := store.CatalogBook{ID: "book-1", RelativePath: "book.epub", Status: store.BookActive}
	idsByBook := map[string][]store.BookIdentifier{
		book.ID: {
			{Scheme: "isbn", Value: "9780000000001"},
			{Scheme: "asin", Value: "B000000001"},
		},
	}
	audiobooks := []store.CatalogAudiobook{
		{ID: "audio-isbn", RelativePath: "audio-isbn.m4b", Status: store.BookActive, ISBN: "9780000000001"},
		{ID: "audio-asin", RelativePath: "audio-asin.m4b", Status: store.BookActive, ASIN: "B000000001"},
	}

	pairs := matchCatalogAudiobooksToBooks([]store.CatalogBook{book}, audiobooks, nil, idsByBook)
	if len(pairs) != 0 {
		t.Fatalf("conflicting identifier candidates must not pair, got %+v", pairs)
	}
}

func TestIdentifierMatchRejectsBookWithConflictingKeys(t *testing.T) {
	books := []store.CatalogBook{
		{ID: "book-isbn", RelativePath: "isbn.epub", Status: store.BookActive},
		{ID: "book-asin", RelativePath: "asin.epub", Status: store.BookActive},
	}
	idsByBook := map[string][]store.BookIdentifier{
		books[0].ID: {{Scheme: "isbn", Value: "9780000000001"}},
		books[1].ID: {{Scheme: "asin", Value: "B000000001"}},
	}
	audiobooks := []store.CatalogAudiobook{{
		ID:           "audio-1",
		RelativePath: "audio.m4b",
		Status:       store.BookActive,
		ISBN:         "9780000000001",
		ASIN:         "B000000001",
	}}

	pairs := matchCatalogAudiobooksToBooks(books, audiobooks, nil, idsByBook)
	if len(pairs) != 0 {
		t.Fatalf("audiobook matching two books via different identifiers must not pair, got %+v", pairs)
	}
}

func TestIdentifierMatchPairsUnambiguousBookAndAudiobook(t *testing.T) {
	book := store.CatalogBook{ID: "book-1", RelativePath: "book.epub", Status: store.BookActive}
	idsByBook := map[string][]store.BookIdentifier{
		book.ID: {
			{Scheme: "isbn", Value: "9780000000001"},
			{Scheme: "asin", Value: "B000000001"},
		},
	}
	audiobooks := []store.CatalogAudiobook{{
		ID:           "audio-1",
		RelativePath: "audio.m4b",
		Status:       store.BookActive,
		ISBN:         "9780000000001",
		ASIN:         "B000000001",
	}}

	pairs := matchCatalogAudiobooksToBooks([]store.CatalogBook{book}, audiobooks, nil, idsByBook)
	if len(pairs) != 1 {
		t.Fatalf("expected one unambiguous identifier pair, got %+v", pairs)
	}
	if pairs[0].book.ID != book.ID || pairs[0].audiobook.ID != audiobooks[0].ID || pairs[0].source != "identifier" {
		t.Fatalf("unexpected pair %+v", pairs[0])
	}
}
