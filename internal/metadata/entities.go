package metadata

import "strings"

// NormalizeName folds a display name to the key used for matching
// series, contributor and tag entities. Matching is case-insensitive and
// whitespace-insensitive while the original spelling stays the display
// value, so "Frank  Herbert" and "frank herbert" are one contributor.
func NormalizeName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// TitleAuthorFingerprint is the fuzzy alias two devices can compute for
// the same book without having the same bytes. It lives here, beside the
// fold it is built from, because both the work-identity layer and the
// catalog pass have to produce exactly the same string: one that folded
// differently would never meet the other, and the reader's position
// would stop following them between devices.
func TitleAuthorFingerprint(title, author string) string {
	folded := NormalizeName(title)
	if folded == "" {
		return ""
	}
	return folded + "|" + NormalizeName(author)
}
