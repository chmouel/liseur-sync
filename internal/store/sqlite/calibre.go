package sqlite

// The Calibre side of a library (ADR-0014): the identity map between
// Calibre's book ids and this catalog's, the deletion that follows a
// book vanishing from metadata.db, and the digest that lets an unchanged
// library cost one read and no writes.

import (
	"context"
	"database/sql"
	"errors"
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
	ctx context.Context, libraryID, owner, digest string, at time.Time,
) error {
	if libraryID == "" || at.IsZero() {
		return store.ErrInvalidTransition
	}
	// The lease is part of the statement: a worker that was taken over
	// half way through must not record a gate saying this library is
	// up to date, because the pass that would have made it so is the one
	// that stopped.
	res, err := s.db.ExecContext(ctx,
		`UPDATE libraries
		    SET last_inventory_digest = ?, updated_at = ?
		  WHERE id = ? AND COALESCE(refresh_lease_owner, '') = ?`,
		sql.NullString{String: digest, Valid: digest != ""},
		formatTime(at.UTC()), libraryID, owner)
	return leaseAffectedOne(res, err)
}

// SetBookFileCover records the cover somebody chose for one file, or,
// with an empty path and digest, that there is no longer one.
//
// The digest is what the rendered-cover cache is keyed by, so writing it
// is also what invalidates that cache: a curator who replaces cover.jpg
// gets a new key rather than the picture the old bytes rendered to
// (ADR-0014). It reports whether anything changed, so a refresh that
// re-hashes an unchanged cover writes nothing — unchanged meaning the
// same bytes at the same path, since renaming a book in Calibre moves
// the same cover somewhere else.
func (s *Store) SetBookFileCover(
	ctx context.Context, libraryID, fileID, relativePath, coverSHA256 string,
	at time.Time,
) (bool, error) {
	if libraryID == "" || fileID == "" || at.IsZero() {
		return false, store.ErrInvalidTransition
	}
	if (relativePath == "") != (coverSHA256 == "") {
		// A cover is a path and the bytes at it. Half of that is a
		// caller that has not decided, and storing it would leave a key
		// naming nothing or a picture nothing can find.
		return false, store.ErrInvalidTransition
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE book_files
		    SET cover_relative_path = ?, cover_sha256 = ?, updated_at = ?
		  WHERE library_id = ? AND id = ?
		    AND (COALESCE(cover_sha256, '') != ?
		         OR COALESCE(cover_relative_path, '') != ?)`,
		nullStr(&relativePath), nullStr(&coverSHA256), formatTime(at.UTC()),
		libraryID, fileID, coverSHA256, relativePath)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RelocateWatchedFile moves one catalogued file to the path its source
// now has, keeping the row and everything that references it.
//
// This is a book Calibre renamed: the directory changed because the
// title or the author did, and the bytes did not. Treating the new path
// as a new file would catalogue the same book twice and leave the old
// one to be marked absent, which is how a rename becomes a duplicate
// and a loss (ADR-0014).
func (s *Store) RelocateWatchedFile(
	ctx context.Context,
	libraryID, fileID, sourceRelativePath string,
	modifiedAt, at time.Time,
) error {
	if libraryID == "" || fileID == "" || sourceRelativePath == "" ||
		modifiedAt.IsZero() || at.IsZero() {
		return store.ErrInvalidTransition
	}
	stamp := formatTime(at.UTC())
	res, err := s.db.ExecContext(ctx,
		`UPDATE book_files
		    SET source_relative_path = ?, source_modified_at = ?,
		        source_seen_at = ?, source_absent_at = NULL, updated_at = ?
		  WHERE library_id = ? AND id = ?`,
		sourceRelativePath, formatTime(modifiedAt.UTC()), stamp, stamp,
		libraryID, fileID)
	if err != nil {
		if isUniqueErr(err) {
			return store.ErrConflict
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// SupersedeInPlaceBookFile replaces the bytes one book is served from
// without replacing the book.
//
// Calibre converted or replaced the format: the file is a different
// publication, but the book it belongs to is the same one, with the same
// reading lists, the same manual edits and the same identity. The old
// row becomes `superseded` rather than being deleted, which is the
// vocabulary availability already has for exactly this and which keeps
// the earlier edition's identity intact rather than transferring it
// (ADR-0014).
//
// It is idempotent: a replacement already recorded is read back rather
// than inserted again, so a refresh interrupted after the commit does
// the same thing the second time.
func (s *Store) SupersedeInPlaceBookFile(
	ctx context.Context,
	libraryID, supersededFileID string,
	replacement store.BookFile,
	at time.Time,
) (store.BookFile, error) {
	replacement = replacement.Normalized()
	if libraryID == "" || supersededFileID == "" || at.IsZero() ||
		replacement.ID == "" || replacement.BookID == "" ||
		replacement.LibraryID != libraryID ||
		replacement.Storage != store.LibraryStorageInPlace ||
		replacement.ContentSHA256 == "" ||
		replacement.SourceRelativePath == nil ||
		replacement.SourceModifiedAt == nil {
		return store.BookFile{}, store.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.BookFile{}, err
	}
	defer tx.Rollback()

	var bookID string
	if err := tx.QueryRowContext(ctx,
		`SELECT book_id FROM book_files WHERE library_id = ? AND id = ?`,
		libraryID, supersededFileID).Scan(&bookID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.BookFile{}, store.ErrNotFound
		}
		return store.BookFile{}, err
	}
	if bookID != replacement.BookID {
		// Superseding across books would move one book's history onto
		// another's file, which is the merge this design refuses.
		return store.BookFile{}, store.ErrInvalidTransition
	}
	stamp := formatTime(at.UTC())
	existing, err := scanBookFile(tx.QueryRowContext(ctx,
		`SELECT `+bookFileColumns+` FROM book_files f
		  WHERE f.library_id = ? AND f.id = ?`, libraryID, replacement.ID))
	if err == nil {
		if existing.ContentSHA256 != replacement.ContentSHA256 {
			return store.BookFile{}, store.ErrConflict
		}
		return existing, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return store.BookFile{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE book_files SET availability = 'superseded', updated_at = ?
		  WHERE library_id = ? AND id = ?`,
		stamp, libraryID, supersededFileID); err != nil {
		return store.BookFile{}, err
	}
	mediaType := replacement.MediaType
	if mediaType == "" {
		mediaType = "application/epub+zip"
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO book_files
		 (id, library_id, book_id, storage, content_sha256,
		  content_size_bytes, blob_sha256, source,
		  source_relative_path, original_filename, media_type,
		  partial_md5, dc_identifier, availability,
		  source_seen_at, source_modified_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, NULL, NULL, ?, ?, ?, ?, ?)`,
		replacement.ID, libraryID, replacement.BookID,
		string(store.LibraryStorageInPlace), replacement.ContentSHA256,
		replacement.ContentSizeBytes, string(replacement.Source),
		nullStr(replacement.SourceRelativePath),
		replacement.OriginalFilename, mediaType,
		string(store.BookFileAvailable), stamp,
		formatTime(replacement.SourceModifiedAt.UTC()), stamp, stamp,
	); err != nil {
		if isUniqueErr(err) {
			return store.BookFile{}, store.ErrConflict
		}
		return store.BookFile{}, err
	}
	stored, err := scanBookFile(tx.QueryRowContext(ctx,
		`SELECT `+bookFileColumns+` FROM book_files f WHERE f.id = ?`,
		replacement.ID))
	if err != nil {
		return store.BookFile{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.BookFile{}, err
	}
	return stored, nil
}
