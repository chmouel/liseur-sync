//go:build linux

package content

import (
	"context"
	"errors"
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
	publication epub.Result
	location    ArtifactLocation
	err         error
}

func (f *validationArtifactFake) ValidateEPUBArtifact(
	context.Context,
	string,
	string,
	int64,
	epub.Limits,
) (epub.Result, ArtifactLocation, error) {
	return f.publication, f.location, f.err
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

func TestValidateIngestJobAdvancesValidContent(t *testing.T) {
	now := time.Now().UTC()
	job := stagedValidationJob(now)
	st := &validationStoreFake{job: job}
	artifacts := &validationArtifactFake{
		publication: epub.Result{PackagePath: "OPS/book.opf"},
		location:    ArtifactStaged,
	}
	result, err := ValidateIngestJob(
		context.Background(), st, artifacts, job, now,
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
		context.Background(), st, artifacts, job, now,
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

func TestValidateIngestJobLeavesOperationalFailureRetryable(t *testing.T) {
	now := time.Now().UTC()
	job := stagedValidationJob(now)
	st := &validationStoreFake{job: job}
	ioErr := errors.New("read failed")
	artifacts := &validationArtifactFake{err: ioErr}
	if _, err := ValidateIngestJob(
		context.Background(), st, artifacts, job, now,
		24*time.Hour, epub.DefaultLimits()); !errors.Is(err, ioErr) {
		t.Fatalf("operational validation error: %v", err)
	}
	if st.transitions != 0 || st.job.State != store.IngestStaged {
		t.Fatalf("operational failure changed job: %+v", st.job)
	}
}
