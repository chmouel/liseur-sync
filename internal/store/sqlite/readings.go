package sqlite

import (
	"context"
	"database/sql"

	"github.com/chmouel/liseur-sync/internal/store"
)

// WorkReadings returns each work's newest status act and newest
// position, read in one transaction so the two halves cannot describe
// different moments (ADR-0066).
func (s *Store) WorkReadings(ctx context.Context, userID string, workIDs []string) (map[string]store.WorkReading, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return workReadingsTx(ctx, tx, userID, workIDs)
}

// workReadingsTx is [Store.WorkReadings] inside the caller's snapshot.
// The newest op of each kind per work is one windowed pass over the
// log, not a query per work.
func workReadingsTx(
	ctx context.Context, db querier, userID string, workIDs []string,
) (map[string]store.WorkReading, error) {
	query := `SELECT work_id, kind, seq, progression, reading_status, coverage FROM (
		    SELECT work_id, kind, seq, progression, reading_status, coverage,
		           ROW_NUMBER() OVER (PARTITION BY work_id, kind ORDER BY seq DESC) AS rn
		      FROM ops WHERE user_id = ?`
	args := []any{userID}
	if len(workIDs) > 0 {
		placeholders, idArgs := inArgs(workIDs)
		query += ` AND work_id IN (` + placeholders + `)`
		args = append(args, idArgs...)
	}
	query += `) WHERE rn = 1`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]store.WorkReading, len(workIDs))
	for rows.Next() {
		var (
			workID, kind string
			seq          int64
			prog         float64
			status, cov  sql.NullString
		)
		if err := rows.Scan(&workID, &kind, &seq, &prog, &status, &cov); err != nil {
			return nil, err
		}
		r := out[workID]
		r.WorkID = workID
		if store.OpKind(kind) == store.OpKindStatus {
			r.Choice = store.ReadingStatus(status.String)
			r.ChoiceSeq = seq
		} else {
			r.HasPosition = true
			r.Progression = prog
			r.Coverage = store.Coverage(cov.String)
			r.PositionSeq = seq
		}
		out[workID] = r
	}
	return out, rows.Err()
}
