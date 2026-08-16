package metadata

import "strings"

// NormalizeName folds a display name to the key used for matching
// series, contributor and tag entities. Matching is case-insensitive and
// whitespace-insensitive while the original spelling stays the display
// value, so "Frank  Herbert" and "frank herbert" are one contributor.
func NormalizeName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}
