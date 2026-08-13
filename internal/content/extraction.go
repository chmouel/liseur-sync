//go:build linux

package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

// IngestMetadataExtractionResult is the durable outcome of one
// validated-to-extracted worker step.
type IngestMetadataExtractionResult struct {
	Job      store.IngestJob
	Metadata epub.Metadata
	Location ArtifactLocation
}

// IngestMetadataExtractionReport describes one bounded validated-job pass.
type IngestMetadataExtractionReport struct {
	Extracted   int
	Quarantined int
	Skipped     int
}

// ExtractIngestMetadata revalidates the immutable EPUB artifact, serializes
// its bounded embedded metadata, and persists that snapshot in the same
// revision-checked transition to extracted. Content failures are retained as
// quarantined jobs; operational failures leave the validated job retryable.
func ExtractIngestMetadata(
	ctx context.Context,
	st ingestTransitionStore,
	artifacts ingestArtifactValidator,
	job store.IngestJob,
	clock func() time.Time,
	failureRetention time.Duration,
	limits epub.Limits,
) (IngestMetadataExtractionResult, error) {
	var result IngestMetadataExtractionResult
	if st == nil || artifacts == nil || job.State != store.IngestValidated ||
		job.ContentSHA256 == nil || job.StagingPath == nil ||
		clock == nil || failureRetention <= 0 {
		return result, store.ErrInvalidTransition
	}
	publication, location, err := artifacts.ValidateEPUBArtifact(
		ctx, *job.StagingPath, *job.ContentSHA256, job.BytesReceived, limits)
	if err != nil {
		updatedAt, timeErr := ingestPostProcessTime(job, clock)
		if timeErr != nil {
			return result, timeErr
		}
		code, contentFailure := epub.ErrorCode(err)
		if !contentFailure {
			return result, fmt.Errorf(
				"extract ingest metadata for job %q: %w", job.ID, err)
		}
		expiresAt := updatedAt.Add(failureRetention)
		quarantined, transitionErr := st.TransitionIngestJob(
			ctx, job.UserID, job.ID, store.IngestJobTransition{
				ExpectedState: job.State, ExpectedRevision: job.Revision,
				NextState:   store.IngestQuarantined,
				ErrorCode:   string(code),
				ErrorDetail: "EPUB content failed validation during metadata extraction",
				ExpiresAt:   &expiresAt, UpdatedAt: updatedAt,
			})
		if transitionErr != nil {
			return result, fmt.Errorf(
				"quarantine metadata extraction job %q: %w",
				job.ID, transitionErr)
		}
		result.Job = quarantined
		result.Location = location
		return result, nil
	}

	metadataJSON, err := json.Marshal(publication.Metadata)
	if err != nil {
		return result, fmt.Errorf(
			"marshal extracted metadata for ingest job %q: %w", job.ID, err)
	}
	updatedAt, err := ingestPostProcessTime(job, clock)
	if err != nil {
		return result, err
	}
	extracted, err := st.TransitionIngestJob(
		ctx, job.UserID, job.ID, store.IngestJobTransition{
			ExpectedState: job.State, ExpectedRevision: job.Revision,
			NextState:                     store.IngestExtracted,
			ExtractedEmbeddedMetadataJSON: metadataJSON,
			UpdatedAt:                     updatedAt,
		})
	if err != nil {
		return result, fmt.Errorf(
			"advance extracted ingest job %q: %w", job.ID, err)
	}
	result.Job = extracted
	result.Metadata = publication.Metadata
	result.Location = location
	return result, nil
}

// RunIngestMetadataExtractionPass extracts one bounded snapshot of validated
// jobs. Later polls pick up jobs outside this batch.
func RunIngestMetadataExtractionPass(
	ctx context.Context,
	st ingestWorkerQueue,
	artifacts ingestArtifactValidator,
	clock func() time.Time,
	failureRetention time.Duration,
	limits epub.Limits,
	batchSize int,
) (IngestMetadataExtractionReport, error) {
	var report IngestMetadataExtractionReport
	if st == nil || artifacts == nil || clock == nil ||
		failureRetention <= 0 || batchSize < 1 || batchSize > 500 {
		return report, store.ErrInvalidTransition
	}
	if err := limits.Validate(); err != nil {
		return report, err
	}
	jobs, err := st.ListIngestWorkerJobs(
		ctx, store.IngestValidated, batchSize)
	if err != nil {
		return report, fmt.Errorf("list validated ingest jobs: %w", err)
	}
	for _, job := range jobs {
		result, err := ExtractIngestMetadata(
			ctx, st, artifacts, job, clock, failureRetention, limits)
		if errors.Is(err, store.ErrStaleRevision) {
			report.Skipped++
			continue
		}
		if err != nil {
			return report, err
		}
		switch result.Job.State {
		case store.IngestExtracted:
			report.Extracted++
		case store.IngestQuarantined:
			report.Quarantined++
		default:
			return report, store.ErrInvariantViolation
		}
	}
	return report, nil
}
