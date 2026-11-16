package postgres

import (
	"context"

	"github.com/chmouel/liseur-sync/internal/store"
)

// WorkReadings: see the SQLite implementation's doc comment.
func (s *Store) WorkReadings(ctx context.Context, userID string, workIDs []string) (map[string]store.WorkReading, error) {
	tx, err := s.db.BeginTx(ctx, snapshotTx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return workReadingsTx(ctx, tx, userID, workIDs)
}

// workReadingsTx is [Store.WorkReadings] inside the caller's snapshot.
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
	query += `) latest WHERE rn = 1`
	rows, err := db.QueryContext(ctx, q(query), args...)
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
			status, cov  *string
		)
		if err := rows.Scan(&workID, &kind, &seq, &prog, &status, &cov); err != nil {
			return nil, err
		}
		r := out[workID]
		r.WorkID = workID
		if store.OpKind(kind) == store.OpKindStatus {
			r.Choice = store.ReadingStatus(derefStr(status))
			r.ChoiceSeq = seq
		} else {
			r.HasPosition = true
			r.Progression = prog
			r.Coverage = store.Coverage(derefStr(cov))
			r.PositionSeq = seq
		}
		out[workID] = r
	}
	return out, rows.Err()
}
