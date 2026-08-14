package metadata

import (
	"strings"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

// IdentifierKey identifies one publication identifier row. The scheme is
// folded for matching; the value is not, because identifier values are
// case-sensitive in general.
type IdentifierKey struct {
	Scheme string
	Value  string
}

// ContributorKey identifies one contributor in one role.
type ContributorKey struct {
	Name string
	Role string
}

// SeriesValue is the payload of a series membership row.
type SeriesValue struct {
	Display     string
	Position    float64
	HasPosition bool
}

// Proposal is everything one source claims about one book, expressed in the
// vocabulary the precedence engine consumes. Confidence grades how much
// structure the source relied on and is reported for display; it is not a
// gate. A source that had to guess at a value leaves that value out of its
// proposal rather than proposing it weakly, so a proposal graded low still
// asserts only what its source actually determined, and refusing it would
// discard facts along with the guesses.
//
// PartialSets decides how the sets must be merged. A source that reads the
// whole publication asserts complete sets and its proposal goes through
// MergeSet, which drops what the source no longer claims. A source that can
// only ever see part of the picture sets PartialSets and must go through
// MergeEntries instead, or it would delete rows it never had any knowledge
// of.
type Proposal struct {
	Source      store.MetadataSource
	Confidence  Confidence
	PartialSets bool
	// ClearsUnstated says that a scalar this proposal leaves empty is an
	// assertion of emptiness rather than a value the source could not
	// determine. Only a source that reads a complete, curated record may
	// set it — Calibre does; an EPUB extraction that failed to find a
	// publisher does not — and what it leaves behind is a tombstone, so
	// a weaker source cannot refill the field on the next pass.
	ClearsUnstated bool
	// StatedSets names the multi-valued sets this proposal speaks for.
	// It only matters when the proposal asserts complete sets and clears
	// what it does not state: a set named here and left empty is an
	// assertion that the set is empty — the last tag removed in Calibre
	// is a book with no tags — while one not named is a set the source
	// cannot express an opinion about and is left exactly as it was.
	StatedSets    SetFields
	Title         Candidate
	Subtitle      Candidate
	Description   Candidate
	Publisher     Candidate
	PublishedDate Candidate
	Identifiers   []Assertion[IdentifierKey, struct{}]
	Languages     []Assertion[string, string]
	Tags          []Assertion[string, string]
	Series        []Assertion[string, SeriesValue]
	Contributors  []Assertion[ContributorKey, string]
}

// SetFields marks which of a book's multi-valued sets a proposal speaks
// for. It is a struct of named flags rather than a list of keys so that
// a set added later is a compile error at every source that has to
// decide about it, rather than a silently unstated one.
type SetFields struct {
	Identifiers  bool
	Languages    bool
	Tags         bool
	Genres       bool
	Series       bool
	Contributors bool
}

// FromEmbedded maps a bounded OPF extraction to an embedded-source proposal.
// EPUB subjects become tags rather than genres: a subject list mixes both
// freely, and inventing a genre from it would be exactly the guessing the
// design forbids.
func FromEmbedded(metadata epub.Metadata) Proposal {
	proposal := Proposal{
		Source:        store.MetadataEmbedded,
		Confidence:    ConfidenceHigh,
		Title:         Candidate{Value: metadata.Title, Source: store.MetadataEmbedded},
		Subtitle:      Candidate{Value: metadata.Subtitle, Source: store.MetadataEmbedded},
		Description:   Candidate{Value: metadata.Description, Source: store.MetadataEmbedded},
		Publisher:     Candidate{Value: metadata.Publisher, Source: store.MetadataEmbedded},
		PublishedDate: Candidate{Value: metadata.PublishedDate, Source: store.MetadataEmbedded},
	}
	for _, identifier := range metadata.Identifiers {
		value := strings.TrimSpace(identifier.Value)
		if value == "" {
			continue
		}
		proposal.Identifiers = append(proposal.Identifiers,
			Assertion[IdentifierKey, struct{}]{Key: IdentifierKey{
				Scheme: NormalizeName(identifier.Scheme),
				Value:  value,
			}})
	}
	for _, language := range metadata.Languages {
		proposal.Languages = appendNamed(proposal.Languages, language)
	}
	for _, subject := range metadata.Subjects {
		proposal.Tags = appendNamed(proposal.Tags, subject)
	}
	for _, series := range metadata.Series {
		name := strings.TrimSpace(series.Name)
		if name == "" {
			continue
		}
		value := SeriesValue{Display: name}
		if series.Position != nil {
			value.Position = *series.Position
			value.HasPosition = true
		}
		proposal.Series = append(proposal.Series,
			Assertion[string, SeriesValue]{Key: NormalizeName(name), Value: value})
	}
	for _, contributor := range metadata.Contributors {
		name := strings.TrimSpace(contributor.Name)
		role := NormalizeName(contributor.Role)
		if name == "" || role == "" {
			continue
		}
		proposal.Contributors = append(proposal.Contributors,
			Assertion[ContributorKey, string]{
				Key:   ContributorKey{Name: NormalizeName(name), Role: role},
				Value: name,
			})
	}
	return proposal
}

// FromPath maps a parsed library path to a filename-source proposal. A path
// says nothing about a description, a publisher or a date, so those stay
// empty and the precedence engine leaves whatever the file itself supplied.
// Its sets are partial for the same reason: a path names at most one author
// and one series and knows nothing of the other contributors or series the
// file declared, so the result must be merged with MergeEntries.
//
// A value the layout recovered by splitting one name component is left out
// entirely. A filename outranks the publication's own metadata, so a guess
// about where one field ended would overwrite what the file declared and
// stamp filename provenance on it, which no later extraction can take back.
// A field read from a directory of its own is unaffected by a guess made
// elsewhere in the same name.
func FromPath(candidate PathCandidate) Proposal {
	proposal := Proposal{
		Source:      store.MetadataFilename,
		Confidence:  candidate.Confidence,
		PartialSets: true,
	}
	if !candidate.Guessed.Title {
		proposal.Title = Candidate{
			Value: candidate.Title, Source: store.MetadataFilename}
	}
	if candidate.Guessed.Series {
		candidate.Series = ""
	}
	if candidate.Guessed.Author {
		candidate.Author = ""
	}
	if series := strings.TrimSpace(candidate.Series); series != "" {
		proposal.Series = []Assertion[string, SeriesValue]{{
			Key: NormalizeName(series),
			Value: SeriesValue{
				Display:     series,
				Position:    candidate.SeriesPosition,
				HasPosition: candidate.HasPosition,
			},
		}}
	}
	if author := strings.TrimSpace(candidate.Author); author != "" {
		proposal.Contributors = []Assertion[ContributorKey, string]{{
			Key:   ContributorKey{Name: NormalizeName(author), Role: "author"},
			Value: author,
		}}
	}
	return proposal
}

// AssertsNothing reports a proposal with nothing in it: every value its
// source might have carried was one it could not determine. Applying it is
// a no-op, so a caller can skip the work of resolving it at all.
//
// A proposal that clears what it does not state always asserts something,
// because saying nothing is what it says.
func (p Proposal) AssertsNothing() bool {
	if p.ClearsUnstated {
		return false
	}
	return p.Title.Value == "" && p.Subtitle.Value == "" &&
		p.Description.Value == "" && p.Publisher.Value == "" &&
		p.PublishedDate.Value == "" &&
		len(p.Identifiers) == 0 && len(p.Languages) == 0 &&
		len(p.Tags) == 0 && len(p.Series) == 0 &&
		len(p.Contributors) == 0
}

func appendNamed(
	assertions []Assertion[string, string], name string,
) []Assertion[string, string] {
	display := strings.TrimSpace(name)
	if display == "" {
		return assertions
	}
	return append(assertions,
		Assertion[string, string]{Key: NormalizeName(display), Value: display})
}
