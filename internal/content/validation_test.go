//go:build linux

package content

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

type validationStoreFake struct {
	job         store.IngestJob
	transitions int
}

func (f *validationStoreFake) TransitionIngestJob(
	_ context.Context,
	userID, jobID string,
	change store.IngestJobTransition,
) (store.IngestJob, error) {
	if f.job.UserID != userID || f.job.ID != jobID {
		return store.IngestJob{}, store.ErrNotFound
	}
	next, err := store.ApplyIngestTransition(f.job, change)
	if err != nil {
		return store.IngestJob{}, err
	}
	f.job = next
	f.transitions++
	return next, nil
}

type validationArtifactFake struct {
	publication  epub.Result
	location     ArtifactLocation
	err          error
	errorsByPath map[string]error
	onValidate   func()
}

func (f *validationArtifactFake) ValidateEPUBArtifact(
	_ context.Context,
	stagingPath string,
	_ string,
	_ int64,
	_ epub.Limits,
) (epub.Result, ArtifactLocation, error) {
	if f.onValidate != nil {
		f.onValidate()
	}
	if err := f.errorsByPath[stagingPath]; err != nil {
		return epub.Result{}, f.location, err
	}
	return f.publication, f.location, f.err
}

type validationQueueFake struct {
	jobs             map[string]store.IngestJob
	transitionErrors map[string]error
}

func (f *validationQueueFake) ListIngestWorkerJobs(
	_ context.Context,
	state store.IngestState,
	limit int,
) ([]store.IngestJob, error) {
	var jobs []store.IngestJob
	for _, job := range f.jobs {
		if job.State != state {
			continue
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].UpdatedAt.Equal(jobs[j].UpdatedAt) {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].UpdatedAt.Before(jobs[j].UpdatedAt)
	})
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}

func (f *validationQueueFake) TransitionIngestJob(
	_ context.Context,
	userID, jobID string,
	change store.IngestJobTransition,
) (store.IngestJob, error) {
	if err := f.transitionErrors[jobID]; err != nil {
		return store.IngestJob{}, err
	}
	job, ok := f.jobs[jobID]
	if !ok || job.UserID != userID {
		return store.IngestJob{}, store.ErrNotFound
	}
	next, err := store.ApplyIngestTransition(job, change)
	if err != nil {
		return store.IngestJob{}, err
	}
	f.jobs[jobID] = next
	return next, nil
}

func stagedValidationJob(now time.Time) store.IngestJob {
	sha := digestOf([]byte("validation"))
	path := contentpath.StagingPath("validation-job")
	return store.IngestJob{
		ID: "validation-job", UserID: "user", State: store.IngestStaged,
		Revision: 2, ContentSHA256: &sha, StagingPath: &path,
		BytesReceived: 10, CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Minute),
	}
}

func validationClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

func TestValidateIngestJobAdvancesValidContent(t *testing.T) {
	now := time.Now().UTC()
	job := stagedValidationJob(now)
	st := &validationStoreFake{job: job}
	artifacts := &validationArtifactFake{
		publication: epub.Result{PackagePath: "OPS/book.opf"},
		location:    ArtifactStaged,
	}
	result, err := ValidateIngestJob(
		context.Background(), st, artifacts, job, validationClock(now),
		24*time.Hour, epub.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if result.Job.State != store.IngestValidated ||
		result.Publication == nil ||
		result.Publication.PackagePath != "OPS/book.opf" ||
		result.Location != ArtifactStaged || st.transitions != 1 {
		t.Fatalf("validation result: %+v", result)
	}
}

func TestValidateIngestJobQuarantinesContentFailure(t *testing.T) {
	now := time.Now().UTC()
	job := stagedValidationJob(now)
	st := &validationStoreFake{job: job}
	artifacts := &validationArtifactFake{
		location: ArtifactPromoted,
		err: &epub.ValidationError{
			Code: epub.CodeUnsupportedDRM, Err: errors.New("DRM"),
		},
	}
	result, err := ValidateIngestJob(
		context.Background(), st, artifacts, job, validationClock(now),
		24*time.Hour, epub.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if result.Job.State != store.IngestQuarantined ||
		result.Job.ErrorCode == nil ||
		*result.Job.ErrorCode != string(epub.CodeUnsupportedDRM) ||
		result.Job.ExpiresAt == nil ||
		result.Location != ArtifactPromoted ||
		result.Publication != nil || st.transitions != 1 {
		t.Fatalf("quarantined validation result: %+v", result)
	}
}

func TestValidateIngestJobTimestampsAfterValidation(t *testing.T) {
	startedAt := time.Now().UTC()
	finishedAt := startedAt.Add(2 * time.Hour)
	current := startedAt
	job := stagedValidationJob(startedAt)
	st := &validationStoreFake{job: job}
	artifacts := &validationArtifactFake{
		err: &epub.ValidationError{
			Code: epub.CodeInvalidEPUB, Err: errors.New("invalid"),
		},
		onValidate: func() { current = finishedAt },
	}
	result, err := ValidateIngestJob(
		context.Background(), st, artifacts, job,
		func() time.Time { return current }, time.Hour, epub.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Job.UpdatedAt.Equal(finishedAt) ||
		result.Job.ExpiresAt == nil ||
		!result.Job.ExpiresAt.Equal(finishedAt.Add(time.Hour)) {
		t.Fatalf("post-validation timestamps: %+v", result.Job)
	}
}

func TestValidateIngestJobLeavesOperationalFailureRetryable(t *testing.T) {
	now := time.Now().UTC()
	job := stagedValidationJob(now)
	st := &validationStoreFake{job: job}
	ioErr := errors.New("read failed")
	artifacts := &validationArtifactFake{err: ioErr}
	if _, err := ValidateIngestJob(
		context.Background(), st, artifacts, job, validationClock(now),
		24*time.Hour, epub.DefaultLimits()); !errors.Is(err, ioErr) {
		t.Fatalf("operational validation error: %v", err)
	}
	if st.transitions != 0 || st.job.State != store.IngestStaged {
		t.Fatalf("operational failure changed job: %+v", st.job)
	}
}

func TestRunIngestValidationPassProcessesBoundedBatches(t *testing.T) {
	now := time.Now().UTC()
	jobs := make(map[string]store.IngestJob)
	for index, id := range []string{"job-a", "job-b", "job-c"} {
		job := stagedValidationJob(now)
		path := contentpath.StagingPath(id)
		job.ID = id
		job.StagingPath = &path
		job.UpdatedAt = now.Add(time.Duration(index-3) * time.Minute)
		jobs[id] = job
	}
	queue := &validationQueueFake{jobs: jobs}
	invalidPath := *jobs["job-b"].StagingPath
	artifacts := &validationArtifactFake{
		publication: epub.Result{PackagePath: "OPS/book.opf"},
		location:    ArtifactStaged,
		errorsByPath: map[string]error{
			invalidPath: &epub.ValidationError{
				Code: epub.CodeInvalidEPUB, Err: errors.New("invalid"),
			},
		},
	}
	report, err := RunIngestValidationPass(
		context.Background(), queue, artifacts, validationClock(now),
		24*time.Hour, epub.DefaultLimits(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Validated != 1 || report.Quarantined != 1 ||
		report.Skipped != 0 {
		t.Fatalf("first validation pass report: %+v", report)
	}
	if queue.jobs["job-a"].State != store.IngestValidated ||
		queue.jobs["job-b"].State != store.IngestQuarantined ||
		queue.jobs["job-c"].State != store.IngestStaged {
		t.Fatalf("first validation pass jobs: %+v", queue.jobs)
	}
	report, err = RunIngestValidationPass(
		context.Background(), queue, artifacts, validationClock(now),
		24*time.Hour, epub.DefaultLimits(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Validated != 1 || report.Quarantined != 0 ||
		report.Skipped != 0 ||
		queue.jobs["job-c"].State != store.IngestValidated {
		t.Fatalf("second validation pass: %+v %+v", report, queue.jobs)
	}
}

func TestRunIngestValidationPassStopsOnOperationalError(t *testing.T) {
	now := time.Now().UTC()
	job := stagedValidationJob(now)
	queue := &validationQueueFake{
		jobs: map[string]store.IngestJob{job.ID: job},
	}
	ioErr := errors.New("read failed")
	artifacts := &validationArtifactFake{err: ioErr}
	report, err := RunIngestValidationPass(
		context.Background(), queue, artifacts, validationClock(now),
		time.Hour, epub.DefaultLimits(), 10)
	if !errors.Is(err, ioErr) || report.Validated != 0 ||
		queue.jobs[job.ID].State != store.IngestStaged {
		t.Fatalf("operational validation pass: %+v %v", report, err)
	}
}

func TestRunIngestValidationPassSkipsStaleRevision(t *testing.T) {
	now := time.Now().UTC()
	stale := stagedValidationJob(now)
	stale.ID = "job-a"
	stalePath := contentpath.StagingPath(stale.ID)
	stale.StagingPath = &stalePath
	valid := stagedValidationJob(now)
	valid.ID = "job-b"
	validPath := contentpath.StagingPath(valid.ID)
	valid.StagingPath = &validPath
	queue := &validationQueueFake{
		jobs: map[string]store.IngestJob{
			stale.ID: stale,
			valid.ID: valid,
		},
		transitionErrors: map[string]error{
			stale.ID: store.ErrStaleRevision,
		},
	}
	report, err := RunIngestValidationPass(
		context.Background(), queue, &validationArtifactFake{},
		validationClock(now), time.Hour, epub.DefaultLimits(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Validated != 1 || report.Skipped != 1 ||
		queue.jobs[stale.ID].State != store.IngestStaged ||
		queue.jobs[valid.ID].State != store.IngestValidated {
		t.Fatalf("stale validation pass: %+v %+v", report, queue.jobs)
	}
}
