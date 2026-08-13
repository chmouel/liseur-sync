package metadata

import (
	"strconv"
	"strings"
)

// The text forms below exist because a metadata form is typed, not
// clicked. They are deliberately simple and lossy in one direction only:
// anything they cannot express is a name the parser leaves alone rather
// than a name it mangles.

// ParseNameList reads a comma-separated list of names, the form used for
// tags, genres and languages. A name containing a comma cannot be
// written here, which is a real limit and the reason series and
// contributors — where punctuation is common — are one per line instead.
func ParseNameList(raw string) []EntryEdit {
	var out []EntryEdit
	for _, part := range strings.Split(raw, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, EntryEdit{Name: name})
		}
	}
	return out
}

// FormatNameList writes what ParseNameList reads.
func FormatNameList(names []string) string {
	kept := make([]string, 0, len(names))
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, ", ")
}

// ParseSeriesList reads one series per line, with an optional position
// after a `#`:
//
//	Discworld #3
//	The Culture
//
// A line with no `#` has no position, and none is invented: an unplaced
// book is an unanswered question rather than book zero. A `#` followed by
// something that is not a number is left as part of the name, because a
// title may legitimately contain one.
func ParseSeriesList(raw string) []EntryEdit {
	var out []EntryEdit
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entry := EntryEdit{Name: line}
		if hash := strings.LastIndex(line, "#"); hash >= 0 {
			tail := strings.TrimSpace(line[hash+1:])
			if position, err := strconv.ParseFloat(tail, 64); err == nil {
				if name := strings.TrimSpace(line[:hash]); name != "" {
					entry = EntryEdit{Name: name, Position: &position}
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

// FormatSeriesList writes what ParseSeriesList reads. A position is
// printed with the shortest representation that round-trips, so a whole
// number reads as `#3` rather than `#3.000000`.
func FormatSeriesList(entries []EntryEdit) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		if entry.Position != nil {
			name += " #" + strconv.FormatFloat(*entry.Position, 'f', -1, 64)
		}
		lines = append(lines, name)
	}
	return strings.Join(lines, "\n")
}

// ParseContributorList reads one contributor per line, with an optional
// role in parentheses:
//
//	Frank Herbert (author)
//	Brian Herbert (editor)
//
// A line with no role means author, because that is what an unqualified
// credit means everywhere a book is described. Parentheses that do not
// close, or that hold something with a comma in it, stay part of the
// name: "Smith (writing as Jones)" is a name, not a role.
func ParseContributorList(raw string) []EntryEdit {
	var out []EntryEdit
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entry := EntryEdit{Name: line}
		if strings.HasSuffix(line, ")") {
			if open := strings.LastIndex(line, "("); open > 0 {
				role := strings.TrimSpace(line[open+1 : len(line)-1])
				name := strings.TrimSpace(line[:open])
				if name != "" && role != "" && !strings.ContainsAny(role, ",()") &&
					len(strings.Fields(role)) == 1 {
					entry = EntryEdit{Name: name, Role: role}
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

// FormatContributorList writes what ParseContributorList reads. The role
// is printed even when it is `author`, so that the round trip is visible
// rather than something the user has to know about.
func FormatContributorList(entries []EntryEdit) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		if role := strings.TrimSpace(entry.Role); role != "" {
			name += " (" + role + ")"
		}
		lines = append(lines, name)
	}
	return strings.Join(lines, "\n")
}
