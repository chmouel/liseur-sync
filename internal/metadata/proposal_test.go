package metadata

import (
	"testing"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

func position(value float64) *float64 { return &value }

func TestFromEmbedded(t *testing.T) {
	got := FromEmbedded(epub.Metadata{
		Title:         "Dune",
		Subtitle:      "  ",
		Description:   "A desert planet.",
		Publisher:     "Chilton",
		PublishedDate: "1965-08-01",
		Identifiers: []epub.Identifier{
			{Scheme: "ISBN", Value: "9780441013593"},
			{Scheme: "", Value: "  "},
			{Scheme: "UUID", Value: " abc "},
		},
		Languages: []string{"EN", " ", "fr-CA"},
		Subjects:  []string{"Science Fiction", " ", "  classic  "},
		Series: []epub.Series{
			{Name: "Dune", Position: position(1)},
			{Name: " ", Position: position(2)},
			{Name: "Hainish"},
		},
		Contributors: []epub.Contributor{
			{Name: "Frank Herbert", Role: "Author"},
			{Name: " ", Role: "author"},
			{Name: "Someone", Role: " "},
		},
		CoverPath: "cover.jpg",
	})

	if got.Source != store.MetadataEmbedded || got.Confidence != ConfidenceHigh {
		t.Fatalf("source/confidence = %q/%q", got.Source, got.Confidence)
	}
	if got.Title.Value != "Dune" || got.Title.Source != store.MetadataEmbedded {
		t.Fatalf("title = %+v", got.Title)
	}
	if got.Description.Value != "A desert planet." ||
		got.Publisher.Value != "Chilton" ||
		got.PublishedDate.Value != "1965-08-01" {
		t.Fatalf("scalar fields = %+v", got)
	}

	wantIdentifiers := []Assertion[IdentifierKey, struct{}]{
		{Key: IdentifierKey{Scheme: "isbn", Value: "9780441013593"}},
		{Key: IdentifierKey{Scheme: "uuid", Value: "abc"}},
	}
	assertAssertions(t, "identifiers", got.Identifiers, wantIdentifiers)

	assertAssertions(t, "languages", got.Languages,
		[]Assertion[string, string]{
			{Key: "en", Value: "EN"},
			{Key: "fr-ca", Value: "fr-CA"},
		})
	assertAssertions(t, "tags", got.Tags,
		[]Assertion[string, string]{
			{Key: "science fiction", Value: "Science Fiction"},
			{Key: "classic", Value: "classic"},
		})
	assertAssertions(t, "series", got.Series,
		[]Assertion[string, SeriesValue]{
			{Key: "dune", Value: SeriesValue{
				Display: "Dune", Position: 1, HasPosition: true}},
			{Key: "hainish", Value: SeriesValue{Display: "Hainish"}},
		})
	assertAssertions(t, "contributors", got.Contributors,
		[]Assertion[ContributorKey, string]{
			{Key: ContributorKey{Name: "frank herbert", Role: "author"},
				Value: "Frank Herbert"},
		})
	if got.Subtitle.Value != "  " {
		t.Fatalf("subtitle should pass through untouched: %q", got.Subtitle.Value)
	}
	if _, changed := Apply(Field{}, got.Subtitle); changed {
		t.Fatalf("a blank subtitle must not reach the catalog")
	}
}

func TestFromEmbeddedEmptyMetadata(t *testing.T) {
	got := FromEmbedded(epub.Metadata{})
	if got.Identifiers != nil || got.Languages != nil || got.Tags != nil ||
		got.Series != nil || got.Contributors != nil {
		t.Fatalf("empty metadata produced assertions: %+v", got)
	}
	current := []SetEntry[string, string]{
		{Key: "classic", Value: "classic", Source: store.MetadataEmbedded}}
	merged, changed := MergeSet(current, got.Tags, got.Source, false)
	if changed || len(merged) != 1 {
		t.Fatalf("an empty extraction stripped existing tags: %+v", merged)
	}
}

func TestFromPath(t *testing.T) {
	parsed := ParsePath("Frank Herbert/Dune/02 - Dune Messiah.epub",
		DefaultPathPatterns())
	got := FromPath(parsed)
	if got.Source != store.MetadataFilename ||
		got.Confidence != ConfidenceHigh {
		t.Fatalf("source/confidence = %q/%q", got.Source, got.Confidence)
	}
	if got.Title.Value != "Dune Messiah" ||
		got.Title.Source != store.MetadataFilename {
		t.Fatalf("title = %+v", got.Title)
	}
	if got.Description.Value != "" || got.Publisher.Value != "" ||
		got.PublishedDate.Value != "" || got.Subtitle.Value != "" {
		t.Fatalf("a path invented fields it cannot know: %+v", got)
	}
	assertAssertions(t, "series", got.Series,
		[]Assertion[string, SeriesValue]{
			{Key: "dune", Value: SeriesValue{
				Display: "Dune", Position: 2, HasPosition: true}},
		})
	assertAssertions(t, "contributors", got.Contributors,
		[]Assertion[ContributorKey, string]{
			{Key: ContributorKey{Name: "frank herbert", Role: "author"},
				Value: "Frank Herbert"},
		})
	if got.Identifiers != nil || got.Languages != nil || got.Tags != nil {
		t.Fatalf("a path produced unrelated assertions: %+v", got)
	}
	if !got.PartialSets {
		t.Fatalf("a path proposal must be partial: %+v", got)
	}
}

// TestFromPathKeepsUnrelatedEmbeddedRows pins the reason a path proposal is
// partial: merging it as a complete assertion would delete every embedded
// contributor and series the path could not name.
func TestFromPathKeepsUnrelatedEmbeddedRows(t *testing.T) {
	embedded := FromEmbedded(epub.Metadata{
		Contributors: []epub.Contributor{
			{Name: "Frank Herbert", Role: "author"},
			{Name: "Jane Doe", Role: "translator"},
		},
		Series: []epub.Series{{Name: "Dune"}, {Name: "Omnibus"}},
	})
	if embedded.PartialSets {
		t.Fatalf("an extraction reads the whole publication and is complete")
	}
	var contributors []SetEntry[ContributorKey, string]
	contributors, _ = MergeSet(
		contributors, embedded.Contributors, embedded.Source, false)
	var series []SetEntry[string, SeriesValue]
	series, _ = MergeSet(series, embedded.Series, embedded.Source, false)

	fromPath := FromPath(ParsePath(
		"Frank Herbert/Omnibus/02 - Dune Messiah.epub", DefaultPathPatterns()))
	mergedContributors, changed := MergeEntries(
		contributors, fromPath.Contributors, fromPath.Source, false)
	if !changed || len(mergedContributors) != 2 {
		t.Fatalf("a path deleted contributors it never saw: %+v",
			mergedContributors)
	}
	if mergedContributors[0].Source != store.MetadataFilename ||
		mergedContributors[1].Source != store.MetadataEmbedded {
		t.Fatalf("the path should own only the author: %+v", mergedContributors)
	}
	mergedSeries, changed := MergeEntries(
		series, fromPath.Series, fromPath.Source, false)
	if !changed || len(mergedSeries) != 2 {
		t.Fatalf("a path deleted series it never saw: %+v", mergedSeries)
	}
	if mergedSeries[1].Value.Position != 2 || !mergedSeries[1].Value.HasPosition {
		t.Fatalf("the path should supply the omnibus position: %+v", mergedSeries)
	}

	dropped, _ := MergeSet(contributors, fromPath.Contributors,
		fromPath.Source, false)
	if len(dropped) != 1 {
		t.Fatalf("a complete merge of a partial proposal is expected to " +
			"delete rows, which is why PartialSets exists")
	}
}

func TestFromPathUnparsable(t *testing.T) {
	got := FromPath(ParsePath("Dune.epub", DefaultPathPatterns()))
	if got.Confidence != ConfidenceNone || got.Title.Value != "" ||
		got.Series != nil || got.Contributors != nil {
		t.Fatalf("an unparsable path produced a proposal: %+v", got)
	}
	if _, changed := Apply(
		Field{Value: "Dune", Source: store.MetadataEmbedded}, got.Title,
	); changed {
		t.Fatalf("an unparsable path overwrote an embedded title")
	}
}

func TestEmbeddedThenFilenamePrecedence(t *testing.T) {
	embedded := FromEmbedded(epub.Metadata{
		Title: "dune messiah", Series: []epub.Series{{Name: "Dune"}}})
	fromPath := FromPath(ParsePath(
		"Frank Herbert/Dune/02 - Dune Messiah.epub", DefaultPathPatterns()))

	title, _ := Apply(Field{}, embedded.Title)
	title, changed := Apply(title, fromPath.Title)
	if !changed || title.Value != "Dune Messiah" ||
		title.Source != store.MetadataFilename {
		t.Fatalf("filename should outrank embedded: %+v", title)
	}

	var series []SetEntry[string, SeriesValue]
	series, _ = MergeSet(series, embedded.Series, embedded.Source, false)
	if len(series) != 1 || series[0].Value.HasPosition {
		t.Fatalf("embedded series = %+v", series)
	}
	series, changed = MergeSet(series, fromPath.Series, fromPath.Source, false)
	if !changed || len(series) != 1 || series[0].Source != store.MetadataFilename ||
		series[0].Value.Position != 2 || !series[0].Value.HasPosition {
		t.Fatalf("filename should supply the missing position: %+v", series)
	}
}

func assertAssertions[K comparable, V comparable](
	t *testing.T, name string, got, want []Assertion[K, V],
) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %+v, want %+v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %+v, want %+v (full %+v)",
				name, i, got[i], want[i], got)
		}
	}
}
