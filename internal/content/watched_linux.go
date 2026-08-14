//go:build linux

package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/chmouel/liseur-sync/internal/store"
)

// watchedNS derives a watched job's id from the evidence that produced it,
// the same way uploads derive theirs from an idempotency key. A sweep has
// no client to supply one, so the file's own identity — where it is, how
// big it is, and when it was last written — is the key. Rescanning an
// unchanged file therefore re-enters the job it already created instead of
// queueing the same book again, and a file that changed asks a new
// question under a new id.
var watchedNS = uuid.MustParse("7e0a3b10-5f2e-4c3a-9b6d-2f6f6c1a0006")

// watchedAbsenceBatch bounds one absence-marking statement. The pass loops
// until a sweep has marked everything it proved gone.
const watchedAbsenceBatch = 500

// maxWatchedAbsencePasses stops a store that reports work it did not do
// from spinning the sweep forever, the same ceiling ReconcileCatalogAvailability
// uses for the same reason.
const maxWatchedAbsencePasses = 10_000

// Review reasons a sweep can record. They are stable strings because an
// administrator resolving a queue reads them, and a later pass compares
// against what an earlier one wrote.
const (
	reviewContentChanged = "the file at this watched path was replaced with " +
		"different content; the previous snapshot is kept because identity " +
		"is never transferred on a content change alone"
	reviewPathAmbiguous = "more than one catalog book claims this watched " +
		"path, so a sweep cannot tell which one the file belongs to"
)

// ScannedLibrary is the one library a sweep is asked about.
type ScannedLibrary struct {
	ID string
	// RootPath is the administrator-configured directory. Nothing beneath
	// it is ever written, renamed, moved, trashed or deleted.
	RootPath string
	// ActorUserID is the principal the resulting ingest jobs belong to.
	// A sweep runs on the server's behalf, so this is the library's owner
	// rather than whoever happens to be logged in.
	ActorUserID string
}

// WatchedSyncReport totals one library's sweep.
type WatchedSyncReport struct {
	Scan ScanReport
	// Ingested counts paths queued for ingestion, including paths a
	// previous sweep had already queued and that have not been promoted
	// yet — those re-enter their existing job rather than creating one.
	Ingested int
	// Unchanged counts paths whose recorded snapshot still describes what
	// is on disk, which on a steady-state library is all of them.
	Unchanged int
	// Rehashed counts paths whose size or modification time moved but
	// whose bytes turned out to be the same. A touched file is not a new
	// book.
	Rehashed int
	// Review counts paths a sweep refused to resolve.
	Review int
	// MarkedAbsent counts files a completed sweep proved are no longer at
	// their recorded path. It is always zero for an incomplete sweep.
	MarkedAbsent int
	// Failed counts paths a sweep could not queue for reasons that are
	// worth another attempt — a quota refusal, a read error. They clear
	// Scan.Complete, because a sweep that could not account for a path has
	// not accounted for the library.
	Failed int
}

// watchedStore is the durable surface one sweep needs.
type watchedStore interface {
	WatchedFilesByPath(context.Context, string, string) ([]store.WatchedFile, error)
	MarkWatchedSourcesSeen(context.Context, string, []store.WatchedObservation, time.Time) (int, error)
	MarkWatchedSourcesAbsent(context.Context, string, time.Time, time.Time, int) (int, error)
	SetCatalogBookReview(context.Context, string, string, string, time.Time) (bool, error)
	CreateIngestJob(context.Context, string, store.IngestJobRequest) (store.IngestJob, bool, error)
	CommitIngestStage(context.Context, string, string, store.CommitIngestStageRequest) (store.CommitIngestStageResult, error)
}

// watchedStager is the CAS surface one sweep needs. Ingest copies from the
// watched source into CAS-side staging; the source itself is never the
// thing a download reads.
type watchedStager interface {
	Stage(ctx context.Context, jobID string, src io.Reader, maxBytes int64) (StagedBlob, error)
	RemoveStage(ctx context.Context, stagingPath string) error
}

// WatchedSyncOptions carry the limits one sweep runs under.
type WatchedSyncOptions struct {
	Scan ScanLimits
	// MaxFileBytes bounds a single publication, as the upload limit does.
	MaxFileBytes int64
	// QuotaLimitBytes is the quota principal's ceiling, or nil for no
	// limit. A watched snapshot is charged exactly like an upload.
	QuotaLimitBytes *int64
}

// SyncScannedLibrary reconciles one watched library against its root.
//
// It is the only place that decides what a sweep means, and the decisions
// are deliberately conservative:
//
//   - A path the catalog does not know becomes an ingest job. Everything
//     after that — validation, metadata extraction, promotion — is the
//     existing pipeline's, so a watched book and an uploaded one differ
//     only in where their bytes were read from.
//   - A path whose recorded size and modification time still match is
//     left alone. This is the common case and must not cost a read.
//   - A path whose stat moved is rehashed. Identical bytes mean somebody
//     touched the file, which is not a catalog event.
//   - A path whose bytes changed is **not** re-ingested and does not
//     update the book it used to describe. Preserving a book id across a
//     content change needs proof this sweep does not have, so the existing
//     snapshot is kept and the book goes to review for an administrator.
//   - Absence is concluded only from a completed full sweep. A traversal
//     that hit a limit, was cancelled, or could not read a directory
//     leaves every book exactly as it found it.
//
// Nothing beneath the root is written to. The source is opened read-only,
// refusing a symlink at the final component, and every path is resolved
// relative to a descriptor for the root rather than by name.
func SyncScannedLibrary(
	ctx context.Context,
	st watchedStore,
	blobs watchedStager,
	library ScannedLibrary,
	opts WatchedSyncOptions,
	clock func() time.Time,
) (WatchedSyncReport, error) {
	var report WatchedSyncReport
	if st == nil || blobs == nil || clock == nil ||
		library.ID == "" || library.RootPath == "" ||
		library.ActorUserID == "" || opts.MaxFileBytes <= 0 {
		return report, store.ErrInvalidTransition
	}
	sweepStartedAt := clock().UTC()
	if sweepStartedAt.IsZero() {
		return report, store.ErrInvalidTransition
	}

	scan, err := ScanWatchedRoot(ctx, library.RootPath, opts.Scan)
	report.Scan = scan
	if err != nil {
		// A root that cannot be opened is not a sweep that found nothing.
		// An unmounted volume and a deleted library look identical from
		// here, and only one of them is a reason to take a household's
		// catalog away, so this reports the failure and changes nothing.
		return report, err
	}

	root, err := os.OpenRoot(library.RootPath)
	if err != nil {
		return report, fmt.Errorf("%w: %s: %v",
			ErrRootUnavailable, library.RootPath, err)
	}
	defer root.Close()

	observations := make([]store.WatchedObservation, 0, len(scan.Files))
	for _, file := range scan.Files {
		if err := ctx.Err(); err != nil {
			report.Scan.Complete = false
			return report, err
		}
		// The path is recorded as seen whatever the outcome below. Seen is
		// a statement about the filesystem, not about whether the server
		// managed to do something with what it found; a path that exists
		// but could not be staged must not then be swept away as absent.
		observations = append(observations, store.WatchedObservation{
			SourceRelativePath: file.RelativePath,
			SizeBytes:          file.SizeBytes,
			ModifiedAt:         file.ModifiedAt,
		})
		if err := reconcileWatchedFile(
			ctx, st, blobs, root, library, file, opts, clock, &report,
		); err != nil {
			return report, err
		}
	}

	if err := recordWatchedObservations(
		ctx, st, library.ID, observations, clock().UTC()); err != nil {
		return report, err
	}
	if !report.Scan.Complete {
		return report, nil
	}
	absent, err := markWatchedAbsent(
		ctx, st, library.ID, sweepStartedAt, clock().UTC())
	report.MarkedAbsent = absent
	return report, err
}

// reconcileWatchedFile decides what one discovered path means.
func reconcileWatchedFile(
	ctx context.Context,
	st watchedStore,
	blobs watchedStager,
	root *os.Root,
	library ScannedLibrary,
	file ScannedFile,
	opts WatchedSyncOptions,
	clock func() time.Time,
	report *WatchedSyncReport,
) error {
	known, err := st.WatchedFilesByPath(ctx, library.ID, file.RelativePath)
	if err != nil {
		return fmt.Errorf("read watched path %q: %w", file.RelativePath, err)
	}

	switch len(known) {
	case 0:
		if err := ingestWatchedFile(
			ctx, st, blobs, root, library, file, opts, clock,
		); err != nil {
			if isRetryableWatchedFailure(err) {
				report.Failed++
				report.Scan.Complete = false
				return nil
			}
			return err
		}
		report.Ingested++
		return nil
	case 1:
	default:
		// Two books claiming one path is not something a sweep may pick a
		// winner for, and re-ingesting would make it three.
		report.Review++
		for _, existing := range known {
			if _, err := st.SetCatalogBookReview(ctx, library.ID,
				existing.BookID, reviewPathAmbiguous, clock().UTC()); err != nil {
				return err
			}
		}
		return nil
	}

	existing := known[0]
	if watchedFileUnchanged(existing, file) {
		report.Unchanged++
		return nil
	}

	// The stat moved, so the bytes are the only thing that can settle it.
	digest, err := hashWatchedSource(ctx, root, file.RelativePath, opts.MaxFileBytes)
	if err != nil {
		if isRetryableWatchedFailure(err) {
			report.Failed++
			report.Scan.Complete = false
			return nil
		}
		return err
	}
	if digest == existing.BlobSHA256 {
		// A touched file. MarkWatchedSourcesSeen records the new
		// modification time, so the next sweep does not read it again.
		report.Rehashed++
		if existing.BookStatus == store.BookReview {
			// The content came back. Clearing the review returns the book
			// to missing, from where the availability pass decides whether
			// it is servable — which is that pass's judgement, not this
			// one's.
			if _, err := st.SetCatalogBookReview(
				ctx, library.ID, existing.BookID, "", clock().UTC()); err != nil {
				return err
			}
		}
		return nil
	}

	report.Review++
	if _, err := st.SetCatalogBookReview(ctx, library.ID, existing.BookID,
		reviewContentChanged, clock().UTC()); err != nil {
		return err
	}
	return nil
}

// watchedFileUnchanged reports whether the recorded snapshot still
// describes what is on disk, without reading the file.
//
// The recorded size is the blob's, because the blob was copied from this
// path: if the source is a different length, it is different bytes, and no
// read is needed to know it. A file no sweep has stat'ed yet — one promoted
// before this pass existed — has no recorded modification time and is
// rehashed once, which is the migration.
func watchedFileUnchanged(existing store.WatchedFile, file ScannedFile) bool {
	if existing.SourceModifiedAt == nil || existing.SourceAbsent {
		return false
	}
	return existing.SizeBytes == file.SizeBytes &&
		existing.SourceModifiedAt.Equal(file.ModifiedAt)
}

// ingestWatchedFile copies one discovered publication into CAS staging and
// records that it did, which is exactly what an upload does once its body
// has arrived. The workers that validate, read and promote it cannot tell
// the two apart, which is the point: a watched book is not a second kind of
// book.
func ingestWatchedFile(
	ctx context.Context,
	st watchedStore,
	blobs watchedStager,
	root *os.Root,
	library ScannedLibrary,
	file ScannedFile,
	opts WatchedSyncOptions,
	clock func() time.Time,
) error {
	relativePath := file.RelativePath
	jobID := watchedJobID(library.ID, file)
	now := clock().UTC()
	job, created, err := st.CreateIngestJob(ctx, library.ActorUserID,
		store.IngestJobRequest{
			ID:                 jobID,
			LibraryID:          library.ID,
			Source:             store.IngestWatched,
			SourceRelativePath: &relativePath,
			RequestFingerprint: watchedFingerprint(library.ID, file),
			CreatedAt:          now,
		})
	if err != nil {
		return fmt.Errorf("queue watched path %q: %w", relativePath, err)
	}
	// A job that already carries its bytes is one an earlier sweep staged
	// and the workers have not finished with. Re-reading the source would
	// either discard what was already committed or contradict a digest the
	// database has, so it is left to finish.
	if !created && job.State != store.IngestReceived {
		return nil
	}

	src, err := openWatchedSource(root, relativePath)
	if err != nil {
		return err
	}
	defer src.Close()

	staged, err := blobs.Stage(ctx, job.ID, src, opts.MaxFileBytes)
	if err != nil {
		return fmt.Errorf("stage watched path %q: %w", relativePath, err)
	}
	if _, err := st.CommitIngestStage(ctx, library.ActorUserID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: job.Revision,
			Artifact: store.BlobInfo{
				SHA256: staged.SHA256, SizeBytes: staged.Size},
			StagingPath:     staged.Path,
			QuotaLimitBytes: opts.QuotaLimitBytes,
			UpdatedAt:       clock().UTC(),
		}); err != nil {
		// Bytes on disk the database does not point at are unreachable:
		// no recovery or GC pass can find a stage no row names, so the
		// sweep that created it drops it rather than leaving a library
		// over quota able to fill the disk one scan at a time.
		_ = blobs.RemoveStage(ctx, staged.Path)
		return fmt.Errorf("commit watched path %q: %w", relativePath, err)
	}
	return nil
}

// openWatchedSource opens one publication read-only, relative to the root's
// descriptor and refusing a symlink at the final component.
//
// O_NOFOLLOW closes the window between the traversal's lstat and this open.
// os.Root already guarantees the path cannot leave the tree; this
// additionally means a path swapped for a link to another file inside the
// tree is refused rather than silently ingested under the wrong name.
func openWatchedSource(root *os.Root, relativePath string) (*os.File, error) {
	src, err := root.OpenFile(relativePath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open watched path %q: %w", relativePath, err)
	}
	return src, nil
}

// hashWatchedSource reads one source and reports its digest, bounded by the
// same limit ingestion is.
func hashWatchedSource(
	ctx context.Context, root *os.Root, relativePath string, maxBytes int64,
) (string, error) {
	src, err := openWatchedSource(root, relativePath)
	if err != nil {
		return "", err
	}
	defer src.Close()
	digest := sha256.New()
	if _, err := copyBounded(ctx, io.Discard, digest, src, maxBytes); err != nil {
		return "", fmt.Errorf("read watched path %q: %w", relativePath, err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// recordWatchedObservations writes what the traversal saw, in batches the
// store will accept.
func recordWatchedObservations(
	ctx context.Context,
	st watchedStore,
	libraryID string,
	observations []store.WatchedObservation,
	at time.Time,
) error {
	for len(observations) > 0 {
		batch := observations
		if len(batch) > store.MaxWatchedObservationBatch {
			batch = batch[:store.MaxWatchedObservationBatch]
		}
		if _, err := st.MarkWatchedSourcesSeen(
			ctx, libraryID, batch, at); err != nil {
			return fmt.Errorf("record watched observations: %w", err)
		}
		observations = observations[len(batch):]
	}
	return nil
}

// markWatchedAbsent records everything a completed sweep did not find.
func markWatchedAbsent(
	ctx context.Context,
	st watchedStore,
	libraryID string,
	sweepStartedAt, at time.Time,
) (int, error) {
	total := 0
	for pass := 0; pass < maxWatchedAbsencePasses; pass++ {
		marked, err := st.MarkWatchedSourcesAbsent(
			ctx, libraryID, sweepStartedAt, at, watchedAbsenceBatch)
		if err != nil {
			return total, fmt.Errorf("mark watched sources absent: %w", err)
		}
		total += marked
		if marked == 0 {
			return total, nil
		}
	}
	return total, fmt.Errorf(
		"watched absence did not converge in %d passes: %w",
		maxWatchedAbsencePasses, store.ErrInvariantViolation)
}

// watchedJobID and watchedFingerprint both key on the file's identity
// rather than only its path, so that a sweep re-entering an unchanged file
// finds its own job and a changed file cannot re-enter the previous one's.
func watchedJobID(libraryID string, file ScannedFile) string {
	return uuid.NewSHA1(watchedNS, []byte(watchedKey(libraryID, file))).String()
}

func watchedFingerprint(libraryID string, file ScannedFile) string {
	return "watched|" + watchedKey(libraryID, file)
}

func watchedKey(libraryID string, file ScannedFile) string {
	return libraryID + "|" + file.RelativePath + "|" +
		strconv.FormatInt(file.SizeBytes, 10) + "|" +
		strconv.FormatInt(file.ModifiedAt.UTC().UnixNano(), 10)
}

// isRetryableWatchedFailure separates one path the sweep could not handle
// from a sweep that cannot continue. A file that vanished mid-scan, one the
// server may not read, one that is too large, and a quota refusal are all
// facts about that path; the next sweep asks again. Anything else — a
// database that will not answer, a CAS that cannot write — would fail
// identically for every remaining path, so it stops the pass rather than
// being counted several thousand times.
func isRetryableWatchedFailure(err error) bool {
	switch {
	case errors.Is(err, store.ErrQuotaExceeded),
		errors.Is(err, ErrTooLarge),
		errors.Is(err, ErrStagingFull),
		errors.Is(err, os.ErrNotExist),
		errors.Is(err, os.ErrPermission),
		errors.Is(err, syscall.ELOOP):
		return true
	default:
		return false
	}
}

// scannableLibraryLister is the housekeeping surface a scan pass needs.
type scannableLibraryLister interface {
	watchedStore
	ListScannableLibraries(context.Context) ([]store.Library, error)
}

// WatchedScanReport totals one pass over every root-backed library.
type WatchedScanReport struct {
	Libraries int
	// Swept counts libraries whose traversal completed, so absence was
	// safe to conclude for them.
	Swept int
	// Unavailable counts libraries whose root could not be opened at all.
	// Their catalogs are left untouched.
	Unavailable  int
	Ingested     int
	Unchanged    int
	Rehashed     int
	Review       int
	MarkedAbsent int
	Failed       int
}

// Changed reports whether the pass did anything worth logging.
func (r WatchedScanReport) Changed() bool {
	return r.Ingested != 0 || r.Rehashed != 0 || r.Review != 0 ||
		r.MarkedAbsent != 0 || r.Failed != 0 || r.Unavailable != 0
}

// RunScanPass sweeps every root-backed library once.
//
// One library's failure does not stop the others. A root on a network
// mount that is down is the ordinary case here, and letting it stall every
// other library's scanning would make one flaky disk look like a broken
// server.
func RunScanPass(
	ctx context.Context,
	st scannableLibraryLister,
	blobs watchedStager,
	opts WatchedSyncOptions,
	clock func() time.Time,
) (WatchedScanReport, error) {
	var report WatchedScanReport
	if st == nil || blobs == nil || clock == nil || opts.MaxFileBytes <= 0 {
		return report, store.ErrInvalidTransition
	}
	libraries, err := st.ListScannableLibraries(ctx)
	if err != nil {
		return report, fmt.Errorf("list scannable libraries: %w", err)
	}
	for _, library := range libraries {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if library.RootPath == nil || *library.RootPath == "" {
			continue
		}
		report.Libraries++
		result, err := SyncScannedLibrary(ctx, st, blobs, ScannedLibrary{
			ID:       library.ID,
			RootPath: *library.RootPath,
			// The owner is the principal the jobs belong to. The quota
			// principal is charged separately by the commit, so an owner
			// who is not the payer still cannot spend somebody else's
			// allowance by being named here.
			ActorUserID: library.OwnerUserID,
		}, opts, clock)
		report.Ingested += result.Ingested
		report.Unchanged += result.Unchanged
		report.Rehashed += result.Rehashed
		report.Review += result.Review
		report.MarkedAbsent += result.MarkedAbsent
		report.Failed += result.Failed
		if err != nil {
			if errors.Is(err, ErrRootUnavailable) {
				report.Unavailable++
				continue
			}
			if ctx.Err() != nil {
				return report, err
			}
			return report, fmt.Errorf(
				"sweep library %q: %w", library.ID, err)
		}
		if result.Scan.Complete {
			report.Swept++
		}
	}
	return report, nil
}
