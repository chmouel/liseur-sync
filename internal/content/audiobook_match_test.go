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

// An audiobook named like its book, beside it, pairs with that book even
// at a folder root where the dedicated-directory rule cannot apply, and
// even when neither title nor identifier would have said so.
func TestFilenameMatchPairsAnAudiobookBesideItsBook(t *testing.T) {
	books := []store.CatalogBook{
		{ID: "dune", FolderID: "f", RelativePath: "Frank Herbert - Dune.epub", Status: store.BookActive},
		{ID: "other", FolderID: "f", RelativePath: "Other.epub", Status: store.BookActive},
	}
	audiobooks := []store.CatalogAudiobook{
		{ID: "a", FolderID: "f", RelativePath: "Frank Herbert - Dune.m4b", Status: store.BookActive, Title: "Something Else"},
		// The same name in another folder is a different file.
		{ID: "b", FolderID: "g", RelativePath: "Other.m4b", Status: store.BookActive},
	}
	pairs := matchCatalogAudiobooksToBooks(books, audiobooks, nil, nil)
	if len(pairs) != 1 || pairs[0].book.ID != "dune" || pairs[0].audiobook.ID != "a" ||
		pairs[0].source != "filename" {
		t.Fatalf("pairs = %+v, want dune with a by filename", pairs)
	}
}

// Two books sharing a name (an EPUB and a PDF) make the name ambiguous,
// and an ambiguous name pairs nothing.
func TestFilenameMatchSkipsAnAmbiguousName(t *testing.T) {
	books := []store.CatalogBook{
		{ID: "epub", FolderID: "f", RelativePath: "dir/Dune.epub", Status: store.BookActive},
		{ID: "pdf", FolderID: "f", RelativePath: "dir/Dune.pdf", Status: store.BookActive},
	}
	audiobooks := []store.CatalogAudiobook{
		{ID: "a", FolderID: "f", RelativePath: "dir/Dune.m4b", Status: store.BookActive},
	}
	if pairs := matchCatalogAudiobooksToBooks(books, audiobooks, nil, nil); len(pairs) != 0 {
		t.Fatalf("pairs = %+v, want none", pairs)
	}
}
