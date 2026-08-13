package postgres

import (
	"context"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// markFilesMissing hides files whose bytes the blob reconciliation pass
// could not find, or whose watched source a completed sweep proved gone.
// Those are separate axes: a watched book's snapshot stays in the CAS
// after the file it was copied from is deleted, so blob presence alone
// would keep offering a book the library no longer contains.
//
// Superseded files are excluded because supersession already means "do
// not serve this", and restoring the blob must not resurrect a file a
// newer upload replaced.
const markFilesMissing = `
UPDATE book_files SET availability = 'missing', updated_at = ?
WHERE id IN (
    SELECT f.id FROM book_files f
    JOIN blobs b ON b.sha256 = f.blob_sha256
    WHERE f.availability = 'available'
      AND (b.missing_at IS NOT NULL OR f.source_absent_at IS NOT NULL)
    ORDER BY f.id
    LIMIT ?
)`

const markFilesAvailable = `
UPDATE book_files SET availability = 'available', updated_at = ?
WHERE id IN (
    SELECT f.id FROM book_files f
    JOIN blobs b ON b.sha256 = f.blob_sha256
    WHERE f.availability = 'missing' AND b.missing_at IS NULL
      AND f.source_absent_at IS NULL
    ORDER BY f.id
    LIMIT ?
)`

// markBooksMissing requires the book to have at least one file. A book with
// no files at all has never had bytes to lose, so its status is not
// evidence about the filesystem and this pass leaves it alone.
const markBooksMissing = `
UPDATE books SET status = 'missing', updated_at = ?
WHERE id IN (
    SELECT bk.id FROM books bk
    WHERE bk.status = 'active'
      AND EXISTS (
          SELECT 1 FROM book_files f
          WHERE f.library_id = bk.library_id AND f.book_id = bk.id
      )
      AND NOT EXISTS (
          SELECT 1 FROM book_files f
          WHERE f.library_id = bk.library_id AND f.book_id = bk.id
            AND f.availability = 'available'
      )
    ORDER BY bk.id
    LIMIT ?
)`

const markBooksActive = `
UPDATE books SET status = 'active', updated_at = ?
WHERE id IN (
    SELECT bk.id FROM books bk
    WHERE bk.status = 'missing'
      AND EXISTS (
          SELECT 1 FROM book_files f
          WHERE f.library_id = bk.library_id AND f.book_id = bk.id
            AND f.availability = 'available'
      )
    ORDER BY bk.id
    LIMIT ?
)`

// ReconcileCatalogAvailability propagates blob presence into the catalog.
// It deliberately does not bump books.revision: presence is not metadata,
// and a background pass must not invalidate an editor's in-flight
// optimistic update.
func (s *Store) ReconcileCatalogAvailability(
	ctx context.Context,
	at time.Time,
	limit int,
) (store.CatalogAvailabilityResult, error) {
	var result store.CatalogAvailabilityResult
	if at.IsZero() || limit < 1 || limit > 500 {
		return result, store.ErrInvalidTransition
	}
	stamp := at.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	// Files are settled before books so that the book statements observe
	// this pass's file changes rather than the previous pass's.
	for _, step := range []struct {
		query string
		count *int
	}{
		{markFilesMissing, &result.FilesMarkedMissing},
		{markFilesAvailable, &result.FilesMarkedAvailable},
		{markBooksMissing, &result.BooksMarkedMissing},
		{markBooksActive, &result.BooksMarkedActive},
	} {
		res, err := tx.ExecContext(ctx, q(step.query), stamp, limit)
		if err != nil {
			return store.CatalogAvailabilityResult{}, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return store.CatalogAvailabilityResult{}, err
		}
		*step.count = int(affected)
	}
	if err := tx.Commit(); err != nil {
		return store.CatalogAvailabilityResult{}, err
	}
	return result, nil
}
