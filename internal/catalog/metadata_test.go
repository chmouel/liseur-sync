package catalog

import (
	"testing"

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
