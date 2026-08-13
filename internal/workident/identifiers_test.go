package workident

import (
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestCatalogBookIdentifiersIgnoresUnavailableFiles: an unavailable file's
// digest is a claim about bytes the server cannot produce. Registering it
// as an alias would point the reader's work graph at nothing.
func TestCatalogBookIdentifiersIgnoresUnavailableFiles(t *testing.T) {
	partial, dc := "aabbcc", "urn:isbn:9780000000001"
	meta := store.BookMetadata{
		Book:         store.CatalogBook{ID: "b1", Title: "  The   Title "},
		Contributors: []store.BookContributor{{Name: "Ada Author", Role: "author"}},
	}
	files := []store.BookFile{
		{BlobSHA256: "gone", Availability: store.BookFileMissing},
		{
			BlobSHA256: "here", PartialMD5: &partial, DCIdentifier: &dc,
			Availability: store.BookFileAvailable,
		},
	}
	got := ForCatalogBook(meta, files)
	want := []store.Identifier{
		{Kind: "sha256", Value: "here"},
		{Kind: "partial-md5", Value: partial},
		{Kind: "dc", Value: dc},
		{Kind: "ta", Value: "the title|ada author"},
	}
	if len(got) != len(want) {
		t.Fatalf("identifiers = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("identifier %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestCatalogBookIdentifiersOrdersAndDeduplicates: the files loop emits
// each file's identifiers together, so two files leave the list interleaved
// by file rather than ordered by strength. Resolution is strongest-first,
// and a duplicated book contributes the same digest twice.
func TestCatalogBookIdentifiersOrdersAndDeduplicates(t *testing.T) {
	md5a, md5b := "aaa111", "bbb222"
	dcA, dcB := "urn:isbn:1", "urn:isbn:2"
	meta := store.BookMetadata{Book: store.CatalogBook{ID: "b1", Title: "T"}}
	files := []store.BookFile{
		{
			BlobSHA256: "sha-a", PartialMD5: &md5a, DCIdentifier: &dcA,
			Availability: store.BookFileAvailable,
		},
		{
			BlobSHA256: "sha-b", PartialMD5: &md5b, DCIdentifier: &dcB,
			Availability: store.BookFileAvailable,
		},
		// The same file catalogued twice must not say so twice.
		{
			BlobSHA256: "sha-a", PartialMD5: &md5a, DCIdentifier: &dcA,
			Availability: store.BookFileAvailable,
		},
		// A file the catalog holds no digest for contributes nothing.
		// "sha256:" would otherwise alias every such book to one work.
		{BlobSHA256: "", Availability: store.BookFileAvailable},
	}
	got := ForCatalogBook(meta, files)
	want := []store.Identifier{
		{Kind: "sha256", Value: "sha-a"},
		{Kind: "sha256", Value: "sha-b"},
		{Kind: "partial-md5", Value: md5a},
		{Kind: "partial-md5", Value: md5b},
		{Kind: "dc", Value: dcA},
		{Kind: "dc", Value: dcB},
		{Kind: "ta", Value: "t|"},
	}
	if len(got) != len(want) {
		t.Fatalf("identifiers = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("identifier %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestTitleAuthorFingerprintNeedsATitle: an untitled book would otherwise
// fingerprint as "|author" and collide with every other untitled book by
// that author.
func TestTitleAuthorFingerprintNeedsATitle(t *testing.T) {
	meta := store.BookMetadata{
		Contributors: []store.BookContributor{{Name: "Ada", Role: "author"}},
	}
	if got := TitleAuthorFingerprint(meta); got != "" {
		t.Fatalf("fingerprint without a title = %q, want empty", got)
	}
	meta.Book.Title = "Title"
	if got := TitleAuthorFingerprint(meta); got != "title|ada" {
		t.Fatalf("fingerprint = %q", got)
	}
}

// TestFirstAuthorSkipsOtherRoles: a translator is not the author, and
// folding one into the fingerprint would split a work across editions.
func TestFirstAuthorSkipsOtherRoles(t *testing.T) {
	meta := store.BookMetadata{Contributors: []store.BookContributor{
		{Name: "Tara Translator", Role: "translator"},
		{Name: "Ada Author", Role: "author"},
		{Name: "Bea Author", Role: "author"},
	}}
	if got := FirstAuthor(meta); got != "Ada Author" {
		t.Fatalf("firstAuthor = %q, want %q", got, "Ada Author")
	}
	if got := FirstAuthor(store.BookMetadata{}); got != "" {
		t.Fatalf("firstAuthor with no contributors = %q", got)
	}
}
