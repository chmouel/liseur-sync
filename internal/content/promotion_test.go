//go:build linux

package content

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// fakePromotionStore models the parts of the real backend a promotion caller
// can observe: the revision check every transition makes, the cross-checks
// CommitNewBookPromotion runs against the job it is promoting, and the shared
// request validation both backends run.
type fakePromotionStore struct {
	job store.IngestJob

	listErr   error
	commitErr error

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
	return store.IngestPromotionResult{
		Job: f.job, Book: request.Book, File: request.File, Blob: request.Blob,
	}, nil
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
		StagingPath:   strptr("incoming/3a7bd3e2360a3d29eea436fcfb7e44c735d117c42d1c1835420b6b9942dd4f1b"),
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
		context.Background(), st, blobs, st.job, fixedClock(now), time.Hour)
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
	if !result.Book.CreatedAt.Equal(now) || !result.Book.UpdatedAt.Equal(now) ||
		!result.File.CreatedAt.Equal(now) || !result.File.UpdatedAt.Equal(now) {
		t.Fatalf("promotion timestamps = %v/%v and %v/%v, want %v",
			result.Book.CreatedAt, result.Book.UpdatedAt,
			result.File.CreatedAt, result.File.UpdatedAt, now)
	}
	if result.File.BookID != result.Book.ID ||
		result.File.BlobSHA256 != "3a7bd3e2360a3d29eea436fcfb7e44c735d117c42d1c1835420b6b9942dd4f1b" ||
		result.File.MediaType != mediaTypeEPUB ||
		result.File.Availability != store.BookFileAvailable {
		t.Fatalf("file = %+v", result.File)
	}
	if len(blobs.calls) != 1 ||
		blobs.calls[0] != "incoming/3a7bd3e2360a3d29eea436fcfb7e44c735d117c42d1c1835420b6b9942dd4f1b|3a7bd3e2360a3d29eea436fcfb7e44c735d117c42d1c1835420b6b9942dd4f1b|4096" {
		t.Fatalf("promote calls = %v", blobs.calls)
	}
}

// A book is created with nothing asserted about the publication. Anything the
// server claims about a book has to be resolved against rows that only exist
// once the book does, so promotion states no title, author or language it
// would later have to take back.
func TestPromoteIngestJobClaimsNothingAboutThePublication(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	st.job.ExtractedEmbeddedMetadataJSON = []byte(`{"title":"Dune"}`)
	blobs := &fakeBlobPromoter{}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	result, err := PromoteIngestJob(
		context.Background(), st, blobs, st.job, fixedClock(now), time.Hour)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if result.Book.Title != "" || result.Book.Subtitle != "" ||
		result.Book.Description != "" || result.Book.Publisher != "" ||
		result.Book.PublishedDate != "" || result.Book.RawMetadataJSON != nil {
		t.Fatalf("book asserted metadata: %+v", result.Book)
	}
	if result.File.PartialMD5 != nil || result.File.DCIdentifier != nil {
		t.Fatalf("file asserted fingerprints: %+v", result.File)
	}
}

// The ids a job's book and file get are a function of that job, so a worker
// that has to build the request a second time names the same rows.
func TestPromoteIngestJobDerivesStableIdentifiers(t *testing.T) {
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)
	first := &fakePromotionStore{job: extractedJob()}
	second := &fakePromotionStore{job: extractedJob()}

	a, err := PromoteIngestJob(context.Background(), first,
		&fakeBlobPromoter{}, first.job, fixedClock(now), time.Hour)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	b, err := PromoteIngestJob(context.Background(), second,
		&fakeBlobPromoter{}, second.job, fixedClock(now.Add(time.Hour)), time.Hour)
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

	other := &fakePromotionStore{job: extractedJob()}
	other.job.ID = "job-2"
	c, err := PromoteIngestJob(context.Background(), other,
		&fakeBlobPromoter{}, other.job, fixedClock(now), time.Hour)
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
		context.Background(), st, blobs, st.job, fixedClock(now), time.Hour)
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
		context.Background(), st, blobs, st.job, fixedClock(now), time.Hour)
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
		context.Background(), st, blobs, st.job, fixedClock(now), time.Hour)
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
	}{
		{"missing stage", ErrStageMissing},
		{"digest mismatch", ErrDigestMismatch},
		{"corrupt blob", ErrCorruptBlob},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakePromotionStore{job: extractedJob()}
			blobs := &fakeBlobPromoter{err: tc.err}
			now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

			result, err := PromoteIngestJob(context.Background(), st, blobs,
				st.job, fixedClock(now), 48*time.Hour)
			if err != nil {
				t.Fatalf("promote: %v", err)
			}
			if result.Job.State != store.IngestQuarantined {
				t.Fatalf("state = %q, want quarantined", result.Job.State)
			}
			if result.Job.ErrorCode == nil ||
				*result.Job.ErrorCode != codeMissingArtifact {
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
		{"unsafe path", ErrUnsafePath},
		{"unsupported filesystem", ErrUnsupportedFilesystem},
		{"unknown", errors.New("no space left on device")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakePromotionStore{job: extractedJob()}
			blobs := &fakeBlobPromoter{err: tc.err}
			now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

			_, err := PromoteIngestJob(context.Background(), st, blobs,
				st.job, fixedClock(now), time.Hour)
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
				st.job, fixedClock(now), time.Hour)
			if !errors.Is(err, store.ErrInvalidTransition) {
				t.Fatalf("err = %v, want invalid transition", err)
			}
			if len(blobs.calls) != 0 {
				t.Fatalf("published a blob for an unpromotable job")
			}
		})
	}
}

// A job whose clock has already run past the worker's keeps its own time, so
// a promoted book never predates the job that produced it.
func TestPromoteIngestJobNeverMovesTimeBackwards(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	ahead := st.job.UpdatedAt.Add(time.Hour)
	st.job.UpdatedAt = ahead

	result, err := PromoteIngestJob(context.Background(), st,
		&fakeBlobPromoter{}, st.job,
		fixedClock(ahead.Add(-30*time.Minute)), time.Hour)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if !result.Book.CreatedAt.Equal(ahead) {
		t.Fatalf("created at = %v, want %v", result.Book.CreatedAt, ahead)
	}
}

func TestRunIngestPromotionPassCountsWhatItDid(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	blobs := &fakeBlobPromoter{}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	report, err := RunIngestPromotionPass(
		context.Background(), st, blobs, fixedClock(now), time.Hour, 25)
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
		context.Background(), st, blobs, fixedClock(now), time.Hour, 25)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if report.Skipped != 1 || report.Promoted != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunIngestPromotionPassReportsAQuarantine(t *testing.T) {
	st := &fakePromotionStore{job: extractedJob()}
	blobs := &fakeBlobPromoter{err: ErrDigestMismatch}
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

	report, err := RunIngestPromotionPass(
		context.Background(), st, blobs, fixedClock(now), time.Hour, 25)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if report.Quarantined != 1 || report.Promoted != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunIngestPromotionPassRejectsAnUnusableRequest(t *testing.T) {
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		st        ingestPromotionQueue
		blobs     ingestBlobPromoter
		clock     func() time.Time
		retention time.Duration
		batch     int
	}{
		{"no store", nil, &fakeBlobPromoter{}, fixedClock(now), time.Hour, 25},
		{"no blobs", &fakePromotionStore{}, nil, fixedClock(now), time.Hour, 25},
		{"no clock", &fakePromotionStore{}, &fakeBlobPromoter{}, nil, time.Hour, 25},
		{"no retention", &fakePromotionStore{}, &fakeBlobPromoter{}, fixedClock(now), 0, 25},
		{"empty batch", &fakePromotionStore{}, &fakeBlobPromoter{}, fixedClock(now), time.Hour, 0},
		{"huge batch", &fakePromotionStore{}, &fakeBlobPromoter{}, fixedClock(now), time.Hour, 501},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RunIngestPromotionPass(context.Background(),
				tc.st, tc.blobs, tc.clock, tc.retention, tc.batch)
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
		&fakeBlobPromoter{}, fixedClock(now), time.Hour, 25)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}
