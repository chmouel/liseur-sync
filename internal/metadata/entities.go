package metadata

import (
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

// NormalizeName folds a display name to the key used for matching series,
// contributor, tag and genre entities. Matching is case-insensitive and
// whitespace-insensitive while the original spelling stays the display
// value, so "Frank  Herbert" and "frank herbert" are one contributor.
func NormalizeName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// SetEntry is one persisted row of a multi-valued metadata set, such as a
// tag, an identifier, or a series membership. Key is the row's identity and
// Value carries any additional payload, such as a series position.
type SetEntry[K comparable, V comparable] struct {
	Key    K
	Value  V
	Source store.MetadataSource
	Locked bool
}

// Assertion is one member of a set proposed by a single source.
type Assertion[K comparable, V comparable] struct {
	Key   K
	Value V
}

// MergeSet resolves a complete set assertion from one source against the
// currently persisted rows. Unlike a scalar field, a source asserts the
// whole set at once, so entries it no longer lists are removed. The rules
// mirror Apply:
//
//   - An empty assertion or an unknown source is ignored. A source that
//     found nothing is treated as having no opinion, never as a request to
//     empty the set, so a failed parse cannot strip a book's tags.
//   - A set-level manual lock rejects the assertion outright. Removing a row
//     leaves nothing behind to carry a row lock, so this is what makes a
//     user's deliberate emptying of a set survive later rescans.
//   - Locked rows are always kept exactly as they are.
//   - Rows owned by a strictly stronger source are kept, and an assertion
//     that repeats their key does not downgrade them.
//   - Unlocked rows owned by this source or a weaker one are removed when
//     the assertion omits them, and are taken over by this source, payload
//     included, when it repeats them.
//
// The result keeps surviving rows in their original order and appends newly
// asserted ones in assertion order, so repeated merges are stable.
func MergeSet[K comparable, V comparable](
	current []SetEntry[K, V],
	incoming []Assertion[K, V],
	source store.MetadataSource,
	setLocked bool,
) ([]SetEntry[K, V], bool) {
	return mergeEntries(current, incoming, source, setLocked, true)
}

// MergeEntries resolves a partial assertion: a source that knows about some
// rows but cannot see the rest. It adds and takes over the rows it names by
// the same precedence rules as MergeSet, and removes nothing. A library path
// naming one author is the motivating case, since it says nothing about the
// translators and illustrators the file itself declared.
func MergeEntries[K comparable, V comparable](
	current []SetEntry[K, V],
	incoming []Assertion[K, V],
	source store.MetadataSource,
	setLocked bool,
) ([]SetEntry[K, V], bool) {
	return mergeEntries(current, incoming, source, setLocked, false)
}

func mergeEntries[K comparable, V comparable](
	current []SetEntry[K, V],
	incoming []Assertion[K, V],
	source store.MetadataSource,
	setLocked bool,
	dropUnasserted bool,
) ([]SetEntry[K, V], bool) {
	if setLocked || len(incoming) == 0 || !KnownSource(source) {
		return current, false
	}
	asserted := make(map[K]V, len(incoming))
	order := make([]K, 0, len(incoming))
	for _, assertion := range incoming {
		if _, seen := asserted[assertion.Key]; seen {
			continue
		}
		asserted[assertion.Key] = assertion.Value
		order = append(order, assertion.Key)
	}

	merged := make([]SetEntry[K, V], 0, len(current)+len(order))
	kept := make(map[K]struct{}, len(current))
	for _, entry := range current {
		value, listed := asserted[entry.Key]
		switch {
		case entry.Locked || Rank(entry.Source) > Rank(source):
			merged = append(merged, entry)
		case !listed:
			if dropUnasserted {
				continue
			}
			merged = append(merged, entry)
		default:
			entry.Value = value
			entry.Source = source
			merged = append(merged, entry)
		}
		kept[entry.Key] = struct{}{}
	}
	for _, key := range order {
		if _, exists := kept[key]; exists {
			continue
		}
		merged = append(merged, SetEntry[K, V]{
			Key: key, Value: asserted[key], Source: source})
	}
	return merged, !equalSets(current, merged)
}

// ManualSet replaces a set on explicit user request. Manual edits outrank
// every other source, so this is the only operation that may empty a set or
// discard a locked row, and every surviving row becomes a locked manual one
// that no later rescan can revive or remove. Emptying a set leaves no row to
// hold a lock, so the caller must also raise the set-level lock it passes to
// MergeSet for the result to survive a rescan.
func ManualSet[K comparable, V comparable](
	current []SetEntry[K, V],
	incoming []Assertion[K, V],
) ([]SetEntry[K, V], bool) {
	merged := make([]SetEntry[K, V], 0, len(incoming))
	seen := make(map[K]struct{}, len(incoming))
	for _, assertion := range incoming {
		if _, duplicate := seen[assertion.Key]; duplicate {
			continue
		}
		seen[assertion.Key] = struct{}{}
		merged = append(merged, SetEntry[K, V]{
			Key:    assertion.Key,
			Value:  assertion.Value,
			Source: store.MetadataManual,
			Locked: true,
		})
	}
	return merged, !equalSets(current, merged)
}

func equalSets[K comparable, V comparable](a, b []SetEntry[K, V]) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
