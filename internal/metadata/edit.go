package metadata

import (
	"sort"
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

// ScalarEdit is a manual change to one scalar field. A nil *ScalarEdit
// means the field was not part of the edit at all and keeps whatever it
// has, which is what lets a form submit one field without asserting
// anything about the rest.
type ScalarEdit struct {
	// Value is the new display value. A blank value is a manual
	// assertion that the field should be empty — the user deleted a
	// wrong title — and is honoured, unlike a blank candidate from an
	// extractor, which is only ever ignorance.
	Value string
	// Unlock hands the field back to the extractors instead of locking
	// it. Editing and unlocking in the same request is contradictory, so
	// an unlock leaves the value alone and only clears the lock.
	Unlock bool
}

// EntryEdit is one member of an edited set. Series read Position,
// contributors read Role, and tags, genres and languages read neither.
type EntryEdit struct {
	Name     string
	Position *float64
	Role     string
}

// SetEdit replaces one whole set. A set is asserted all at once because
// that is what a form does — the rows the user left are the rows they
// want — so an entry that is not listed is an entry they removed.
type SetEdit struct {
	Entries []EntryEdit
	// Unlock returns the set to the extractors. As with a scalar, it is
	// exclusive with editing: the entries are ignored.
	Unlock bool
}

// ManualEdit is one user's change to a book's metadata. Every field is
// optional, and a nil field is silence rather than a request to clear.
//
// Identifiers are deliberately absent. They feed work identity
// (ADR-0003), so editing one moves a reader's reading history from one
// book to another; that is a different operation with different
// consequences and it does not belong behind a metadata form.
type ManualEdit struct {
	Title         *ScalarEdit
	Subtitle      *ScalarEdit
	Description   *ScalarEdit
	Publisher     *ScalarEdit
	PublishedDate *ScalarEdit

	Tags         *SetEdit
	Genres       *SetEdit
	Languages    *SetEdit
	Series       *SetEdit
	Contributors *SetEdit
}

// ApplyManualEdit resolves a user's edit against a book's current
// metadata and reports the new state and whether anything changed.
//
// Every accepted change is recorded as `manual` and locks its field or
// set, which is the whole point: the next rescan must not undo what a
// person just corrected. newID supplies ids for entities the library has
// never seen, because id generation belongs at the edge.
func ApplyManualEdit(
	current store.BookMetadata,
	edit ManualEdit,
	newID func() string,
) (store.BookMetadata, bool) {
	next := current
	changed := false

	for _, field := range []struct {
		edit   *ScalarEdit
		value  *string
		source *store.MetadataSource
		locked *bool
	}{
		{edit.Title, &next.Book.Title, &next.Book.TitleSource, &next.Book.TitleLocked},
		{edit.Subtitle, &next.Book.Subtitle, &next.Book.SubtitleSource, &next.Book.SubtitleLocked},
		{edit.Description, &next.Book.Description, &next.Book.DescriptionSource, &next.Book.DescriptionLocked},
		{edit.Publisher, &next.Book.Publisher, &next.Book.PublisherSource, &next.Book.PublisherLocked},
		{edit.PublishedDate, &next.Book.PublishedDate, &next.Book.PublishedDateSource, &next.Book.PublishedDateLocked},
	} {
		if field.edit == nil {
			continue
		}
		before := Field{Value: *field.value, Source: *field.source, Locked: *field.locked}
		var after Field
		var did bool
		switch {
		case field.edit.Unlock:
			after, did = SetLocked(before, false)
		case strings.TrimSpace(field.edit.Value) == "":
			after, did = ManualClear(before)
		default:
			after, did = Apply(before, Candidate{
				Value: field.edit.Value, Source: store.MetadataManual})
		}
		if did {
			*field.value, *field.source, *field.locked =
				after.Value, after.Source, after.Locked
			changed = true
		}
	}

	if applyTaxonEdit(edit.Tags, &next.Tags, &next.Book.SetLocks.Tags, newID) {
		changed = true
	}
	if applyTaxonEdit(edit.Genres, &next.Genres, &next.Book.SetLocks.Genres, newID) {
		changed = true
	}
	if applyLanguageEdit(edit.Languages, &next) {
		changed = true
	}
	if applySeriesEdit(edit.Series, &next, newID) {
		changed = true
	}
	if applyContributorEdit(edit.Contributors, &next, newID) {
		changed = true
	}
	return next, changed
}

// dedupe folds an edited set to one entry per key, keeping the first,
// because a form can submit the same tag twice and the store rejects a
// duplicate rather than tidying it.
func dedupe(entries []EntryEdit, key func(EntryEdit) string) []EntryEdit {
	seen := make(map[string]struct{}, len(entries))
	out := make([]EntryEdit, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.Name) == "" {
			continue
		}
		k := key(entry)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, entry)
	}
	return out
}

// entityID reuses the id the library already has for a normalized name,
// so an edit that re-adds a tag rejoins the entity rather than asking for
// a second one under the same name.
//
// A minted id is remembered, because one name is one entity even within a
// single edit: a person credited as both author and illustrator makes two
// claims about one contributor, not two contributors.
func entityID(normalized string, known map[string]string, newID func() string) string {
	if id, ok := known[normalized]; ok {
		return id
	}
	id := newID()
	known[normalized] = id
	return id
}

func applyTaxonEdit(
	edit *SetEdit, rows *[]store.BookTaxon, locked *bool, newID func() string,
) bool {
	if edit == nil {
		return false
	}
	if edit.Unlock {
		if !*locked {
			return false
		}
		*locked = false
		return true
	}
	known := make(map[string]string, len(*rows))
	for _, row := range *rows {
		known[row.NormalizedName] = row.ID
	}
	next := make([]store.BookTaxon, 0, len(edit.Entries))
	for _, entry := range dedupe(edit.Entries, func(e EntryEdit) string {
		return NormalizeName(e.Name)
	}) {
		name := strings.TrimSpace(entry.Name)
		normalized := NormalizeName(name)
		next = append(next, store.BookTaxon{
			ID:             entityID(normalized, known, newID),
			Name:           name,
			NormalizedName: normalized,
			Source:         store.MetadataManual,
			Locked:         true,
		})
	}
	sort.Slice(next, func(i, j int) bool {
		return next[i].NormalizedName < next[j].NormalizedName
	})
	same := len(next) == len(*rows)
	if same {
		for i := range next {
			if next[i] != (*rows)[i] {
				same = false
				break
			}
		}
	}
	*rows = next
	// The set lock is set even when the rows did not move, because
	// submitting a set unchanged is still a person saying "this is
	// right", and that is exactly what must survive the next rescan.
	wasLocked := *locked
	*locked = true
	return !same || !wasLocked
}

func applyLanguageEdit(edit *SetEdit, meta *store.BookMetadata) bool {
	if edit == nil {
		return false
	}
	if edit.Unlock {
		if !meta.Book.SetLocks.Languages {
			return false
		}
		meta.Book.SetLocks.Languages = false
		return true
	}
	next := make([]store.BookLanguage, 0, len(edit.Entries))
	for _, entry := range dedupe(edit.Entries, func(e EntryEdit) string {
		return NormalizeName(e.Name)
	}) {
		next = append(next, store.BookLanguage{
			Language: strings.TrimSpace(entry.Name),
			Source:   store.MetadataManual,
			Locked:   true,
		})
	}
	sort.Slice(next, func(i, j int) bool {
		return next[i].Language < next[j].Language
	})
	same := len(next) == len(meta.Languages)
	if same {
		for i := range next {
			if next[i] != meta.Languages[i] {
				same = false
				break
			}
		}
	}
	meta.Languages = next
	wasLocked := meta.Book.SetLocks.Languages
	meta.Book.SetLocks.Languages = true
	return !same || !wasLocked
}

func applySeriesEdit(
	edit *SetEdit, meta *store.BookMetadata, newID func() string,
) bool {
	if edit == nil {
		return false
	}
	if edit.Unlock {
		if !meta.Book.SetLocks.Series {
			return false
		}
		meta.Book.SetLocks.Series = false
		return true
	}
	known := make(map[string]string, len(meta.Series))
	for _, row := range meta.Series {
		known[row.NormalizedName] = row.SeriesID
	}
	next := make([]store.BookSeries, 0, len(edit.Entries))
	for _, entry := range dedupe(edit.Entries, func(e EntryEdit) string {
		return NormalizeName(e.Name)
	}) {
		name := strings.TrimSpace(entry.Name)
		normalized := NormalizeName(name)
		// A missing position stays missing. Defaulting it to one would
		// claim every unplaced book is the first of its series.
		var position *float64
		if entry.Position != nil {
			value := *entry.Position
			position = &value
		}
		next = append(next, store.BookSeries{
			SeriesID:       entityID(normalized, known, newID),
			Name:           name,
			NormalizedName: normalized,
			Position:       position,
			Source:         store.MetadataManual,
			Locked:         true,
		})
	}
	sort.Slice(next, func(i, j int) bool {
		return next[i].NormalizedName < next[j].NormalizedName
	})
	same := len(next) == len(meta.Series)
	if same {
		for i := range next {
			if !sameSeries(next[i], meta.Series[i]) {
				same = false
				break
			}
		}
	}
	meta.Series = next
	wasLocked := meta.Book.SetLocks.Series
	meta.Book.SetLocks.Series = true
	return !same || !wasLocked
}

func sameSeries(a, b store.BookSeries) bool {
	if a.SeriesID != b.SeriesID || a.Name != b.Name ||
		a.NormalizedName != b.NormalizedName || a.Source != b.Source ||
		a.Locked != b.Locked {
		return false
	}
	switch {
	case a.Position == nil && b.Position == nil:
		return true
	case a.Position == nil || b.Position == nil:
		return false
	default:
		return *a.Position == *b.Position
	}
}

func applyContributorEdit(
	edit *SetEdit, meta *store.BookMetadata, newID func() string,
) bool {
	if edit == nil {
		return false
	}
	if edit.Unlock {
		if !meta.Book.SetLocks.Contributors {
			return false
		}
		meta.Book.SetLocks.Contributors = false
		return true
	}
	known := make(map[string]string, len(meta.Contributors))
	for _, row := range meta.Contributors {
		known[row.NormalizedName] = row.ContributorID
	}
	next := make([]store.BookContributor, 0, len(edit.Entries))
	// A contributor keeps the order the form listed them in, because the
	// first author of a book is a fact about the book and alphabetizing
	// it away would lose it.
	for i, entry := range dedupe(edit.Entries, func(e EntryEdit) string {
		return NormalizeName(e.Name) + "\x00" + NormalizeName(e.Role)
	}) {
		name := strings.TrimSpace(entry.Name)
		normalized := NormalizeName(name)
		role := NormalizeName(entry.Role)
		if role == "" {
			role = "author"
		}
		next = append(next, store.BookContributor{
			ContributorID:  entityID(normalized, known, newID),
			Name:           name,
			NormalizedName: normalized,
			Role:           role,
			Position:       i,
			Source:         store.MetadataManual,
			Locked:         true,
		})
	}
	same := len(next) == len(meta.Contributors)
	if same {
		for i := range next {
			if next[i] != meta.Contributors[i] {
				same = false
				break
			}
		}
	}
	meta.Contributors = next
	wasLocked := meta.Book.SetLocks.Contributors
	meta.Book.SetLocks.Contributors = true
	return !same || !wasLocked
}
