package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chmouel/liseur-sync/internal/store"
)

// DeleteWork removes one reader's work and, by cascade, its editions,
// aliases, ops, sessions, supersessions, rollups and book mapping.
//
// The guard is the whole point: a work a catalog book still maps to is
// refused. A mapping row cannot outlive its book — the composite
// foreign key cascades it away — so the presence of one means this
// server still lists that book, whether or not a pass can find the file
// today, and a disk that is merely unplugged must not cost anybody
// their reading history.
func (s *Store) DeleteWork(ctx context.Context, userID, workID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := lockWorkGraph(ctx, tx, userID); err != nil {
			return err
		}
		switch err := deleteWorkTx(ctx, tx, userID, workID); {
		case errors.Is(err, errWorkStillMapped):
			return store.ErrInvalidInput
		default:
			return err
		}
	})
}

// errWorkStillMapped is deleteWorkTx refusing a work a catalog book
// still maps. Its two callers report it differently — a reader asking
// directly is told no, a reader deleting a book they own a second copy
// of is told their reading was kept — so the distinction lives here
// rather than being flattened into ErrInvalidInput too early.
var errWorkStillMapped = errors.New("work is still mapped by a book")

// deleteWorkTx is DeleteWork without the transaction or the lock, so
// that deleting a book can forget the caller's reading in the same
// transaction that removes the book. Both paths share the guard, which
// is the point: there is one definition of when a work may go.
func deleteWorkTx(ctx context.Context, tx *sql.Tx, userID, workID string) error {
	var mapped int
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM user_book_works
		                 WHERE user_id = ? AND work_id = ?)`,
		userID, workID).Scan(&mapped); err != nil {
		return err
	}
	if mapped == 1 {
		return errWorkStillMapped
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM works WHERE user_id = ? AND id = ?`, userID, workID)
	if err != nil {
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

// DeleteMissingBook removes a catalog book a pass already marked
// missing, running the same collection a pass dropping a book runs: the
// works nothing maps and nobody has read, then the entities no book
// names any more.
func (s *Store) DeleteMissingBook(ctx context.Context, bookID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		// The no-op write takes SQLite's writer lock before the status
		// is read, so a pass cannot reactivate this book between the
		// check and the delete.
		res, err := tx.ExecContext(ctx,
			`UPDATE books SET id = id WHERE id = ?`, bookID)
		if err != nil {
			return err
		}
		switch n, err := res.RowsAffected(); {
		case err != nil:
			return err
		case n == 0:
			return store.ErrNotFound
		}
		var folderID, status string
		if err := tx.QueryRowContext(ctx,
			`SELECT folder_id, status FROM books WHERE id = ?`, bookID).
			Scan(&folderID, &status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return store.ErrNotFound
			}
			return err
		}
		if store.BookStatus(status) != store.BookMissing {
			return store.ErrInvalidInput
		}
		if err := deleteBookTx(ctx, tx, folderID, bookID); err != nil {
			return err
		}
		if err := collectEmptyWorksTx(ctx, tx); err != nil {
			return err
		}
		return collectOrphanEntitiesTx(ctx, tx)
	})
}

// DeleteCatalogBook removes a catalog book whose file this server has
// just deleted (ADR-0025).
//
// The shape follows DeleteMissingBook, including the no-op write that
// takes the writer lock before anything is read. What differs is the
// guard: not "a pass called this missing" but "an administrator marked
// this folder writable", because that flag is what makes the file this
// server's to remove at all, and it is re-read here rather than trusted
// from the check the caller made before unlinking.
func (s *Store) DeleteCatalogBook(
	ctx context.Context, bookID string, opts store.DeleteBookOptions,
) (store.DeleteBookResult, error) {
	var out store.DeleteBookResult
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		out = store.DeleteBookResult{}
		if opts.ForgetReadingFor != "" {
			if err := lockWorkGraph(ctx, tx, opts.ForgetReadingFor); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE books SET id = id WHERE id = ?`, bookID)
		if err != nil {
			return err
		}
		switch n, err := res.RowsAffected(); {
		case err != nil:
			return err
		case n == 0:
			return store.ErrNotFound
		}
		var folderID string
		var accepts bool
		if err := tx.QueryRowContext(ctx,
			`SELECT b.folder_id, f.accepts_uploads
			   FROM books b JOIN folders f ON f.id = b.folder_id
			  WHERE b.id = ?`, bookID).Scan(&folderID, &accepts); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return store.ErrNotFound
			}
			return err
		}
		if !accepts {
			return store.ErrInvalidInput
		}
		// Read before the delete, not after: user_book_works is
		// cascaded away with the book, so afterwards there is nothing
		// left to say which work this reader's reading hangs off.
		var workID string
		if opts.ForgetReadingFor != "" {
			err := tx.QueryRowContext(ctx,
				`SELECT work_id FROM user_book_works
				  WHERE user_id = ? AND book_id = ?`,
				opts.ForgetReadingFor, bookID).Scan(&workID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if err := deleteBookTx(ctx, tx, folderID, bookID); err != nil {
			return err
		}
		if workID != "" {
			switch err := deleteWorkTx(
				ctx, tx, opts.ForgetReadingFor, workID,
			); {
			case err == nil:
				out.ReadingForgotten = true
			case errors.Is(err, errWorkStillMapped):
				// Another catalog book maps this work: a second copy of
				// the same book, which the reading now belongs to. The
				// reader asked to forget a book they still have, so the
				// book goes and the reading stays with the copy.
				out.ReadingKept = true
			case errors.Is(err, store.ErrNotFound):
				// The mapping named a work that is not there. Nothing
				// to forget, and nothing worth failing a delete over.
			default:
				return err
			}
		}
		if err := collectEmptyWorksTx(ctx, tx); err != nil {
			return err
		}
		return collectOrphanEntitiesTx(ctx, tx)
	})
	if err != nil {
		return store.DeleteBookResult{}, err
	}
	return out, nil
}
