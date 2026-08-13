package store

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// MaxSearchTerms bounds how many words one query may use. A search box is
// typed into by a person, and a request with more words than this is
// either a paste or an attempt to make the index do expensive work.
const MaxSearchTerms = 12

// SearchTerms splits what somebody typed into the words to look for.
//
// Both backends call this so that a query means the same thing on each,
// which is what makes their results comparable at all. It is also the
// only sanitizing either backend needs: everything that is not a letter
// or a digit is a separator, so nothing a person can type survives as
// index syntax — no quote, wildcard or boolean operator reaches FTS5 or
// `to_tsquery` to change how the query is read or to make it fail.
//
// Folding to lower case here rather than in the query keeps the two
// backends honest about the same thing: `unicode61` folds case, and
// `simple` does too, but only for the text they each index.
func SearchTerms(text string) []string {
	var terms []string
	for _, field := range strings.FieldsFunc(FoldForSearch(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		terms = append(terms, field)
		if len(terms) == MaxSearchTerms {
			break
		}
	}
	return terms
}

// diacriticFolder strips the marks that decompose off a letter, which is
// what turns "Émile" into "emile".
//
// It is built per call rather than kept in a package variable because a
// transform.Chain carries the state of the conversion it is in the middle
// of. Sharing one would mean two books being indexed at once folding each
// other's text, which is a corrupt index rather than a slow one.
func diacriticFolder() transform.Transformer {
	return transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
}

// FoldForSearch lowercases text and removes diacritics, so a library
// catalogued with accents is searchable by somebody whose keyboard has
// none. Both the indexed text and the query go through it.
//
// SQLite's tokenizer would do this itself, and does; doing it here as
// well is harmless there and is the only way PostgreSQL can match it
// without the `unaccent` extension, which a managed database may not
// offer. A server that behaves differently depending on which database
// somebody picked is a server nobody can support.
func FoldForSearch(text string) string {
	folded, _, err := transform.String(diacriticFolder(), text)
	if err != nil {
		// The transform only fails on malformed input, which is still
		// searchable text; folding nothing is better than finding
		// nothing.
		folded = text
	}
	return strings.ToLower(folded)
}
