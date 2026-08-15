//go:build linux

package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/epub"
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
	// Source decides how the library's books are discovered: by walking
	// the root, or by reading the metadata.db that describes it.
	Source store.LibrarySource
	// Storage decides what discovering a file means: copying it into
	// content-addressed storage, or cataloguing it where it lies.
	Storage store.LibraryStorage
	// RootPath is the administrator-configured directory. Nothing beneath
	// it is ever written, renamed, moved, trashed or deleted.
	RootPath string
	// ActorUserID is the principal the resulting ingest jobs belong to.
	// A sweep runs on the server's behalf, so this is the library's owner
	// rather than whoever happens to be logged in.
	ActorUserID string
	// InventoryDigest is what the last Calibre refresh of this library
	// recorded. A refresh that computes the same value stops there. It is
	// empty for every other source, which have no such gate.
	InventoryDigest string
	// Lease is the claim the worker running this sweep holds on the
	// library. Every unit of work checks it, so a sweep whose lease was
	// taken over stops at the next book rather than writing over the
	// worker that replaced it. A zero lease is a caller that is not
	// running under one — a test, or a sweep of a library nobody else
	// can reach — and nothing is checked.
	Lease store.RefreshLease
}

// refreshLease is the claim one sweep holds, and the schedule on which
// it proves it still has it.
//
// The renewal is here rather than on a timer in the background because a
// lease that renews itself while the work is wedged is a lease that says
// nothing. Renewing between units of work means the library is held for
// exactly as long as it is being refreshed.
type refreshLease struct {
	st        refreshLeaseStore
	libraryID string
	lease     store.RefreshLease
	renewAt   time.Time
}

// newRefreshLease starts holding a claim, or returns nil for a sweep
// that has none.
func newRefreshLease(
	st refreshLeaseStore, library ScannedLibrary, now time.Time,
) *refreshLease {
	if st == nil || library.Lease.Owner == "" {
		return nil
	}
	return &refreshLease{
		st: st, libraryID: library.ID, lease: library.Lease,
		renewAt: now.Add(store.RefreshLeaseRenewAfter),
	}
}

// hold is what a sweep calls before each unit of work: a read most of
// the time, and a renewal once half the lease has gone. Either way, a
// worker that was taken over learns it here and gets
// store.ErrRefreshLeaseLost.
func (l *refreshLease) hold(ctx context.Context, now time.Time) error {
	if l == nil {
		return nil
	}
	if now.Before(l.renewAt) {
		return l.st.CheckLibraryRefreshLease(
			ctx, l.libraryID, l.lease.Owner, now)
	}
	l.lease.Until = now.Add(store.DefaultRefreshLease)
	if err := l.st.RenewLibraryRefreshLease(
		ctx, l.libraryID, l.lease, now); err != nil {
		return err
	}
	l.renewAt = now.Add(store.RefreshLeaseRenewAfter)
	return nil
}

// owner is the token every lease-guarded write carries, and "" for a
// sweep running without one.
func (l *refreshLease) owner() string {
	if l == nil {
		return ""
	}
	return l.lease.Owner
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
	// Relocated counts files whose bytes were found at a new path and
	// whose catalog row moved with them. Only a source that names its
	// books — Calibre — can produce these: a directory sweep has no way
	// to tell a rename from a deletion and an arrival.
	Relocated int
	// Superseded counts books whose file was replaced by different
	// bytes: a new file row on the same book, with the old one kept as
	// superseded.
	Superseded int
	// MarkedAbsent counts files a completed sweep proved are no longer at
	// their recorded path. It is always zero for an incomplete sweep.
	MarkedAbsent int
	// Failed counts paths a sweep could not queue for reasons that are
	// worth another attempt — a quota refusal, a read error. They clear
	// Scan.Complete, because a sweep that could not account for a path has
	// not accounted for the library.
	Failed int
}

// refreshLeaseStore is how a sweep asks whether it still owns the
// library it is writing to.
type refreshLeaseStore interface {
	RenewLibraryRefreshLease(context.Context, string, store.RefreshLease, time.Time) error
	CheckLibraryRefreshLease(context.Context, string, string, time.Time) error
}

// watchedStore is the durable surface one sweep needs.
type watchedStore interface {
	bookMetadataStore
	refreshLeaseStore
	ingestTransitionStore
	WatchedFilesByPath(context.Context, string, string) ([]store.WatchedFile, error)
	MarkWatchedSourcesSeen(context.Context, string, []store.WatchedObservation, time.Time) (int, error)
	MarkWatchedSourcesAbsent(context.Context, string, time.Time, time.Time, int) (int, error)
	SetCatalogBookReview(context.Context, string, string, string, time.Time) (bool, error)
	CreateIngestJob(context.Context, string, store.IngestJobRequest) (store.IngestJob, bool, error)
	CommitIngestStage(context.Context, string, string, store.CommitIngestStageRequest) (store.CommitIngestStageResult, error)
	CommitInPlaceBook(context.Context, string, string, store.CommitInPlaceBookRequest) (store.IngestPromotionResult, error)
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
	// limit. A watched snapshot is charged exactly like an upload. An
	// in-place library charges nothing, because the server stores
	// nothing.
	QuotaLimitBytes *int64
	// Patterns resolves each library's filename layouts. An in-place
	// pass publishes the book itself, so it needs them where a copying
	// pass leaves the work to the promotion worker.
	Patterns PatternResolver
	// EPUBLimits bounds the structural validation an in-place pass does
	// from its own descriptor. Zero means the defaults.
	EPUBLimits epub.Limits
	// FailureRetention is how long a refused in-place job is kept for an
	// operator to read. Zero means the default.
	FailureRetention time.Duration
}

// epubLimits is the configured bound or the default one.
func (o WatchedSyncOptions) epubLimits() epub.Limits {
	if o.EPUBLimits.Validate() != nil {
		return epub.DefaultLimits()
	}
	return o.EPUBLimits
}

func (o WatchedSyncOptions) failureRetention() time.Duration {
	if o.FailureRetention <= 0 {
		return defaultInPlaceFailureRetention
	}
	return o.FailureRetention
}

// defaultInPlaceFailureRetention keeps a refused scan visible for a week,
// long enough for an operator to notice a library full of unreadable
// files without keeping the record forever.
const defaultInPlaceFailureRetention = 7 * 24 * time.Hour

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

	lease := newRefreshLease(st, library, sweepStartedAt)
	observations := make([]store.WatchedObservation, 0, len(scan.Files))
	for _, file := range scan.Files {
		if err := ctx.Err(); err != nil {
			report.Scan.Complete = false
			return report, err
		}
		if err := lease.hold(ctx, clock().UTC()); err != nil {
			// The library is somebody else's now. Absence must not be
			// concluded from a sweep that stopped, so the traversal is
			// marked incomplete and nothing further is written.
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
		if err := ingestDiscoveredFile(
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
	if digest == existing.ContentSHA256 {
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

// ingestDiscoveredFile publishes one newly discovered path, by whichever
// route its library's storage mode calls for.
func ingestDiscoveredFile(
	ctx context.Context,
	st watchedStore,
	blobs watchedStager,
	root *os.Root,
	library ScannedLibrary,
	file ScannedFile,
	opts WatchedSyncOptions,
	clock func() time.Time,
) error {
	if library.Storage == store.LibraryStorageInPlace {
		return ingestInPlaceFile(ctx, st, root, library, file, opts, clock)
	}
	return ingestWatchedFile(ctx, st, blobs, root, library, file, opts, clock)
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
			Source:             store.IngestScanned,
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
		errors.Is(err, ErrSourceRaced),
		errors.Is(err, ErrStagingFull),
		errors.Is(err, os.ErrNotExist),
		errors.Is(err, os.ErrPermission),
		errors.Is(err, syscall.ELOOP):
		return true
	default:
		return false
	}
}

// refreshableLibraryStore is the housekeeping surface a refresh pass
// needs. A pass no longer reads every library and walks each one: it
// claims the libraries that are due, one at a time, so a library
// refreshed by hand and a library on a five-minute interval are the
// same mechanism with different triggers.
type refreshableLibraryStore interface {
	calibreStore
	catalogAvailabilityStore
	ClaimLibraryRefresh(context.Context, time.Time, store.RefreshLease) (store.Library, bool, error)
	FinishLibraryRefresh(context.Context, string, string, time.Time, store.RefreshCode) error
}

// WatchedScanReport totals one pass over the libraries that were due.
type WatchedScanReport struct {
	Libraries int
	// Swept counts libraries whose traversal completed, so absence was
	// safe to conclude for them.
	Swept int
	// Unavailable counts libraries whose root could not be opened at all.
	// Their catalogs are left untouched.
	Unavailable int
	// Errored counts libraries whose refresh failed for a reason that is
	// not the root being unreachable. The reason itself is on the
	// library row, not here.
	Errored  int
	Ingested int
	// IngestedOwners names the owner of each library whose sweep
	// ingested at least one book, in the order first seen. A book only
	// joins a reader's sync work when something maps it, so a library
	// that just gained books has readers whose shelves cannot show them
	// yet. The pass reports who that is and lets its caller decide;
	// mapping is a work-graph write, which is not this package's job.
	IngestedOwners []string
	Unchanged      int
	Rehashed       int
	Review         int
	Relocated      int
	Superseded     int
	MarkedAbsent   int
	Failed         int
	// Skipped counts Calibre libraries whose inventory digest had not
	// moved, so the refresh stopped at the gate without a catalog write.
	Skipped int
	// Deleted counts catalog books removed because Calibre no longer has
	// them, and MetadataUpdated books Calibre described differently than
	// the catalog did.
	Deleted         int
	MetadataUpdated int
	// FilesUnavailable and FilesRestored are what the availability
	// reconciliation the pass ends with changed: a refresh records that
	// a source file is gone or back, and this is that record reaching
	// the catalog, so a file deleted while the server runs stops being
	// offered without waiting for a restart.
	FilesUnavailable int
	FilesRestored    int
}

// noteIngestedOwner records an owner once. One pass sweeps at most a
// handful of libraries, so a linear scan beats carrying a set around,
// and preserving the order keeps a pass's log and its follow-up work
// deterministic.
func (r *WatchedScanReport) noteIngestedOwner(userID string) {
	if userID == "" {
		return
	}
	if slices.Contains(r.IngestedOwners, userID) {
		return
	}
	r.IngestedOwners = append(r.IngestedOwners, userID)
}

// boolCount counts a flag, so a report can total what happened across
// libraries without a branch at every call site.
func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Changed reports whether the pass did anything worth logging.
func (r WatchedScanReport) Changed() bool {
	return r.Ingested != 0 || r.Rehashed != 0 || r.Review != 0 ||
		r.Relocated != 0 || r.Superseded != 0 ||
		r.MarkedAbsent != 0 || r.Failed != 0 || r.Unavailable != 0 ||
		r.Errored != 0 || r.Deleted != 0 || r.MetadataUpdated != 0 ||
		r.FilesUnavailable != 0 || r.FilesRestored != 0
}

// refreshAvailabilityPageSize bounds one availability reconciliation
// step after a refresh. It is the same order as the recovery batch the
// startup pass uses: large enough that a library whose mount came back
// converges in a few statements, small enough not to hold one long
// transaction over a catalog somebody is reading.
const refreshAvailabilityPageSize = 200

// maxRefreshesPerPass bounds one pass. A claim stamps the attempt, so a
// library cannot be claimed twice within its own interval and the loop
// terminates on its own; the ceiling is there for the case where it
// does not — a clock that jumped backwards, a store that reports a
// claim it did not make — so that a broken schedule costs a slow tick
// rather than a worker that never returns.
const maxRefreshesPerPass = 1000

// RunRefreshPass refreshes every library that is due, and only those.
//
// One library's failure does not stop the others. A root on a network
// mount that is down is the ordinary case here, and letting it stall every
// other library's scanning would make one flaky disk look like a broken
// server. The failure is recorded against the library that had it, which
// is what the admin panel reads: an error that only ever reached the log
// is an error nobody sees.
func RunRefreshPass(
	ctx context.Context,
	st refreshableLibraryStore,
	blobs watchedStager,
	opts WatchedSyncOptions,
	clock func() time.Time,
) (WatchedScanReport, error) {
	var report WatchedScanReport
	if st == nil || blobs == nil || clock == nil || opts.MaxFileBytes <= 0 {
		return report, store.ErrInvalidTransition
	}
	if err := refreshDueLibraries(
		ctx, st, blobs, opts, clock, &report); err != nil {
		return report, err
	}
	if report.Libraries == 0 {
		return report, nil
	}
	// A refresh writes presence — source_seen_at and source_absent_at —
	// and presence only becomes availability here. Running it now rather
	// than at the next startup is what makes a file deleted or returned
	// under a running server change what the catalog offers.
	availability, err := ReconcileCatalogAvailability(
		ctx, st, clock().UTC(), refreshAvailabilityPageSize)
	report.FilesUnavailable = availability.FilesMarkedMissing
	report.FilesRestored = availability.FilesMarkedAvailable
	if err != nil {
		return report, err
	}
	return report, nil
}

// refreshDueLibraries is the claiming loop itself, split out so that
// what follows every pass — reconciling availability — happens whether
// the loop ran out of libraries or ran out of its ceiling.
func refreshDueLibraries(
	ctx context.Context,
	st refreshableLibraryStore,
	blobs watchedStager,
	opts WatchedSyncOptions,
	clock func() time.Time,
	report *WatchedScanReport,
) error {
	for range maxRefreshesPerPass {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := clock().UTC()
		lease := store.RefreshLease{
			Owner: newRefreshOwner(),
			Until: now.Add(store.DefaultRefreshLease),
		}
		library, claimed, err := st.ClaimLibraryRefresh(ctx, now, lease)
		if err != nil {
			return fmt.Errorf("claim library refresh: %w", err)
		}
		if !claimed {
			return nil
		}
		if library.RootPath == nil || *library.RootPath == "" {
			// A library with a root is the only thing a claim can
			// return, so this is a database somebody edited by hand.
			// Finishing it stops it being claimed again in a loop.
			_ = st.FinishLibraryRefresh(ctx, library.ID, lease.Owner,
				clock(), store.RefreshCodeNoRootPath)
			continue
		}
		report.Libraries++
		scanned := ScannedLibrary{
			ID:       library.ID,
			Source:   library.Source,
			Storage:  library.Storage,
			RootPath: *library.RootPath,
			// The owner is the principal the jobs belong to. The quota
			// principal is charged separately by the commit, so an owner
			// who is not the payer still cannot spend somebody else's
			// allowance by being named here.
			ActorUserID:     library.OwnerUserID,
			InventoryDigest: library.LastInventoryDigest,
			Lease:           lease,
		}
		var result WatchedSyncReport
		var syncErr error
		if library.Source == store.LibraryCalibre {
			var calibreResult CalibreSyncReport
			calibreResult, syncErr = SyncCalibreLibrary(
				ctx, st, blobs, scanned, opts, clock)
			result = calibreResult.Sync
			report.Skipped += boolCount(calibreResult.Skipped)
			report.Deleted += calibreResult.Deleted
			report.MetadataUpdated += calibreResult.MetadataUpdated
		} else {
			result, syncErr = SyncScannedLibrary(
				ctx, st, blobs, scanned, opts, clock)
		}
		report.Ingested += result.Ingested
		if result.Ingested > 0 {
			report.noteIngestedOwner(library.OwnerUserID)
		}
		report.Unchanged += result.Unchanged
		report.Rehashed += result.Rehashed
		report.Relocated += result.Relocated
		report.Superseded += result.Superseded
		report.Review += result.Review
		report.MarkedAbsent += result.MarkedAbsent
		report.Failed += result.Failed
		if syncErr != nil && ctx.Err() != nil {
			return syncErr
		}
		code := refreshCodeFor(syncErr, result)
		if syncErr != nil {
			// The error itself goes here and nowhere else. It names
			// paths, mount points and database files, which no page
			// under /ui may render (ADR-0013), so the library keeps the
			// bounded code and the log keeps the detail.
			slog.Error("library refresh failed",
				"library", library.ID, "code", string(code), "err", syncErr)
		}
		if err := st.FinishLibraryRefresh(
			ctx, library.ID, lease.Owner, clock(), code); err != nil {
			if errors.Is(err, store.ErrRefreshLeaseLost) {
				// Another worker owns this library and has recorded, or
				// will record, its own outcome. Ours is not news.
				report.Errored++
				continue
			}
			return fmt.Errorf(
				"finish refresh of library %q: %w", library.ID, err)
		}
		switch {
		case syncErr != nil && errors.Is(syncErr, ErrRootUnavailable):
			report.Unavailable++
		case syncErr != nil:
			report.Errored++
		case result.Scan.Complete:
			report.Swept++
		}
	}
	return nil
}

// newRefreshOwner mints the token one claim is held by. It is random
// rather than derived from the host or the process, because two workers
// on one machine — a `refresh-library` run and the scheduler — must not
// be able to mistake each other for themselves.
func newRefreshOwner() string { return uuid.NewString() }

// refreshCodeFor is what the library remembers about a refresh: a code
// from a closed set the admin panel has wording for, never the error.
func refreshCodeFor(syncErr error, result WatchedSyncReport) store.RefreshCode {
	switch {
	case syncErr == nil && result.Scan.Complete:
		return store.RefreshCodeNone
	case syncErr == nil:
		// An incomplete traversal is not an error — a limit was hit, or
		// a subdirectory could not be read — but it is not a refresh
		// either, because absence cannot be concluded from it. Saying so
		// is the difference between a library that is quietly
		// half-indexed and one an administrator can see is.
		return store.RefreshCodeIncompleteScan
	case errors.Is(syncErr, store.ErrRefreshLeaseLost):
		return store.RefreshCodeLeaseLost
	case errors.Is(syncErr, calibre.ErrUnsupportedSchema):
		return store.RefreshCodeUnsupportedSchema
	case errors.Is(syncErr, ErrCalibreUnreadable):
		return store.RefreshCodeUnreadableDatabase
	case errors.Is(syncErr, ErrRootUnavailable):
		return store.RefreshCodeRootUnavailable
	default:
		return store.RefreshCodeFailed
	}
}
