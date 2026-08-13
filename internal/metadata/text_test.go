package metadata

import "testing"

func TestNameListRoundTrips(t *testing.T) {
	entries := ParseNameList("  science fiction ,, politics,  ")
	if len(entries) != 2 ||
		entries[0].Name != "science fiction" || entries[1].Name != "politics" {
		t.Fatalf("parsed %+v", entries)
	}
	if got := FormatNameList([]string{"science fiction", "", "politics"}); got !=
		"science fiction, politics" {
		t.Fatalf("formatted %q", got)
	}
}

func TestSeriesPositionIsReadOnlyWhenItIsANumber(t *testing.T) {
	entries := ParseSeriesList("Discworld #3\nThe Culture\n\nIssue #4 Anthology")
	if len(entries) != 3 {
		t.Fatalf("parsed %+v", entries)
	}
	if entries[0].Name != "Discworld" ||
		entries[0].Position == nil || *entries[0].Position != 3 {
		t.Fatalf("positioned series: %+v", entries[0])
	}
	// No position is not position zero. Inventing one would claim the
	// book opens the series, which nobody said.
	if entries[1].Name != "The Culture" || entries[1].Position != nil {
		t.Fatalf("unpositioned series: %+v", entries[1])
	}
	// A `#` that is not followed by a number is part of the name, because
	// a title may legitimately contain one.
	if entries[2].Name != "Issue #4 Anthology" || entries[2].Position != nil {
		t.Fatalf("hash inside a name: %+v", entries[2])
	}
}

func TestSeriesFormatRoundTripsThroughTheParser(t *testing.T) {
	three, half := 3.0, 2.5
	text := FormatSeriesList([]EntryEdit{
		{Name: "Discworld", Position: &three},
		{Name: "Dune", Position: &half},
		{Name: "Standalone"},
	})
	if text != "Discworld #3\nDune #2.5\nStandalone" {
		t.Fatalf("formatted %q", text)
	}
	back := ParseSeriesList(text)
	if len(back) != 3 || *back[0].Position != 3 || *back[1].Position != 2.5 ||
		back[2].Position != nil {
		t.Fatalf("round trip lost something: %+v", back)
	}
}

func TestContributorRoleIsOptionalAndOnlyReadWhenItLooksLikeOne(t *testing.T) {
	entries := ParseContributorList(
		"Frank Herbert (author)\nBrian Herbert\nSmith (writing as Jones)\nJones (a, b)")
	if len(entries) != 4 {
		t.Fatalf("parsed %+v", entries)
	}
	if entries[0].Name != "Frank Herbert" || entries[0].Role != "author" {
		t.Fatalf("explicit role: %+v", entries[0])
	}
	// An unqualified credit means author everywhere a book is described,
	// but the default is applied by the edit rather than the parser, so
	// the parser leaves it blank.
	if entries[1].Name != "Brian Herbert" || entries[1].Role != "" {
		t.Fatalf("bare name: %+v", entries[1])
	}
	// A parenthesis is far more often part of a name than a role, so
	// anything that does not look like a single word stays in the name.
	if entries[2].Name != "Smith (writing as Jones)" || entries[2].Role != "" {
		t.Fatalf("parenthetical name: %+v", entries[2])
	}
	if entries[3].Name != "Jones (a, b)" || entries[3].Role != "" {
		t.Fatalf("comma inside parentheses: %+v", entries[3])
	}
}

func TestContributorFormatRoundTripsThroughTheParser(t *testing.T) {
	text := FormatContributorList([]EntryEdit{
		{Name: "Frank Herbert", Role: "author"},
		{Name: "Brian Herbert", Role: "editor"},
	})
	if text != "Frank Herbert (author)\nBrian Herbert (editor)" {
		t.Fatalf("formatted %q", text)
	}
	back := ParseContributorList(text)
	if len(back) != 2 || back[0].Role != "author" || back[1].Role != "editor" {
		t.Fatalf("round trip lost a role: %+v", back)
	}
}

// An emptied box is how a user empties a set, so it must parse to no
// entries rather than to one blank one.
func TestBlankTextIsAnEmptySetRatherThanOneBlankEntry(t *testing.T) {
	for _, parse := range []func(string) []EntryEdit{
		ParseNameList, ParseSeriesList, ParseContributorList,
	} {
		if got := parse("  \n \n "); len(got) != 0 {
			t.Fatalf("blank text parsed to %+v", got)
		}
	}
}
