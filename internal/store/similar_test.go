package store_test

import (
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

func TestNormalizeTitleFoldsOnlyWhatItShould(t *testing.T) {
	same := [][2]string{
		{"Dune", "DUNE"},
		{"Dune", "  dune  "},
		{"Dune!", "Dune."},
		{"Émile", "emile"},
		{"Left Hand of Darkness", "Left-Hand of Darkness"},
	}
	for _, pair := range same {
		if store.NormalizeTitle(pair[0]) != store.NormalizeTitle(pair[1]) {
			t.Errorf("%q and %q normalize apart: %q vs %q",
				pair[0], pair[1],
				store.NormalizeTitle(pair[0]), store.NormalizeTitle(pair[1]))
		}
	}
	// The rule deliberately stops short of these. An article or an
	// edition word is a difference somebody may have meant to keep, and
	// folding it away in English would mangle every other language.
	apart := [][2]string{
		{"The Tower", "Tower"},
		{"Dune", "Dune: Revised"},
		{"Dune", "Dune 2"},
	}
	for _, pair := range apart {
		if store.NormalizeTitle(pair[0]) == store.NormalizeTitle(pair[1]) {
			t.Errorf("%q and %q were folded together", pair[0], pair[1])
		}
	}
	if store.NormalizeTitle("!!!") != "" {
		t.Errorf("a title of punctuation normalized to %q, want empty",
			store.NormalizeTitle("!!!"))
	}
}

func TestGroupSimilarBooksChainsAndRefuses(t *testing.T) {
	book := func(id, title string) store.CatalogBook {
		return store.CatalogBook{ID: id, Title: title}
	}
	candidate := func(id, title, digest string, people ...string) store.SimilarityCandidate {
		return store.SimilarityCandidate{
			Book: book(id, title), Digests: []string{digest},
			ContributorIDs: people, SeriesPositions: map[string]float64{},
		}
	}

	// Three builds where the first and last share no author with each
	// other but both share one with the middle. They are one book, so
	// they must come back as one group rather than two overlapping pairs.
	chain := []store.SimilarityCandidate{
		candidate("a", "Dune", "d1", "herbert"),
		candidate("b", "dune", "d2", "herbert", "translator"),
		candidate("c", "DUNE", "d3", "translator"),
	}
	groups := store.GroupSimilarBooks(chain)
	if len(groups) != 1 || len(groups[0].Books) != 3 {
		t.Fatalf("chained books came back as %+v", groups)
	}

	// A book with no contributors at all cannot be matched. That is the
	// conservative half of the rule: nothing is known about it beyond a
	// title, and a title is not enough.
	anonymous := []store.SimilarityCandidate{
		candidate("a", "Poems", "d1"),
		candidate("b", "poems", "d2"),
	}
	if groups := store.GroupSimilarBooks(anonymous); len(groups) != 0 {
		t.Fatalf("anonymous books were grouped: %+v", groups)
	}

	// One placed volume and one unplaced book are not excluded: nobody
	// said where the second goes, so nobody said it was a different book.
	placed := candidate("a", "Chronicles", "d1", "herbert")
	placed.SeriesPositions = map[string]float64{"s": 1}
	unplaced := candidate("b", "chronicles", "d2", "herbert")
	if groups := store.GroupSimilarBooks(
		[]store.SimilarityCandidate{placed, unplaced}); len(groups) != 1 {
		t.Fatalf("an unplaced book was treated as a different volume: %+v", groups)
	}
}
