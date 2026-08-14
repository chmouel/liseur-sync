package catalog

import (
	"testing"

	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

func emptyBook() store.BookMetadata {
	return store.BookMetadata{
		Book: store.CatalogBook{
			ID: "book-1", LibraryID: "lib-1", Status: store.BookActive,
			Revision: 1,
		},
	}
}

func embeddedProposal() metadata.Proposal {
	position := 1.0
	return metadata.FromEmbedded(epub.Metadata{
		Title:       "Dune",
		Publisher:   "Chilton",
		Languages:   []string{"en"},
		Subjects:    []string{"Science Fiction"},
		Identifiers: []epub.Identifier{{Scheme: "ISBN", Value: "9780441013593"}},
		Series:      []epub.Series{{Name: "Dune", Position: &position}},
		Contributors: []epub.Contributor{
			{Name: "Frank Herbert", Role: "author"},
			{Name: "Alia Translator", Role: "translator"},
		},
	})
}

func TestResolveEmbeddedProposalOntoEmptyBook(t *testing.T) {
	resolved, changed := Resolve(emptyBook(), embeddedProposal())
	if !changed {
		t.Fatal("embedded proposal on an empty book changed nothing")
	}
	if resolved.Book.Title != "Dune" ||
		resolved.Book.TitleSource != store.MetadataEmbedded ||
		resolved.Book.TitleLocked {
		t.Fatalf("title: %+v", resolved.Book)
	}
	if len(resolved.Identifiers) != 1 ||
		resolved.Identifiers[0].Scheme != "isbn" ||
		resolved.Identifiers[0].Value != "9780441013593" {
		t.Fatalf("identifiers: %+v", resolved.Identifiers)
	}
	if len(resolved.Tags) != 1 || resolved.Tags[0].Name != "Science Fiction" ||
		resolved.Tags[0].NormalizedName != "science fiction" ||
		resolved.Tags[0].ID != EntityID("lib-1", "tag", "science fiction") {
		t.Fatalf("tags: %+v", resolved.Tags)
	}
	// A subject list mixes tags and genres, so no genre is ever invented.
	if len(resolved.Genres) != 0 {
		t.Fatalf("genres invented from subjects: %+v", resolved.Genres)
	}
	if len(resolved.Series) != 1 || resolved.Series[0].Position == nil ||
		*resolved.Series[0].Position != 1 {
		t.Fatalf("series: %+v", resolved.Series)
	}
	if len(resolved.Contributors) != 2 ||
		resolved.Contributors[0].Role != "author" ||
		resolved.Contributors[0].Position != 0 ||
		resolved.Contributors[1].Position != 1 {
		t.Fatalf("contributors: %+v", resolved.Contributors)
	}
}

func TestResolveIsIdempotent(t *testing.T) {
	once, changed := Resolve(emptyBook(), embeddedProposal())
	if !changed {
		t.Fatal("first apply changed nothing")
	}
	twice, changed := Resolve(once, embeddedProposal())
	if changed {
		t.Fatalf("re-applying the same proposal changed the book: %+v", twice)
	}
}

// A path names at most one author and knows nothing of the translators the
// file declared. Merging it as a complete assertion deleted them; see the
// "Stop path metadata deleting rows it cannot see" fix.
func TestResolvePathProposalKeepsUnseenContributors(t *testing.T) {
	current, _ := Resolve(emptyBook(), embeddedProposal())
	before := len(current.Contributors)
	if before != 2 {
		t.Fatalf("fixture contributors: %d", before)
	}
	resolved, changed := Resolve(current, metadata.FromPath(metadata.PathCandidate{
		RelativePath: "Frank Herbert/Dune.epub",
		Confidence:   metadata.ConfidenceHigh,
		Title:        "Dune",
		Author:       "Frank Herbert",
	}))
	if !changed {
		t.Fatal("path proposal took over nothing")
	}
	if len(resolved.Contributors) != before {
		t.Fatalf("path proposal dropped contributors it never saw: %+v",
			resolved.Contributors)
	}
	var author, translator store.BookContributor
	for _, row := range resolved.Contributors {
		switch row.Role {
		case "author":
			author = row
		case "translator":
			translator = row
		}
	}
	// The stronger filename source takes over the row it names and leaves
	// the rest of the set exactly as the file declared it.
	if author.Source != store.MetadataFilename {
		t.Fatalf("author not taken over: %+v", author)
	}
	if translator.Source != store.MetadataEmbedded ||
		translator.Name != "Alia Translator" {
		t.Fatalf("translator disturbed: %+v", translator)
	}
	// Entity ids of rows that already existed are reused, never regenerated.
	if author.ContributorID != current.Contributors[0].ContributorID {
		t.Fatalf("author entity id changed: %q -> %q",
			current.Contributors[0].ContributorID, author.ContributorID)
	}
}

func TestResolveRespectsLocks(t *testing.T) {
	current, _ := Resolve(emptyBook(), embeddedProposal())
	current.Book.Title = "Corrected Title"
	current.Book.TitleSource = store.MetadataManual
	current.Book.TitleLocked = true
	current.Book.SetLocks.Tags = true
	current.Tags = nil

	resolved, changed := Resolve(current, embeddedProposal())
	if changed {
		t.Fatalf("a rescan overrode manual state: %+v", resolved)
	}
	if resolved.Book.Title != "Corrected Title" || len(resolved.Tags) != 0 {
		t.Fatalf("locked state disturbed: %+v", resolved)
	}
}

func TestResolveEmptyProposalNeverClears(t *testing.T) {
	current, _ := Resolve(emptyBook(), embeddedProposal())
	resolved, changed := Resolve(current, metadata.FromEmbedded(epub.Metadata{}))
	if changed {
		t.Fatalf("an empty extraction cleared catalog data: %+v", resolved)
	}
}

func TestResolveUnparseablePathIsIgnored(t *testing.T) {
	current, _ := Resolve(emptyBook(), embeddedProposal())
	resolved, changed := Resolve(current, metadata.FromPath(metadata.PathCandidate{}))
	if changed {
		t.Fatalf("an unusable path changed the book: %+v", resolved)
	}
}

func TestResolveRefreshesItsOwnSource(t *testing.T) {
	current, _ := Resolve(emptyBook(), embeddedProposal())
	updated := embeddedProposal()
	updated.Title = metadata.Candidate{
		Value: "Dune (Revised)", Source: store.MetadataEmbedded}
	resolved, changed := Resolve(current, updated)
	if !changed || resolved.Book.Title != "Dune (Revised)" {
		t.Fatalf("re-extraction did not refresh its own value: %+v", resolved.Book)
	}
}

func TestResolveDropsRowsACompleteAssertionNoLongerNames(t *testing.T) {
	current, _ := Resolve(emptyBook(), embeddedProposal())
	shrunk := embeddedProposal()
	shrunk.Contributors = shrunk.Contributors[:1]
	resolved, changed := Resolve(current, shrunk)
	if !changed || len(resolved.Contributors) != 1 ||
		resolved.Contributors[0].Role != "author" {
		t.Fatalf("complete assertion kept a dropped row: %+v", resolved.Contributors)
	}
}

// The entity row owns an entity's display spelling: the store resolves by
// normalized name and deliberately never renames, so a read-back returns the
// library's first spelling rather than the one just proposed. Resolve must
// agree, or a book whose spelling differs from the library's is rewritten on
// every pass and never reaches a fixed point.
func TestResolveConvergesOnThePersistedSpelling(t *testing.T) {
	stored := emptyBook()
	stored.Tags = []store.BookTaxon{{
		ID:             EntityID("lib-1", "tag", "science fiction"),
		Name:           "Science Fiction",
		NormalizedName: "science fiction",
		Source:         store.MetadataEmbedded,
	}}
	stored.Contributors = []store.BookContributor{{
		ContributorID:  EntityID("lib-1", "contributor", "frank herbert"),
		Name:           "Frank Herbert",
		NormalizedName: "frank herbert",
		Role:           "author",
		Source:         store.MetadataEmbedded,
	}}
	stored.Series = []store.BookSeries{{
		SeriesID:       EntityID("lib-1", "series", "dune"),
		Name:           "Dune",
		NormalizedName: "dune",
		Source:         store.MetadataEmbedded,
	}}

	proposal := metadata.FromEmbedded(epub.Metadata{
		Subjects:     []string{"science fiction"},
		Series:       []epub.Series{{Name: "DUNE"}},
		Contributors: []epub.Contributor{{Name: "frank herbert", Role: "author"}},
	})
	resolved, changed := Resolve(stored, proposal)
	if changed {
		t.Fatalf("a spelling the library already owns was treated as news: %+v",
			resolved)
	}
}

// A path that names a series but no position within it has determined
// nothing about that position. Treating the absence as a value would erase
// the number the file itself declared.
func TestResolvePathKeepsAnUnclaimedSeriesPosition(t *testing.T) {
	position := 2.0
	stored := emptyBook()
	stored.Series = []store.BookSeries{{
		SeriesID:       EntityID("lib-1", "series", "dune"),
		Name:           "Dune",
		NormalizedName: "dune",
		Position:       &position,
		Source:         store.MetadataEmbedded,
	}}

	resolved, _ := Resolve(stored, metadata.FromPath(metadata.PathCandidate{
		Confidence: metadata.ConfidenceHigh,
		Title:      "Dune Messiah",
		Series:     "Dune",
		Author:     "Frank Herbert",
	}))
	if len(resolved.Series) != 1 {
		t.Fatalf("series: %+v", resolved.Series)
	}
	if resolved.Series[0].Position == nil || *resolved.Series[0].Position != 2 {
		t.Fatalf("path erased the position the file declared: %+v",
			resolved.Series[0])
	}
	// Provenance is row-level, so the path owns the number it carried over
	// and a later extraction can no longer correct it. Pinned so the
	// trade-off is visible if per-field provenance is ever added.
	if resolved.Series[0].Source != store.MetadataFilename {
		t.Fatalf("series source: %+v", resolved.Series[0])
	}
}

// Blanking a value is the one thing only a person can mean. A manual
// proposal that names a series without a number is saying the book has no
// number in it, so the old one must not be resurrected underneath them.
func TestResolveManualProposalClearsASeriesPosition(t *testing.T) {
	position := 2.0
	stored := emptyBook()
	stored.Series = []store.BookSeries{{
		SeriesID:       EntityID("lib-1", "series", "dune"),
		Name:           "Dune",
		NormalizedName: "dune",
		Position:       &position,
		Source:         store.MetadataEmbedded,
	}}

	resolved, changed := Resolve(stored, metadata.Proposal{
		Source: store.MetadataManual,
		Series: []metadata.Assertion[string, metadata.SeriesValue]{{
			Key:   "dune",
			Value: metadata.SeriesValue{Display: "Dune"},
		}},
	})
	if !changed {
		t.Fatal("a manual proposal clearing a position changed nothing")
	}
	if len(resolved.Series) != 1 || resolved.Series[0].Position != nil {
		t.Fatalf("manual clear was overruled: %+v", resolved.Series)
	}
}

func TestResolveClearsWhatCalibreNoLongerStates(t *testing.T) {
	t.Parallel()
	current := store.BookMetadata{
		Book: store.CatalogBook{
			ID: "book", LibraryID: "lib",
			Title:             "Small Gods",
			TitleSource:       store.MetadataCalibre,
			Subtitle:          "A Discworld Novel",
			SubtitleSource:    store.MetadataEmbedded,
			Description:       "A blurb Calibre used to have",
			DescriptionSource: store.MetadataCalibre,
			Publisher:         "Gollancz",
			PublisherSource:   store.MetadataEmbedded,
		},
		Tags: []store.BookTaxon{{
			ID: "t1", Name: "Fantasy", NormalizedName: "fantasy",
			Source: store.MetadataCalibre,
		}},
	}
	proposal := metadata.FromCalibre(calibre.Book{
		ID: 1, Title: "Small Gods", Tags: []string{"Comedy"},
	})

	next, changed := Resolve(current, proposal)
	if !changed {
		t.Fatal("a Calibre read that emptied fields changed nothing")
	}
	// Cleared, with the provenance kept, which is what stops the EPUB
	// refilling it on the next extraction.
	if next.Book.Description != "" ||
		next.Book.DescriptionSource != store.MetadataCalibre {
		t.Errorf("description = %q from %q", next.Book.Description,
			next.Book.DescriptionSource)
	}
	// The publisher was the EPUB's and Calibre outranks it, so Calibre
	// having none clears that too.
	if next.Book.Publisher != "" {
		t.Errorf("publisher = %q, want cleared", next.Book.Publisher)
	}
	// Calibre has no subtitle column, so it does not get an opinion.
	if next.Book.Subtitle != "A Discworld Novel" {
		t.Errorf("subtitle = %q, want left alone", next.Book.Subtitle)
	}
	// Its sets are complete, so a tag it no longer lists is gone.
	if len(next.Tags) != 1 || next.Tags[0].Name != "Comedy" {
		t.Errorf("tags = %+v", next.Tags)
	}

	// And the tombstone holds: the EPUB cannot put the description back.
	refilled, changed := Resolve(next, metadata.FromEmbedded(epub.Metadata{
		Title: "small gods", Description: "The blurb from the file",
	}))
	if changed && refilled.Book.Description != "" {
		t.Errorf("the EPUB refilled a description Calibre cleared: %q",
			refilled.Book.Description)
	}
}

func TestResolveKeepsAManualEditThroughACalibreRefresh(t *testing.T) {
	t.Parallel()
	current := store.BookMetadata{
		Book: store.CatalogBook{
			ID: "book", LibraryID: "lib",
			Title:       "The Title I Chose",
			TitleSource: store.MetadataManual,
			TitleLocked: true,
		},
	}
	next, _ := Resolve(current, metadata.FromCalibre(calibre.Book{
		ID: 1, Title: "The Title Calibre Has",
	}))
	if next.Book.Title != "The Title I Chose" {
		t.Fatalf("title = %q, want the manual one", next.Book.Title)
	}
}
