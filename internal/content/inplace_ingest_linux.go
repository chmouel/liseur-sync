//go:build linux

package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// ErrSourceRaced says the file moved underneath the pass that was reading
// it. Nothing durable was written, and the next sweep reads it again; it
// is a fact about that one path, not about the library.
var ErrSourceRaced = errors.New("content: source changed while it was being read")

// ingestInPlaceFile publishes one discovered publication without copying
// it anywhere.
//
// Everything is done from a single descriptor: the digest, the structural
// validation and the metadata all come from the file that was opened once,
// and the descriptor is fstat'ed again at the end. That is what replaces
// the durable staged artifact an upload has. There is nothing to restart
// from if this pass dies halfway — no bytes were written, so there is
// nothing to clean up either, and the next sweep simply does the work
// again (ADR-0014).
func ingestInPlaceFile(
	ctx context.Context,
	st watchedStore,
	root *os.Root,
	library ScannedLibrary,
	file ScannedFile,
	opts WatchedSyncOptions,
	clock func() time.Time,
) (ingestOutcome, error) {
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
		return ingestPending, fmt.Errorf("queue in-place path %q: %w", relativePath, err)
	}
	if !created && job.State != store.IngestReceived {
		// Already published, already refused, or being published by
		// another pass. Reading the file again could only produce a
		// second answer to a question that has one.
		return ingestPending, nil
	}

	src, err := openWatchedSource(root, relativePath)
	if err != nil {
		return ingestPending, err
	}
	defer src.Close()

	before, err := src.Stat()
	if err != nil {
		return ingestPending, err
	}
	if !before.Mode().IsRegular() {
		return ingestPending, fmt.Errorf("read in-place path %q: %w", relativePath, ErrUnsafePath)
	}
	if before.Size() > opts.MaxFileBytes {
		return ingestPending, fmt.Errorf("read in-place path %q: %w", relativePath, ErrTooLarge)
	}

	digest := sha256.New()
	if _, err := copyBounded(ctx, io.Discard, digest, src, opts.MaxFileBytes); err != nil {
		return ingestPending, fmt.Errorf("read in-place path %q: %w", relativePath, err)
	}
	contentSHA := hex.EncodeToString(digest.Sum(nil))

	// Validate reads through ReadAt, which is positional, so the hash
	// above left nothing to rewind.
	publication, err := epub.Validate(ctx, src, before.Size(), opts.epubLimits())
	if err != nil {
		code, contentFailure := epub.ErrorCode(err)
		if !contentFailure {
			return ingestPending, fmt.Errorf("validate in-place path %q: %w", relativePath, err)
		}
		return ingestRefused, quarantineInPlaceJob(ctx, st, job, string(code),
			"EPUB content failed structural validation", opts, clock)
	}

	// The last word belongs to the descriptor, not to the traversal: if
	// the file moved while it was being read, the digest and the metadata
	// describe bytes that are no longer there, and publishing them would
	// catalogue a book that never existed in that form.
	after, err := src.Stat()
	if err != nil {
		return ingestPending, err
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return ingestPending, fmt.Errorf("read in-place path %q: %w", relativePath, ErrSourceRaced)
	}

	metadataJSON, err := json.Marshal(publication.Metadata)
	if err != nil {
		return ingestPending, fmt.Errorf(
			"marshal metadata for in-place path %q: %w", relativePath, err)
	}
	patterns, err := inPlacePatterns(ctx, opts.Patterns, job)
	if err != nil {
		return ingestPending, err
	}
	commitAt := clock().UTC()
	request := newInPlaceBook(job, metadataJSON, patterns, store.BookFile{
		ContentSHA256:    contentSHA,
		ContentSizeBytes: before.Size(),
		SourceModifiedAt: ptr(before.ModTime().UTC()),
	}, commitAt)
	result, err := st.CommitInPlaceBook(
		ctx, library.ActorUserID, job.ID, request)
	if err != nil {
		return ingestPending, fmt.Errorf("commit in-place path %q: %w", relativePath, err)
	}
	if result.Replayed {
		return ingestQueued, nil
	}
	if _, _, err := MaterializeBookMetadata(
		ctx, st, result.Job, patterns, clock); err != nil {
		// The book, its file and its title are durable and correct. A tag
		// that did not attach is worth another pass, not undoing a good
		// publication.
		return ingestQueued, nil
	}
	return ingestQueued, nil
}

// newInPlaceBook describes the book and file one in-place job becomes.
// Like promotion, it is a pure function of the job and what was read, so
// a commit whose response was lost rebuilds byte-identically and replays.
func newInPlaceBook(
	job store.IngestJob,
	metadataJSON []byte,
	patterns []metadata.PathPattern,
	read store.BookFile,
	at time.Time,
) store.CommitInPlaceBookRequest {
	described := job
	described.ExtractedEmbeddedMetadataJSON = metadataJSON
	bookID := promotionID("book", job)
	book := promotedBook(described, patterns, store.CatalogBook{
		ID:        bookID,
		LibraryID: job.LibraryID,
		Status:    store.BookActive,
		CreatedAt: at,
		UpdatedAt: at,
	})
	return store.CommitInPlaceBookRequest{
		ExpectedRevision:              job.Revision,
		ExtractedEmbeddedMetadataJSON: metadataJSON,
		Book:                          book,
		File: store.BookFile{
			ID:                 promotionID("file", job),
			LibraryID:          job.LibraryID,
			BookID:             bookID,
			Storage:            store.LibraryStorageInPlace,
			ContentSHA256:      read.ContentSHA256,
			ContentSizeBytes:   read.ContentSizeBytes,
			Source:             job.Source,
			SourceRelativePath: job.SourceRelativePath,
			SourceModifiedAt:   read.SourceModifiedAt,
			OriginalFilename:   promotionFilename(job),
			MediaType:          mediaTypeEPUB,
			Availability:       store.BookFileAvailable,
			CreatedAt:          at,
			UpdatedAt:          at,
		},
		UpdatedAt: at,
	}
}

// inPlacePatterns resolves the library's filename layouts. A library
// nobody can describe stalls its own books rather than being filed under
// whatever layout happened to be compiled in.
func inPlacePatterns(
	ctx context.Context, resolver PatternResolver, job store.IngestJob,
) ([]metadata.PathPattern, error) {
	if resolver == nil {
		return nil, nil
	}
	patterns, err := resolver.PatternsFor(ctx, job.UserID, job.LibraryID)
	if err != nil {
		return nil, fmt.Errorf(
			"read library layouts for job %q: %w", job.ID, err)
	}
	return patterns, nil
}

// quarantineInPlaceJob records a file this server will never publish. It
// is the same answer an unreadable upload gets, minus the artifact there
// is nothing to expire.
func quarantineInPlaceJob(
	ctx context.Context,
	st watchedStore,
	job store.IngestJob,
	code, detail string,
	opts WatchedSyncOptions,
	clock func() time.Time,
) error {
	at := clock().UTC()
	expiresAt := at.Add(opts.failureRetention())
	if _, err := st.TransitionIngestJob(ctx, job.UserID, job.ID,
		store.IngestJobTransition{
			ExpectedState: job.State, ExpectedRevision: job.Revision,
			NextState:   store.IngestQuarantined,
			ErrorCode:   code,
			ErrorDetail: detail,
			ExpiresAt:   &expiresAt, UpdatedAt: at,
		}); err != nil {
		return fmt.Errorf("quarantine in-place job %q: %w", job.ID, err)
	}
	return nil
}

func ptr[T any](value T) *T { return &value }

// readInPlaceReplacement reads bytes that are to replace a book's
// current file, with exactly the checks an in-place ingest makes: one
// descriptor, bounded, structurally validated, and stat'ed again at the
// end so a file that moved while it was being read is refused rather
// than catalogued.
//
// It returns only what a file row needs. The metadata is deliberately
// not extracted: a book whose file was replaced keeps the description
// its library is the source of truth for, and re-extracting it here
// would have the EPUB argue with Calibre on every conversion.
func readInPlaceReplacement(
	ctx context.Context,
	root *os.Root,
	relativePath string,
	opts WatchedSyncOptions,
) (store.BookFile, error) {
	var read store.BookFile
	src, err := openWatchedSource(root, relativePath)
	if err != nil {
		return read, err
	}
	defer src.Close()

	before, err := src.Stat()
	if err != nil {
		return read, err
	}
	if !before.Mode().IsRegular() {
		return read, fmt.Errorf("read in-place path %q: %w",
			relativePath, ErrUnsafePath)
	}
	if before.Size() > opts.MaxFileBytes {
		return read, fmt.Errorf("read in-place path %q: %w",
			relativePath, ErrTooLarge)
	}
	digest := sha256.New()
	if _, err := copyBounded(
		ctx, io.Discard, digest, src, opts.MaxFileBytes); err != nil {
		return read, fmt.Errorf("read in-place path %q: %w", relativePath, err)
	}
	if _, err := epub.Validate(
		ctx, src, before.Size(), opts.epubLimits()); err != nil {
		return read, fmt.Errorf(
			"validate in-place path %q: %w", relativePath, err)
	}
	after, err := src.Stat()
	if err != nil {
		return read, err
	}
	if after.Size() != before.Size() ||
		!after.ModTime().Equal(before.ModTime()) {
		return read, fmt.Errorf("read in-place path %q: %w",
			relativePath, ErrSourceRaced)
	}
	return store.BookFile{
		ContentSHA256:    hex.EncodeToString(digest.Sum(nil)),
		ContentSizeBytes: before.Size(),
		SourceModifiedAt: ptr(before.ModTime().UTC()),
	}, nil
}
