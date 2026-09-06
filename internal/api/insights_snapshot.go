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
		"account_id": tok.UserID, "all_time": true, "comparison": true,
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
	Comparison      *insightComparison `json:"comparison"`
}

type insightComparison struct {
	CurrentFrom  string `json:"current_from"`
	CurrentTo    string `json:"current_to"`
	PreviousFrom string `json:"previous_from"`
	PreviousTo   string `json:"previous_to"`
	Through      string `json:"through"`
}

type comparisonWindows struct {
	current  insights.ComparisonWindow
	previous insights.ComparisonWindow
}

func snapshotWindow(req insightSnapshotRequest, loc *time.Location, now time.Time) (insights.Window, bool) {
	if req.From != "" || req.To != "" {
		from, errFrom := time.Parse(insights.DayFormat, req.From)
		to, errTo := time.Parse(insights.DayFormat, req.To)
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

func snapshotComparisonWindows(
	req *insightComparison,
	headline insights.Window,
	loc *time.Location,
	now time.Time,
) (comparisonWindows, bool) {
	if req == nil || headline.Unbounded() ||
		req.CurrentFrom != headline.FromDay() || req.CurrentTo != headline.ToDay() ||
		req.CurrentTo != now.In(loc).Format(insights.DayFormat) ||
		req.PreviousTo >= req.CurrentFrom {
		return comparisonWindows{}, false
	}
	current, ok := insights.NewComparisonWindow(req.CurrentFrom, req.CurrentTo, req.Through, loc)
	if !ok || current.Days() > maxCalendarDays {
		return comparisonWindows{}, false
	}
	previous, ok := insights.NewComparisonWindow(req.PreviousFrom, req.PreviousTo, req.Through, loc)
	if !ok || previous.Days() > maxCalendarDays {
		return comparisonWindows{}, false
	}
	return comparisonWindows{current: current, previous: previous}, true
}

func comparisonAnswer(current, previous float64, windows comparisonWindows) map[string]any {
	return map[string]any{
		"current_active_minutes":  current,
		"previous_active_minutes": previous,
		"current_from":            windows.current.FromDay(),
		"current_to":              windows.current.ToDay(),
		"previous_from":           windows.previous.FromDay(),
		"previous_to":             windows.previous.ToDay(),
		"through":                 windows.current.Through(),
	}
}

func comparisonTotals(current, previous float64) map[string]any {
	return map[string]any{
		"current_active_minutes":  current,
		"previous_active_minutes": previous,
	}
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
	var comparison *comparisonWindows
	if req.Comparison != nil {
		windows, valid := snapshotComparisonWindows(req.Comparison, win, loc, now)
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid comparison window")
			return
		}
		comparison = &windows
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
	first, errFrom := time.Parse(insights.DayFormat, calendarFrom)
	last, errTo := time.Parse(insights.DayFormat, calendarTo)
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
	comparisonOverlapSnapshot := store.StatsSnapshot{Timezone: snap.Timezone}
	raw := make(map[string]store.Session, len(snap.Sessions))
	for _, ses := range snap.Sessions {
		raw[ses.SessionID] = ses
	}
	knownWorks := make(map[string]bool, len(snap.Works))
	for _, work := range snap.Works {
		knownWorks[work.ID] = true
	}
	incomplete := func(reason string) {
		result.Complete = false
		result.IncompleteReason = reason
	}
	comparisonIncompleteReason := ""
	comparisonIncomplete := func(reason string) {
		if comparisonIncompleteReason == "" {
			comparisonIncompleteReason = reason
		}
	}
	for _, candidate := range candidates {
		headlineCandidate := win.HoldsSession(candidate)
		comparisonCandidate := comparison != nil &&
			(comparison.current.HoldsSession(candidate) || comparison.previous.HoldsSession(candidate))
		if !headlineCandidate && !comparisonCandidate {
			continue
		}
		fingerprint := store.SessionFingerprint(candidate)
		if ses, exists := raw[candidate.SessionID]; exists {
			if store.SessionFingerprint(ses) != fingerprint {
				if headlineCandidate {
					incomplete("candidate_payload_mismatch")
				}
				if comparisonCandidate {
					comparisonIncomplete("candidate_payload_mismatch")
				}
				continue
			}
			if headlineCandidate {
				overlapSnapshot.Sessions = append(overlapSnapshot.Sessions, ses)
			}
			if comparisonCandidate {
				comparisonOverlapSnapshot.Sessions = append(comparisonOverlapSnapshot.Sessions, ses)
			}
			continue
		}
		proof, archived := snap.Archived[candidate.SessionID]
		if !archived {
			continue
		}
		if proof.Fingerprint != fingerprint || proof.AttributionVersion != 2 {
			if headlineCandidate {
				incomplete("unknown_archived_contribution")
			}
			if comparisonCandidate {
				comparisonIncomplete("unknown_archived_contribution")
			}
			continue
		}
		if !proof.Present {
			continue
		}
		if proof.Timezone != snap.Timezone {
			if headlineCandidate {
				incomplete("archived_timezone_mismatch")
			}
			if comparisonCandidate {
				comparisonIncomplete("archived_timezone_mismatch")
			}
			continue
		}
		if proof.WorkID != candidate.WorkID || proof.Day != candidate.EndedAt.In(loc).Format(insights.DayFormat) {
			if headlineCandidate {
				incomplete("unknown_archived_contribution")
			}
			if comparisonCandidate {
				comparisonIncomplete("unknown_archived_contribution")
			}
			continue
		}
		if !knownWorks[proof.WorkID] {
			if headlineCandidate {
				incomplete("archived_work_missing")
			}
			if comparisonCandidate {
				comparisonIncomplete("archived_work_missing")
			}
			continue
		}
		if headlineCandidate {
			overlapSnapshot.Rollups = append(overlapSnapshot.Rollups, store.SessionRollup{
				WorkID: proof.WorkID, Day: proof.Day, Timezone: proof.Timezone,
				AttributionVersion: 2, SessionCount: 1, ActiveSeconds: proof.ActiveSeconds,
				Pages: proof.Pages, ProgDelta: proof.ProgDelta,
				MeasuredActiveSeconds: proof.MeasuredActiveSeconds, MeasuredProgDelta: proof.MeasuredProgDelta,
			})
		}
		if comparisonCandidate {
			if proof.ComparisonActiveMs == nil || *proof.ComparisonActiveMs < 0 {
				comparisonIncomplete("unknown_archived_contribution")
				continue
			}
			archivedCandidate := candidate
			activeMs := *proof.ComparisonActiveMs
			archivedCandidate.ActiveMs = &activeMs
			comparisonOverlapSnapshot.Sessions = append(comparisonOverlapSnapshot.Sessions, archivedCandidate)
		}
	}
	overlap, ok := buildInsights(w, overlapSnapshot, win, now)
	if !ok {
		return
	}
	overlapAnswer := map[string]any{
		"total_active_minutes": overlap.Summary.TotalActiveMinutes, "sessions": overlap.Summary.Sessions,
		"works": overlap.Works, "days": calendarDays(overlap.Days, calendarWin),
	}
	answer := map[string]any{
		"version": 1, "account_id": tok.UserID, "snapshot_id": req.SnapshotID,
		"complete": result.Complete, "first_activity_day": result.FirstActivityDay,
		"today": today, "calendar_from": calendarFrom, "calendar_to": calendarTo,
		"summary": result.Summary, "works": result.Works, "days": calendarDays(result.Days, calendarWin),
		"combined_streak_days": insights.StreakDays(result.ActiveDays, loc, now),
		"overlap":              overlapAnswer,
	}
	if !result.Complete {
		answer["incomplete_reason"] = result.IncompleteReason
	}
	if comparison != nil {
		current, currentOK := insights.SpanMinutes(snap, comparison.current, loc)
		previous, previousOK := insights.SpanMinutes(snap, comparison.previous, loc)
		overlapCurrent, overlapCurrentOK := insights.SpanMinutes(comparisonOverlapSnapshot, comparison.current, loc)
		overlapPrevious, overlapPreviousOK := insights.SpanMinutes(comparisonOverlapSnapshot, comparison.previous, loc)
		if comparisonIncompleteReason == "" && currentOK && previousOK && overlapCurrentOK && overlapPreviousOK {
			answer["comparison"] = comparisonAnswer(current, previous, *comparison)
			overlapAnswer["comparison"] = comparisonTotals(overlapCurrent, overlapPrevious)
		} else {
			if comparisonIncompleteReason == "" {
				comparisonIncompleteReason = "comparison_history_incomplete"
			}
			answer["comparison_incomplete_reason"] = comparisonIncompleteReason
		}
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
