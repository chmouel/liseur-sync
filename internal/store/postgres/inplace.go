package postgres

import (
	"context"
	"database/sql"

	"github.com/chmouel/liseur-sync/internal/store"
)

// CommitInPlaceBook publishes a book whose bytes this server never copied.
//
// It is promotion with everything about ownership removed: no blob row, no
// reservation, no quota hold, and therefore nothing a later garbage
// collection, backup or reconciliation pass can act on. What replaces the
// blob as proof is the file itself — its digest, its size and the
// modification time it carried when it was read — recorded on the file row
// so the read path can refuse bytes that moved since (ADR-0014).
//
// The job goes from `received` straight to `promoted`. The intermediate
// states name where a staged artifact is, and here the bytes never moved.
func (s *Store) CommitInPlaceBook(
	ctx context.Context,
	userID, jobID string,
	request store.CommitInPlaceBookRequest,
) (store.IngestPromotionResult, error) {
	if err := store.ValidateInPlaceBook(request); err != nil {
		return store.IngestPromotionResult{}, err
	}
	fingerprint, err := store.InPlaceBookFingerprint(request)
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	defer tx.Rollback()
	job, err := ingestJobByIDTx(ctx, tx, userID, jobID)
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	if job.State == store.IngestPromoted {
		return inPlaceReplayTx(ctx, tx, job, request, fingerprint)
	}
	if job.State != store.IngestReceived ||
		job.Revision != request.ExpectedRevision {
		return store.IngestPromotionResult{}, store.ErrStaleRevision
	}
	if request.UpdatedAt.Before(job.UpdatedAt) {
		return store.IngestPromotionResult{}, store.ErrInvalidTransition
	}
	if job.Storage != store.LibraryStorageInPlace ||
		request.Book.LibraryID != job.LibraryID ||
		request.File.LibraryID != job.LibraryID ||
		request.File.BookID != request.Book.ID ||
		request.File.Source != job.Source ||
		!sameOptionalString(request.File.SourceRelativePath, job.SourceRelativePath) {
		return store.IngestPromotionResult{}, store.ErrContentMismatch
	}
	if err := insertPromotionBookTx(ctx, tx, request.Book); err != nil {
		return store.IngestPromotionResult{}, err
	}
	file := request.File
	mediaType := file.MediaType
	if mediaType == "" {
		mediaType = "application/epub+zip"
	}
	if _, err := tx.ExecContext(ctx, q(`INSERT INTO book_files
		 (id, library_id, book_id, storage, content_sha256,
		  content_size_bytes, blob_sha256, source,
		  source_relative_path, original_filename, media_type,
		  partial_md5, dc_identifier, availability,
		  source_seen_at, source_modified_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		file.ID, file.LibraryID, file.BookID, string(file.Storage),
		file.ContentSHA256, file.ContentSizeBytes, string(file.Source),
		file.SourceRelativePath, file.OriginalFilename, mediaType,
		file.PartialMD5, file.DCIdentifier,
		string(file.Availability), request.UpdatedAt.UTC(),
		file.SourceModifiedAt.UTC(), file.CreatedAt.UTC(),
		file.UpdatedAt.UTC()); err != nil {
		if isUniqueErr(err) {
			return store.IngestPromotionResult{}, store.ErrConflict
		}
		return store.IngestPromotionResult{}, err
	}
	res, err := tx.ExecContext(ctx, q(`UPDATE ingest_jobs
		 SET state = 'promoted', book_library_id = ?, book_id = ?,
		     content_sha256 = ?, bytes_received = ?,
		     extracted_embedded_metadata_json = ?,
		     promotion_fingerprint = ?, revision = revision + 1, updated_at = ?
		 WHERE user_id = ? AND id = ? AND state = 'received' AND revision = ?`),
		job.LibraryID, request.Book.ID, file.ContentSHA256,
		file.ContentSizeBytes, request.ExtractedEmbeddedMetadataJSON,
		fingerprint,
		request.UpdatedAt.UTC(),
		userID, jobID, request.ExpectedRevision)
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return store.IngestPromotionResult{}, err
	} else if n == 0 {
		return store.IngestPromotionResult{}, store.ErrStaleRevision
	}
	job, err = ingestJobByIDTx(ctx, tx, userID, jobID)
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	book, err := scanCatalogBook(tx.QueryRowContext(ctx, q(`SELECT `+bookColumns+` FROM books b WHERE b.id = ?`), request.Book.ID))
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	stored, err := scanBookFile(tx.QueryRowContext(ctx, q(`SELECT `+bookFileColumns+` FROM book_files f WHERE f.id = ?`),
		file.ID))
	if err != nil {
		return store.IngestPromotionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.IngestPromotionResult{}, err
	}
	return store.IngestPromotionResult{Job: job, Book: book, File: stored}, nil
}

// inPlaceReplayTx reads back the rows an earlier identical commit created.
// A different payload under the same job is a conflict, never an
// overwrite: two passes that disagree about what a file is have found two
// different files.
func inPlaceReplayTx(
	ctx context.Context,
	tx *sql.Tx,
	job store.IngestJob,
	request store.CommitInPlaceBookRequest,
	fingerprint string,
) (store.IngestPromotionResult, error) {
	if job.PromotionFingerprint == nil ||
		*job.PromotionFingerprint != fingerprint ||
		job.BookID == nil || *job.BookID != request.Book.ID {
		return store.IngestPromotionResult{}, store.ErrPromotionConflict
	}
	book, err := scanCatalogBook(tx.QueryRowContext(ctx, q(`SELECT `+bookColumns+` FROM books b
		 WHERE b.library_id = ? AND b.id = ?`),
		job.LibraryID, *job.BookID))
	if err != nil {
		return store.IngestPromotionResult{}, store.ErrInvariantViolation
	}
	file, err := scanBookFile(tx.QueryRowContext(ctx, q(`SELECT `+bookFileColumns+` FROM book_files f
		 WHERE f.library_id = ? AND f.id = ? AND f.book_id = ?`),
		job.LibraryID, request.File.ID, *job.BookID))
	if err != nil {
		return store.IngestPromotionResult{}, store.ErrPromotionConflict
	}
	if file.ContentSHA256 != request.File.ContentSHA256 {
		return store.IngestPromotionResult{}, store.ErrPromotionConflict
	}
	return store.IngestPromotionResult{
		Job: job, Book: book, File: file, Replayed: true,
	}, nil
}
