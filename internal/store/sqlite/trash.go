package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// manageableBook is the ACL predicate for destructive catalog operations.
// Read access is never enough: trashing and restoring change what everyone
// sharing the library sees.
const manageableBook = `
 FROM books b
 JOIN libraries l ON l.id = b.library_id
 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
 WHERE b.id = ? AND (l.owner_user_id = ? OR a.role = 'manage')`

func (s *Store) TrashCatalogBook(
	ctx context.Context,
	userID, bookID string,
	at, expiresAt time.Time,
) (store.CatalogBook, error) {
	if at.IsZero() || expiresAt.IsZero() || !expiresAt.After(at) {
		return store.CatalogBook{}, store.ErrInvalidTransition
	}
	return s.setBookTrashState(ctx, userID, bookID, func(
		ctx context.Context, tx *sql.Tx, book store.CatalogBook,
	) error {
		// Trashing an already-trashed book is not a transition; it would
		// silently extend someone else's retention window.
		if book.Status == store.BookTrashed {
			return store.ErrInvalidTransition
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE books
			 SET status = 'trashed', trashed_at = ?, trash_expires_at = ?,
			     updated_at = ?
			 WHERE id = ?`,
			formatTime(at.UTC()), formatTime(expiresAt.UTC()),
			formatTime(at.UTC()), bookID)
		return err
	})
}

func (s *Store) RestoreCatalogBook(
	ctx context.Context,
	userID, bookID string,
	at time.Time,
) (store.CatalogBook, error) {
	if at.IsZero() {
		return store.CatalogBook{}, store.ErrInvalidTransition
	}
	return s.setBookTrashState(ctx, userID, bookID, func(
		ctx context.Context, tx *sql.Tx, book store.CatalogBook,
	) error {
		if book.Status != store.BookTrashed {
			return store.ErrInvalidTransition
		}
		// Retention is what makes restore possible at all; past it the
		// book is waiting to be purged and its blobs may already be
		// orphan-marked.
		if book.TrashExpiresAt != nil && !book.TrashExpiresAt.After(at.UTC()) {
			return store.ErrInvalidTransition
		}
		// Restore to what the bytes actually support, so that a book
		// whose file went missing while it sat in the trash does not
		// come back advertising a download.
		var available int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM book_files
			 WHERE library_id = ? AND book_id = ? AND availability = 'available'`,
			book.LibraryID, bookID).Scan(&available); err != nil {
			return err
		}
		status := store.BookMissing
		if available > 0 {
			status = store.BookActive
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE books
			 SET status = ?, trashed_at = NULL, trash_expires_at = NULL,
			     updated_at = ?
			 WHERE id = ?`,
			string(status), formatTime(at.UTC()), bookID)
		return err
	})
}

func (s *Store) setBookTrashState(
	ctx context.Context,
	userID, bookID string,
	apply func(context.Context, *sql.Tx, store.CatalogBook) error,
) (store.CatalogBook, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.CatalogBook{}, err
	}
	defer tx.Rollback()
	book, err := scanCatalogBook(tx.QueryRowContext(ctx,
		`SELECT `+bookColumns+manageableBook, userID, bookID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return store.CatalogBook{}, store.ErrNotFound
	}
	if err != nil {
		return store.CatalogBook{}, err
	}
	if err := apply(ctx, tx, book); err != nil {
		return store.CatalogBook{}, err
	}
	updated, err := scanCatalogBook(tx.QueryRowContext(ctx,
		`SELECT `+bookColumns+` FROM books b WHERE b.id = ?`, bookID))
	if err != nil {
		return store.CatalogBook{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.CatalogBook{}, err
	}
	return updated, nil
}

func (s *Store) PurgeExpiredTrash(
	ctx context.Context,
	before time.Time,
	limit int,
) (store.TrashPurgeResult, error) {
	var result store.TrashPurgeResult
	if before.IsZero() || limit < 1 || limit > 500 {
		return result, store.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	// Timestamps are fixed-width RFC3339 (see timeFormat), so text order
	// is time order and a plain comparison is correct.
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM books
		 WHERE status = 'trashed'
		   AND trash_expires_at IS NOT NULL
		   AND trash_expires_at <= ?
		 ORDER BY trash_expires_at, id
		 LIMIT ?`,
		formatTime(before.UTC()), limit)
	if err != nil {
		return result, err
	}
	var bookIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return store.TrashPurgeResult{}, err
		}
		bookIDs = append(bookIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return store.TrashPurgeResult{}, err
	}
	if len(bookIDs) == 0 {
		return result, nil
	}

	type reference struct{ quotaUserID, blob string }
	affected := map[reference]bool{}
	blobs := map[string]bool{}
	for _, bookID := range bookIDs {
		fileRows, err := tx.QueryContext(ctx,
			`SELECT f.blob_sha256, l.quota_user_id
			 FROM book_files f
			 JOIN libraries l ON l.id = f.library_id
			 WHERE f.book_id = ?`, bookID)
		if err != nil {
			return store.TrashPurgeResult{}, err
		}
		for fileRows.Next() {
			var blob, quotaUserID string
			if err := fileRows.Scan(&blob, &quotaUserID); err != nil {
				fileRows.Close()
				return store.TrashPurgeResult{}, err
			}
			affected[reference{quotaUserID, blob}] = true
			blobs[blob] = true
			result.FilesPurged++
		}
		fileRows.Close()
		if err := fileRows.Err(); err != nil {
			return store.TrashPurgeResult{}, err
		}
		// book_files cascade with the book, which is what turns the
		// remaining reference counts below into the truth.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM books WHERE id = ?`, bookID); err != nil {
			return store.TrashPurgeResult{}, err
		}
	}

	for ref := range affected {
		// Quota is charged per principal, so it is released only when
		// that principal has no reference left anywhere — another of
		// their libraries may still hold the same deduplicated blob.
		var remaining int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM book_files f
			 JOIN libraries l ON l.id = f.library_id
			 WHERE f.blob_sha256 = ? AND l.quota_user_id = ?`,
			ref.blob, ref.quotaUserID).Scan(&remaining); err != nil {
			return store.TrashPurgeResult{}, err
		}
		if remaining > 0 {
			continue
		}
		res, err := tx.ExecContext(ctx,
			`DELETE FROM blob_reservations
			 WHERE quota_user_id = ? AND blob_sha256 = ?`,
			ref.quotaUserID, ref.blob)
		if err != nil {
			return store.TrashPurgeResult{}, err
		}
		released, err := res.RowsAffected()
		if err != nil {
			return store.TrashPurgeResult{}, err
		}
		result.ReservationsReleased += int(released)
	}

	for blob := range blobs {
		// Start the orphan grace clock here rather than waiting for the
		// next inventory pass: this transaction knows exactly which
		// blobs just lost their last reference. An ingest hold still
		// counts, so a concurrent upload of the same bytes is safe.
		res, err := tx.ExecContext(ctx,
			`UPDATE blobs SET orphaned_at = ?
			 WHERE sha256 = ? AND orphaned_at IS NULL
			   AND NOT EXISTS (
			       SELECT 1 FROM book_files f WHERE f.blob_sha256 = blobs.sha256
			   )
			   AND NOT EXISTS (
			       SELECT 1 FROM ingest_blob_holds h
			       WHERE h.blob_sha256 = blobs.sha256
			   )`,
			formatTime(before.UTC()), blob)
		if err != nil {
			return store.TrashPurgeResult{}, err
		}
		marked, err := res.RowsAffected()
		if err != nil {
			return store.TrashPurgeResult{}, err
		}
		result.BlobsOrphaned += int(marked)
	}

	if err := tx.Commit(); err != nil {
		return store.TrashPurgeResult{}, err
	}
	result.BookIDs = bookIDs
	return result, nil
}
