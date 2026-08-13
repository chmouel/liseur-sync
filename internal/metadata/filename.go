package metadata

import (
	"math"
	"path"
	"strconv"
	"strings"
)

// PathPattern names one conservative library layout the filename parser can
// recognize. Patterns are enabled per library because several of them claim
// the same directory depth and only the operator knows which one their
// library actually uses.
type PathPattern string

const (
	// PatternAuthorTitle reads "Author/Title.epub".
	PatternAuthorTitle PathPattern = "author/title"
	// PatternAuthorSeriesTitle reads "Author/Series/02 - Title.epub".
	PatternAuthorSeriesTitle PathPattern = "author/series/title"
	// PatternSeriesAuthorTitle reads "Series/Author - Title.epub". It claims
	// the same depth as PatternAuthorTitle, so it is off by default.
	PatternSeriesAuthorTitle PathPattern = "series/author-title"
	// PatternFlatAuthorSeriesTitle reads "Author - Series 02 - Title.epub".
	PatternFlatAuthorSeriesTitle PathPattern = "author-series-title"
)

// KnownPathPattern reports whether pattern is a recognized layout.
func KnownPathPattern(pattern PathPattern) bool {
	switch pattern {
	case PatternAuthorTitle, PatternAuthorSeriesTitle,
		PatternSeriesAuthorTitle, PatternFlatAuthorSeriesTitle:
		return true
	default:
		return false
	}
}

// DefaultPathPatterns is the conservative default layout set. It omits
// PatternSeriesAuthorTitle because that pattern would otherwise silently
// reinterpret every "Author/Title.epub" library as a series library.
func DefaultPathPatterns() []PathPattern {
	return []PathPattern{
		PatternAuthorTitle,
		PatternAuthorSeriesTitle,
		PatternFlatAuthorSeriesTitle,
	}
}

// Confidence grades how much structure a parsed candidate relied on.
type Confidence string

const (
	// ConfidenceNone means nothing could be parsed.
	ConfidenceNone Confidence = ""
	// ConfidenceLow means at least one value came from splitting a single
	// name on " - ", a separator that also occurs inside real titles.
	ConfidenceLow Confidence = "low"
	// ConfidenceHigh means every value came from a directory boundary.
	ConfidenceHigh Confidence = "high"
)

// PathCandidate is what one library layout claims about a file. Every field
// is optional: a value the layout could not determine unambiguously is left
// unset rather than guessed. RelativePath is always the untouched input, so
// the original is never discarded.
type PathCandidate struct {
	RelativePath   string
	Pattern        PathPattern
	Confidence     Confidence
	Title          string
	Series         string
	SeriesPosition float64
	HasPosition    bool
	Author         string
}

// ParsePath applies the first enabled pattern that matches the shape of
// relative, a slash-separated path below the library root, and returns what
// it could determine. Patterns are tried in the given order, so an operator
// resolves the layouts that claim the same depth by ordering them. An
// unusable path or an empty pattern set yields an empty candidate.
func ParsePath(relative string, patterns []PathPattern) PathCandidate {
	candidate := PathCandidate{RelativePath: relative}
	parts, ok := splitRelative(relative)
	if !ok {
		return candidate
	}
	leaf := strings.TrimSpace(trimContentExt(parts[len(parts)-1]))
	if leaf == "" {
		return candidate
	}
	for _, pattern := range patterns {
		parsed, matched := applyPathPattern(pattern, parts, leaf)
		if !matched {
			continue
		}
		parsed.RelativePath = relative
		parsed.Pattern = pattern
		return parsed
	}
	return candidate
}

func applyPathPattern(
	pattern PathPattern, parts []string, leaf string,
) (PathCandidate, bool) {
	var candidate PathCandidate
	switch pattern {
	case PatternAuthorTitle:
		if len(parts) != 2 {
			return candidate, false
		}
		candidate.Author = parts[0]
		candidate.Title = leaf
		candidate.Confidence = ConfidenceHigh
	case PatternAuthorSeriesTitle:
		if len(parts) != 3 {
			return candidate, false
		}
		candidate.Author = parts[0]
		candidate.Series = parts[1]
		candidate.Confidence = ConfidenceHigh
		title, position, hasPosition, exact := splitPositionPrefix(leaf)
		candidate.Title = title
		candidate.SeriesPosition = position
		candidate.HasPosition = hasPosition
		if !exact {
			candidate.Confidence = ConfidenceLow
		}
	case PatternSeriesAuthorTitle:
		if len(parts) != 2 {
			return candidate, false
		}
		fields := splitNameSeparator(leaf)
		if len(fields) != 2 {
			return candidate, false
		}
		candidate.Series = parts[0]
		candidate.Author = fields[0]
		candidate.Title = fields[1]
		candidate.Confidence = ConfidenceLow
	case PatternFlatAuthorSeriesTitle:
		if len(parts) != 1 {
			return candidate, false
		}
		fields := splitNameSeparator(leaf)
		if len(fields) != 3 {
			return candidate, false
		}
		series, position, ok := splitPositionSuffix(fields[1])
		if !ok {
			return candidate, false
		}
		candidate.Author = fields[0]
		candidate.Series = series
		candidate.SeriesPosition = position
		candidate.HasPosition = true
		candidate.Title = fields[2]
		candidate.Confidence = ConfidenceLow
	default:
		return candidate, false
	}
	if candidate.Title == "" {
		return PathCandidate{}, false
	}
	return candidate, true
}

// trimContentExt removes only a recognized content extension, so a title
// such as "Dune. Part Two" keeps everything after its last dot.
func trimContentExt(name string) string {
	ext := path.Ext(name)
	if strings.EqualFold(ext, ".epub") {
		return name[:len(name)-len(ext)]
	}
	return name
}

// splitRelative rejects anything that is not a clean relative path with
// non-empty components, so a traversal or an absolute path never reaches a
// pattern.
func splitRelative(relative string) ([]string, bool) {
	trimmed := strings.TrimSpace(relative)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") ||
		strings.Contains(trimmed, `\`) {
		return nil, false
	}
	parts := strings.Split(trimmed, "/")
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			return nil, false
		}
		parts[i] = part
	}
	return parts, true
}

// splitNameSeparator splits on " - ", the only separator these layouts use.
// Surrounding whitespace variations are tolerated but a hyphen inside a word
// such as "sci-fi" is not a separator.
func splitNameSeparator(name string) []string {
	fields := strings.Split(name, " - ")
	for i, field := range fields {
		fields[i] = strings.TrimSpace(field)
		if fields[i] == "" {
			return nil
		}
	}
	return fields
}

// splitPositionPrefix reads a leading "02 - " series position. exact reports
// whether the whole leaf was consumed by the numeric prefix rule; a leaf
// without one keeps its full text as the title.
func splitPositionPrefix(leaf string) (
	title string, position float64, hasPosition, exact bool,
) {
	index := strings.Index(leaf, " - ")
	if index < 0 {
		return leaf, 0, false, true
	}
	number, ok := parsePosition(leaf[:index])
	if !ok {
		return leaf, 0, false, false
	}
	return strings.TrimSpace(leaf[index+len(" - "):]), number, true, true
}

// splitPositionSuffix reads a trailing "Series 02" position.
func splitPositionSuffix(name string) (string, float64, bool) {
	index := strings.LastIndex(name, " ")
	if index <= 0 {
		return "", 0, false
	}
	number, ok := parsePosition(name[index+1:])
	if !ok {
		return "", 0, false
	}
	series := strings.TrimSpace(name[:index])
	if series == "" {
		return "", 0, false
	}
	return series, number, true
}

// parsePosition accepts only a plain decimal volume number, so a year or a
// subtitle is never mistaken for a series position.
func parsePosition(text string) (float64, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.ContainsAny(trimmed, "+-eExXpP_") {
		return 0, false
	}
	number, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) ||
		number < 0 || number > 100000 {
		return 0, false
	}
	return number, true
}
