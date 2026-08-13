package metadata

import (
	"fmt"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

func testIDs() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("new-%d", n)
	}
}

func TestManualEditLocksWhatItTouchesAndLeavesTheRestAlone(t *testing.T) {
	current := store.BookMetadata{
		Book: store.CatalogBook{
			Title: "Dune", TitleSource: store.MetadataEmbedded,
			Publisher: "Chilton", PublisherSource: store.MetadataEmbedded,
		},
	}
	next, changed := ApplyManualEdit(current, ManualEdit{
		Title: &ScalarEdit{Value: "Dune (Deluxe)"},
	}, testIDs())
	if !changed {
		t.Fatal("an edit that changed the title reported no change")
	}
	if next.Book.Title != "Dune (Deluxe)" ||
		next.Book.TitleSource != store.MetadataManual ||
		!next.Book.TitleLocked {
		t.Fatalf("edited title: %+v", next.Book)
	}
	// A field the edit said nothing about is untouched, including its
	// lock: a form that submits one field must not silently assert the
	// others.
	if next.Book.Publisher != "Chilton" ||
		next.Book.PublisherSource != store.MetadataEmbedded ||
		next.Book.PublisherLocked {
		t.Fatalf("untouched publisher moved: %+v", next.Book)
	}
}

func TestManualEditClearsAndUnlocks(t *testing.T) {
	current := store.BookMetadata{
		Book: store.CatalogBook{
			Subtitle: "A wrong subtitle", SubtitleSource: store.MetadataEmbedded,
		},
	}
	cleared, changed := ApplyManualEdit(current, ManualEdit{
		Subtitle: &ScalarEdit{Value: "   "},
	}, testIDs())
	if !changed {
		t.Fatal("clearing a field reported no change")
	}
	// A blank from a person is an assertion that the field is empty, and
	// it locks — otherwise the next rescan puts the wrong value back,
	// which is exactly what the user was trying to stop.
	if cleared.Book.Subtitle != "" || !cleared.Book.SubtitleLocked ||
		cleared.Book.SubtitleSource != store.MetadataManual {
		t.Fatalf("cleared subtitle: %+v", cleared.Book)
	}

	unlocked, changed := ApplyManualEdit(cleared, ManualEdit{
		Subtitle: &ScalarEdit{Unlock: true},
	}, testIDs())
	if !changed || unlocked.Book.SubtitleLocked {
		t.Fatalf("unlock: %+v", unlocked.Book)
	}
	// Unlocking is not an edit: it hands the field back without asserting
	// a value of its own.
	if unlocked.Book.Subtitle != "" ||
		unlocked.Book.SubtitleSource != store.MetadataManual {
		t.Fatalf("unlock changed the value: %+v", unlocked.Book)
	}
}

func TestManualEditReplacesASetWholesale(t *testing.T) {
	current := store.BookMetadata{
		Tags: []store.BookTaxon{
			{ID: "tag-sf", Name: "Science Fiction",
				NormalizedName: "science fiction", Source: store.MetadataEmbedded},
			{ID: "tag-old", Name: "Old", NormalizedName: "old",
				Source: store.MetadataEmbedded},
		},
	}
	next, changed := ApplyManualEdit(current, ManualEdit{
		Tags: &SetEdit{Entries: []EntryEdit{
			{Name: "  science   fiction "}, {Name: "Desert"},
			// A form can submit the same tag twice; the store would
			// reject a duplicate rather than tidy it.
			{Name: "DESERT"}, {Name: "   "},
		}},
	}, testIDs())
	if !changed {
		t.Fatal("replacing a set reported no change")
	}
	if len(next.Tags) != 2 {
		t.Fatalf("tags: %+v", next.Tags)
	}
	// An entry that survives an edit rejoins the entity it already had,
	// rather than asking the library for a second one under one name.
	byName := map[string]store.BookTaxon{}
	for _, tag := range next.Tags {
		byName[tag.NormalizedName] = tag
	}
	if byName["science fiction"].ID != "tag-sf" {
		t.Fatalf("a surviving tag lost its entity: %+v", next.Tags)
	}
	// The display spelling is what the user typed, trimmed but not
	// re-cased: normalization is for matching, not for correcting people.
	if byName["science fiction"].Name != "science   fiction" {
		t.Fatalf("display spelling: %q", byName["science fiction"].Name)
	}
	if byName["desert"].ID != "new-1" {
		t.Fatalf("a new tag: %+v", byName["desert"])
	}
	for _, tag := range next.Tags {
		if tag.Source != store.MetadataManual || !tag.Locked {
			t.Fatalf("edited tag is not manual and locked: %+v", tag)
		}
	}
	if !next.Book.SetLocks.Tags {
		t.Fatal("editing a set did not lock it")
	}

	// Submitting an empty set is how a user empties one, and the set lock
	// is what keeps it empty: no row survives to carry a row lock.
	emptied, changed := ApplyManualEdit(next, ManualEdit{
		Tags: &SetEdit{},
	}, testIDs())
	if !changed || len(emptied.Tags) != 0 || !emptied.Book.SetLocks.Tags {
		t.Fatalf("emptied: %+v %v", emptied.Tags, emptied.Book.SetLocks)
	}
}

func TestManualEditKeepsASeriesPositionAbsentWhenNobodyGaveOne(t *testing.T) {
	position := 2.0
	next, _ := ApplyManualEdit(store.BookMetadata{}, ManualEdit{
		Series: &SetEdit{Entries: []EntryEdit{
			{Name: "Discworld"},
			{Name: "Dune", Position: &position},
		}},
	}, testIDs())
	if len(next.Series) != 2 {
		t.Fatalf("series: %+v", next.Series)
	}
	for _, s := range next.Series {
		switch s.NormalizedName {
		case "discworld":
			// Defaulting to 1 would claim every unplaced book is the
			// first of its series.
			if s.Position != nil {
				t.Fatalf("invented a position: %+v", s)
			}
		case "dune":
			if s.Position == nil || *s.Position != position {
				t.Fatalf("lost a position: %+v", s)
			}
		}
	}
}

func TestManualEditKeepsContributorOrderAndDefaultsTheRole(t *testing.T) {
	next, _ := ApplyManualEdit(store.BookMetadata{}, ManualEdit{
		Contributors: &SetEdit{Entries: []EntryEdit{
			{Name: "Frank Herbert"},
			{Name: "Brian Herbert", Role: "  Editor "},
			// The same person in two roles is two claims, not a
			// duplicate.
			{Name: "Frank Herbert", Role: "illustrator"},
		}},
	}, testIDs())
	if len(next.Contributors) != 3 {
		t.Fatalf("contributors: %+v", next.Contributors)
	}
	// Order is a fact about the book — the first author is the first
	// author — so it survives rather than being alphabetized away.
	if next.Contributors[0].Name != "Frank Herbert" ||
		next.Contributors[0].Position != 0 {
		t.Fatalf("first contributor: %+v", next.Contributors[0])
	}
	if next.Contributors[0].Role != "author" {
		t.Fatalf("a contributor with no role given: %q", next.Contributors[0].Role)
	}
	if next.Contributors[1].Role != "editor" {
		t.Fatalf("role normalization: %q", next.Contributors[1].Role)
	}
	// One person, two roles, one entity.
	if next.Contributors[2].ContributorID != next.Contributors[0].ContributorID {
		t.Fatalf("one person became two entities: %+v", next.Contributors)
	}
}

func TestManualEditReportsNoChangeWhenNothingMoved(t *testing.T) {
	current := store.BookMetadata{
		Book: store.CatalogBook{
			Title: "Dune", TitleSource: store.MetadataManual, TitleLocked: true,
		},
	}
	if _, changed := ApplyManualEdit(current, ManualEdit{}, testIDs()); changed {
		t.Fatal("an empty edit reported a change")
	}
	if _, changed := ApplyManualEdit(current, ManualEdit{
		Title: &ScalarEdit{Value: "Dune"},
	}, testIDs()); changed {
		t.Fatal("resubmitting an identical title reported a change")
	}
}

// TestManualEditSurvivesARescan is the acceptance criterion of ADR-0004
// stated as a test: a correction must outlast the extractor that was
// wrong, or the lock is decoration.
func TestManualEditSurvivesARescan(t *testing.T) {
	edited, _ := ApplyManualEdit(store.BookMetadata{
		Book: store.CatalogBook{
			Title: "dune (retail) (v2)", TitleSource: store.MetadataFilename,
		},
	}, ManualEdit{Title: &ScalarEdit{Value: "Dune"}}, testIDs())

	after, changed := Apply(
		Field{
			Value:  edited.Book.Title,
			Source: edited.Book.TitleSource,
			Locked: edited.Book.TitleLocked,
		},
		Candidate{Value: "dune (retail) (v3)", Source: store.MetadataEmbedded})
	if changed || after.Value != "Dune" {
		t.Fatalf("a rescan overwrote a manual correction: %+v", after)
	}
}
