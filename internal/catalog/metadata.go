// Package catalog resolves metadata proposals against the catalog rows a
// book already has. It is the only place that knows both the pure
// precedence engine and the store's row shapes: internal/metadata imports
// internal/store for MetadataSource, so the store cannot run the engine
// itself without an import cycle.
package catalog

import (
	"github.com/google/uuid"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// entityNS derives stable ids for metadata entities. A deterministic id
// means two servers ingesting the same library agree on it, and a retried
// apply never creates a second row for a name the store already resolved by
// normalized name.
var entityNS = uuid.MustParse("7e0a3b10-5f2e-4c3a-9b6d-2f6f6c1a0003")

// EntityID derives the candidate id for one metadata entity. The store uses
// it only when the library has no entity with this normalized name yet.
func EntityID(libraryID, kind, normalizedName string) string {
	return uuid.NewSHA1(entityNS, []byte(libraryID+"|"+kind+"|"+normalizedName)).String()
}

// Resolve merges one proposal into a book's current metadata and reports
// whether anything changed. A false result means the caller must not write:
// a rescan that learned nothing leaves the revision alone.
//
// Whether a set assertion may remove rows comes from the proposal, never
// from its source. A source that read the whole publication asserts complete
// sets; one that saw only a filename asserts a partial set and may add or
// take over rows but never delete.
func Resolve(
	current store.BookMetadata, proposal metadata.Proposal,
) (store.BookMetadata, bool) {
	next := current
	changed := false

	apply := func(value *string, source *store.MetadataSource, locked *bool,
		candidate metadata.Candidate,
	) {
		field, ok := metadata.Apply(
			metadata.Field{Value: *value, Source: *source, Locked: *locked},
			candidate)
		if !ok {
			return
		}
		*value, *source, *locked = field.Value, field.Source, field.Locked
		changed = true
	}
	book := &next.Book
	apply(&book.Title, &book.TitleSource, &book.TitleLocked, proposal.Title)
	apply(&book.Subtitle, &book.SubtitleSource, &book.SubtitleLocked, proposal.Subtitle)
	apply(&book.Description, &book.DescriptionSource, &book.DescriptionLocked,
		proposal.Description)
	apply(&book.Publisher, &book.PublisherSource, &book.PublisherLocked,
		proposal.Publisher)
	apply(&book.PublishedDate, &book.PublishedDateSource, &book.PublishedDateLocked,
		proposal.PublishedDate)

	libraryID := current.Book.LibraryID
	locks := current.Book.SetLocks

	identifiers, identifiersChanged := mergeSet(
		identifierEntries(current.Identifiers), proposal.Identifiers,
		proposal, locks.Identifiers)
	if identifiersChanged {
		next.Identifiers = make([]store.BookIdentifier, 0, len(identifiers))
		for _, entry := range identifiers {
			next.Identifiers = append(next.Identifiers, store.BookIdentifier{
				Scheme: entry.Key.Scheme, Value: entry.Key.Value,
				Source: entry.Source, Locked: entry.Locked,
			})
		}
		changed = true
	}

	languages, languagesChanged := mergeSet(
		languageEntries(current.Languages), proposal.Languages,
		proposal, locks.Languages)
	if languagesChanged {
		next.Languages = make([]store.BookLanguage, 0, len(languages))
		for _, entry := range languages {
			next.Languages = append(next.Languages, store.BookLanguage{
				Language: entry.Value, Source: entry.Source, Locked: entry.Locked,
			})
		}
		changed = true
	}

	tags, tagsChanged := mergeSet(
		taxonEntries(current.Tags),
		adoptNamed(proposal.Tags, taxonDisplay(current.Tags)),
		proposal, locks.Tags)
	if tagsChanged {
		next.Tags = taxonRows(tags, current.Tags, libraryID, "tag")
		changed = true
	}

	// No source proposes genres yet: an EPUB subject list mixes tags and
	// genres freely and inventing one would be guessing. The merge still
	// runs so a locked or stronger row behaves identically once one does.
	genres, genresChanged := mergeSet(
		taxonEntries(current.Genres), nil, proposal, locks.Genres)
	if genresChanged {
		next.Genres = taxonRows(genres, current.Genres, libraryID, "genre")
		changed = true
	}

	series, seriesChanged := mergeSet(
		seriesEntries(current.Series),
		adoptSeries(proposal.Series, current.Series, proposal.Source),
		proposal, locks.Series)
	if seriesChanged {
		existing := make(map[string]string, len(current.Series))
		for _, row := range current.Series {
			existing[row.NormalizedName] = row.SeriesID
		}
		next.Series = make([]store.BookSeries, 0, len(series))
		for _, entry := range series {
			row := store.BookSeries{
				SeriesID:       entityIDFor(existing, entry.Key, libraryID, "series"),
				Name:           entry.Value.Display,
				NormalizedName: entry.Key,
				Source:         entry.Source,
				Locked:         entry.Locked,
			}
			if entry.Value.HasPosition {
				position := entry.Value.Position
				row.Position = &position
			}
			next.Series = append(next.Series, row)
		}
		changed = true
	}

	contributors, contributorsChanged := mergeSet(
		contributorEntries(current.Contributors),
		adoptContributors(proposal.Contributors, current.Contributors),
		proposal, locks.Contributors)
	if contributorsChanged {
		existing := make(map[string]string, len(current.Contributors))
		for _, row := range current.Contributors {
			existing[row.NormalizedName] = row.ContributorID
		}
		next.Contributors = make([]store.BookContributor, 0, len(contributors))
		for i, entry := range contributors {
			next.Contributors = append(next.Contributors, store.BookContributor{
				ContributorID: entityIDFor(
					existing, entry.Key.Name, libraryID, "contributor"),
				Name:           entry.Value,
				NormalizedName: entry.Key.Name,
				Role:           entry.Key.Role,
				Position:       i,
				Source:         entry.Source,
				Locked:         entry.Locked,
			})
		}
		changed = true
	}

	if !changed {
		return current, false
	}
	return next, true
}

// mergeSet dispatches on the proposal's own declaration. A partial proposal
// may only add and take over rows; treating it as complete would delete the
// rows its source never saw.
func mergeSet[K comparable, V comparable](
	current []metadata.SetEntry[K, V],
	incoming []metadata.Assertion[K, V],
	proposal metadata.Proposal,
	setLocked bool,
) ([]metadata.SetEntry[K, V], bool) {
	if proposal.PartialSets {
		return metadata.MergeEntries(current, incoming, proposal.Source, setLocked)
	}
	return metadata.MergeSet(current, incoming, proposal.Source, setLocked)
}

func entityIDFor(
	existing map[string]string, normalizedName, libraryID, kind string,
) string {
	if id, ok := existing[normalizedName]; ok {
		return id
	}
	return EntityID(libraryID, kind, normalizedName)
}

// An entity row owns its display spelling: the store resolves an entity by
// normalized name and never renames it, so a read-back returns whichever
// spelling the library saw first. The helpers below make an assertion adopt
// what the library already owns, so a proposal that differs only in spelling
// is not mistaken for news — otherwise the book would be rewritten, and its
// revision bumped, on every pass without ever reaching a fixed point.
//
// A book that does not yet link an entity can still propose a new spelling
// and be corrected by the next pass; only the library-wide row the store
// alone can see would avoid that, and one extra write on first link is
// cheaper than reading the whole library here.

func adoptNamed(
	incoming []metadata.Assertion[string, string], persisted map[string]string,
) []metadata.Assertion[string, string] {
	if len(incoming) == 0 || len(persisted) == 0 {
		return incoming
	}
	out := make([]metadata.Assertion[string, string], 0, len(incoming))
	for _, assertion := range incoming {
		if display, ok := persisted[assertion.Key]; ok {
			assertion.Value = display
		}
		out = append(out, assertion)
	}
	return out
}

func adoptContributors(
	incoming []metadata.Assertion[metadata.ContributorKey, string],
	current []store.BookContributor,
) []metadata.Assertion[metadata.ContributorKey, string] {
	if len(incoming) == 0 || len(current) == 0 {
		return incoming
	}
	persisted := make(map[string]string, len(current))
	for _, row := range current {
		persisted[row.NormalizedName] = row.Name
	}
	out := make([]metadata.Assertion[metadata.ContributorKey, string], 0, len(incoming))
	for _, assertion := range incoming {
		if display, ok := persisted[assertion.Key.Name]; ok {
			assertion.Value = display
		}
		out = append(out, assertion)
	}
	return out
}

// adoptSeries also carries over a position the assertion never claimed. A
// path that names a series but no number within it has determined nothing
// about that number, and a source may only take over what it determined:
// replacing the whole payload would erase the position the file declared.
//
// A person saying a book is in a series with no number in it is stating a
// fact, not failing to determine one, so a manual proposal is taken at its
// word — the same escape hatch ManualClear gives the scalar fields.
//
// Provenance stays row-level: the assertion takes over the row, so the
// carried-over position ends up attributed to the weaker source that did
// not determine it, and a later extraction can no longer correct that
// number. Fixing that properly means per-field provenance inside a set row,
// which is a wider change than the erasure this prevents.
func adoptSeries(
	incoming []metadata.Assertion[string, metadata.SeriesValue],
	current []store.BookSeries,
	source store.MetadataSource,
) []metadata.Assertion[string, metadata.SeriesValue] {
	if len(incoming) == 0 || len(current) == 0 {
		return incoming
	}
	persisted := make(map[string]store.BookSeries, len(current))
	for _, row := range current {
		persisted[row.NormalizedName] = row
	}
	out := make([]metadata.Assertion[string, metadata.SeriesValue], 0, len(incoming))
	for _, assertion := range incoming {
		if row, ok := persisted[assertion.Key]; ok {
			assertion.Value.Display = row.Name
			if source != store.MetadataManual &&
				!assertion.Value.HasPosition && row.Position != nil {
				assertion.Value.Position = *row.Position
				assertion.Value.HasPosition = true
			}
		}
		out = append(out, assertion)
	}
	return out
}

func taxonDisplay(rows []store.BookTaxon) map[string]string {
	if len(rows) == 0 {
		return nil
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.NormalizedName] = row.Name
	}
	return out
}

func identifierEntries(
	rows []store.BookIdentifier,
) []metadata.SetEntry[metadata.IdentifierKey, struct{}] {
	out := make([]metadata.SetEntry[metadata.IdentifierKey, struct{}], 0, len(rows))
	for _, row := range rows {
		out = append(out, metadata.SetEntry[metadata.IdentifierKey, struct{}]{
			Key: metadata.IdentifierKey{
				Scheme: metadata.NormalizeName(row.Scheme), Value: row.Value},
			Source: row.Source, Locked: row.Locked,
		})
	}
	return out
}

func languageEntries(rows []store.BookLanguage) []metadata.SetEntry[string, string] {
	out := make([]metadata.SetEntry[string, string], 0, len(rows))
	for _, row := range rows {
		out = append(out, metadata.SetEntry[string, string]{
			Key: metadata.NormalizeName(row.Language), Value: row.Language,
			Source: row.Source, Locked: row.Locked,
		})
	}
	return out
}

func taxonEntries(rows []store.BookTaxon) []metadata.SetEntry[string, string] {
	out := make([]metadata.SetEntry[string, string], 0, len(rows))
	for _, row := range rows {
		out = append(out, metadata.SetEntry[string, string]{
			Key: row.NormalizedName, Value: row.Name,
			Source: row.Source, Locked: row.Locked,
		})
	}
	return out
}

func taxonRows(
	entries []metadata.SetEntry[string, string],
	current []store.BookTaxon,
	libraryID, kind string,
) []store.BookTaxon {
	existing := make(map[string]string, len(current))
	for _, row := range current {
		existing[row.NormalizedName] = row.ID
	}
	out := make([]store.BookTaxon, 0, len(entries))
	for _, entry := range entries {
		out = append(out, store.BookTaxon{
			ID:             entityIDFor(existing, entry.Key, libraryID, kind),
			Name:           entry.Value,
			NormalizedName: entry.Key,
			Source:         entry.Source,
			Locked:         entry.Locked,
		})
	}
	return out
}

func seriesEntries(
	rows []store.BookSeries,
) []metadata.SetEntry[string, metadata.SeriesValue] {
	out := make([]metadata.SetEntry[string, metadata.SeriesValue], 0, len(rows))
	for _, row := range rows {
		value := metadata.SeriesValue{Display: row.Name}
		if row.Position != nil {
			value.Position = *row.Position
			value.HasPosition = true
		}
		out = append(out, metadata.SetEntry[string, metadata.SeriesValue]{
			Key: row.NormalizedName, Value: value,
			Source: row.Source, Locked: row.Locked,
		})
	}
	return out
}

func contributorEntries(
	rows []store.BookContributor,
) []metadata.SetEntry[metadata.ContributorKey, string] {
	out := make([]metadata.SetEntry[metadata.ContributorKey, string], 0, len(rows))
	for _, row := range rows {
		out = append(out, metadata.SetEntry[metadata.ContributorKey, string]{
			Key: metadata.ContributorKey{
				Name: row.NormalizedName, Role: row.Role},
			Value: row.Name, Source: row.Source, Locked: row.Locked,
		})
	}
	return out
}
