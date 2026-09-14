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

const rollupBatchSize = 500

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
		user, err := s.St.UserByID(ctx, userID)
		if err != nil {
			slog.Warn("session rollup: user", "user", userID, "err", err)
			continue
		}
		timezone := user.Timezone
		if timezone == "" {
			timezone = "UTC"
		}
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			slog.Warn("session rollup: timezone", "user", userID, "err", err)
			continue
		}
		// Each pass asks for the oldest page and rolls it up, so a long
		// history is never held in memory at once. A rolled-up page is
		// deleted, so the next read returns the page after it.
		for {
			batch, err := s.St.SessionsEndedBefore(ctx, userID, cutoff, rollupBatchSize)
			if err != nil {
				slog.Warn("session rollup: sessions", "user", userID, "err", err)
				break
			}
			if len(batch) == 0 {
				break
			}
			editions, err := s.St.EditionsBySHA(ctx, userID, store.EditionSHAsNeedingPages(batch))
			if err != nil {
				slog.Warn("session rollup: editions", "user", userID, "err", err)
				break
			}
			rollups, aggregateErr := buildSessionRollups(batch, userID, timezone, loc, editions)
			if aggregateErr != nil {
				// Fail closed: a page total that cannot be computed is
				// not silently recorded as zero. This stops the user's
				// rollup until the metadata is there, so name the
				// sitting and the edition that blocked it.
				if sha, id, ok := missingEditionSession(batch, editions); ok {
					slog.Warn("session rollup: missing edition", "user", userID,
						"session", id, "edition_sha", sha, "err", aggregateErr)
				} else {
					slog.Warn("session rollup: aggregate", "user", userID, "err", aggregateErr)
				}
				break
			}
			if err := s.St.ApplyRollups(ctx, userID, rollups, batch); err != nil {
				if errors.Is(err, store.ErrConflict) {
					// The account's zone moved, or a sitting changed
					// under us. Keep the sessions and retry next hour.
					slog.Info("session rollup: deferred batch", "user", userID, "sessions", len(batch))
					break
				}
				slog.Warn("session rollup", "user", userID, "err", err)
				break
			}
		}
	}
}

// missingEditionSession names the first sitting in the batch whose page
// count needed an edition that was not returned.
func missingEditionSession(sessions []store.Session, editions map[string]store.Edition) (sha, sessionID string, ok bool) {
	for _, ses := range sessions {
		if !store.SessionNeedsEditionPages(ses) {
			continue
		}
		if _, found := editions[*ses.EditionSHA]; !found {
			return *ses.EditionSHA, ses.SessionID, true
		}
	}
	return "", "", false
}

func buildSessionRollups(sessions []store.Session, userID, timezone string, loc *time.Location, editions map[string]store.Edition) ([]store.SessionRollup, error) {
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
		pages, err := insights.Pages(ses, editions)
		if err != nil {
			return nil, err
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
	rollups := make([]store.SessionRollup, 0, len(byDay))
	for _, ru := range byDay {
		rollups = append(rollups, *ru)
	}
	return rollups, nil
}

func positiveProgDelta(ses store.Session) float64 {
	delta := ses.EndProg - ses.StartProg
	if delta < 0 {
		return 0
	}
	return delta
}
