package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chmouel/liseur-sync/internal/store"
)

// lockWorkGraph takes SQLite's writer lock before any read that will drive a
// work-graph mutation. It serializes resolution with every alias mutator,
// including other processes using the same database.
func lockWorkGraph(ctx context.Context, tx *sql.Tx, userID string) error {
	res, err := tx.ExecContext(ctx, `UPDATE users SET id = id WHERE id = ?`, userID)
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

func resolveMatches(ctx context.Context, tx *sql.Tx, userID string, ids []store.Identifier) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		var aliasWork string
		aliasErr := tx.QueryRowContext(ctx,
			`SELECT work_id FROM aliases WHERE user_id = ? AND kind = ? AND value = ?`,
			userID, id.Kind, id.Value).Scan(&aliasWork)
		if aliasErr != nil && !errors.Is(aliasErr, sql.ErrNoRows) {
			return nil, aliasErr
		}

		var editionWork string
		editionErr := sql.ErrNoRows
		if id.Kind == "sha256" {
			editionErr = tx.QueryRowContext(ctx,
				`SELECT work_id FROM editions WHERE user_id = ? AND sha256 = ?`,
				userID, id.Value).Scan(&editionWork)
			if editionErr != nil && !errors.Is(editionErr, sql.ErrNoRows) {
				return nil, editionErr
			}
		}
		if aliasErr == nil && editionErr == nil && aliasWork != editionWork {
			return nil, store.ErrConflict
		}
		switch {
		case aliasErr == nil:
			out[id.Kind+":"+id.Value] = aliasWork
		case editionErr == nil:
			out[id.Kind+":"+id.Value] = editionWork
		}
	}
	return out, nil
}

func ensureEditions(ctx context.Context, tx *sql.Tx, userID, workID string, editions []store.Edition) error {
	for _, edition := range editions {
		var existing string
		err := tx.QueryRowContext(ctx,
			`SELECT work_id FROM editions WHERE user_id = ? AND sha256 = ?`,
			userID, edition.SHA256).Scan(&existing)
		if err == nil {
			if existing != workID {
				return store.ErrConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO editions (user_id, sha256, work_id, page_count, char_count, meta_json)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			userID, edition.SHA256, workID, edition.PageCount, edition.CharCount, edition.MetaJSON)
		if isUniqueErr(err) {
			return store.ErrConflict
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func ensureAliases(ctx context.Context, tx *sql.Tx, userID, workID string, ids []store.Identifier) error {
	for _, id := range ids {
		var existing string
		err := tx.QueryRowContext(ctx,
			`SELECT work_id FROM aliases WHERE user_id = ? AND kind = ? AND value = ?`,
			userID, id.Kind, id.Value).Scan(&existing)
		if err == nil {
			if existing != workID {
				return store.ErrConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO aliases (user_id, kind, value, work_id) VALUES (?, ?, ?, ?)`,
			userID, id.Kind, id.Value, workID); err != nil {
			if isUniqueErr(err) {
				return store.ErrConflict
			}
			return err
		}
	}
	return nil
}

func resolveWorkTx(
	ctx context.Context,
	tx *sql.Tx,
	userID string,
	proposed store.Work,
	editions []store.Edition,
	ids []store.Identifier,
	confirmed bool,
) (store.WorkResolution, error) {
	matches, err := resolveMatches(ctx, tx, userID, ids)
	if err != nil {
		return store.WorkResolution{}, err
	}
	result := store.DecideWorkResolution(ids, matches, confirmed)
	if len(result.ConflictingWorkIDs) > 0 {
		return result, nil
	}
	if result.WorkID == "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO works (id, user_id, title, author, pending, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			proposed.ID, userID, proposed.Title, proposed.Author, b2i(proposed.Pending),
			formatTime(proposed.CreatedAt)); err != nil {
			if isUniqueErr(err) {
				return store.WorkResolution{}, store.ErrConflict
			}
			return store.WorkResolution{}, err
		}
		if err := ensureEditions(ctx, tx, userID, proposed.ID, editions); err != nil {
			return store.WorkResolution{}, err
		}
		if err := ensureAliases(ctx, tx, userID, proposed.ID, ids); err != nil {
			return store.WorkResolution{}, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO seq_counters (user_id, next_seq) VALUES (?, 1)`, userID); err != nil {
			return store.WorkResolution{}, err
		}
		return store.WorkResolution{
			WorkID: proposed.ID, Confidence: "high", Created: true,
		}, nil
	}
	if result.Confidence == "low" {
		return result, nil
	}
	if err := ensureEditions(ctx, tx, userID, result.WorkID, editions); err != nil {
		return store.WorkResolution{}, err
	}
	if err := ensureAliases(ctx, tx, userID, result.WorkID, ids); err != nil {
		return store.WorkResolution{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE works SET pending = 0 WHERE user_id = ? AND id = ?`, userID, result.WorkID); err != nil {
		return store.WorkResolution{}, err
	}
	return result, nil
}

// ResolveWork resolves and promotes a work graph in one writer transaction.
func (s *Store) ResolveWork(
	ctx context.Context,
	userID string,
	proposed store.Work,
	editions []store.Edition,
	ids []store.Identifier,
	confirmed bool,
) (store.WorkResolution, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.WorkResolution{}, err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return store.WorkResolution{}, err
	}
	result, err := resolveWorkTx(ctx, tx, userID, proposed, editions, ids, confirmed)
	if err != nil || len(result.ConflictingWorkIDs) > 0 || result.Confidence == "low" {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return store.WorkResolution{}, err
	}
	return result, nil
}

// ResolveAliases returns, for each identifier that currently maps to a
// work, that work ID: map["kind:value"] -> workID. Missing identifiers
// are absent from the map. Aliases never exist without a work.
func (s *Store) ResolveAliases(ctx context.Context, userID string, ids []store.Identifier) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		var workID string
		err := s.db.QueryRowContext(ctx,
			`SELECT work_id FROM aliases WHERE user_id = ? AND kind = ? AND value = ?`,
			userID, id.Kind, id.Value).Scan(&workID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[id.Kind+":"+id.Value] = workID
	}
	return out, nil
}

// CreateWork inserts a work, its edition (if any), and its aliases in
// one transaction, plus the user's seq counter row if new.
func (s *Store) CreateWork(ctx context.Context, w store.Work, e *store.Edition, ids []store.Identifier) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, w.UserID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO works (id, user_id, title, author, pending, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		w.ID, w.UserID, w.Title, w.Author, b2i(w.Pending), formatTime(w.CreatedAt)); err != nil {
		if isUniqueErr(err) {
			return store.ErrConflict
		}
		return err
	}
	if e != nil {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO editions (user_id, sha256, work_id, page_count, char_count, meta_json)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			e.UserID, e.SHA256, e.WorkID, e.PageCount, e.CharCount, e.MetaJSON); err != nil {
			if isUniqueErr(err) {
				return store.ErrConflict
			}
			return err
		}
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO aliases (user_id, kind, value, work_id) VALUES (?, ?, ?, ?)`,
			userIDof(w), id.Kind, id.Value, w.ID); err != nil {
			if isUniqueErr(err) {
				return store.ErrConflict
			}
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO seq_counters (user_id, next_seq) VALUES (?, 1)`, w.UserID); err != nil {
		return err
	}
	return tx.Commit()
}

func userIDof(w store.Work) string { return w.UserID }

func (s *Store) WorkByID(ctx context.Context, userID, workID string) (store.Work, error) {
	var w store.Work
	var pending int
	var created string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, title, author, pending, created_at
		 FROM works WHERE user_id = ? AND id = ?`, userID, workID).
		Scan(&w.ID, &w.UserID, &w.Title, &w.Author, &pending, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return w, store.ErrNotFound
	}
	if err != nil {
		return w, err
	}
	w.Pending = pending != 0
	w.CreatedAt, err = parseTime(created)
	return w, err
}

// SplitWork detaches an edition (and the explicitly listed aliases)
// into a new work. Records are reassigned by edition_sha; records with
// NULL edition_sha whose origin_alias is in the moved alias list follow
// their alias. Runs in one transaction; the edition<->record FK is
// DEFERRABLE INITIALLY DEFERRED so intermediate states are fine.
func (s *Store) SplitWork(ctx context.Context, userID, workID, editionSHA string, aliasIDs []store.Identifier, newWork store.Work) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}

	// Verify the edition belongs to the source work.
	var found string
	err = tx.QueryRowContext(ctx,
		`SELECT work_id FROM editions WHERE user_id = ? AND sha256 = ?`, userID, editionSHA).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	if found != workID {
		return store.ErrConflict
	}
	var shaAliasWork string
	shaAliasErr := tx.QueryRowContext(ctx,
		`SELECT work_id FROM aliases WHERE user_id = ? AND kind = 'sha256' AND value = ?`,
		userID, editionSHA).Scan(&shaAliasWork)
	if shaAliasErr == nil && shaAliasWork != workID {
		return store.ErrConflict
	}
	if shaAliasErr != nil && !errors.Is(shaAliasErr, sql.ErrNoRows) {
		return shaAliasErr
	}
	for _, alias := range aliasIDs {
		if alias.Kind == "sha256" && alias.Value != editionSHA {
			return store.ErrConflict
		}
	}
	var rollupCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM session_rollups WHERE user_id = ? AND work_id = ?`,
		userID, workID).Scan(&rollupCount); err != nil {
		return err
	}
	if rollupCount > 0 {
		return store.ErrConflict
	}
	var unpartitionable int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM sessions
		 WHERE user_id = ? AND work_id = ? AND origin = 'inferred'
		   AND edition_sha IS NULL AND origin_alias IS NULL`,
		userID, workID).Scan(&unpartitionable); err != nil {
		return err
	}
	if unpartitionable > 0 {
		return store.ErrConflict
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO works (id, user_id, title, author, pending, created_at) VALUES (?, ?, ?, ?, 0, ?)`,
		newWork.ID, userID, newWork.Title, newWork.Author, formatTime(newWork.CreatedAt)); err != nil {
		return err
	}

	// Move the edition and its records.
	if _, err := tx.ExecContext(ctx,
		`UPDATE editions SET work_id = ? WHERE user_id = ? AND sha256 = ?`, newWork.ID, userID, editionSHA); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE ops SET work_id = ? WHERE user_id = ? AND edition_sha = ? AND work_id = ?`,
		newWork.ID, userID, editionSHA, workID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET work_id = ? WHERE user_id = ? AND edition_sha = ? AND work_id = ?`,
		newWork.ID, userID, editionSHA, workID); err != nil {
		return err
	}
	// The edition's SHA alias is implied by the split contract.
	if errors.Is(shaAliasErr, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO aliases (user_id, kind, value, work_id) VALUES (?, 'sha256', ?, ?)`,
			userID, editionSHA, newWork.ID); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx,
		`UPDATE aliases SET work_id = ? WHERE user_id = ? AND kind = 'sha256' AND value = ? AND work_id = ?`,
		newWork.ID, userID, editionSHA, workID); err != nil {
		return err
	}
	shaOrigin := "sha256:" + editionSHA
	if _, err := tx.ExecContext(ctx,
		`UPDATE ops SET work_id = ? WHERE user_id = ? AND edition_sha IS NULL AND origin_alias = ? AND work_id = ?`,
		newWork.ID, userID, shaOrigin, workID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET work_id = ? WHERE user_id = ? AND edition_sha IS NULL AND origin_alias = ? AND work_id = ?`,
		newWork.ID, userID, shaOrigin, workID); err != nil {
		return err
	}

	// Move the explicitly listed aliases, and edition-less records whose
	// origin_alias matches a moved alias.
	for _, a := range aliasIDs {
		if a.Kind == "sha256" && a.Value == editionSHA {
			continue
		}
		kv := a.Kind + ":" + a.Value
		res, err := tx.ExecContext(ctx,
			`UPDATE aliases SET work_id = ? WHERE user_id = ? AND kind = ? AND value = ? AND work_id = ?`,
			newWork.ID, userID, a.Kind, a.Value, workID)
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
		if _, err := tx.ExecContext(ctx,
			`UPDATE ops SET work_id = ? WHERE user_id = ? AND edition_sha IS NULL AND origin_alias = ? AND work_id = ?`,
			newWork.ID, userID, kv, workID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE sessions SET work_id = ? WHERE user_id = ? AND edition_sha IS NULL AND origin_alias = ? AND work_id = ?`,
			newWork.ID, userID, kv, workID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE aliases
		 SET work_id = ?
		 WHERE user_id = ? AND work_id = ? AND kind = 'source'
		   AND value IN (
		       SELECT 'liseur-sync:' || m.book_id
		       FROM user_book_works m
		       JOIN books b
		         ON b.folder_id = m.folder_id AND b.id = m.book_id
		       WHERE m.user_id = ? AND m.work_id = ?
		         AND b.content_sha256 = ?
		   )`,
		newWork.ID, userID, workID, userID, workID, editionSHA); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE user_book_works
		 SET work_id = ?
		 WHERE user_id = ? AND work_id = ?
		   AND book_id IN (
		       SELECT id FROM books WHERE content_sha256 = ?
		   )`,
		newWork.ID, userID, workID, editionSHA); err != nil {
		return err
	}
	return tx.Commit()
}

// MergeWorks moves every edition, alias, op, session, and supersession
// from fromWorkID into intoWorkID, then deletes the source work. One
// transaction; deferrable FKs make intermediate states legal.
func (s *Store) MergeWorks(ctx context.Context, userID, fromWorkID, intoWorkID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	var rollupCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM session_rollups WHERE user_id = ? AND work_id = ?`,
		userID, fromWorkID).Scan(&rollupCount); err != nil {
		return err
	}
	if rollupCount > 0 {
		return store.ErrConflict
	}
	var dummy int
	err = tx.QueryRowContext(ctx,
		`SELECT 1 FROM works WHERE user_id = ? AND id = ?`, userID, fromWorkID).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx,
		`SELECT 1 FROM works WHERE user_id = ? AND id = ?`, userID, intoWorkID).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	// Aliases are unique per (user, kind, value); on collision the
	// incoming work's alias already exists — drop the duplicate.
	if _, err := tx.ExecContext(ctx,
		`UPDATE OR IGNORE aliases SET work_id = ? WHERE user_id = ? AND work_id = ?`,
		intoWorkID, userID, fromWorkID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM aliases WHERE user_id = ? AND work_id = ?`, userID, fromWorkID); err != nil {
		return err
	}
	// Editions are unique per (user, sha256); on collision keep the
	// target's edition row but point the records at the shared edition.
	rows, err := tx.QueryContext(ctx,
		`SELECT sha256 FROM editions WHERE user_id = ? AND work_id = ?`, userID, fromWorkID)
	if err != nil {
		return err
	}
	var shas []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			rows.Close()
			return err
		}
		shas = append(shas, sha)
	}
	rows.Close()
	for _, sha := range shas {
		if _, err := tx.ExecContext(ctx,
			`UPDATE OR IGNORE editions SET work_id = ? WHERE user_id = ? AND sha256 = ?`,
			intoWorkID, userID, sha); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE ops SET work_id = ? WHERE user_id = ? AND work_id = ? AND (edition_sha IS NULL OR edition_sha = ?)`,
			intoWorkID, userID, fromWorkID, sha); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE sessions SET work_id = ? WHERE user_id = ? AND work_id = ? AND (edition_sha IS NULL OR edition_sha = ?)`,
			intoWorkID, userID, fromWorkID, sha); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM editions WHERE user_id = ? AND sha256 = ? AND work_id = ?`,
			userID, sha, fromWorkID); err != nil {
			return err
		}
	}
	// Any records still on the source work (NULL-edition, no matching
	// edition row) move wholesale.
	if _, err := tx.ExecContext(ctx,
		`UPDATE ops SET work_id = ? WHERE user_id = ? AND work_id = ?`, intoWorkID, userID, fromWorkID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET work_id = ? WHERE user_id = ? AND work_id = ?`, intoWorkID, userID, fromWorkID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE user_book_works SET work_id = ? WHERE user_id = ? AND work_id = ?`,
		intoWorkID, userID, fromWorkID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM works WHERE user_id = ? AND id = ?`, userID, fromWorkID); err != nil {
		return err
	}
	return tx.Commit()
}

// AddAliases registers additional aliases on an existing work.
func (s *Store) AddAliases(ctx context.Context, userID, workID string, ids []store.Identifier) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return err
	}
	if err := ensureAliases(ctx, tx, userID, workID, ids); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearPending flips a pending work to established once real
// identifiers resolve to it.
func (s *Store) ClearPending(ctx context.Context, userID, workID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE works SET pending = 0 WHERE user_id = ? AND id = ?`, userID, workID)
	return err
}
