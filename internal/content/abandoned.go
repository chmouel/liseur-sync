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

type abandonedUploadStore interface {
	ListAbandonedIngestJobs(context.Context, string, int) ([]store.IngestJob, error)
	TransitionIngestJob(context.Context, string, string, store.IngestJobTransition) (store.IngestJob, error)
}

type abandonedUploadArtifacts interface {
	RemoveStage(context.Context, string) error
}

// codeUploadAbandoned marks a job whose bytes never reached the database.
const codeUploadAbandoned = "upload_abandoned"

// AbandonedUploadReport describes one sweep over interrupted uploads.
type AbandonedUploadReport struct {
	Failed  int
	Skipped int
}

// SweepAbandonedUploads terminalizes uploads that were interrupted between
// writing their bytes and recording them, and deletes the bytes.
//
// Nothing else collects them. An upload writes its stage before the
// database points at it, so a crash in that window leaves a file no table
// mentions and a job in `received` that no worker looks at: blob
// reconciliation only sees committed content, and ingest recovery only
// visits jobs that reached `staged`. The file then occupies the staging
// area forever, which the instance-wide cap turns from waste into refused
// uploads.
//
// The caller must guarantee no upload is in flight, because a job that is
// still receiving its body is in exactly the same state as one that was
// interrupted and nothing in the database can tell them apart. Startup is
// where that guarantee holds: the process that could have been serving
// those uploads is the one that died.
//
// The row is claimed before its bytes are deleted, so that a violation of
// that guarantee — two servers sharing one content root, say — costs
// nothing. Whoever else was uploading commits first, this sweep's
// revision-checked transition fails, and the file it would have deleted is
// left alone; the upload path's own commit-failure cleanup handles the
// mirror case. The reverse order is tidier after a crash and was rejected
// anyway: it deletes first and asks afterwards, which turns that same
// mistake into a destroyed book. A crash between the two steps leaks one
// staged file instead, which is waste rather than loss.
func SweepAbandonedUploads(
	ctx context.Context,
	st abandonedUploadStore,
	artifacts abandonedUploadArtifacts,
	now time.Time,
	failureRetention time.Duration,
	pageSize int,
) (AbandonedUploadReport, error) {
	var report AbandonedUploadReport
	// The page bounds duplicate what the store enforces. They are kept
	// because this function deletes files: it should refuse a caller it
	// does not understand before it touches anything, not rely on the
	// first query to say no.
	if st == nil || artifacts == nil || now.IsZero() ||
		failureRetention <= 0 || pageSize < 1 || pageSize > 500 {
		return report, store.ErrInvalidTransition
	}
	now = now.UTC()
	var afterID string
	for {
		jobs, err := st.ListAbandonedIngestJobs(ctx, afterID, pageSize)
		if err != nil {
			return report, err
		}
		if len(jobs) == 0 {
			return report, nil
		}
		for _, job := range jobs {
			updatedAt := now
			if job.UpdatedAt.After(updatedAt) {
				updatedAt = job.UpdatedAt
			}
			expiresAt := updatedAt.Add(failureRetention)
			_, err := st.TransitionIngestJob(ctx, job.UserID, job.ID,
				store.IngestJobTransition{
					ExpectedState: job.State, ExpectedRevision: job.Revision,
					NextState: store.IngestFailed,
					ErrorCode: codeUploadAbandoned,
					ErrorDetail: "upload was interrupted before its bytes " +
						"were recorded",
					ExpiresAt: &expiresAt, UpdatedAt: updatedAt,
				})
			switch {
			case err == nil:
			case errors.Is(err, store.ErrStaleRevision),
				errors.Is(err, store.ErrInvalidTransition),
				errors.Is(err, store.ErrNotFound):
				// Somebody advanced the job while we were reading it,
				// which means an upload was in flight after all and its
				// bytes are now referenced. Leaving them alone is the
				// entire reason the row is claimed before they are
				// deleted.
				report.Skipped++
				continue
			default:
				return report, fmt.Errorf(
					"fail abandoned upload %q: %w", job.ID, err)
			}
			// Only now, with the row terminal and no longer committable
			// by anyone, are the bytes safe to delete.
			if err := artifacts.RemoveStage(ctx,
				contentpath.StagingPath(job.ID)); err != nil {
				return report, fmt.Errorf(
					"remove abandoned upload %q: %w", job.ID, err)
			}
			report.Failed++
		}
		if len(jobs) < pageSize {
			return report, nil
		}
		afterID = jobs[len(jobs)-1].ID
	}
}
