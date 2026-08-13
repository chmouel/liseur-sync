//go:build linux

package content

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/store"
)

type ingestRecoveryStore interface {
	ListIngestRecoveryJobs(context.Context, time.Time, *store.IngestRecoveryCursor, int) ([]store.IngestJob, error)
	PurgeExpiredIngestArtifacts(context.Context, time.Time, int) ([]store.IngestJob, error)
	CompleteIngestArtifactCleanup(context.Context, string, string) error
	TransitionIngestJob(context.Context, string, string, store.IngestJobTransition) (store.IngestJob, error)
}

type artifactRecoveryStore interface {
	InspectArtifact(context.Context, string, string, int64) (ArtifactLocation, error)
	RemoveStage(context.Context, string) error
}

// RecoveredIngestJob is safe for a worker to resume from its persisted state.
type RecoveredIngestJob struct {
	Job      store.IngestJob
	Location ArtifactLocation
}

// IngestRecoveryReport describes one bounded global recovery pass.
type IngestRecoveryReport struct {
	Ready       []RecoveredIngestJob
	Failed      int
	Quarantined int
	Cleaned     int
	Skipped     int
}

// RecoverIngest verifies stale durable artifacts, terminalizes missing or
// corrupt jobs, and cleans expired retained stages. It does not advance valid
// jobs; the ingest worker resumes them from the returned persisted state.
func RecoverIngest(
	ctx context.Context,
	st ingestRecoveryStore,
	artifacts artifactRecoveryStore,
	now, before time.Time,
	failureRetention time.Duration,
	pageSize int,
) (IngestRecoveryReport, error) {
	var report IngestRecoveryReport
	if st == nil || artifacts == nil || now.IsZero() || before.IsZero() ||
		before.After(now) || failureRetention <= 0 ||
		pageSize < 1 || pageSize > 500 {
		return report, store.ErrInvalidTransition
	}
	for {
		expired, err := st.PurgeExpiredIngestArtifacts(ctx, now, pageSize)
		if err != nil {
			return report, err
		}
		for _, job := range expired {
			if job.StagingPath == nil {
				return report, fmt.Errorf(
					"recover ingest job %q: %w", job.ID, store.ErrInvariantViolation)
			}
			if *job.StagingPath != contentpath.StagingPath(job.ID) {
				return report, fmt.Errorf(
					"recover ingest job %q: %w", job.ID, store.ErrInvariantViolation)
			}
			if err := artifacts.RemoveStage(ctx, *job.StagingPath); err != nil {
				return report, fmt.Errorf("clean expired ingest job %q: %w", job.ID, err)
			}
			if err := st.CompleteIngestArtifactCleanup(
				ctx, job.ID, *job.StagingPath); err != nil {
				return report, fmt.Errorf(
					"acknowledge ingest cleanup %q: %w", job.ID, err)
			}
			report.Cleaned++
		}
		if len(expired) < pageSize {
			break
		}
	}

	var cursor *store.IngestRecoveryCursor
	for {
		jobs, err := st.ListIngestRecoveryJobs(ctx, before, cursor, pageSize)
		if err != nil {
			return report, err
		}
		for _, job := range jobs {
			if job.ContentSHA256 == nil || job.StagingPath == nil {
				return report, fmt.Errorf(
					"recover ingest job %q: %w", job.ID, store.ErrInvariantViolation)
			}
			if *job.StagingPath != contentpath.StagingPath(job.ID) {
				return report, fmt.Errorf(
					"recover ingest job %q: %w", job.ID, store.ErrInvariantViolation)
			}
			location, inspectErr := artifacts.InspectArtifact(
				ctx, *job.StagingPath, *job.ContentSHA256, job.BytesReceived)
			if inspectErr == nil {
				report.Ready = append(report.Ready, RecoveredIngestJob{
					Job: job, Location: location,
				})
				continue
			}
			var nextState store.IngestState
			var code, detail string
			switch {
			case errors.Is(inspectErr, ErrStageMissing):
				nextState = store.IngestFailed
				code = "artifact_missing"
				detail = "staged and promoted content are unavailable"
			case errors.Is(inspectErr, ErrDigestMismatch),
				errors.Is(inspectErr, ErrCorruptBlob),
				errors.Is(inspectErr, ErrUnsafePath):
				nextState = store.IngestQuarantined
				code = "artifact_corrupt"
				detail = "persisted content failed recovery verification"
			default:
				return report, fmt.Errorf(
					"inspect ingest job %q: %w", job.ID, inspectErr)
			}
			updatedAt := now
			if job.UpdatedAt.After(updatedAt) {
				updatedAt = job.UpdatedAt
			}
			expiresAt := updatedAt.Add(failureRetention)
			_, err = st.TransitionIngestJob(ctx, job.UserID, job.ID,
				store.IngestJobTransition{
					ExpectedState: job.State, ExpectedRevision: job.Revision,
					NextState: nextState, ErrorCode: code, ErrorDetail: detail,
					ExpiresAt: &expiresAt, UpdatedAt: updatedAt,
				})
			if errors.Is(err, store.ErrStaleRevision) ||
				errors.Is(err, store.ErrInvalidTransition) {
				report.Skipped++
				continue
			}
			if err != nil {
				return report, fmt.Errorf(
					"terminalize ingest job %q: %w", job.ID, err)
			}
			if nextState == store.IngestQuarantined {
				report.Quarantined++
			} else {
				report.Failed++
			}
		}
		if len(jobs) < pageSize {
			return report, nil
		}
		last := jobs[len(jobs)-1]
		cursor = &store.IngestRecoveryCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
}
