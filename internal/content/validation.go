//go:build linux

package content

import (
	"context"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

type ingestValidationStore interface {
	TransitionIngestJob(context.Context, string, string, store.IngestJobTransition) (store.IngestJob, error)
}

type ingestArtifactValidator interface {
	ValidateEPUBArtifact(context.Context, string, string, int64, epub.Limits) (epub.Result, ArtifactLocation, error)
}

// IngestValidationResult is the durable outcome of one staged-job validation
// step. Publication is populated only when the job reaches validated.
type IngestValidationResult struct {
	Job         store.IngestJob
	Publication *epub.Result
	Location    ArtifactLocation
}

// ValidateIngestJob performs one revision-checked staged-to-validated worker
// step. Content validation failures become retained quarantined jobs;
// operational failures are returned for retry without changing durable state.
func ValidateIngestJob(
	ctx context.Context,
	st ingestValidationStore,
	artifacts ingestArtifactValidator,
	job store.IngestJob,
	now time.Time,
	failureRetention time.Duration,
	limits epub.Limits,
) (IngestValidationResult, error) {
	var result IngestValidationResult
	if st == nil || artifacts == nil || job.State != store.IngestStaged ||
		job.ContentSHA256 == nil || job.StagingPath == nil ||
		now.IsZero() || failureRetention <= 0 {
		return result, store.ErrInvalidTransition
	}
	publication, location, err := artifacts.ValidateEPUBArtifact(
		ctx, *job.StagingPath, *job.ContentSHA256, job.BytesReceived, limits)
	updatedAt := now.UTC()
	if job.UpdatedAt.After(updatedAt) {
		updatedAt = job.UpdatedAt
	}
	if err != nil {
		code, contentFailure := epub.ErrorCode(err)
		if !contentFailure {
			return result, fmt.Errorf(
				"validate ingest job %q: %w", job.ID, err)
		}
		expiresAt := updatedAt.Add(failureRetention)
		quarantined, transitionErr := st.TransitionIngestJob(
			ctx, job.UserID, job.ID, store.IngestJobTransition{
				ExpectedState: job.State, ExpectedRevision: job.Revision,
				NextState:   store.IngestQuarantined,
				ErrorCode:   string(code),
				ErrorDetail: "EPUB content failed structural validation",
				ExpiresAt:   &expiresAt, UpdatedAt: updatedAt,
			})
		if transitionErr != nil {
			return result, fmt.Errorf(
				"quarantine ingest job %q: %w", job.ID, transitionErr)
		}
		result.Job = quarantined
		result.Location = location
		return result, nil
	}
	validated, err := st.TransitionIngestJob(
		ctx, job.UserID, job.ID, store.IngestJobTransition{
			ExpectedState: job.State, ExpectedRevision: job.Revision,
			NextState: store.IngestValidated, UpdatedAt: updatedAt,
		})
	if err != nil {
		return result, fmt.Errorf(
			"advance validated ingest job %q: %w", job.ID, err)
	}
	result.Job = validated
	result.Publication = &publication
	result.Location = location
	return result, nil
}
