//go:build linux

package content

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

type fakeMetadataStore struct {
	current store.BookMetadata
	reads   int
	applies []store.ApplyBookMetadataRequest
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
		return store.BookMetadata{}, store.ErrStaleRevision
	}
	applied := request.Metadata
	applied.Book.Revision = request.ExpectedRevision + 1
	f.current = applied
	return applied, nil
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
	// declared.
	if len(applied.Contributors) != 2 {
		t.Fatalf("contributors: %+v", applied.Contributors)
	}
	var roles []string
	for _, row := range applied.Contributors {
		roles = append(roles, row.Role)
	}
	if roles[0] != "translator" && roles[1] != "translator" {
		t.Fatalf("translator lost: %+v", applied.Contributors)
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
	job := materializeJob(t, epub.Metadata{Title: "Dune"}, "")
	job.BookID = nil

	if _, _, err := MaterializeBookMetadata(
		context.Background(), st, job, metadata.DefaultPathPatterns(),
		clockAt(now)); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("job without a book: %v", err)
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
