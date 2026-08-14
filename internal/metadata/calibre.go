package metadata

import (
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/store"
)

// FromCalibre maps one Calibre book to a proposal.
//
// It asserts complete sets and clears what it does not state, because
// Calibre is a curated record rather than a partial observation: a book
// with no tags in Calibre has no tags, and a description deleted there
// is a description deleted. That is the whole reason to point this
// server at a Calibre library.
func FromCalibre(book calibre.Book) Proposal {
	proposal := Proposal{
		Source:         store.MetadataCalibre,
		Confidence:     ConfidenceHigh,
		ClearsUnstated: true,
		// Every set Calibre curates, and only those. Genres are absent
		// because Calibre has no such notion: clearing them would delete
		// rows over an opinion it never expressed.
		StatedSets: SetFields{
			Identifiers:  true,
			Languages:    true,
			Tags:         true,
			Series:       true,
			Contributors: true,
		},
		Title: Candidate{
			Value: book.Title, Source: store.MetadataCalibre},
		Description: Candidate{
			Value: book.Description, Source: store.MetadataCalibre},
		Publisher: Candidate{
			Value: book.Publisher, Source: store.MetadataCalibre},
		PublishedDate: Candidate{
			Value: calibreDate(book.Published), Source: store.MetadataCalibre},
	}
	// Calibre has no subtitle column. Leaving the candidate empty while
	// the proposal clears what it does not state would erase a subtitle
	// the EPUB declared, over a field Calibre cannot express an opinion
	// about, so it is left out of the clearing entirely.
	proposal.Subtitle = Candidate{}

	for scheme, value := range book.Identifiers {
		scheme, value = strings.TrimSpace(scheme), strings.TrimSpace(value)
		if scheme == "" || value == "" {
			continue
		}
		proposal.Identifiers = append(proposal.Identifiers,
			Assertion[IdentifierKey, struct{}]{Key: IdentifierKey{
				Scheme: NormalizeName(scheme), Value: value,
			}})
	}
	for _, language := range book.Languages {
		proposal.Languages = appendNamed(proposal.Languages, language)
	}
	for _, tag := range book.Tags {
		proposal.Tags = appendNamed(proposal.Tags, tag)
	}
	if series := strings.TrimSpace(book.Series); series != "" {
		proposal.Series = []Assertion[string, SeriesValue]{{
			Key: NormalizeName(series),
			Value: SeriesValue{
				Display: series,
				// Calibre always has a series index, defaulting to 1.0,
				// so a book in a series always has a place in it.
				Position: book.SeriesIndex, HasPosition: true,
			},
		}}
	}
	for _, author := range book.Authors {
		name := strings.TrimSpace(author)
		if name == "" {
			continue
		}
		proposal.Contributors = append(proposal.Contributors,
			Assertion[ContributorKey, string]{
				Key: ContributorKey{
					Name: NormalizeName(name),
					Role: store.ContributorRoleAuthor,
				},
				Value: name,
			})
	}
	return proposal
}

// calibreDate renders a publication date the way an OPF does, so two
// sources describing the same book do not differ by their formatting.
// A date with no day, which Calibre writes as the first of a month, is
// still a date: this is not the place to guess at precision.
func calibreDate(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	if t.Year() <= 1 {
		return ""
	}
	return t.Format("2006-01-02")
}

// CalibreIdentifierValue is how a Calibre book id is spelled when one is
// displayed. The catalog does not resolve books by it — that is the
// mapping table's job — so this exists for humans and for logs.
func CalibreIdentifierValue(id int64) string {
	return strconv.FormatInt(id, 10)
}
