package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) ResolveAliases(ctx context.Context, userID string, ids []store.Identifier) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		var workID string
		err := s.db.QueryRowContext(ctx, q(
			`SELECT work_id FROM aliases WHERE user_id = ? AND kind = ? AND value = ?`),
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

func (s *Store) CreateWork(ctx context.Context, w store.Work, e *store.Edition, ids []store.Identifier) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO works (id, user_id, title, author, pending, created_at) VALUES (?, ?, ?, ?, ?, ?)`),
		w.ID, w.UserID, w.Title, w.Author, w.Pending, w.CreatedAt.UTC()); err != nil {
		if isUniqueErr(err) {
			return store.ErrConflict
		}
		return err
	}
	if e != nil {
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO editions (user_id, sha256, work_id, page_count, char_count, meta_json)
			 VALUES (?, ?, ?, ?, ?, ?)`),
			e.UserID, e.SHA256, e.WorkID, e.PageCount, e.CharCount, e.MetaJSON); err != nil {
			if isUniqueErr(err) {
				return store.ErrConflict
			}
			return err
		}
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO aliases (user_id, kind, value, work_id) VALUES (?, ?, ?, ?)`),
			w.UserID, id.Kind, id.Value, w.ID); err != nil {
			if isUniqueErr(err) {
				return store.ErrConflict
			}
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO seq_counters (user_id, next_seq) VALUES (?, 1) ON CONFLICT DO NOTHING`),
		w.UserID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) WorkByID(ctx context.Context, userID, workID string) (store.Work, error) {
	var w store.Work
	err := s.db.QueryRowContext(ctx, q(
		`SELECT id, user_id, title, author, pending, created_at
		 FROM works WHERE user_id = ? AND id = ?`), userID, workID).
		Scan(&w.ID, &w.UserID, &w.Title, &w.Author, &w.Pending, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return w, store.ErrNotFound
	}
	return w, err
}

func (s *Store) SplitWork(ctx context.Context, userID, workID, editionSHA string, aliasIDs []store.Identifier, newWork store.Work) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var found string
	err = tx.QueryRowContext(ctx, q(
		`SELECT work_id FROM editions WHERE user_id = ? AND sha256 = ?`), userID, editionSHA).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	if found != workID {
		return store.ErrConflict
	}

	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO works (id, user_id, title, author, pending, created_at) VALUES (?, ?, ?, ?, FALSE, ?)`),
		newWork.ID, userID, newWork.Title, newWork.Author, newWork.CreatedAt.UTC()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q(
		`UPDATE editions SET work_id = ? WHERE user_id = ? AND sha256 = ?`),
		newWork.ID, userID, editionSHA); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q(
		`UPDATE ops SET work_id = ? WHERE user_id = ? AND edition_sha = ? AND work_id = ?`),
		newWork.ID, userID, editionSHA, workID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q(
		`UPDATE sessions SET work_id = ? WHERE user_id = ? AND edition_sha = ? AND work_id = ?`),
		newWork.ID, userID, editionSHA, workID); err != nil {
		return err
	}
	for _, a := range aliasIDs {
		kv := a.Kind + ":" + a.Value
		res, err := tx.ExecContext(ctx, q(
			`UPDATE aliases SET work_id = ? WHERE user_id = ? AND kind = ? AND value = ? AND work_id = ?`),
			newWork.ID, userID, a.Kind, a.Value, workID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return store.ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, q(
			`UPDATE ops SET work_id = ? WHERE user_id = ? AND edition_sha IS NULL AND origin_alias = ? AND work_id = ?`),
			newWork.ID, userID, kv, workID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, q(
			`UPDATE sessions SET work_id = ? WHERE user_id = ? AND edition_sha IS NULL AND origin_alias = ? AND work_id = ?`),
			newWork.ID, userID, kv, workID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) MergeWorks(ctx context.Context, userID, fromWorkID, intoWorkID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var dummy int
	for _, id := range []string{fromWorkID, intoWorkID} {
		err := tx.QueryRowContext(ctx, q(
			`SELECT 1 FROM works WHERE user_id = ? AND id = ?`), userID, id).Scan(&dummy)
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
	}

	// Aliases: move those that don't collide, drop the rest.
	if _, err := tx.ExecContext(ctx, q(
		`UPDATE aliases a SET work_id = ? WHERE a.user_id = ? AND a.work_id = ?
		 AND NOT EXISTS (SELECT 1 FROM aliases b
		                 WHERE b.user_id = a.user_id AND b.kind = a.kind AND b.value = a.value AND b.work_id = ?)`),
		intoWorkID, userID, fromWorkID, intoWorkID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q(
		`DELETE FROM aliases WHERE user_id = ? AND work_id = ?`), userID, fromWorkID); err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, q(
		`SELECT sha256 FROM editions WHERE user_id = ? AND work_id = ?`), userID, fromWorkID)
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
		if _, err := tx.ExecContext(ctx, q(
			`UPDATE editions SET work_id = ? WHERE user_id = ? AND sha256 = ?`),
			intoWorkID, userID, sha); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, q(
		`UPDATE ops SET work_id = ? WHERE user_id = ? AND work_id = ?`),
		intoWorkID, userID, fromWorkID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q(
		`UPDATE sessions SET work_id = ? WHERE user_id = ? AND work_id = ?`),
		intoWorkID, userID, fromWorkID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q(
		`DELETE FROM works WHERE user_id = ? AND id = ?`), userID, fromWorkID); err != nil {
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
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO aliases (user_id, kind, value, work_id) VALUES (?, ?, ?, ?)
			 ON CONFLICT (user_id, kind, value) DO NOTHING`),
			userID, id.Kind, id.Value, workID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearPending flips a pending work to established.
func (s *Store) ClearPending(ctx context.Context, userID, workID string) error {
	_, err := s.db.ExecContext(ctx, q(
		`UPDATE works SET pending = FALSE WHERE user_id = ? AND id = ?`), userID, workID)
	return err
}
