package postgres

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// watchedFileColumns joins the blob because a sweep decides whether to
// reread a source by comparing sizes, and the size the snapshot was taken
// at is the blob's, not the file row's.
const watchedFileColumns = `f.id, f.library_id, f.book_id, bk.status,
	f.source_relative_path, f.blob_sha256, bl.size_bytes,
	f.source_modified_at, f.availability, f.source_absent_at`

func scanWatchedFile(row interface{ Scan(...any) error }) (store.WatchedFile, error) {
	var file store.WatchedFile
	var path sql.NullString
	var absent *time.Time
	if err := row.Scan(
		&file.FileID, &file.LibraryID, &file.BookID, &file.BookStatus,
		&path, &file.BlobSHA256, &file.SizeBytes,
		&file.SourceModifiedAt, &file.Availability, &absent,
	); err != nil {
		return file, err
	}
	file.SourceRelativePath = path.String
	file.SourceAbsent = absent != nil
	return file, nil
}

// WatchedFilesByPath reads every watched file recorded at one source path.
// Trashed books are excluded: their path is no longer a claim the catalog
// makes, so a sweep must neither keep it alive nor treat a file appearing
// there as a change to something.
func (s *Store) WatchedFilesByPath(
	ctx context.Context,
	libraryID, sourceRelativePath string,
) ([]store.WatchedFile, error) {
	if libraryID == "" || sourceRelativePath == "" {
		return nil, store.ErrInvalidTransition
	}
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT `+watchedFileColumns+`
		 FROM book_files f
		 JOIN books bk ON bk.library_id = f.library_id AND bk.id = f.book_id
		 JOIN blobs bl ON bl.sha256 = f.blob_sha256
		 WHERE f.library_id = ? AND f.source_relative_path = ?
		   AND f.source = 'watched' AND bk.status <> 'trashed'
		 ORDER BY f.id`), libraryID, sourceRelativePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []store.WatchedFile
	for rows.Next() {
		file, err := scanWatchedFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

// MarkWatchedSourcesSeen records one traversal batch's observations.
func (s *Store) MarkWatchedSourcesSeen(
	ctx context.Context,
	libraryID string,
	paths []store.WatchedObservation,
	at time.Time,
) (int, error) {
	if libraryID == "" || at.IsZero() {
		return 0, store.ErrInvalidTransition
	}
	if err := store.ValidateWatchedObservations(paths); err != nil {
		return 0, err
	}
	stamp := at.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// One statement per path rather than a single CASE over all of them:
	// the batch is bounded, the index on (library_id, source_relative_path)
	// makes each one a lookup, and the readable version is the one that
	// stays correct when a column is added to it.
	total := 0
	for _, p := range paths {
		res, err := tx.ExecContext(ctx, q(
			`UPDATE book_files
			 SET source_seen_at = ?, source_absent_at = NULL,
			     source_modified_at = ?, updated_at = ?
			 WHERE library_id = ? AND source_relative_path = ?
			   AND source = 'watched'`),
			stamp, p.ModifiedAt.UTC(), stamp,
			libraryID, p.SourceRelativePath)
		if err != nil {
			return 0, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		total += int(affected)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, nil
}

// MarkWatchedSourcesAbsent records the paths a completed sweep did not find.
func (s *Store) MarkWatchedSourcesAbsent(
	ctx context.Context,
	libraryID string,
	sweepStartedAt, at time.Time,
	limit int,
) (int, error) {
	if libraryID == "" || sweepStartedAt.IsZero() || at.IsZero() ||
		limit < 1 || limit > 500 {
		return 0, store.ErrInvalidTransition
	}
	started := sweepStartedAt.UTC()
	stamp := at.UTC()
	res, err := s.db.ExecContext(ctx, q(
		`UPDATE book_files SET source_absent_at = ?, updated_at = ?
		 WHERE id IN (
		     SELECT f.id FROM book_files f
		     WHERE f.library_id = ? AND f.source = 'watched'
		       AND f.source_absent_at IS NULL
		       AND f.created_at < ?
		       AND (f.source_seen_at IS NULL OR f.source_seen_at < ?)
		     ORDER BY f.id
		     LIMIT ?
		 )`),
		stamp, stamp, libraryID, started, started, limit)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	return int(affected), err
}

// SetCatalogBookReview moves one book into or out of review.
func (s *Store) SetCatalogBookReview(
	ctx context.Context,
	libraryID, bookID, reason string,
	at time.Time,
) (bool, error) {
	if libraryID == "" || bookID == "" || at.IsZero() || len(reason) > 4096 {
		return false, store.ErrInvalidTransition
	}
	stamp := at.UTC()
	var res sql.Result
	var err error
	if strings.TrimSpace(reason) == "" {
		res, err = s.db.ExecContext(ctx, q(
			`UPDATE books SET status = 'missing', review_reason = NULL,
			     updated_at = ?
			 WHERE library_id = ? AND id = ? AND status = 'review'`),
			stamp, libraryID, bookID)
	} else {
		// Trashed books are excluded because trash is a user's decision
		// and a background pass must not undo it; a book already in
		// review has its reason replaced, so a later sweep can say
		// something more accurate than the first one did.
		res, err = s.db.ExecContext(ctx, q(
			`UPDATE books SET status = 'review', review_reason = ?,
			     updated_at = ?
			 WHERE library_id = ? AND id = ?
			   AND status IN ('active', 'missing', 'review')`),
			reason, stamp, libraryID, bookID)
	}
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

// ListBooksInReview pages one library's books awaiting a decision.
func (s *Store) ListBooksInReview(
	ctx context.Context,
	userID, libraryID string,
	limit int,
) ([]store.CatalogBook, error) {
	if limit < 1 || limit > 500 {
		return nil, store.ErrInvalidTransition
	}
	if _, err := s.LibraryByID(
		ctx, userID, libraryID, store.LibraryRoleManage); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT `+bookColumns+`
		 FROM books b
		 WHERE b.library_id = ? AND b.status = 'review'
		 ORDER BY b.updated_at, b.id
		 LIMIT ?`), libraryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var books []store.CatalogBook
	for rows.Next() {
		book, err := scanCatalogBook(rows)
		if err != nil {
			return nil, err
		}
		books = append(books, book)
	}
	return books, rows.Err()
}

// ListWatchedLibraries reads every watched library on the instance. It is
// a background-job query and crosses users by design: a scanner sweeps
// what the server was configured to sweep, not what the caller can see.
// Libraries with no root are excluded, because there is nothing to sweep.
func (s *Store) ListWatchedLibraries(ctx context.Context) ([]store.Library, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, owner_user_id, quota_user_id, kind, name, root_path,
		        config_json, created_at, updated_at
		 FROM libraries
		 WHERE kind = 'watched' AND root_path IS NOT NULL AND root_path <> ''
		 ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Library
	for rows.Next() {
		var lib store.Library
		if err := rows.Scan(&lib.ID, &lib.OwnerUserID, &lib.QuotaUserID,
			&lib.Kind, &lib.Name, &lib.RootPath, &lib.ConfigJSON,
			&lib.CreatedAt, &lib.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, lib)
	}
	return out, rows.Err()
}
