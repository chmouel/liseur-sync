package sqlite

// The Calibre side of a library (ADR-0014): the identity map between
// Calibre's book ids and this catalog's, the deletion that follows a
// book vanishing from metadata.db, and the digest that lets an unchanged
// library cost one read and no writes.

import (
	"context"
	"database/sql"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) CalibreBookMappings(
	ctx context.Context, libraryID string,
) (map[int64]string, error) {
	if libraryID == "" {
		return nil, store.ErrInvalidTransition
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT calibre_id, book_id FROM library_calibre_books
		  WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var calibreID int64
		var bookID string
		if err := rows.Scan(&calibreID, &bookID); err != nil {
			return nil, err
		}
		out[calibreID] = bookID
	}
	return out, rows.Err()
}

func (s *Store) MapCalibreBook(
	ctx context.Context,
	libraryID string,
	calibreID int64,
	bookID string,
	at time.Time,
) error {
	if libraryID == "" || bookID == "" || calibreID <= 0 || at.IsZero() {
		return store.ErrInvalidTransition
	}
	// Both directions are unique, so a book that took over another
	// Calibre id's row has to give up its old mapping first. That is one
	// Calibre book being renumbered, which Calibre does not do by
	// itself but a restored backup can.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM library_calibre_books
		  WHERE library_id = ? AND book_id = ? AND calibre_id <> ?`,
		libraryID, bookID, calibreID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO library_calibre_books
		     (library_id, calibre_id, book_id, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (library_id, calibre_id)
		 DO UPDATE SET book_id = excluded.book_id`,
		libraryID, calibreID, bookID, formatTime(at.UTC())); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteCalibreBooks(
	ctx context.Context,
	libraryID string,
	calibreIDs []int64,
	at time.Time,
) (store.TrashPurgeResult, error) {
	var result store.TrashPurgeResult
	if libraryID == "" || at.IsZero() {
		return result, store.ErrInvalidTransition
	}
	if len(calibreIDs) == 0 {
		return result, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	bookIDs := make([]string, 0, len(calibreIDs))
	for _, calibreID := range calibreIDs {
		var bookID string
		err := tx.QueryRowContext(ctx,
			`SELECT book_id FROM library_calibre_books
			  WHERE library_id = ? AND calibre_id = ?`,
			libraryID, calibreID).Scan(&bookID)
		if err != nil {
			// A Calibre id with no mapping is a book this server never
			// catalogued, which is nothing to delete rather than an
			// error: the refresh that found it gone is right either way.
			continue
		}
		bookIDs = append(bookIDs, bookID)
	}
	if len(bookIDs) == 0 {
		return result, nil
	}
	// The mapping rows cascade with the books they name.
	result, err = purgeBooksTx(ctx, tx, bookIDs, at)
	if err != nil {
		return store.TrashPurgeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.TrashPurgeResult{}, err
	}
	result.BookIDs = bookIDs
	return result, nil
}

func (s *Store) SetLibraryInventoryDigest(
	ctx context.Context, libraryID, digest string, at time.Time,
) error {
	if libraryID == "" || at.IsZero() {
		return store.ErrInvalidTransition
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE libraries
		    SET last_inventory_digest = ?, updated_at = ?
		  WHERE id = ?`,
		sql.NullString{String: digest, Valid: digest != ""},
		formatTime(at.UTC()), libraryID)
	return affectedOne(res, err)
}
