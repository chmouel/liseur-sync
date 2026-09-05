package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/insights"
	"github.com/chmouel/liseur-sync/internal/store"
)

const maxInsightCandidates = 10_000

func (s *Server) HandleInsightsCapabilities(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	user, err := s.St.UserByID(r.Context(), tok.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account read failed")
		return
	}
	if _, err := time.LoadLocation(user.Timezone); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid account timezone")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": 1, "active_ms": true, "attribution_version": 2,
		"account_id": tok.UserID, "all_time": true,
		"timezone": user.Timezone, "max_candidates": maxInsightCandidates,
		"max_calendar_days":     maxCalendarDays,
		"max_body_bytes":        s.Cfg.Ops.MaxBodyBytes,
		"max_local_active_days": maxInsightCandidates,
	})
}

type insightCandidate struct {
	sessionReqJSON
	DeviceID string `json:"device_id"`
}

type insightSnapshotRequest struct {
	SnapshotID      string             `json:"snapshot_id"`
	Timezone        string             `json:"timezone"`
	Range           string             `json:"range"`
	From            string             `json:"from"`
	To              string             `json:"to"`
	Candidates      []insightCandidate `json:"candidates"`
	LocalActiveDays []string           `json:"local_active_days"`
	CalendarFrom    string             `json:"calendar_from"`
	CalendarTo      string             `json:"calendar_to"`
}

func snapshotWindow(req insightSnapshotRequest, loc *time.Location, now time.Time) (insights.Window, bool) {
	if req.From != "" || req.To != "" {
		from, errFrom := time.ParseInLocation(insights.DayFormat, req.From, loc)
		to, errTo := time.ParseInLocation(insights.DayFormat, req.To, loc)
		if errFrom != nil || errTo != nil || to.Before(from) || req.Range != "" {
			return insights.Window{}, false
		}
		return insights.DayWindow(from, to, loc), true
	}
	if req.Range == "all" {
		return insights.Window{}, true
	}
	if !strings.HasSuffix(req.Range, "d") {
		return insights.Window{}, false
	}
	days, err := strconv.Atoi(strings.TrimSuffix(req.Range, "d"))
	if err != nil || days <= 0 || days > insights.MaxRangeDays {
		return insights.Window{}, false
	}
	return insights.ParseWindow("", "", req.Range, "", loc, now), true
}

// Candidates and aggregates are read together; an upload acknowledgement,
// a separate receipt query, or an op cursor cannot prove this overlap.
func (s *Server) HandleInsightsSnapshot(w http.ResponseWriter, r *http.Request) {
	var req insightSnapshotRequest
	if decodeBatch(w, r, s.Cfg.Ops.MaxBodyBytes, &req) {
		return
	}
	if req.SnapshotID == "" || len(req.SnapshotID) > 128 ||
		len(req.Candidates) > maxInsightCandidates || len(req.LocalActiveDays) > maxInsightCandidates {
		writeError(w, http.StatusBadRequest, "invalid or excessive snapshot evidence")
		return
	}
	tok, _ := auth.TokenFrom(r)
	candidates := make([]store.Session, 0, len(req.Candidates))
	ids := make([]string, 0, len(req.Candidates))
	seen := make(map[string]bool)
	for _, candidate := range req.Candidates {
		if candidate.DeviceID == "" || len(candidate.DeviceID) > 64 || seen[candidate.SessionID] {
			writeError(w, http.StatusBadRequest, "invalid candidate identity")
			return
		}
		ses, err := parseSessionRequest(candidate.sessionReqJSON, candidate.DeviceID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid candidate payload")
			return
		}
		ses.UserID = tok.UserID
		seen[ses.SessionID] = true
		ids = append(ids, ses.SessionID)
		candidates = append(candidates, ses)
	}
	now := time.Now()
	snap, loc, ok := s.insightInput(w, r, ids)
	if !ok {
		return
	}
	if req.Timezone != snap.Timezone {
		writeError(w, http.StatusConflict, "statistics timezone changed")
		return
	}
	win, ok := snapshotWindow(req, loc, now)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid statistics window")
		return
	}
	result, ok := buildInsights(w, snap, win, now)
	if !ok {
		return
	}
	today := now.In(loc).Format(insights.DayFormat)
	for _, date := range req.LocalActiveDays {
		if _, err := time.Parse(insights.DayFormat, date); err != nil || date > today {
			writeError(w, http.StatusBadRequest, "invalid local activity day")
			return
		}
		result.ActiveDays[date] = true
	}
	calendarFrom, calendarTo := win.DayBounds(now, loc)
	if win.Unbounded() {
		calendarFrom = today
		if result.FirstActivityDay != nil {
			calendarFrom = *result.FirstActivityDay
		}
		first := now.In(loc).AddDate(0, 0, -(maxCalendarDays - 1)).Format(insights.DayFormat)
		if calendarFrom < first {
			calendarFrom = first
		}
	}
	if req.CalendarFrom != "" || req.CalendarTo != "" {
		calendarFrom, calendarTo = req.CalendarFrom, req.CalendarTo
	}
	first, errFrom := time.ParseInLocation(insights.DayFormat, calendarFrom, loc)
	last, errTo := time.ParseInLocation(insights.DayFormat, calendarTo, loc)
	if errFrom != nil || errTo != nil || last.Before(first) {
		writeError(w, http.StatusBadRequest, "invalid calendar window")
		return
	}
	calendarWin := insights.DayWindow(first, last, loc)
	if calendarWin.Days() > maxCalendarDays ||
		(!win.Unbounded() && (!win.HoldsDay(calendarFrom) || !win.HoldsDay(calendarTo))) {
		writeError(w, http.StatusBadRequest, "calendar window outside supported bounds")
		return
	}
	overlapSnapshot := store.StatsSnapshot{
		Timezone: snap.Timezone, Works: snap.Works, Editions: snap.Editions,
	}
	raw := make(map[string]store.Session, len(snap.Sessions))
	for _, ses := range snap.Sessions {
		raw[ses.SessionID] = ses
	}
	incomplete := func(reason string) {
		result.Complete = false
		result.IncompleteReason = reason
	}
	for _, candidate := range candidates {
		fingerprint := store.SessionFingerprint(candidate)
		if ses, exists := raw[candidate.SessionID]; exists {
			if store.SessionFingerprint(ses) != fingerprint {
				incomplete("candidate_payload_mismatch")
				continue
			}
			overlapSnapshot.Sessions = append(overlapSnapshot.Sessions, ses)
			continue
		}
		proof, archived := snap.Archived[candidate.SessionID]
		if !archived {
			continue
		}
		if proof.Fingerprint != fingerprint || proof.AttributionVersion != 2 {
			incomplete("unknown_archived_contribution")
			continue
		}
		if !proof.Present {
			continue
		}
		if proof.Timezone != snap.Timezone {
			incomplete("archived_timezone_mismatch")
			continue
		}
		if _, exists := result.ByWork[proof.WorkID]; !exists {
			incomplete("archived_work_missing")
			continue
		}
		overlapSnapshot.Rollups = append(overlapSnapshot.Rollups, store.SessionRollup{
			WorkID: proof.WorkID, Day: proof.Day, Timezone: proof.Timezone,
			AttributionVersion: 2, SessionCount: 1, ActiveSeconds: proof.ActiveSeconds,
			Pages: proof.Pages, ProgDelta: proof.ProgDelta,
			MeasuredActiveSeconds: proof.MeasuredActiveSeconds, MeasuredProgDelta: proof.MeasuredProgDelta,
		})
	}
	overlap, ok := buildInsights(w, overlapSnapshot, win, now)
	if !ok {
		return
	}
	answer := map[string]any{
		"version": 1, "account_id": tok.UserID, "snapshot_id": req.SnapshotID,
		"complete": result.Complete, "first_activity_day": result.FirstActivityDay,
		"today": today, "calendar_from": calendarFrom, "calendar_to": calendarTo,
		"summary": result.Summary, "works": result.Works, "days": calendarDays(result.Days, calendarWin),
		"combined_streak_days": insights.StreakDays(result.ActiveDays, loc, now),
		"overlap": map[string]any{
			"total_active_minutes": overlap.Summary.TotalActiveMinutes, "sessions": overlap.Summary.Sessions,
			"works": overlap.Works, "days": calendarDays(overlap.Days, calendarWin),
		},
	}
	if !result.Complete {
		answer["incomplete_reason"] = result.IncompleteReason
	}
	describe(win, answer)
	describeSnapshot(snap, answer)
	writeJSON(w, http.StatusOK, answer)
}

func calendarDays(days []insights.Day, win insights.Window) []insights.Day {
	out := make([]insights.Day, 0)
	for _, day := range days {
		if win.HoldsDay(day.Date) {
			out = append(out, day)
		}
	}
	return out
}
