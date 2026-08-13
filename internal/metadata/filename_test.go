package metadata

import (
	"testing"
)

func TestKnownPathPatternAndDefaults(t *testing.T) {
	for _, pattern := range DefaultPathPatterns() {
		if !KnownPathPattern(pattern) {
			t.Fatalf("default pattern %q is not known", pattern)
		}
		if pattern == PatternSeriesAuthorTitle {
			t.Fatalf("ambiguous pattern %q must not be a default", pattern)
		}
	}
	if KnownPathPattern(PathPattern("author/whatever")) {
		t.Fatalf("unknown pattern reported as known")
	}
	if !KnownPathPattern(PatternSeriesAuthorTitle) {
		t.Fatalf("opt-in pattern must still be known")
	}
	DefaultPathPatterns()[0] = PatternSeriesAuthorTitle
	if DefaultPathPatterns()[0] != PatternAuthorTitle {
		t.Fatalf("DefaultPathPatterns returns shared state")
	}
}

func TestParsePath(t *testing.T) {
	all := []PathPattern{
		PatternAuthorTitle,
		PatternAuthorSeriesTitle,
		PatternFlatAuthorSeriesTitle,
	}
	tests := []struct {
		name     string
		path     string
		patterns []PathPattern
		want     PathCandidate
	}{{
		name:     "author and title folders",
		path:     "Frank Herbert/Dune.epub",
		patterns: all,
		want: PathCandidate{
			Pattern:    PatternAuthorTitle,
			Confidence: ConfidenceHigh,
			Author:     "Frank Herbert",
			Title:      "Dune",
		},
	}, {
		name:     "author series and numbered title",
		path:     "Frank Herbert/Dune/02 - Dune Messiah.epub",
		patterns: all,
		want: PathCandidate{
			Pattern:        PatternAuthorSeriesTitle,
			Confidence:     ConfidenceHigh,
			Author:         "Frank Herbert",
			Series:         "Dune",
			SeriesPosition: 2,
			HasPosition:    true,
			Title:          "Dune Messiah",
		},
	}, {
		name:     "fractional series position",
		path:     "Frank Herbert/Dune/1.5 - Interlude.epub",
		patterns: all,
		want: PathCandidate{
			Pattern:        PatternAuthorSeriesTitle,
			Confidence:     ConfidenceHigh,
			Author:         "Frank Herbert",
			Series:         "Dune",
			SeriesPosition: 1.5,
			HasPosition:    true,
			Title:          "Interlude",
		},
	}, {
		name:     "series entry without a number keeps the whole title",
		path:     "Frank Herbert/Dune/Dune Messiah.epub",
		patterns: all,
		want: PathCandidate{
			Pattern:    PatternAuthorSeriesTitle,
			Confidence: ConfidenceHigh,
			Author:     "Frank Herbert",
			Series:     "Dune",
			Title:      "Dune Messiah",
		},
	}, {
		name:     "an unparsable separator lowers confidence but keeps the title",
		path:     "Frank Herbert/Dune/Book Two - Dune Messiah.epub",
		patterns: all,
		want: PathCandidate{
			Pattern:    PatternAuthorSeriesTitle,
			Confidence: ConfidenceLow,
			Guessed:    PathFields{Title: true},
			Author:     "Frank Herbert",
			Series:     "Dune",
			Title:      "Book Two - Dune Messiah",
		},
	}, {
		name:     "flat author series and title",
		path:     "Frank Herbert - Dune 02 - Dune Messiah.epub",
		patterns: all,
		want: PathCandidate{
			Pattern:        PatternFlatAuthorSeriesTitle,
			Confidence:     ConfidenceLow,
			Guessed:        PathFields{Title: true, Series: true, Author: true},
			Author:         "Frank Herbert",
			Series:         "Dune",
			SeriesPosition: 2,
			HasPosition:    true,
			Title:          "Dune Messiah",
		},
	}, {
		name:     "flat name without a series number is not guessed",
		path:     "Frank Herbert - Dune - Dune Messiah.epub",
		patterns: all,
		want:     PathCandidate{},
	}, {
		name:     "a dangling position separator is not a title",
		path:     "Frank Herbert/Dune/02 -.epub",
		patterns: all,
		want:     PathCandidate{},
	}, {
		name:     "a name that merely ends in a hyphen is still a title",
		path:     "Frank Herbert/Dune/Dune Messiah -.epub",
		patterns: all,
		want: PathCandidate{
			Pattern:    PatternAuthorSeriesTitle,
			Confidence: ConfidenceHigh,
			Author:     "Frank Herbert",
			Series:     "Dune",
			Title:      "Dune Messiah -",
		},
	}, {
		name:     "a bare filename yields nothing",
		path:     "Dune.epub",
		patterns: all,
		want:     PathCandidate{},
	}, {
		name:     "series author title is opt-in",
		path:     "Dune/Frank Herbert - Dune Messiah.epub",
		patterns: []PathPattern{PatternSeriesAuthorTitle},
		want: PathCandidate{
			Pattern:    PatternSeriesAuthorTitle,
			Confidence: ConfidenceLow,
			Guessed:    PathFields{Title: true, Author: true},
			Series:     "Dune",
			Author:     "Frank Herbert",
			Title:      "Dune Messiah",
		},
	}, {
		name:     "pattern order resolves the depth conflict",
		path:     "Dune/Frank Herbert - Dune Messiah.epub",
		patterns: all,
		want: PathCandidate{
			Pattern:    PatternAuthorTitle,
			Confidence: ConfidenceLow,
			Guessed:    PathFields{Title: true},
			Author:     "Dune",
			Title:      "Frank Herbert - Dune Messiah",
		},
	}, {
		name:     "a separator left inside a parsed title lowers confidence",
		path:     "Frank Herbert/Dune/02 - Dune - Messiah.epub",
		patterns: all,
		want: PathCandidate{
			Pattern:        PatternAuthorSeriesTitle,
			Confidence:     ConfidenceLow,
			Guessed:        PathFields{Title: true},
			Author:         "Frank Herbert",
			Series:         "Dune",
			SeriesPosition: 2,
			HasPosition:    true,
			Title:          "Dune - Messiah",
		},
	}, {
		name:     "an ambiguous leaf is not split",
		path:     "Dune/Frank Herbert - Dune - Messiah.epub",
		patterns: []PathPattern{PatternSeriesAuthorTitle},
		want:     PathCandidate{},
	}, {
		name:     "an empty pattern set parses nothing",
		path:     "Frank Herbert/Dune.epub",
		patterns: nil,
		want:     PathCandidate{},
	}, {
		name:     "an unknown pattern is ignored",
		path:     "Frank Herbert/Dune.epub",
		patterns: []PathPattern{PathPattern("author/whatever")},
		want:     PathCandidate{},
	}, {
		name:     "a title keeps text after its last dot",
		path:     "Frank Herbert/Dune. Part Two.epub",
		patterns: all,
		want: PathCandidate{
			Pattern:    PatternAuthorTitle,
			Confidence: ConfidenceHigh,
			Author:     "Frank Herbert",
			Title:      "Dune. Part Two",
		},
	}, {
		name:     "a non-epub extension is preserved as part of the title",
		path:     "Frank Herbert/Dune.txt",
		patterns: all,
		want: PathCandidate{
			Pattern:    PatternAuthorTitle,
			Confidence: ConfidenceHigh,
			Author:     "Frank Herbert",
			Title:      "Dune.txt",
		},
	}, {
		name:     "surrounding whitespace is trimmed",
		path:     "  Frank Herbert / Dune .epub ",
		patterns: all,
		want: PathCandidate{
			Pattern:    PatternAuthorTitle,
			Confidence: ConfidenceHigh,
			Author:     "Frank Herbert",
			Title:      "Dune",
		},
	}, {
		name:     "a hyphen inside a word is not a separator",
		path:     "Frank Herbert - Sci-Fi 02 - Dune.epub",
		patterns: all,
		want: PathCandidate{
			Pattern:        PatternFlatAuthorSeriesTitle,
			Confidence:     ConfidenceLow,
			Guessed:        PathFields{Title: true, Series: true, Author: true},
			Author:         "Frank Herbert",
			Series:         "Sci-Fi",
			SeriesPosition: 2,
			HasPosition:    true,
			Title:          "Dune",
		},
	}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			want.RelativePath = tc.path
			got := ParsePath(tc.path, tc.patterns)
			if got != want {
				t.Fatalf("ParsePath(%q) = %+v, want %+v", tc.path, got, want)
			}
		})
	}
}

func TestParsePathRejectsUnusablePaths(t *testing.T) {
	paths := []string{
		"",
		"   ",
		"/Frank Herbert/Dune.epub",
		"Frank Herbert//Dune.epub",
		"../Dune.epub",
		"Frank Herbert/../Dune.epub",
		"Frank Herbert/./Dune.epub",
		`Frank Herbert\Dune.epub`,
		"Frank Herbert/.epub",
		"Frank Herbert/   .epub",
		"Frank Herbert/Dune/02 - .epub",
		"Frank Herbert/Dune/02 -   .epub",
		"Frank Herbert/Dune/1.5 - .epub",
	}
	for _, p := range paths {
		got := ParsePath(p, DefaultPathPatterns())
		want := PathCandidate{RelativePath: p}
		if got != want {
			t.Fatalf("ParsePath(%q) = %+v, want an empty candidate", p, got)
		}
	}
}

func TestParsePositionRejectsNonVolumeNumbers(t *testing.T) {
	rejected := []string{
		"", " ", "two", "2nd", "1e3", "0x10", "-1", "+1", "1_0",
		"NaN", "Inf", "1000000", "1.2.3",
	}
	for _, text := range rejected {
		if _, ok := parsePosition(text); ok {
			t.Fatalf("parsePosition(%q) was accepted", text)
		}
	}
	accepted := map[string]float64{"0": 0, "02": 2, "1.5": 1.5, "100000": 100000}
	for text, want := range accepted {
		got, ok := parsePosition(text)
		if !ok || got != want {
			t.Fatalf("parsePosition(%q) = %v, %v; want %v, true", text, got, ok, want)
		}
	}
}
