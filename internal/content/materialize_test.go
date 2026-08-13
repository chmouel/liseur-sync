//go:build linux

package content

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

type fakeMetadataStore struct {
	current   store.BookMetadata
	reads     int
	spellings map[string]string
	applies   []store.ApplyBookMetadataRequest
	// concurrentWrite models the writer that won the revision race doing
	// something to the book, not merely bumping its number.
	concurrentWrite func(*store.BookMetadata)
	// staleFor makes the first n applies lose the revision race, as a
	// concurrent writer would.
	staleFor int
	applyErr error
	readErr  error
}

func (f *fakeMetadataStore) CatalogBookMetadata(
	_ context.Context, _, _ string, _ store.LibraryRole,
) (store.BookMetadata, error) {
	f.reads++
	if f.readErr != nil {
		return store.BookMetadata{}, f.readErr
	}
	return f.current, nil
}

func (f *fakeMetadataStore) ApplyCatalogBookMetadata(
	_ context.Context, _ string, request store.ApplyBookMetadataRequest,
) (store.BookMetadata, error) {
	f.applies = append(f.applies, request)
	if f.applyErr != nil {
		return store.BookMetadata{}, f.applyErr
	}
	if f.staleFor > 0 {
		f.staleFor--
		// A losing writer sees the winner's revision on its next read.
		f.current.Book.Revision++
		if f.concurrentWrite != nil {
			f.concurrentWrite(&f.current)
		}
		return store.BookMetadata{}, store.ErrStaleRevision
	}
	if request.ExpectedRevision != f.current.Book.Revision {
		return store.BookMetadata{}, store.ErrStaleRevision
	}
	applied := request.Metadata
	applied.Book.Revision = request.ExpectedRevision + 1
	// Copy the sets before rewriting them, or the request just recorded
	// would be mutated too and a later assertion would read back what the
	// store decided instead of what the caller proposed.
	applied.Tags = slices.Clone(applied.Tags)
	applied.Series = slices.Clone(applied.Series)
	applied.Contributors = slices.Clone(applied.Contributors)
	applied.Languages = slices.Clone(applied.Languages)
	applied.Identifiers = slices.Clone(applied.Identifiers)
	// The real store resolves an entity by normalized name and never
	// renames it, so a read-back returns the library's first spelling
	// rather than the one just written.
	if f.spellings == nil {
		f.spellings = map[string]string{}
	}
	for i, row := range applied.Tags {
		applied.Tags[i].Name = f.firstSpelling("tag", row.NormalizedName, row.Name)
	}
	for i, row := range applied.Series {
		applied.Series[i].Name = f.firstSpelling("series", row.NormalizedName, row.Name)
	}
	for i, row := range applied.Contributors {
		applied.Contributors[i].Name = f.firstSpelling(
			"contributor", row.NormalizedName, row.Name)
	}
	// The real queries order every set, so a read-back does not return rows
	// in the order they were written.
	slices.SortFunc(applied.Tags, func(a, b store.BookTaxon) int {
		return strings.Compare(a.NormalizedName, b.NormalizedName)
	})
	slices.SortFunc(applied.Languages, func(a, b store.BookLanguage) int {
		return strings.Compare(a.Language, b.Language)
	})
	slices.SortFunc(applied.Series, func(a, b store.BookSeries) int {
		return strings.Compare(a.NormalizedName, b.NormalizedName)
	})
	slices.SortFunc(applied.Identifiers, func(a, b store.BookIdentifier) int {
		if scheme := strings.Compare(a.Scheme, b.Scheme); scheme != 0 {
			return scheme
		}
		return strings.Compare(a.Value, b.Value)
	})
	slices.SortFunc(applied.Contributors, func(a, b store.BookContributor) int {
		if role := strings.Compare(a.Role, b.Role); role != 0 {
			return role
		}
		return a.Position - b.Position
	})
	f.current = applied
	return applied, nil
}

func (f *fakeMetadataStore) firstSpelling(kind, normalized, display string) string {
	key := kind + "|" + normalized
	if existing, ok := f.spellings[key]; ok {
		return existing
	}
	f.spellings[key] = display
	return display
}

func materializeJob(t *testing.T, embedded epub.Metadata, path string) store.IngestJob {
	t.Helper()
	bookID := "book-1"
	job := store.IngestJob{
		ID: "job-1", UserID: "user-1", LibraryID: "lib-1",
		State: store.IngestPromoted, BookID: &bookID,
	}
	snapshot, err := json.Marshal(embedded)
	if err != nil {
		t.Fatal(err)
	}
	job.ExtractedEmbeddedMetadataJSON = snapshot
	if path != "" {
		job.SourceRelativePath = &path
	}
	return job
}

func emptyMetadata() store.BookMetadata {
	return store.BookMetadata{Book: store.CatalogBook{
		ID: "book-1", LibraryID: "lib-1", Status: store.BookActive, Revision: 1,
	}}
}

func clockAt(at time.Time) func() time.Time { return func() time.Time { return at } }

func TestMaterializeAppliesEmbeddedThenPath(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata()}
	job := materializeJob(t, epub.Metadata{
		Title:        "Dune",
		Languages:    []string{"en"},
		Contributors: []epub.Contributor{{Name: "Alia Translator", Role: "translator"}},
	}, "Frank Herbert/Dune.epub")

	applied, changed, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(), clockAt(now))
	if err != nil || !changed {
		t.Fatalf("materialize: changed=%v err=%v", changed, err)
	}
	if len(st.applies) != 1 || st.applies[0].ExpectedRevision != 1 {
		t.Fatalf("applies: %+v", st.applies)
	}
	if applied.Book.Title != "Dune" ||
		applied.Book.TitleSource != store.MetadataFilename {
		t.Fatalf("title: %+v", applied.Book)
	}
	// The path names one author and must not remove the translator the file
	// declared. Applying the path second is also the one observable
	// difference between the two orders: rows already known keep their
	// index and the later proposal's rows are appended after them.
	if len(applied.Contributors) != 2 {
		t.Fatalf("contributors: %+v", applied.Contributors)
	}
	// Rows are read back ordered by role, so the author leads; the index it
	// carries is the one the write order gave it, and that index is the
	// only thing the order of the two proposals decides.
	if applied.Contributors[0].Role != "author" ||
		applied.Contributors[0].Name != "Frank Herbert" ||
		applied.Contributors[0].Position != 1 ||
		applied.Contributors[0].Source != store.MetadataFilename {
		t.Fatalf("author: %+v", applied.Contributors[0])
	}
	if applied.Contributors[1].Role != "translator" ||
		applied.Contributors[1].Position != 0 ||
		applied.Contributors[1].Source != store.MetadataEmbedded {
		t.Fatalf("translator: %+v", applied.Contributors[1])
	}
}

func TestMaterializeIsIdempotent(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata()}
	job := materializeJob(t, epub.Metadata{Title: "Dune"}, "Frank Herbert/Dune.epub")

	if _, changed, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(),
		clockAt(now)); err != nil || !changed {
		t.Fatalf("first pass: changed=%v err=%v", changed, err)
	}
	_, changed, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(), clockAt(now))
	if err != nil {
		t.Fatal(err)
	}
	if changed || len(st.applies) != 1 {
		t.Fatalf("second pass wrote again: changed=%v applies=%d",
			changed, len(st.applies))
	}
	if st.reads != 2 {
		t.Fatalf("second pass did not re-read: reads=%d", st.reads)
	}
}

func TestMaterializeRetriesStaleRevision(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata(), staleFor: 2}
	job := materializeJob(t, epub.Metadata{Title: "Dune"}, "")

	applied, changed, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(), clockAt(now))
	if err != nil || !changed {
		t.Fatalf("materialize: changed=%v err=%v", changed, err)
	}
	if len(st.applies) != 3 {
		t.Fatalf("attempts: %d", len(st.applies))
	}
	// Every retry re-reads, so it never replays a stale expected revision.
	if st.applies[1].ExpectedRevision == st.applies[0].ExpectedRevision ||
		st.applies[2].ExpectedRevision == st.applies[1].ExpectedRevision {
		t.Fatalf("retry reused an expected revision: %+v", st.applies)
	}
	if applied.Book.Title != "Dune" {
		t.Fatalf("applied: %+v", applied.Book)
	}
}

func TestMaterializeGivesUpAfterBoundedRetries(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata(), staleFor: 99}
	job := materializeJob(t, epub.Metadata{Title: "Dune"}, "")

	if _, _, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(),
		clockAt(now)); !errors.Is(err, store.ErrStaleRevision) {
		t.Fatalf("bounded retry: %v", err)
	}
	if len(st.applies) != metadataApplyAttempts {
		t.Fatalf("attempts: %d", len(st.applies))
	}
}

func TestMaterializeRejectsUnreadableSnapshot(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata()}
	job := materializeJob(t, epub.Metadata{}, "")
	job.ExtractedEmbeddedMetadataJSON = []byte(`{"title":`)

	if _, _, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(),
		clockAt(now)); !errors.Is(err, ErrMetadataSnapshotInvalid) {
		t.Fatalf("invalid snapshot: %v", err)
	}
	if len(st.applies) != 0 {
		t.Fatalf("wrote from an invalid snapshot: %+v", st.applies)
	}
}

func TestMaterializeWithoutEvidenceWritesNothing(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata()}
	bookID := "book-1"
	job := store.IngestJob{
		ID: "job-1", UserID: "user-1", LibraryID: "lib-1",
		State: store.IngestPromoted, BookID: &bookID,
	}
	if _, changed, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(),
		clockAt(now)); err != nil || changed {
		t.Fatalf("no evidence: changed=%v err=%v", changed, err)
	}
	if len(st.applies) != 0 {
		t.Fatalf("wrote without evidence: %+v", st.applies)
	}
}

// An unparseable path is not evidence. It must leave the fields it could not
// determine unset rather than guessing from debris, and a job whose only
// candidate source is such a path is not worth a catalog read.
func TestMaterializeIgnoresUnusablePath(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata()}
	job := materializeJob(t, epub.Metadata{Title: "Dune"}, "../escape/Dune.epub")

	applied, changed, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(), clockAt(now))
	if err != nil || !changed {
		t.Fatalf("materialize: changed=%v err=%v", changed, err)
	}
	if applied.Book.TitleSource != store.MetadataEmbedded ||
		len(applied.Contributors) != 0 {
		t.Fatalf("guessed from an unusable path: %+v", applied)
	}

	pathOnly := materializeJob(t, epub.Metadata{}, "../escape/Dune.epub")
	pathOnly.ExtractedEmbeddedMetadataJSON = nil
	bare := &fakeMetadataStore{current: emptyMetadata()}
	if _, changed, err := MaterializeBookMetadata(
		context.Background(), bare, pathOnly, metadata.DefaultPathPatterns(),
		clockAt(now)); err != nil || changed {
		t.Fatalf("path-only job: changed=%v err=%v", changed, err)
	}
	if bare.reads != 0 || len(bare.applies) != 0 {
		t.Fatalf("touched the catalog for a job with no evidence: reads=%d applies=%d",
			bare.reads, len(bare.applies))
	}
}

func TestMaterializeRequiresAPromotedBook(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata()}

	noBook := materializeJob(t, epub.Metadata{Title: "Dune"}, "")
	noBook.BookID = nil
	if _, _, err := MaterializeBookMetadata(
		context.Background(), st, noBook, metadata.DefaultPathPatterns(),
		clockAt(now)); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("job without a book: %v", err)
	}

	// Only promotion sets a book id, so a job in any other state carrying
	// one is a caller mistake rather than work to do.
	unpromoted := materializeJob(t, epub.Metadata{Title: "Dune"}, "")
	unpromoted.State = store.IngestExtracted
	if _, _, err := MaterializeBookMetadata(
		context.Background(), st, unpromoted, metadata.DefaultPathPatterns(),
		clockAt(now)); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("unpromoted job: %v", err)
	}
}

func TestMaterializePropagatesReadFailure(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata(), readErr: store.ErrNotFound}
	job := materializeJob(t, epub.Metadata{Title: "Dune"}, "")

	if _, _, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(),
		clockAt(now)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("read failure: %v", err)
	}
}

// The catalog's entity rows are library-wide and keep whichever spelling the
// library saw first, so a read-back does not return what was just written.
// Materialization must still converge, or every pass over a book whose
// spelling differs from its library's rewrites it and bumps its revision.
func TestMaterializeConvergesAgainstLibrarySpelling(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{
		current: emptyMetadata(),
		spellings: map[string]string{
			"tag|science fiction":       "Science Fiction",
			"contributor|frank herbert": "Frank Herbert",
			"series|dune":               "Dune",
		},
	}
	job := materializeJob(t, epub.Metadata{
		Title:        "Dune",
		Subjects:     []string{"science fiction"},
		Series:       []epub.Series{{Name: "DUNE"}},
		Contributors: []epub.Contributor{{Name: "frank herbert", Role: "author"}},
	}, "")

	for pass := 0; pass < 3; pass++ {
		_, changed, err := MaterializeBookMetadata(
			context.Background(), st, job, metadata.DefaultPathPatterns(),
			clockAt(now))
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if pass > 0 && changed {
			t.Fatalf("pass %d rewrote the book for a spelling it does not own",
				pass)
		}
	}
	if len(st.applies) != 1 {
		t.Fatalf("applies: %d", len(st.applies))
	}
}

// A layout that had to guess where one field ended is not allowed to
// overwrite what the publication itself declared: a filename outranks
// embedded metadata, and the provenance it stamps cannot be taken back by a
// later extraction.
func TestMaterializeHoldsBackLowConfidencePaths(t *testing.T) {
	path := "Frank Herbert - Dune 1 - Dune Messiah.epub"
	if got := metadata.ParsePath(path, metadata.DefaultPathPatterns()).Confidence; got != metadata.ConfidenceLow {
		t.Fatalf("fixture no longer parses as a guess: %q", got)
	}

	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata()}
	job := materializeJob(t, epub.Metadata{Title: "Dune"}, path)

	applied, changed, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(), clockAt(now))
	if err != nil || !changed {
		t.Fatalf("materialize: changed=%v err=%v", changed, err)
	}
	if applied.Book.Title != "Dune" ||
		applied.Book.TitleSource != store.MetadataEmbedded {
		t.Fatalf("a guessed title overwrote the file's own: %+v", applied.Book)
	}
	if len(applied.Contributors) != 0 || len(applied.Series) != 0 {
		t.Fatalf("guessed rows applied: %+v", applied)
	}

	// With nothing left to assert, such a path is not worth a catalog read.
	pathOnly := materializeJob(t, epub.Metadata{}, path)
	pathOnly.ExtractedEmbeddedMetadataJSON = nil
	bare := &fakeMetadataStore{current: emptyMetadata()}
	if _, changed, err := MaterializeBookMetadata(
		context.Background(), bare, pathOnly, metadata.DefaultPathPatterns(),
		clockAt(now)); err != nil || changed {
		t.Fatalf("path-only job: changed=%v err=%v", changed, err)
	}
	if bare.reads != 0 {
		t.Fatalf("read the catalog for a path that asserts nothing: %d",
			bare.reads)
	}
}

// The writer that wins the race is usually a person editing the book, not a
// bare revision bump. When they have already supplied what this pass
// learned, the retry must find nothing left to do and write nothing.
func TestMaterializeYieldsToAConcurrentEditor(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata(), staleFor: 1}
	st.concurrentWrite = func(current *store.BookMetadata) {
		current.Book.Title = "Dune"
		current.Book.TitleSource = store.MetadataManual
		current.Book.TitleLocked = true
	}
	job := materializeJob(t, epub.Metadata{Title: "Dune Messiah"}, "")

	resolved, changed, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(), clockAt(now))
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("overwrote a manual edit: %+v", resolved.Book)
	}
	if len(st.applies) != 1 {
		t.Fatalf("retried a write it no longer had reason to make: %d",
			len(st.applies))
	}
	if resolved.Book.Title != "Dune" || !resolved.Book.TitleLocked {
		t.Fatalf("returned metadata: %+v", resolved.Book)
	}
}

// Roles are read back in their own order, so a second pass renumbers
// positions over an order the first pass never wrote. Convergence has to
// survive that, since it is what a real read-back does.
func TestMaterializeConvergesAcrossReorderedRoles(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata()}
	job := materializeJob(t, epub.Metadata{
		Title: "Dune",
		Contributors: []epub.Contributor{
			{Name: "Alia Translator", Role: "translator"},
			{Name: "Princess Irulan", Role: "editor"},
		},
	}, "Frank Herbert/Dune.epub")

	for pass := 0; pass < 3; pass++ {
		applied, changed, err := MaterializeBookMetadata(
			context.Background(), st, job, metadata.DefaultPathPatterns(),
			clockAt(now))
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if pass > 0 && changed {
			t.Fatalf("pass %d rewrote the book: %+v", pass, applied.Contributors)
		}
	}
	if len(st.applies) != 1 {
		t.Fatalf("applies: %d", len(st.applies))
	}
	if len(st.current.Contributors) != 3 {
		t.Fatalf("contributors: %+v", st.current.Contributors)
	}
}

// A guess about where one field ended says nothing about a field the layout
// read from a directory of its own, so the author survives while the title
// the parser could not explain is withheld.
func TestMaterializeKeepsWhatAPathReadFromADirectory(t *testing.T) {
	path := "Frank Herbert/Dune - Special Edition.epub"
	candidate := metadata.ParsePath(path, metadata.DefaultPathPatterns())
	if candidate.Confidence != metadata.ConfidenceLow ||
		!candidate.Guessed.Title || candidate.Guessed.Author {
		t.Fatalf("fixture no longer isolates the guess: %+v", candidate)
	}

	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata()}
	job := materializeJob(t, epub.Metadata{Title: "Dune"}, path)

	applied, changed, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(), clockAt(now))
	if err != nil || !changed {
		t.Fatalf("materialize: changed=%v err=%v", changed, err)
	}
	if applied.Book.Title != "Dune" ||
		applied.Book.TitleSource != store.MetadataEmbedded {
		t.Fatalf("a guessed title overwrote the file's own: %+v", applied.Book)
	}
	if len(applied.Contributors) != 1 ||
		applied.Contributors[0].Name != "Frank Herbert" ||
		applied.Contributors[0].Source != store.MetadataFilename {
		t.Fatalf("discarded an author read from a directory: %+v",
			applied.Contributors)
	}
}

// The editor who wins the race usually supersedes only part of what the job
// learned. The retry must then still write, under the revision the winner
// left, without touching what they locked.
func TestMaterializeWritesWhatAnEditorDidNotSupersede(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata(), staleFor: 1}
	st.concurrentWrite = func(current *store.BookMetadata) {
		current.Book.Title = "Dune"
		current.Book.TitleSource = store.MetadataManual
		current.Book.TitleLocked = true
	}
	job := materializeJob(t, epub.Metadata{
		Title:    "Dune Messiah",
		Subjects: []string{"Science Fiction"},
	}, "")

	applied, changed, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(), clockAt(now))
	if err != nil || !changed {
		t.Fatalf("materialize: changed=%v err=%v", changed, err)
	}
	if len(st.applies) != 2 {
		t.Fatalf("applies: %d", len(st.applies))
	}
	// The retry resolved against what the winner left, not against the
	// revision it first read.
	if st.applies[1].ExpectedRevision != 2 {
		t.Fatalf("retry expected revision: %d", st.applies[1].ExpectedRevision)
	}
	if applied.Book.Title != "Dune" || !applied.Book.TitleLocked ||
		applied.Book.TitleSource != store.MetadataManual {
		t.Fatalf("overwrote a locked title: %+v", applied.Book)
	}
	if len(applied.Tags) != 1 || applied.Tags[0].Name != "Science Fiction" {
		t.Fatalf("dropped what the editor never supplied: %+v", applied.Tags)
	}
}

// A publication that declared nothing usable is not evidence either, and
// its snapshot is stored all the same.
func TestMaterializeSkipsAnEmptySnapshot(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeMetadataStore{current: emptyMetadata()}
	job := materializeJob(t, epub.Metadata{}, "")

	if _, changed, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(),
		clockAt(now)); err != nil || changed {
		t.Fatalf("empty snapshot: changed=%v err=%v", changed, err)
	}
	if st.reads != 0 {
		t.Fatalf("read the catalog for a snapshot that asserts nothing: %d",
			st.reads)
	}
}
