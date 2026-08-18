package postgres

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
		var mapped bool
		if err := tx.QueryRowContext(ctx, q(
			`SELECT EXISTS (SELECT 1 FROM user_book_works
			                 WHERE user_id = ? AND work_id = ?)`),
			userID, workID).Scan(&mapped); err != nil {
			return err
		}
		if mapped {
			return store.ErrInvalidInput
		}
		res, err := tx.ExecContext(ctx, q(
			`DELETE FROM works WHERE user_id = ? AND id = ?`), userID, workID)
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
	})
}

// DeleteMissingBook removes a catalog book a pass already marked
// missing, running the same collection a pass dropping a book runs: the
// works nothing maps and nobody has read, then the entities no book
// names any more.
//
// It takes the shared gate exclusively, because collecting empty works
// crosses readers and the lock order has to stay the one reconciliation
// uses.
func (s *Store) DeleteMissingBook(ctx context.Context, bookID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := lockAllWorkGraphs(ctx, tx); err != nil {
			return err
		}
		// FOR UPDATE, so a pass cannot reactivate this book between the
		// status check and the delete.
		var folderID, status string
		err := tx.QueryRowContext(ctx, q(
			`SELECT folder_id, status FROM books WHERE id = ? FOR UPDATE`), bookID).
			Scan(&folderID, &status)
		if err != nil {
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
