package store

import (
	"sort"
	"strings"
)

// SimilarityCandidate is one active book with the facts the similarity
// rule is allowed to look at. The backends gather these; the rule itself
// is here, so the two backends cannot disagree about what a duplicate is
// — the same reason SearchTerms is shared.
type SimilarityCandidate struct {
	Book CatalogBook
	// Digests are the book's stored files. Two books sharing one are
	// already reported as exact duplicates, and saying it twice would
	// only make the weaker report look unreliable.
	Digests []string
	// ContributorIDs are entity ids, not names. The library already
	// decided that "frank herbert" and "Frank  Herbert" are one person,
	// and this rule has no business deciding it again.
	ContributorIDs []string
	// SeriesPositions holds only the positions somebody actually
	// recorded. An absent position is an unanswered question, never zero.
	SeriesPositions map[string]float64
}

// SimilarBookGroup is books that look like one book without being one
// file. It is deliberately weaker than a digest group: it is a question
// put to a librarian, not a finding.
type SimilarBookGroup struct {
	// Title is the folded form the group was built on, which is what the
	// members have in common rather than any one member's spelling.
	Title string
	Books []CatalogBook
}

// NormalizeTitle is the normalization rule ADR-0010 phase 2 was waiting
// for. It folds case and diacritics, drops everything that is not a
// letter or digit, and collapses what is left to single spaces — the
// same folding search uses, so a librarian who found two books with one
// query sees the same two books called duplicates.
//
// It does no more than that on purpose. Stripping articles or subtitles
// would make "The Tower" and "Tower" one book in English and mangle
// every other language; edition words like "revised" are exactly the
// difference somebody may have meant to keep.
func NormalizeTitle(title string) string {
	return strings.Join(SearchTerms(title), " ")
}

// GroupSimilarBooks applies the rule: two books are a possible duplicate
// when their normalized titles are equal and they name at least one
// contributor in common.
//
// The contributor requirement is what keeps this honest. Title alone
// groups every "Selected Poems" in a library, and a report that is wrong
// most of the time is one a librarian learns to ignore — which is worse
// than not having it.
//
// Two exceptions subtract from that:
//
//   - Books sharing a digest are already reported exactly.
//   - Books placed at different positions in one series are different
//     volumes that happen to share a name, which is the failure mode the
//     ADR named when it deferred this. An unplaced book is not excluded:
//     nobody said where it goes, so nobody said it was a different book.
//
// Groups come back with the largest first and books in their listing
// order, so the answer does not depend on the order the rows arrived in.
func GroupSimilarBooks(candidates []SimilarityCandidate) []SimilarBookGroup {
	byTitle := map[string][]int{}
	for i, c := range candidates {
		title := NormalizeTitle(c.Book.Title)
		if title == "" {
			continue
		}
		byTitle[title] = append(byTitle[title], i)
	}

	var groups []SimilarBookGroup
	for title, members := range byTitle {
		if len(members) < 2 {
			continue
		}
		// Union-find over the pairs that survive, so a chain of three
		// books that each pair with the next comes back as one group
		// rather than three overlapping pairs.
		parent := map[int]int{}
		var find func(int) int
		find = func(i int) int {
			if p, ok := parent[i]; ok && p != i {
				parent[i] = find(p)
				return parent[i]
			}
			return i
		}
		for _, i := range members {
			parent[i] = i
		}
		joined := false
		for a := 0; a < len(members); a++ {
			for b := a + 1; b < len(members); b++ {
				if !possiblyOneBook(candidates[members[a]], candidates[members[b]]) {
					continue
				}
				ra, rb := find(members[a]), find(members[b])
				if ra != rb {
					parent[ra] = rb
				}
				joined = true
			}
		}
		if !joined {
			continue
		}
		sets := map[int][]int{}
		for _, i := range members {
			root := find(i)
			sets[root] = append(sets[root], i)
		}
		for _, set := range sets {
			if len(set) < 2 {
				continue
			}
			sort.Ints(set)
			group := SimilarBookGroup{Title: title}
			for _, i := range set {
				group.Books = append(group.Books, candidates[i].Book)
			}
			groups = append(groups, group)
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].Books) != len(groups[j].Books) {
			return len(groups[i].Books) > len(groups[j].Books)
		}
		return groups[i].Title < groups[j].Title
	})
	return groups
}

func possiblyOneBook(a, b SimilarityCandidate) bool {
	if sharesString(a.Digests, b.Digests) {
		return false
	}
	if !sharesString(a.ContributorIDs, b.ContributorIDs) {
		return false
	}
	for series, positionA := range a.SeriesPositions {
		if positionB, ok := b.SeriesPositions[series]; ok && positionA != positionB {
			return false
		}
	}
	return true
}

func sharesString(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if seen[v] {
			return true
		}
	}
	return false
}
