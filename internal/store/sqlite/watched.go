package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// watchedFileColumns reads the snapshot a sweep compares against. Both
// halves of it — which bytes were catalogued and how many there were —
// live on the file row since ADR-0014, because a file the server keeps
// no copy of has no blob to ask.
const watchedFileColumns = `f.id, f.library_id, f.book_id, bk.status,
	f.source_relative_path, f.content_sha256, f.content_size_bytes,
	f.source_modified_at, f.availability, f.source_absent_at`

func scanWatchedFile(row interface{ Scan(...any) error }) (store.WatchedFile, error) {
	var file store.WatchedFile
	var path, modified, absent sql.NullString
	if err := row.Scan(
		&file.FileID, &file.LibraryID, &file.BookID, &file.BookStatus,
		&path, &file.ContentSHA256, &file.SizeBytes,
		&modified, &file.Availability, &absent,
	); err != nil {
		return file, err
	}
	file.SourceRelativePath = path.String
	if modified.Valid {
		at, err := parseTime(modified.String)
		if err != nil {
			return file, err
		}
		file.SourceModifiedAt = &at
	}
	file.SourceAbsent = absent.Valid
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
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+watchedFileColumns+`
		 FROM book_files f
		 JOIN books bk ON bk.library_id = f.library_id AND bk.id = f.book_id
		 WHERE f.library_id = ? AND f.source_relative_path = ?
		   AND f.source = 'scanned' AND bk.status <> 'trashed'
		 ORDER BY f.id`, libraryID, sourceRelativePath)
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
	stamp := formatTime(at.UTC())
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
		res, err := tx.ExecContext(ctx,
			`UPDATE book_files
			 SET source_seen_at = ?, source_absent_at = NULL,
			     source_modified_at = ?, updated_at = ?
			 WHERE library_id = ? AND source_relative_path = ?
			   AND source = 'scanned'`,
			stamp, formatTime(p.ModifiedAt.UTC()), stamp,
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
	// Timestamps are fixed-width RFC3339 (see timeFormat), so text order
	// is time order and plain comparisons are correct.
	started := formatTime(sweepStartedAt.UTC())
	res, err := s.db.ExecContext(ctx,
		`UPDATE book_files SET source_absent_at = ?, updated_at = ?
		 WHERE id IN (
		     SELECT f.id FROM book_files f
		     WHERE f.library_id = ? AND f.source = 'scanned'
		       AND f.source_absent_at IS NULL
		       AND f.created_at < ?
		       AND (f.source_seen_at IS NULL OR f.source_seen_at < ?)
		     ORDER BY f.id
		     LIMIT ?
		 )`,
		formatTime(at.UTC()), formatTime(at.UTC()),
		libraryID, started, started, limit)
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
	stamp := formatTime(at.UTC())
	var res sql.Result
	var err error
	if strings.TrimSpace(reason) == "" {
		res, err = s.db.ExecContext(ctx,
			`UPDATE books SET status = 'missing', review_reason = NULL,
			     updated_at = ?
			 WHERE library_id = ? AND id = ? AND status = 'review'`,
			stamp, libraryID, bookID)
	} else {
		// Trashed books are excluded because trash is a user's decision
		// and a background pass must not undo it; a book already in
		// review has its reason replaced, so a later sweep can say
		// something more accurate than the first one did.
		res, err = s.db.ExecContext(ctx,
			`UPDATE books SET status = 'review', review_reason = ?,
			     updated_at = ?
			 WHERE library_id = ? AND id = ?
			   AND status IN ('active', 'missing', 'review')`,
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
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+bookColumns+`
		 FROM books b
		 WHERE b.library_id = ? AND b.status = 'review'
		 ORDER BY b.updated_at, b.id
		 LIMIT ?`, libraryID, limit)
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

// ListScannableLibraries reads every library on the instance that has a
// root to sweep, whatever its refresh policy — a library refreshed by
// hand is still scannable, it just is not due on its own. It is a
// background-job query and crosses users by design: a scanner sweeps
// what the server was configured to sweep, not what the caller can see.
func (s *Store) ListScannableLibraries(ctx context.Context) ([]store.Library, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+plainLibraryColumns+`
		 FROM libraries
		 WHERE source <> 'managed' AND root_path IS NOT NULL AND root_path <> ''
		 ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Library
	for rows.Next() {
		lib, err := scanPlainLibrary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lib)
	}
	return out, rows.Err()
}
