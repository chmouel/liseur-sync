//go:build linux

package content

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/google/uuid"

	"github.com/chmouel/liseur-sync/internal/catalog"
	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// promotionNS derives a job's catalog ids. Deriving them rather than drawing
// them fresh keeps one job's promotion a pure function of that job, so a
// worker that has to build the request again names the same book and file
// instead of a second set nothing refers to.
var promotionNS = uuid.MustParse("7e0a3b10-5f2e-4c3a-9b6d-2f6f6c1a0004")

// Promotion failure codes share recovery's vocabulary, so an operator reads
// one dictionary rather than two.
const (
	// codeArtifactMissing is the job's own staged bytes being gone. Nothing
	// on the server can bring them back; a re-upload can.
	codeArtifactMissing = "artifact_missing"
	// codeArtifactCorrupt is the job's own staged bytes no longer hashing to
	// what was validated, or a staging path that is not the one this job was
	// staged under. Both are permanent for these bytes.
	codeArtifactCorrupt = "artifact_corrupt"
	// codeStoredBlobCorrupt is a blob already in the store failing its own
	// digest. The upload is blameless: its stage verified moments earlier,
	// and the damage is to shared content other books may also reference.
	// Re-uploading the same bytes hits the same blob, so this needs an
	// operator, and saying so is the difference between a fixable alarm and
	// a user retrying forever.
	codeStoredBlobCorrupt = "stored_blob_corrupt"
)

// mediaTypeEPUB is the only publication type this server accepts, so a
// promoted file always carries it.
const mediaTypeEPUB = "application/epub+zip"

type ingestPromotionStore interface {
	ingestTransitionStore
	CommitNewBookPromotion(
		context.Context, string, string, store.CommitNewBookPromotionRequest,
	) (store.IngestPromotionResult, error)
}

type ingestPromotionQueue interface {
	ingestPromotionStore
	bookMetadataStore
	ListIngestWorkerJobs(context.Context, store.IngestState, int) ([]store.IngestJob, error)
}

// ingestBlobPromoter publishes one staged artifact into the CAS. Promote
// replays a lost success by verifying the final blob, so calling it again
// for a job whose commit never landed is safe.
type ingestBlobPromoter interface {
	Promote(ctx context.Context, stagingPath, expectedSHA string, expectedSize int64) (Blob, error)
}

// IngestPromotionResult is the durable outcome of one extracted-to-promoted
// worker step. Book and File are set only when the job reached promoted.
type IngestPromotionResult struct {
	Job  store.IngestJob
	Book store.CatalogBook
	File store.BookFile
	// Replayed reports that another worker had already promoted this job and
	// these rows are its work, read back rather than created.
	Replayed bool
}

// IngestPromotionReport describes one bounded extracted-job pass.
type IngestPromotionReport struct {
	Promoted    int
	Quarantined int
	Skipped     int
	// Replayed counts jobs another worker had already promoted. It is the
	// only signal that workers are contending for the same batch.
	Replayed int
	// Undescribed counts books that were promoted with their titles intact
	// but whose entity sets did not attach.
	Undescribed int
	// Misconfigured counts jobs left where they were because their
	// library's layout configuration could not be read. They are not
	// failures of the job and are retried on the next pass.
	Misconfigured int
}

// PromoteIngestJob publishes an extracted job's artifact into the CAS and
// creates the catalog book and file it becomes, in one revision-checked
// transition.
//
// The blob is published first. A blob with no row is collectable garbage the
// reconciler already knows how to find, while a row pointing at a blob that
// was never published would be a catalog entry that cannot be read.
//
// The book is created already describing itself. Resolving metadata needs
// rows to resolve against only when the book already exists; a new one has
// nothing to reconcile with, so its scalar fields are a pure function of the
// job and belong in the same transaction. That leaves no window in which a
// book exists with no title, which matters because promoted is a terminal
// state: nothing would ever list such a book to finish it.
func PromoteIngestJob(
	ctx context.Context,
	st ingestPromotionStore,
	blobs ingestBlobPromoter,
	job store.IngestJob,
	patterns []metadata.PathPattern,
	clock func() time.Time,
	failureRetention time.Duration,
) (IngestPromotionResult, error) {
	var result IngestPromotionResult
	if st == nil || blobs == nil || job.State != store.IngestExtracted ||
		job.ContentSHA256 == nil || job.StagingPath == nil ||
		job.UpdatedAt.IsZero() || clock == nil || failureRetention <= 0 {
		return result, store.ErrInvalidTransition
	}
	// A job can only be promoted from the path it was staged under. Anything
	// else is a row the server did not write, and handing it to the CAS turns
	// a corrupted record into a filesystem argument.
	if *job.StagingPath != contentpath.StagingPath(job.ID) {
		return result, fmt.Errorf(
			"promote ingest job %q: %w", job.ID, store.ErrInvariantViolation)
	}
	failedAt, err := ingestPostProcessTime(job, clock)
	if err != nil {
		return result, err
	}
	blob, err := blobs.Promote(
		ctx, *job.StagingPath, *job.ContentSHA256, job.BytesReceived)
	if err != nil {
		code, detail, permanent := classifyPromotionFailure(err)
		if !permanent {
			return result, fmt.Errorf(
				"promote artifact for ingest job %q: %w", job.ID, err)
		}
		expiresAt := failedAt.Add(failureRetention)
		quarantined, transitionErr := st.TransitionIngestJob(
			ctx, job.UserID, job.ID, store.IngestJobTransition{
				ExpectedState: job.State, ExpectedRevision: job.Revision,
				NextState:   store.IngestQuarantined,
				ErrorCode:   code,
				ErrorDetail: detail,
				ExpiresAt:   &expiresAt, UpdatedAt: failedAt,
			})
		if transitionErr != nil {
			return result, fmt.Errorf(
				"quarantine promotion job %q: %w", job.ID, transitionErr)
		}
		result.Job = quarantined
		return result, nil
	}

	promoted, err := st.CommitNewBookPromotion(ctx, job.UserID, job.ID,
		newBookPromotion(job, blob, patterns))
	if err != nil {
		return result, fmt.Errorf(
			"promote ingest job %q: %w", job.ID, err)
	}
	result.Job = promoted.Job
	result.Book = promoted.Book
	result.File = promoted.File
	result.Replayed = promoted.Replayed
	return result, nil
}

// newBookPromotion describes the book and file one job becomes. Every value
// comes from the job or the published blob: nothing about the publication is
// read here, so a book is never created carrying a claim no source made.
//
// The timestamps are the job's own, not the wall clock, and that is
// load-bearing rather than cosmetic. The backend fingerprints this request to
// recognise a replay, so two workers racing the same job must build byte-
// identical requests; a clock reading would differ between them and the loser
// would get ErrPromotionConflict for what is ordinary contention. Deriving
// from the job makes the loser replay the winner's commit and read back the
// winner's rows, which leaves ErrPromotionConflict meaning what it says: two
// different requests claimed the same job.
func newBookPromotion(
	job store.IngestJob, blob Blob, patterns []metadata.PathPattern,
) store.CommitNewBookPromotionRequest {
	info := store.BlobInfo{SHA256: blob.SHA256, SizeBytes: blob.Size}
	bookID := promotionID("book", job)
	updatedAt := job.UpdatedAt
	return store.CommitNewBookPromotionRequest{
		ExpectedRevision: job.Revision,
		Blob:             info,
		Book: promotedBook(job, patterns, store.CatalogBook{
			ID:        bookID,
			LibraryID: job.LibraryID,
			Status:    store.BookActive,
			CreatedAt: updatedAt,
			UpdatedAt: updatedAt,
		}),
		File: store.BookFile{
			ID:                 promotionID("file", job),
			LibraryID:          job.LibraryID,
			BookID:             bookID,
			BlobSHA256:         blob.SHA256,
			Source:             job.Source,
			SourceRelativePath: job.SourceRelativePath,
			OriginalFilename:   promotionFilename(job),
			MediaType:          mediaTypeEPUB,
			Availability:       store.BookFileAvailable,
			CreatedAt:          updatedAt,
			UpdatedAt:          updatedAt,
		},
		UpdatedAt: updatedAt,
	}
}

// promotedBook describes the new book from the job's own evidence. Only the
// scalar fields are resolved here: the entity sets hang off rows the book
// does not have yet, so they are applied once it exists.
//
// An unreadable snapshot is treated as evidence the server does not have
// rather than a reason to refuse. That JSON is a cache the extraction pass
// derived from a file that is itself validated and durable, so losing it must
// not stop the book being published — the title can be corrected, an
// unpublishable book cannot.
//
// The result stays a pure function of the job and the library's configured
// layouts, which the promotion fingerprint depends on. Both workers read
// that configuration from the same row, so ordinary contention produces
// byte-identical requests. An operator who changes the layouts in the
// instant between two racing workers gets a conflict rather than a replay,
// which is the configuration change reporting itself.
func promotedBook(
	job store.IngestJob, patterns []metadata.PathPattern, book store.CatalogBook,
) store.CatalogBook {
	proposals, err := bookMetadataProposals(job, patterns)
	if err != nil {
		return book
	}
	resolved := store.BookMetadata{Book: book}
	for _, proposal := range proposals {
		if next, applied := catalog.Resolve(resolved, proposal); applied {
			resolved = next
		}
	}
	return resolved.Book
}

// promotionFilename recovers the name the file was found under, or the name
// the client supplied for an upload. Neither value is used as a path.
func promotionFilename(job store.IngestJob) string {
	if job.Source == store.IngestUpload {
		return job.OriginalFilename
	}
	if job.SourceRelativePath == nil || *job.SourceRelativePath == "" {
		return ""
	}
	return path.Base(*job.SourceRelativePath)
}

func promotionID(kind string, job store.IngestJob) string {
	return uuid.NewSHA1(promotionNS,
		[]byte(job.LibraryID+"|"+kind+"|"+job.ID)).String()
}

// classifyPromotionFailure separates a job that can never be promoted from a
// server that is temporarily unable to promote it, and names which of the two
// went wrong. Only a permanent failure may be quarantined: quarantining a
// transient one strands work whose next attempt would have succeeded, while
// retrying a permanent one puts the same job at the head of every batch.
func classifyPromotionFailure(err error) (code, detail string, permanent bool) {
	switch {
	case errors.Is(err, ErrStageMissing):
		return codeArtifactMissing,
			"staged content is no longer on disk", true
	case errors.Is(err, ErrDigestMismatch), errors.Is(err, ErrUnsafePath):
		return codeArtifactCorrupt,
			"staged content failed promotion verification", true
	case errors.Is(err, ErrCorruptBlob):
		return codeStoredBlobCorrupt,
			"stored content for this digest failed its own verification", true
	default:
		return "", "", false
	}
}

// RunIngestPromotionPass promotes one bounded snapshot of extracted jobs and
// attaches each new book's entity sets. Later polls pick up jobs outside this
// batch.
//
// The sets are applied after the promotion, not inside it, because they are
// rows that hang off a book that has to exist first. A failure there is
// counted rather than returned: the book, its file and its title are already
// durable and correct, and undoing a good promotion because a tag did not
// attach would be a worse outcome than a book that is briefly untagged.
func RunIngestPromotionPass(
	ctx context.Context,
	st ingestPromotionQueue,
	blobs ingestBlobPromoter,
	patterns PatternResolver,
	clock func() time.Time,
	failureRetention time.Duration,
	batchSize int,
) (IngestPromotionReport, error) {
	var report IngestPromotionReport
	if st == nil || blobs == nil || clock == nil || patterns == nil ||
		failureRetention <= 0 || batchSize < 1 || batchSize > 500 {
		return report, store.ErrInvalidTransition
	}
	jobs, err := st.ListIngestWorkerJobs(ctx, store.IngestExtracted, batchSize)
	if err != nil {
		return report, fmt.Errorf("list extracted ingest jobs: %w", err)
	}
	layouts := memoize(patterns)
	for _, job := range jobs {
		jobPatterns, err := layouts.PatternsFor(ctx, job.UserID, job.LibraryID)
		if err != nil {
			// A library nobody can describe stalls its own backlog and
			// nothing else. Promoting it anyway would file its books under
			// whatever layout happened to be compiled in, which is the
			// misreading this configuration exists to prevent, and failing
			// the pass would let one library's typo stop every other
			// library's uploads.
			if errors.Is(err, metadata.ErrInvalidLibraryConfig) ||
				errors.Is(err, store.ErrNotFound) {
				report.Misconfigured++
				continue
			}
			return report, err
		}
		result, err := PromoteIngestJob(
			ctx, st, blobs, job, jobPatterns, clock, failureRetention)
		if errors.Is(err, store.ErrStaleRevision) {
			report.Skipped++
			continue
		}
		if err != nil {
			return report, err
		}
		switch result.Job.State {
		case store.IngestPromoted:
			if result.Replayed {
				report.Replayed++
				continue
			}
			report.Promoted++
			if _, _, err := MaterializeBookMetadata(
				ctx, st, result.Job, jobPatterns, clock); err != nil {
				report.Undescribed++
			}
		case store.IngestQuarantined:
			report.Quarantined++
		default:
			return report, store.ErrInvariantViolation
		}
	}
	return report, nil
}
