//go:build linux

package content

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

type abandonedFixture struct {
	st      *sqlite.Store
	cas     *CAS
	user    store.User
	library store.Library
	now     time.Time
}

func newAbandonedFixture(t *testing.T) *abandonedFixture {
	t.Helper()
	root := t.TempDir()
	cas, err := Open(filepath.Join(root, "content"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cas.Close() })
	st, err := sqlite.Open(filepath.Join(root, "liseur.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.April, 1, 9, 0, 0, 0, time.UTC)
	f := &abandonedFixture{st: st, cas: cas, now: now,
		user: storetest.MkUser(t, st, "abandoner")}
	f.library = store.Library{
		ID: "lib-1", OwnerUserID: f.user.ID, QuotaUserID: f.user.ID,
		Kind: store.LibraryManaged, Name: "Books", CreatedAt: now,
	}
	if err := st.CreateLibrary(t.Context(), f.library); err != nil {
		t.Fatal(err)
	}
	return f
}

// crash reproduces the only way an upload is ever orphaned: the bytes
// reach the disk and the process dies before the database is told.
func (f *abandonedFixture) crash(t *testing.T, id string, size int) store.IngestJob {
	t.Helper()
	job, created, err := f.st.CreateIngestJob(t.Context(), f.user.ID,
		store.IngestJobRequest{
			ID: id, LibraryID: f.library.ID, Source: store.IngestUpload,
			RequestFingerprint: "request-" + id, CreatedAt: f.now,
		})
	if err != nil || !created {
		t.Fatal(err)
	}
	if _, err := f.cas.Stage(t.Context(), job.ID,
		bytes.NewReader(make([]byte, size)), int64(size)); err != nil {
		t.Fatal(err)
	}
	return job
}

func (f *abandonedFixture) job(t *testing.T, id string) store.IngestJob {
	t.Helper()
	job, err := f.st.IngestJobByID(t.Context(), f.user.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

// TestSweepReclaimsInterruptedUploads is the whole point: nothing else
// looks at these jobs, so without the sweep their bytes stay on the disk
// for the life of the installation.
func TestSweepReclaimsInterruptedUploads(t *testing.T) {
	f := newAbandonedFixture(t)
	f.crash(t, "job-a", 40)
	f.crash(t, "job-b", 60)

	report, err := SweepAbandonedUploads(t.Context(), f.st, f.cas, f.now,
		time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 2 || report.Skipped != 0 {
		t.Fatalf("report = %+v, want 2 failed", report)
	}
	if used := incomingSize(t, f.cas); used != 0 {
		t.Fatalf("%d bytes still staged after the sweep", used)
	}
	// The job has to become terminal too, or the next upload with the
	// same idempotency key re-enters a job whose bytes are gone.
	for _, id := range []string{"job-a", "job-b"} {
		job := f.job(t, id)
		if job.State != store.IngestFailed {
			t.Fatalf("%s = %q, want failed", id, job.State)
		}
		if job.ErrorCode == nil || *job.ErrorCode != codeUploadAbandoned {
			t.Fatalf("%s error code = %v", id, job.ErrorCode)
		}
		if job.ExpiresAt == nil {
			t.Fatalf("%s has no expiry, so its row is never purged", id)
		}
	}
}

// TestSweepLeavesUploadsThatGotTheirBytesRecorded: once a job is past
// `received` its artifact is referenced, and deleting it would destroy an
// upload that succeeded.
func TestSweepLeavesUploadsThatGotTheirBytesRecorded(t *testing.T) {
	f := newAbandonedFixture(t)
	job := f.crash(t, "job-committed", 30)
	staged, err := f.cas.Stage(t.Context(), job.ID,
		bytes.NewReader(make([]byte, 30)), 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CommitIngestStage(t.Context(), f.user.ID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: job.Revision,
			Artifact:         store.BlobInfo{SHA256: staged.SHA256, SizeBytes: staged.Size},
			StagingPath:      staged.Path,
			UpdatedAt:        f.now,
		}); err != nil {
		t.Fatal(err)
	}

	report, err := SweepAbandonedUploads(t.Context(), f.st, f.cas, f.now,
		time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 0 {
		t.Fatalf("report = %+v, want nothing swept", report)
	}
	if f.job(t, job.ID).State != store.IngestStaged {
		t.Fatalf("committed job was disturbed: %+v", f.job(t, job.ID))
	}
	if used := incomingSize(t, f.cas); used != 30 {
		t.Fatalf("staged bytes = %d, want the committed 30 left alone", used)
	}
}

// TestSweepFreesTheStagingCap ties the two together: orphans that no
// table mentions are still charged against the cap, so leaving them there
// eventually refuses real uploads.
func TestSweepFreesTheStagingCap(t *testing.T) {
	f := newAbandonedFixture(t)
	f.crash(t, "job-a", 90)
	f.cas.SetStagingCap(100)
	if _, err := f.cas.Stage(t.Context(), "fresh",
		bytes.NewReader(make([]byte, 50)), 50); !errors.Is(err, ErrStagingFull) {
		t.Fatalf("upload beside an orphan: %v, want ErrStagingFull", err)
	}
	if _, err := SweepAbandonedUploads(t.Context(), f.st, f.cas, f.now,
		time.Hour, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cas.Stage(t.Context(), "fresh",
		bytes.NewReader(make([]byte, 50)), 50); err != nil {
		t.Fatalf("upload after the sweep: %v", err)
	}
}

// TestSweepPagesThroughEveryOrphan: a crash under load leaves many, and
// stopping at one page would leave the rest to accumulate across restarts.
func TestSweepPagesThroughEveryOrphan(t *testing.T) {
	f := newAbandonedFixture(t)
	for _, id := range []string{"job-a", "job-b", "job-c", "job-d", "job-e"} {
		f.crash(t, id, 10)
	}
	report, err := SweepAbandonedUploads(t.Context(), f.st, f.cas, f.now,
		time.Hour, 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 5 {
		t.Fatalf("report = %+v, want all five", report)
	}
	if used := incomingSize(t, f.cas); used != 0 {
		t.Fatalf("%d bytes left after paging", used)
	}
}

// TestSweepToleratesAJobWithNoBytes: the crash may have landed before the
// stage was written. The job still has to be terminalized, or it sits in
// `received` forever and blocks its own idempotency key.
func TestSweepToleratesAJobWithNoBytes(t *testing.T) {
	f := newAbandonedFixture(t)
	if _, _, err := f.st.CreateIngestJob(t.Context(), f.user.ID,
		store.IngestJobRequest{
			ID: "job-empty", LibraryID: f.library.ID, Source: store.IngestUpload,
			RequestFingerprint: "request-empty", CreatedAt: f.now,
		}); err != nil {
		t.Fatal(err)
	}
	report, err := SweepAbandonedUploads(t.Context(), f.st, f.cas, f.now,
		time.Hour, 100)
	if err != nil {
		t.Fatalf("sweep over a job with no stage: %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("report = %+v, want the job failed", report)
	}
	if f.job(t, "job-empty").State != store.IngestFailed {
		t.Fatal("a job with no bytes was left in received")
	}
}

// TestSweepClaimsTheJobBeforeDeletingItsBytes pins the order. Deleting
// first asks whether the upload was really abandoned only after the file
// is gone, so a second server sharing the content root loses a book that
// it had just committed.
func TestSweepClaimsTheJobBeforeDeletingItsBytes(t *testing.T) {
	f := newAbandonedFixture(t)
	job := f.crash(t, "job-a", 40)
	stage := &orderRecordingArtifacts{cas: f.cas}
	recorder := &orderRecordingStore{Store: f.st, order: &stage.order}

	if _, err := SweepAbandonedUploads(t.Context(), recorder, stage, f.now,
		time.Hour, 100); err != nil {
		t.Fatal(err)
	}
	want := []string{"transition:" + job.ID, "remove:" + contentpath.StagingPath(job.ID)}
	if len(stage.order) != 2 || stage.order[0] != want[0] || stage.order[1] != want[1] {
		t.Fatalf("order = %v, want %v", stage.order, want)
	}
}

type orderRecordingArtifacts struct {
	cas   *CAS
	order []string
}

func (a *orderRecordingArtifacts) RemoveStage(ctx context.Context, path string) error {
	a.order = append(a.order, "remove:"+path)
	return a.cas.RemoveStage(ctx, path)
}

type orderRecordingStore struct {
	*sqlite.Store
	order *[]string
}

func (s *orderRecordingStore) TransitionIngestJob(
	ctx context.Context, userID, jobID string, tr store.IngestJobTransition,
) (store.IngestJob, error) {
	*s.order = append(*s.order, "transition:"+jobID)
	return s.Store.TransitionIngestJob(ctx, userID, jobID, tr)
}

// TestSweepRefusesNonsenseArguments: it deletes files, so a caller that
// got its wiring wrong must be stopped rather than allowed to run with a
// zero clock or an unbounded page.
func TestSweepRefusesNonsenseArguments(t *testing.T) {
	f := newAbandonedFixture(t)
	now := f.now
	cases := []struct {
		name      string
		st        abandonedUploadStore
		artifacts abandonedUploadArtifacts
		now       time.Time
		retention time.Duration
		page      int
	}{
		{"no store", nil, f.cas, now, time.Hour, 10},
		{"no artifacts", f.st, nil, now, time.Hour, 10},
		{"zero clock", f.st, f.cas, time.Time{}, time.Hour, 10},
		{"no retention", f.st, f.cas, now, 0, 10},
		{"zero page", f.st, f.cas, now, time.Hour, 0},
		{"oversized page", f.st, f.cas, now, time.Hour, 501},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SweepAbandonedUploads(t.Context(), tc.st,
				tc.artifacts, tc.now, tc.retention, tc.page,
			); !errors.Is(err, store.ErrInvalidTransition) {
				t.Fatalf("err = %v, want ErrInvalidTransition", err)
			}
		})
	}
}

// TestSweepStopsWhenBytesCannotBeDeleted: a disk that refuses deletes is
// not something to carry on through quietly, since every later upload is
// charged for the space this one still holds.
func TestSweepStopsWhenBytesCannotBeDeleted(t *testing.T) {
	f := newAbandonedFixture(t)
	job := f.crash(t, "job-a", 40)
	boom := errors.New("read-only file system")

	_, err := SweepAbandonedUploads(t.Context(), f.st,
		failingArtifacts{err: boom}, f.now, time.Hour, 100)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the delete failure", err)
	}
	if got := f.job(t, job.ID); got.State != store.IngestFailed {
		t.Fatalf("job = %q, want the claim to have stuck", got.State)
	}
	if used := incomingSize(t, f.cas); used != 40 {
		t.Fatalf("staged bytes = %d, want the undeleted 40", used)
	}
}

type failingArtifacts struct{ err error }

func (a failingArtifacts) RemoveStage(context.Context, string) error {
	return a.err
}

// TestSweepSparesAnUploadThatCommittedUnderIt reproduces what a second
// server sharing one content root does: the sweep reads a job as
// `received`, the other process commits it, and the file is now a real
// book. Deleting it would be silent data loss.
func TestSweepSparesAnUploadThatCommittedUnderIt(t *testing.T) {
	f := newAbandonedFixture(t)
	job := f.crash(t, "job-racy", 40)
	staged, err := f.cas.Stage(t.Context(), job.ID,
		bytes.NewReader(make([]byte, 40)), 40)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CommitIngestStage(t.Context(), f.user.ID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: job.Revision,
			Artifact:         store.BlobInfo{SHA256: staged.SHA256, SizeBytes: staged.Size},
			StagingPath:      staged.Path,
			UpdatedAt:        f.now,
		}); err != nil {
		t.Fatal(err)
	}

	// The sweep still holds the pre-commit view of the row.
	report, err := SweepAbandonedUploads(t.Context(),
		staleListStore{Store: f.st, stale: []store.IngestJob{job}},
		f.cas, f.now, time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.Skipped != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v, want the job skipped", report)
	}
	if got := f.job(t, job.ID); got.State != store.IngestStaged {
		t.Fatalf("job = %q, want it left staged", got.State)
	}
	if used := incomingSize(t, f.cas); used != 40 {
		t.Fatalf("staged bytes = %d, want the committed upload intact", used)
	}
}

// staleListStore answers with a snapshot taken before the rows moved,
// which is the only way to reach the race deterministically.
type staleListStore struct {
	*sqlite.Store
	stale []store.IngestJob
}

func (s staleListStore) ListAbandonedIngestJobs(
	_ context.Context, afterID string, _ int,
) ([]store.IngestJob, error) {
	if afterID != "" {
		return nil, nil
	}
	return s.stale, nil
}
