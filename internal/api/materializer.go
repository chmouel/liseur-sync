package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/chmouel/liseur-sync/internal/infer"
	"github.com/chmouel/liseur-sync/internal/insights"
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
		s.sweepAnnotationsOnce(ctx,
			time.Duration(s.Cfg.Ops.AnnotationRetentionDays)*24*time.Hour)
		if err := s.St.Housekeep(ctx, time.Now()); err != nil {
			slog.Warn("housekeeping", "err", err)
		}
	}
}

// sweepAnnotationsOnce removes annotation tombstones older than the
// retention window (ADR-0028): kept long enough for every device to
// learn of the deletion, then the id is simply unknown.
func (s *Server) sweepAnnotationsOnce(ctx context.Context, retention time.Duration) {
	users, err := s.St.UserIDs(ctx)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-retention)
	for _, userID := range users {
		n, err := s.St.SweepAnnotationTombstones(ctx, userID, cutoff)
		if err != nil {
			slog.Warn("annotation sweep", "user", userID, "err", err)
			continue
		}
		if n > 0 {
			slog.Info("annotation sweep", "user", userID, "swept", n)
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
		ops, err := s.St.PendingInferenceOps(ctx, userID)
		if err != nil {
			slog.Warn("materializer: ops", "user", userID, "err", err)
			continue
		}
		for _, g := range infer.ClosedGroups(ops, gap, lateBefore) {
			group := store.InferredSessionGroup{
				Session: infer.Materialize(userID, g),
				Ops:     g,
			}
			if err := s.St.AppendInferredSession(ctx, userID, group); err != nil {
				if errors.Is(err, store.ErrConflict) {
					// A concurrent split/merge or another materializer
					// changed this snapshot. The next pass re-reads it.
					continue
				}
				slog.Warn("materializer: append", "user", userID, "err", err)
			}
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
		sessions, err := s.St.SessionsEndedBefore(ctx, userID, cutoff)
		if err != nil {
			slog.Warn("session rollup: sessions", "user", userID, "err", err)
			continue
		}
		if len(sessions) == 0 {
			continue
		}
		ids := make([]string, 0, len(sessions))
		var aggregateErr error
		for _, ses := range sessions {
			ids = append(ids, ses.SessionID)
		}
		snap, err := s.St.StatisticsSnapshot(ctx, userID, ids)
		if err != nil {
			slog.Warn("session rollup: snapshot", "user", userID, "err", err)
			continue
		}
		timezone := snap.Timezone
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			slog.Warn("session rollup: timezone", "user", userID, "err", err)
			continue
		}

		type key struct{ workID, day string }
		byDay := make(map[key]*store.SessionRollup)
		for _, ses := range sessions {
			day := ses.EndedAt.In(loc).Format(insights.DayFormat)
			k := key{ses.WorkID, day}
			ru := byDay[k]
			if ru == nil {
				ru = &store.SessionRollup{
					UserID:             userID,
					WorkID:             ses.WorkID,
					Day:                day,
					Timezone:           timezone,
					AttributionVersion: 2,
				}
				byDay[k] = ru
			}

			active := insights.ActiveSeconds(ses)
			progDelta := positiveProgDelta(ses)
			pages, err := insights.Pages(ses, snap.Editions)
			if err != nil {
				aggregateErr = err
				break
			}
			ru.ActiveSeconds += active
			ru.Pages += pages
			ru.ProgDelta += progDelta
			ru.SessionCount++
			if ses.Origin != store.OriginInferred {
				ru.MeasuredActiveSeconds += active
				ru.MeasuredProgDelta += progDelta
			}
		}
		if aggregateErr != nil {
			slog.Warn("session rollup: aggregate", "user", userID, "err", aggregateErr)
			continue
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

func positiveProgDelta(ses store.Session) float64 {
	delta := ses.EndProg - ses.StartProg
	if delta < 0 {
		return 0
	}
	return delta
}
