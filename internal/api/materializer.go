package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/chmouel/liseur-sync/internal/infer"
	"github.com/chmouel/liseur-sync/internal/store"
)

// RunMaterializer periodically materializes closed inferred sessions
// and (when enabled) compacts the op log. Runs in-process (v1 is
// single-replica; the store transactions serialize the work).
func (s *Server) RunMaterializer(ctx context.Context) {
	interval := time.Hour
	gap := time.Duration(s.Cfg.Ops.InferenceGapMin) * time.Minute
	lateBy := time.Duration(s.Cfg.Ops.InferenceLateHours) * time.Hour
	retention := time.Duration(s.Cfg.Ops.RetentionDays) * 24 * time.Hour

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.materializeOnce(ctx, gap, lateBy)
		if s.Cfg.Ops.CompactionEnabled {
			s.compactOnce(ctx, retention)
		}
		s.rollupSessionsOnce(ctx, retention)
		if err := s.St.Housekeep(ctx, time.Now()); err != nil {
			slog.Warn("housekeeping", "err", err)
		}
	}
}

// compactOnce runs op-log compaction for every user. Heads are never
// deleted; clients below the new horizon resync via /v1/heads.
func (s *Server) compactOnce(ctx context.Context, retention time.Duration) {
	users, err := s.St.UserIDs(ctx)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-retention)
	for _, userID := range users {
		horizon, err := s.St.Compact(ctx, userID, cutoff)
		if err != nil {
			slog.Warn("compaction", "user", userID, "err", err)
			continue
		}
		if horizon > 0 {
			slog.Info("compaction", "user", userID, "horizon", horizon)
		}
	}
}

func (s *Server) materializeOnce(ctx context.Context, gap, lateBy time.Duration) {
	users, err := s.St.UserIDs(ctx)
	if err != nil {
		slog.Warn("materializer: list users", "err", err)
		return
	}
	lateBefore := time.Now().Add(-lateBy)
	for _, userID := range users {
		ops, err := s.St.OpsBefore(ctx, userID, lateBefore)
		if err != nil {
			slog.Warn("materializer: ops", "user", userID, "err", err)
			continue
		}
		var sessions []store.Session
		for _, g := range infer.ClosedGroups(ops, gap, lateBefore) {
			// Skip native-origin groups: those devices report real
			// sessions; inference is for position-only (kosync) devices.
			if g[0].Origin != store.OriginKosync {
				continue
			}
			sessions = append(sessions, infer.Materialize(userID, g))
		}
		if len(sessions) == 0 {
			continue
		}
		// Idempotent: deterministic session IDs make re-materialization
		// a no-op.
		if err := s.St.AppendSessions(ctx, userID, sessions); err != nil {
			slog.Warn("materializer: append", "user", userID, "err", err)
		}
	}
}

// rollupSessionsOnce replaces immutable sessions older than the
// retention horizon with per-work, timezone-local daily totals.
// Koplugin sessions remain raw because their legacy source keys can
// receive later superseding revisions.
func (s *Server) rollupSessionsOnce(ctx context.Context, retention time.Duration) {
	users, err := s.St.UserIDs(ctx)
	if err != nil {
		slog.Warn("session rollup: list users", "err", err)
		return
	}
	cutoff := time.Now().Add(-retention)
	for _, userID := range users {
		u, err := s.St.UserByID(ctx, userID)
		if err != nil {
			slog.Warn("session rollup: user", "user", userID, "err", err)
			continue
		}
		loc, err := time.LoadLocation(u.Timezone)
		if err != nil {
			loc = time.UTC
		}
		sessions, err := s.St.SessionsEndedBefore(ctx, userID, cutoff)
		if err != nil {
			slog.Warn("session rollup: sessions", "user", userID, "err", err)
			continue
		}
		if len(sessions) == 0 {
			continue
		}

		type key struct{ workID, day string }
		byDay := make(map[key]*store.SessionRollup)
		for _, ses := range sessions {
			parts := s.splitDaysFull(ctx, ses, loc)
			activeTotal := activeSeconds(ses)
			progDelta := ses.EndProg - ses.StartProg
			if progDelta < 0 {
				progDelta = 0
			}
			for i, part := range parts {
				k := key{ses.WorkID, part.date}
				ru := byDay[k]
				if ru == nil {
					ru = &store.SessionRollup{
						UserID: userID,
						WorkID: ses.WorkID,
						Day:    part.date,
					}
					byDay[k] = ru
				}
				ru.ActiveSeconds += part.activeSec
				ru.Pages += part.pages
				if activeTotal > 0 {
					ru.ProgDelta += progDelta * part.activeSec / activeTotal
				} else if i == 0 {
					ru.ProgDelta += progDelta
				}
				if i == 0 {
					ru.SessionCount++
				}
			}
		}
		rollups := make([]store.SessionRollup, 0, len(byDay))
		for _, ru := range byDay {
			rollups = append(rollups, *ru)
		}
		if err := s.St.ApplyRollups(ctx, userID, rollups, sessions); err != nil {
			slog.Warn("session rollup", "user", userID, "err", err)
		}
	}
}
