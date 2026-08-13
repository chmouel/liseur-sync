//go:build linux

package content

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/store"
)

// fakePromotionStore models the parts of the real backend a promotion caller
// can observe: the revision check every transition makes, the cross-checks
// CommitNewBookPromotion runs against the job it is promoting, and the shared
// request validation both backends run.
type fakePromotionStore struct {
	job store.IngestJob

	book store.BookMetadata

	listErr   error
	commitErr error
	applyErr  error
	replay    bool

	listed    []store.IngestState
	committed []store.CommitNewBookPromotionRequest
	transited []store.IngestJobTransition
}

func (f *fakePromotionStore) ListIngestWorkerJobs(
	_ context.Context, state store.IngestState, limit int,
) ([]store.IngestJob, error) {
	f.listed = append(f.listed, state)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if limit < 1 || f.job.State != state {
		return nil, nil
	}
	return []store.IngestJob{f.job}, nil
}

func (f *fakePromotionStore) TransitionIngestJob(
	_ context.Context, userID, jobID string, change store.IngestJobTransition,
) (store.IngestJob, error) {
	if userID != f.job.UserID || jobID != f.job.ID {
		return store.IngestJob{}, store.ErrNotFound
	}
	f.transited = append(f.transited, change)
	next, err := store.ApplyIngestTransition(f.job, change)
	if err != nil {
		return store.IngestJob{}, err
	}
	f.job = next
	return next, nil
}

func (f *fakePromotionStore) CommitNewBookPromotion(
	_ context.Context, userID, jobID string,
	request store.CommitNewBookPromotionRequest,
) (store.IngestPromotionResult, error) {
	if userID != f.job.UserID || jobID != f.job.ID {
		return store.IngestPromotionResult{}, store.ErrNotFound
	}
	f.committed = append(f.committed, request)
	if f.commitErr != nil {
		return store.IngestPromotionResult{}, f.commitErr
	}
	if err := store.ValidateNewBookPromotion(request); err != nil {
		return store.IngestPromotionResult{}, err
	}
	if f.job.State != store.IngestExtracted ||
		f.job.Revision != request.ExpectedRevision {
		return store.IngestPromotionResult{}, store.ErrStaleRevision
	}
	if f.job.ContentSHA256 == nil ||
		*f.job.ContentSHA256 != request.Blob.SHA256 ||
		f.job.BytesReceived != request.Blob.SizeBytes {
		return store.IngestPromotionResult{}, store.ErrInvalidTransition
	}
	if request.File.Source != f.job.Source ||
		!samePath(request.File.SourceRelativePath, f.job.SourceRelativePath) {
		return store.IngestPromotionResult{}, store.ErrInvalidTransition
	}
	f.job.State = store.IngestPromoted
	f.job.Revision++
	f.job.UpdatedAt = request.UpdatedAt
	f.job.BookID = &request.Book.ID
	f.book = store.BookMetadata{Book: request.Book}
	f.book.Book.Revision = 1
	return store.IngestPromotionResult{
		Job: f.job, Book: request.Book, File: request.File, Blob: request.Blob,
		Replayed: f.replay,
	}, nil
}

// The pass attaches entity sets after promoting. These model just enough for
// that call: a book the store knows about, and a revision-checked apply.
func (f *fakePromotionStore) CatalogBookMetadata(
	_ context.Context, userID, bookID string, _ store.LibraryRole,
) (store.BookMetadata, error) {
	if userID != f.job.UserID || f.book.Book.ID != bookID {
		return store.BookMetadata{}, store.ErrNotFound
	}
	return f.book, nil
}

func (f *fakePromotionStore) ApplyCatalogBookMetadata(
	_ context.Context, userID string, request store.ApplyBookMetadataRequest,
) (store.BookMetadata, error) {
	if userID != f.job.UserID {
		return store.BookMetadata{}, store.ErrNotFound
	}
	if f.applyErr != nil {
		return store.BookMetadata{}, f.applyErr
	}
	if request.ExpectedRevision != f.book.Book.Revision {
		return store.BookMetadata{}, store.ErrStaleRevision
	}
	f.book = request.Metadata
	f.book.Book.Revision++
	return f.book, nil
}

func samePath(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

// fakeBlobPromoter records what the pass asked the CAS to publish.
type fakeBlobPromoter struct {
	err   error
	calls []string
}

func (f *fakeBlobPromoter) Promote(
	_ context.Context, stagingPath, expectedSHA string, expectedSize int64,
) (Blob, error) {
	f.calls = append(f.calls, fmt.Sprintf("%s|%s|%d", stagingPath, expectedSHA, expectedSize))
	if f.err != nil {
		return Blob{}, f.err
	}
	return Blob{
		Path:   "/cas/" + expectedSHA,
		SHA256: expectedSHA,
		Size:   expectedSize,
	}, nil
}

func strptr(v string) *string { return &v }

func extractedJob() store.IngestJob {
	created := time.Date(2024, 3, 1, 8, 0, 0, 0, time.UTC)
	return store.IngestJob{
		ID:            "job-1",
		UserID:        "user-1",
		LibraryID:     "lib-1",
		QuotaUserID:   "user-1",
		Source:        store.IngestUpload,
		State:         store.IngestExtracted,
		BytesReceived: 4096,
		ContentSHA256: strptr("3a7bd3e2360a3d29eea436fcfb7e44c735d117c42d1c1835420b6b9942dd4f1b"),
		StagingPath:   strptr(contentpath.StagingPath("job-1")),
		Revision:      3,
		CreatedAt:     created,
		UpdatedAt:     created,
	}
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestPromoteIngestJobCreatesTheBookTheJobBecomes(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	blobs := &fakeBlobPromoter{}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	result, err := PromoteIngestJob(
		context.Background(), st, blobs, st.job, nil, fixedClock(now), time.Hour)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if result.Job.State != store.IngestPromoted {
		t.Fatalf("state = %q, want promoted", result.Job.State)
	}
	if result.Job.BookID == nil || *result.Job.BookID != result.Book.ID {
		t.Fatalf("job book id = %v, want %q", result.Job.BookID, result.Book.ID)
	}
	if result.Book.LibraryID != "lib-1" || result.Book.Status != store.BookActive {
		t.Fatalf("book = %+v", result.Book)
	}
	at := st.job.UpdatedAt
	if !result.Book.CreatedAt.Equal(at) || !result.Book.UpdatedAt.Equal(at) ||
		!result.File.CreatedAt.Equal(at) || !result.File.UpdatedAt.Equal(at) {
		t.Fatalf("promotion timestamps = %v/%v and %v/%v, want %v",
			result.Book.CreatedAt, result.Book.UpdatedAt,
			result.File.CreatedAt, result.File.UpdatedAt, at)
	}
	if result.File.BookID != result.Book.ID ||
		result.File.BlobSHA256 != "3a7bd3e2360a3d29eea436fcfb7e44c735d117c42d1c1835420b6b9942dd4f1b" ||
		result.File.MediaType != mediaTypeEPUB ||
		result.File.Availability != store.BookFileAvailable {
		t.Fatalf("file = %+v", result.File)
	}
	want := contentpath.StagingPath("job-1") +
		"|3a7bd3e2360a3d29eea436fcfb7e44c735d117c42d1c1835420b6b9942dd4f1b|4096"
	if len(blobs.calls) != 1 || blobs.calls[0] != want {
		t.Fatalf("promote calls = %v", blobs.calls)
	}
}

// A promoted book already knows what it is. Resolving metadata only needs
// rows to resolve against when the book already exists, so a new one's scalar
// fields belong in the promotion itself — and must be, because promoted is
// terminal and nothing would list a title-less book to finish it.
func TestPromoteIngestJobDescribesTheBookItCreates(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	st.job.ExtractedEmbeddedMetadataJSON = []byte(
		`{"title":"Dune","publisher":"Chilton"}`)
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	result, err := PromoteIngestJob(context.Background(), st,
		&fakeBlobPromoter{}, st.job, nil, fixedClock(now), time.Hour)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if result.Book.Title != "Dune" ||
		result.Book.TitleSource != store.MetadataEmbedded {
		t.Fatalf("title = %q from %q", result.Book.Title, result.Book.TitleSource)
	}
	if result.Book.Publisher != "Chilton" {
		t.Fatalf("publisher = %q", result.Book.Publisher)
	}
	// Fields the publication never mentioned stay empty rather than guessed.
	if result.Book.Subtitle != "" || result.Book.Description != "" ||
		result.Book.PublishedDate != "" {
		t.Fatalf("book invented a field: %+v", result.Book)
	}
	if result.File.PartialMD5 != nil || result.File.DCIdentifier != nil {
		t.Fatalf("file asserted fingerprints: %+v", result.File)
	}
}

// A snapshot the server cannot read is a cache it derived from a file that is
// itself valid and durable. Losing the cache must not stop the book being
// published: a title can be corrected later, an unpublished book cannot.
func TestPromoteIngestJobPublishesDespiteAnUnreadableSnapshot(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	st.job.ExtractedEmbeddedMetadataJSON = []byte(`{"title":`)
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	result, err := PromoteIngestJob(context.Background(), st,
		&fakeBlobPromoter{}, st.job, nil, fixedClock(now), time.Hour)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if result.Job.State != store.IngestPromoted {
		t.Fatalf("state = %q, want promoted", result.Job.State)
	}
	if result.Book.Title != "" {
		t.Fatalf("title = %q, want empty", result.Book.Title)
	}
}

// The ids a job's book and file get are a function of that job, so a worker
// that has to build the request a second time names the same rows.
func TestPromoteIngestJobDerivesStableIdentifiers(t *testing.T) {
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)
	first := &fakePromotionStore{job: extractedJob()}
	second := &fakePromotionStore{job: extractedJob()}

	a, err := PromoteIngestJob(context.Background(), first,
		&fakeBlobPromoter{}, first.job, nil, fixedClock(now), time.Hour)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	b, err := PromoteIngestJob(context.Background(), second,
		&fakeBlobPromoter{}, second.job, nil, fixedClock(now.Add(time.Hour)), time.Hour)
	if err != nil {
		t.Fatalf("promote again: %v", err)
	}
	if a.Book.ID != b.Book.ID || a.File.ID != b.File.ID {
		t.Fatalf("ids differ: %q/%q vs %q/%q",
			a.Book.ID, a.File.ID, b.Book.ID, b.File.ID)
	}
	if a.Book.ID == a.File.ID {
		t.Fatalf("book and file share id %q", a.Book.ID)
	}

	// The same job id in a different library must not name the same book.
	// Job ids are globally unique today, so nothing else enforces this, and
	// the ids are the only thing keeping two tenants' promotions apart.
	elsewhere := &fakePromotionStore{job: extractedJob()}
	elsewhere.job.LibraryID = "lib-2"
	d, err := PromoteIngestJob(context.Background(), elsewhere,
		&fakeBlobPromoter{}, elsewhere.job, nil, fixedClock(now), time.Hour)
	if err != nil {
		t.Fatalf("promote elsewhere: %v", err)
	}
	if d.Book.ID == a.Book.ID || d.File.ID == a.File.ID {
		t.Fatalf("a job id names the same rows in two libraries")
	}

	other := &fakePromotionStore{job: extractedJob()}
	other.job.ID = "job-2"
	other.job.StagingPath = strptr(contentpath.StagingPath("job-2"))
	c, err := PromoteIngestJob(context.Background(), other,
		&fakeBlobPromoter{}, other.job, nil, fixedClock(now), time.Hour)
	if err != nil {
		t.Fatalf("promote other: %v", err)
	}
	if c.Book.ID == a.Book.ID || c.File.ID == a.File.ID {
		t.Fatalf("distinct jobs produced shared ids")
	}
}

func TestPromoteIngestJobKeepsAWatchedFilesOrigin(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	st.job.Source = store.IngestWatched
	st.job.SourceRelativePath = strptr("Herbert, Frank/Dune.epub")
	blobs := &fakeBlobPromoter{}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	result, err := PromoteIngestJob(
		context.Background(), st, blobs, st.job, nil, fixedClock(now), time.Hour)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if result.File.Source != store.IngestWatched {
		t.Fatalf("source = %q", result.File.Source)
	}
	if result.File.SourceRelativePath == nil ||
		*result.File.SourceRelativePath != "Herbert, Frank/Dune.epub" {
		t.Fatalf("path = %v", result.File.SourceRelativePath)
	}
	if result.File.OriginalFilename != "Dune.epub" {
		t.Fatalf("filename = %q, want Dune.epub", result.File.OriginalFilename)
	}
}

// An upload was never on disk under a name anybody chose, and the job id it
// was staged as is a server detail. Reporting that id as the original
// filename would be a fabricated fact, so the field stays empty.
func TestPromoteIngestJobLeavesAnUploadUnnamed(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	blobs := &fakeBlobPromoter{}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	result, err := PromoteIngestJob(
		context.Background(), st, blobs, st.job, nil, fixedClock(now), time.Hour)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if result.File.SourceRelativePath != nil {
		t.Fatalf("upload carried a path %v", result.File.SourceRelativePath)
	}
	if result.File.OriginalFilename != "" {
		t.Fatalf("filename = %q, want empty", result.File.OriginalFilename)
	}
}

// The blob is published before the row that points at it. A blob with no row
// is collectable garbage; a row naming a blob that was never published would
// be a catalog entry nobody can read.
func TestPromoteIngestJobPublishesTheBlobBeforeCommitting(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	blobs := &fakeBlobPromoter{err: errors.New("disk full")}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	_, err := PromoteIngestJob(
		context.Background(), st, blobs, st.job, nil, fixedClock(now), time.Hour)
	if err == nil {
		t.Fatal("want error")
	}
	if len(st.committed) != 0 {
		t.Fatalf("committed %d promotions after a failed publish", len(st.committed))
	}
	if st.job.State != store.IngestExtracted {
		t.Fatalf("state = %q, want extracted", st.job.State)
	}
}

func TestPromoteIngestJobQuarantinesAnArtifactItCannotPromote(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"missing stage", ErrStageMissing, codeArtifactMissing},
		{"digest mismatch", ErrDigestMismatch, codeArtifactCorrupt},
		// A staging path the CAS refuses is deterministic for these bytes:
		// the next attempt reads the same row and fails identically.
		{"unsafe path", ErrUnsafePath, codeArtifactCorrupt},
		// The upload is blameless here, and the code has to say so or an
		// operator reads it as a bad file and never repairs the blob.
		{"corrupt stored blob", ErrCorruptBlob, codeStoredBlobCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakePromotionStore{job: extractedJob()}
			blobs := &fakeBlobPromoter{err: tc.err}
			now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

			result, err := PromoteIngestJob(context.Background(), st, blobs,
				st.job, nil, fixedClock(now), 48*time.Hour)
			if err != nil {
				t.Fatalf("promote: %v", err)
			}
			if result.Job.State != store.IngestQuarantined {
				t.Fatalf("state = %q, want quarantined", result.Job.State)
			}
			if result.Job.ErrorCode == nil ||
				*result.Job.ErrorCode != tc.code {
				t.Fatalf("error code = %v", result.Job.ErrorCode)
			}
			if result.Job.ExpiresAt == nil ||
				!result.Job.ExpiresAt.Equal(now.Add(48*time.Hour)) {
				t.Fatalf("expiry = %v", result.Job.ExpiresAt)
			}
			if len(st.committed) != 0 {
				t.Fatalf("committed a promotion for a bad artifact")
			}
		})
	}
}

// A server that is temporarily unable to publish is not a job that can never
// be published. Quarantining it would strand work whose next attempt would
// have succeeded.
func TestPromoteIngestJobRetriesAnOperationalFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"unsupported filesystem", ErrUnsupportedFilesystem},
		{"unknown", errors.New("no space left on device")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakePromotionStore{job: extractedJob()}
			blobs := &fakeBlobPromoter{err: tc.err}
			now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

			_, err := PromoteIngestJob(context.Background(), st, blobs,
				st.job, nil, fixedClock(now), time.Hour)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if len(st.transited) != 0 {
				t.Fatalf("changed durable state: %+v", st.transited)
			}
			if st.job.State != store.IngestExtracted {
				t.Fatalf("state = %q, want extracted", st.job.State)
			}
		})
	}
}

func TestPromoteIngestJobRejectsAJobItCannotPromote(t *testing.T) {
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		job  func(store.IngestJob) store.IngestJob
	}{
		{"wrong state", func(j store.IngestJob) store.IngestJob {
			j.State = store.IngestValidated
			return j
		}},
		{"already promoted", func(j store.IngestJob) store.IngestJob {
			j.State = store.IngestPromoted
			return j
		}},
		{"no digest", func(j store.IngestJob) store.IngestJob {
			j.ContentSHA256 = nil
			return j
		}},
		{"no artifact", func(j store.IngestJob) store.IngestJob {
			j.StagingPath = nil
			return j
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakePromotionStore{job: tc.job(extractedJob())}
			blobs := &fakeBlobPromoter{}
			_, err := PromoteIngestJob(context.Background(), st, blobs,
				st.job, nil, fixedClock(now), time.Hour)
			if !errors.Is(err, store.ErrInvalidTransition) {
				t.Fatalf("err = %v, want invalid transition", err)
			}
			if len(blobs.calls) != 0 {
				t.Fatalf("published a blob for an unpromotable job")
			}
		})
	}
}

// A quarantine records when the failure was seen, but never before the job's
// own last write, so a job's timeline only ever moves forward.
func TestPromoteIngestJobNeverMovesTimeBackwards(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	ahead := st.job.UpdatedAt.Add(time.Hour)
	st.job.UpdatedAt = ahead

	result, err := PromoteIngestJob(context.Background(), st,
		&fakeBlobPromoter{err: ErrDigestMismatch}, st.job, nil,
		fixedClock(ahead.Add(-30*time.Minute)), time.Hour)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if !result.Job.UpdatedAt.Equal(ahead) {
		t.Fatalf("updated at = %v, want %v", result.Job.UpdatedAt, ahead)
	}
}

// Two workers racing the same job must build byte-identical requests. The
// backend fingerprints the request to recognise a replay, so a clock reading
// anywhere in it would turn ordinary contention into a promotion conflict and
// the loser would fail instead of reading back the winner's book.
func TestPromoteIngestJobBuildsTheSameRequestOnEveryClock(t *testing.T) {
	job := extractedJob()
	blob := Blob{SHA256: *job.ContentSHA256, Size: job.BytesReceived}

	fingerprint := func(r store.CommitNewBookPromotionRequest) string {
		t.Helper()
		got, err := store.NewBookPromotionFingerprint(r)
		if err != nil {
			t.Fatalf("fingerprint: %v", err)
		}
		return got
	}
	first := newBookPromotion(job, blob, nil)
	if fingerprint(first) != fingerprint(newBookPromotion(job, blob, nil)) {
		t.Fatal("identical inputs produced different fingerprints")
	}
	// Pin the values, not just their agreement: two workers agreeing on the
	// same wrong-but-derived time would still pass the comparison above.
	if !first.UpdatedAt.Equal(job.UpdatedAt) ||
		!first.Book.CreatedAt.Equal(job.UpdatedAt) ||
		!first.Book.UpdatedAt.Equal(job.UpdatedAt) ||
		!first.File.CreatedAt.Equal(job.UpdatedAt) ||
		!first.File.UpdatedAt.Equal(job.UpdatedAt) {
		t.Fatalf("request carries a time the job did not: %+v", first)
	}

	st := &fakePromotionStore{job: job}
	if _, err := PromoteIngestJob(context.Background(), st,
		&fakeBlobPromoter{}, job, nil,
		fixedClock(job.UpdatedAt.Add(97*time.Minute)), time.Hour); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(st.committed) != 1 {
		t.Fatalf("committed %d requests", len(st.committed))
	}
	if fingerprint(st.committed[0]) != fingerprint(first) {
		t.Fatalf("a later clock changed the request fingerprint")
	}
}

// A staging path the server did not write is a corrupted record, not a file
// to hand to the CAS.
func TestPromoteIngestJobRefusesAForeignStagingPath(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	st.job.StagingPath = strptr(contentpath.StagingPath("someone-elses-job"))
	blobs := &fakeBlobPromoter{}

	_, err := PromoteIngestJob(context.Background(), st, blobs, st.job, nil,
		fixedClock(time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)), time.Hour)
	if !errors.Is(err, store.ErrInvariantViolation) {
		t.Fatalf("err = %v, want invariant violation", err)
	}
	if len(blobs.calls) != 0 {
		t.Fatalf("handed a foreign path to the CAS")
	}
}

func TestRunIngestPromotionPassCountsWhatItDid(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	blobs := &fakeBlobPromoter{}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	report, err := RunIngestPromotionPass(
		context.Background(), st, blobs, FixedPatterns(nil), fixedClock(now), time.Hour, 25)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if report.Promoted != 1 || report.Quarantined != 0 || report.Skipped != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(st.listed) != 1 || st.listed[0] != store.IngestExtracted {
		t.Fatalf("listed = %v, want extracted", st.listed)
	}
}

// Another worker winning the same job is ordinary contention, not a failure:
// the pass leaves it to the winner and keeps going.
func TestRunIngestPromotionPassSkipsAJobAnotherWorkerTook(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	st.commitErr = store.ErrStaleRevision
	blobs := &fakeBlobPromoter{}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	report, err := RunIngestPromotionPass(
		context.Background(), st, blobs, FixedPatterns(nil), fixedClock(now), time.Hour, 25)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if report.Skipped != 1 || report.Promoted != 0 {
		t.Fatalf("report = %+v", report)
	}
}

// A worker that lost the race read back a book it did not create. Counting
// that as a promotion would report two promotions for one book and hide the
// contention entirely.
func TestRunIngestPromotionPassCountsAReplaySeparately(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	st.replay = true
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	report, err := RunIngestPromotionPass(context.Background(), st,
		&fakeBlobPromoter{}, FixedPatterns(nil), fixedClock(now), time.Hour, 25)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if report.Replayed != 1 || report.Promoted != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunIngestPromotionPassReportsAQuarantine(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	blobs := &fakeBlobPromoter{err: ErrDigestMismatch}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	report, err := RunIngestPromotionPass(
		context.Background(), st, blobs, FixedPatterns(nil), fixedClock(now), time.Hour, 25)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if report.Quarantined != 1 || report.Promoted != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunIngestPromotionPassRejectsAnUnusableRequest(t *testing.T) {
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)
	all := FixedPatterns(nil)
	for _, tc := range []struct {
		name      string
		st        ingestPromotionQueue
		blobs     ingestBlobPromoter
		patterns  PatternResolver
		clock     func() time.Time
		retention time.Duration
		batch     int
	}{
		{"no store", nil, &fakeBlobPromoter{}, all, fixedClock(now), time.Hour, 25},
		{"no blobs", &fakePromotionStore{}, nil, all, fixedClock(now), time.Hour, 25},
		{"no patterns", &fakePromotionStore{}, &fakeBlobPromoter{}, nil, fixedClock(now), time.Hour, 25},
		{"no clock", &fakePromotionStore{}, &fakeBlobPromoter{}, all, nil, time.Hour, 25},
		{"no retention", &fakePromotionStore{}, &fakeBlobPromoter{}, all, fixedClock(now), 0, 25},
		{"empty batch", &fakePromotionStore{}, &fakeBlobPromoter{}, all, fixedClock(now), time.Hour, 0},
		{"huge batch", &fakePromotionStore{}, &fakeBlobPromoter{}, all, fixedClock(now), time.Hour, 501},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RunIngestPromotionPass(context.Background(),
				tc.st, tc.blobs, tc.patterns, tc.clock, tc.retention, tc.batch)
			if !errors.Is(err, store.ErrInvalidTransition) {
				t.Fatalf("err = %v, want invalid transition", err)
			}
		})
	}
}

func TestRunIngestPromotionPassPropagatesAListingFailure(t *testing.T) {
	sentinel := errors.New("database is gone")
	st := &fakePromotionStore{job: extractedJob(), listErr: sentinel}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	_, err := RunIngestPromotionPass(context.Background(), st,
		&fakeBlobPromoter{}, FixedPatterns(nil), fixedClock(now), time.Hour, 25)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}
