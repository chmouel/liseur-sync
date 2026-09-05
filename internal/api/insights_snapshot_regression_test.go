package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/insights"
	"github.com/chmouel/liseur-sync/internal/store"
)

type snapshotFixture struct {
	ts             *httptest.Server
	st             store.Store
	user           store.User
	work           store.Work
	reader, writer string
	originalDevice string
}

func newSnapshotFixture(t *testing.T) *snapshotFixture {
	t.Helper()
	ts, st := testServer(t)
	t.Cleanup(func() {
		ts.Close()
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	hash, err := auth.HashPassword("hunter2hunter")
	if err != nil {
		t.Fatal(err)
	}
	f := &snapshotFixture{
		ts: ts, st: st,
		user: store.User{ID: "snapshot-user", Name: "snapshot-reader", Argon2Hash: hash, Timezone: "UTC", CreatedAt: time.Now()},
	}
	if err := st.CreateUser(t.Context(), f.user); err != nil {
		t.Fatal(err)
	}
	f.work = store.Work{ID: "snapshot-work", UserID: f.user.ID, Title: "Test book", Author: "Test author", CreatedAt: time.Now()}
	if err := st.CreateWork(t.Context(), f.work, nil, nil); err != nil {
		t.Fatal(err)
	}
	service := auth.NewService(st)
	f.reader, _, err = service.MintToken(t.Context(), f.user.ID, "new phone", store.ScopeSet{store.ScopeReadInsights}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var tok store.Token
	f.writer, tok, err = service.MintToken(t.Context(), f.user.ID, "original phone", store.ScopeSet{store.ScopeSync}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.originalDevice = tok.DeviceID
	return f
}

func (f *snapshotFixture) session(id string) store.Session {
	start := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)
	active := int64(30 * 60 * 1000)
	return store.Session{
		UserID: f.user.ID, SessionID: id, WorkID: f.work.ID, DeviceID: f.originalDevice,
		StartedAt: start, EndedAt: start.Add(10 * time.Minute),
		StartProg: 0.1, EndProg: 0.2, ActiveMs: &active, Origin: store.OriginNative,
	}
}

// Build the wire payload independently of the handler's request types.
func snapshotCandidate(s store.Session) map[string]any {
	out := map[string]any{
		"session_id": s.SessionID, "work_id": s.WorkID, "device_id": s.DeviceID,
		"started_at": s.StartedAt.Format(time.RFC3339Nano), "ended_at": s.EndedAt.Format(time.RFC3339Nano),
		"start_progression": s.StartProg, "end_progression": s.EndProg, "idle_ms": s.IdleMs,
	}
	if s.ActiveMs != nil {
		out["active_ms"] = *s.ActiveMs
	}
	if s.EditionSHA != nil {
		out["edition_sha"] = *s.EditionSHA
	}
	return out
}

func snapshotBody(sessions ...store.Session) map[string]any {
	candidates := make([]map[string]any, 0, len(sessions))
	for _, s := range sessions {
		candidates = append(candidates, snapshotCandidate(s))
	}
	return map[string]any{
		"snapshot_id": "local-generation-7", "timezone": "UTC", "range": "all",
		"candidates": candidates, "local_active_days": []string{},
		"calendar_from": "2024-01-01", "calendar_to": "2024-01-31",
	}
}

type snapshotReply struct {
	AccountID          string           `json:"account_id"`
	SnapshotID         string           `json:"snapshot_id"`
	Timezone           string           `json:"timezone"`
	Revision           string           `json:"stats_revision"`
	Complete           bool             `json:"complete"`
	IncompleteReason   string           `json:"incomplete_reason"`
	CombinedStreakDays int              `json:"combined_streak_days"`
	Summary            insights.Summary `json:"summary"`
	Works              []insights.Work  `json:"works"`
	Days               []insights.Day   `json:"days"`
	Overlap            struct {
		Minutes  float64         `json:"total_active_minutes"`
		Sessions int             `json:"sessions"`
		Works    []insights.Work `json:"works"`
		Days     []insights.Day  `json:"days"`
	} `json:"overlap"`
}

func readSnapshot(t *testing.T, url, token string, body map[string]any) snapshotReply {
	t.Helper()
	code, out := post(t, url+"/v1/insights/snapshot", token, body)
	if code != http.StatusOK {
		t.Fatalf("snapshot: HTTP %d: %v", code, out)
	}
	for _, field := range []string{
		"account_id", "snapshot_id", "timezone", "stats_revision", "complete",
		"summary", "works", "days", "overlap", "combined_streak_days",
	} {
		if _, ok := out[field]; !ok {
			t.Errorf("snapshot missing %q: %v", field, out)
		}
	}
	if out["version"] != float64(1) || out["attribution_version"] != float64(2) {
		t.Errorf("protocol versions: %v", out)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var got snapshotReply
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	revision, err := strconv.ParseInt(got.Revision, 10, 64)
	if err != nil || revision < 0 || strconv.FormatInt(revision, 10) != got.Revision {
		t.Errorf("revision must be a canonical decimal string: %q", got.Revision)
	}
	if got.SnapshotID != body["snapshot_id"] || got.Timezone != body["timezone"] {
		t.Errorf("snapshot identity not echoed: %+v", got)
	}
	return got
}

func snapshotNear(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > 1e-9 {
		t.Errorf("%s: got %v, want %v", label, got, want)
	}
}

func requireSnapshotError(t *testing.T, code int, out map[string]any, want int) {
	t.Helper()
	if code != want {
		t.Fatalf("got HTTP %d, want %d: %v", code, want, out)
	}
	if text, ok := out["error"].(string); !ok || text == "" {
		t.Errorf("missing JSON error: %v", out)
	}
	for _, field := range []string{"summary", "overlap", "stats_revision", "complete"} {
		if _, ok := out[field]; ok {
			t.Errorf("failure contains success field %q: %v", field, out)
		}
	}
}

func TestInsightsSnapshotMeasuredOverlapSurvivesReplayAndCompaction(t *testing.T) {
	f := newSnapshotFixture(t)
	f.work.ID = "edition-work"
	sha, pages := "edition-sha", int64(200)
	if err := f.st.CreateWork(t.Context(), f.work, &store.Edition{
		UserID: f.user.ID, WorkID: f.work.ID, SHA256: sha, PageCount: &pages,
	}, nil); err != nil {
		t.Fatal(err)
	}
	local, remote := f.session("local"), f.session("remote")
	local.EditionSHA, remote.EditionSHA = &sha, &sha
	local.IdleMs = 5 * 60 * 1000
	remote.DeviceID = "another-device"
	remoteActive := int64(20 * 60 * 1000)
	remote.ActiveMs = &remoteActive
	if err := f.st.AppendSessions(t.Context(), f.user.ID, []store.Session{local, remote}); err != nil {
		t.Fatal(err)
	}
	body := snapshotBody(local)
	assertTotals := func(got snapshotReply) {
		t.Helper()
		if got.AccountID != f.user.ID || !got.Complete || got.IncompleteReason != "" {
			t.Errorf("snapshot identity/completeness: %+v", got)
		}
		snapshotNear(t, "server minutes", got.Summary.TotalActiveMinutes, 50)
		snapshotNear(t, "server pages", got.Summary.TotalPages, 40)
		snapshotNear(t, "overlap minutes", got.Overlap.Minutes, 30)
		if got.Summary.Sessions != 2 || got.Overlap.Sessions != 1 {
			t.Errorf("session counts: %+v", got)
		}
		if len(got.Works) != 1 || got.Works[0].WorkID != f.work.ID ||
			got.Works[0].TotalActiveMinutes != 50 || got.Works[0].Sessions != 2 ||
			got.Works[0].TotalPages != 40 ||
			len(got.Overlap.Works) != 1 || got.Overlap.Works[0].TotalActiveMinutes != 30 ||
			got.Overlap.Works[0].Sessions != 1 || got.Overlap.Works[0].TotalPages != 20 {
			t.Errorf("per-work server/overlap totals: %+v", got)
		}
		if want := []insights.Day{{Date: "2024-01-02", Minutes: 50, Pages: 40, Sessions: 2}}; !reflect.DeepEqual(got.Days, want) {
			t.Errorf("server days: got %+v, want %+v", got.Days, want)
		}
		if want := []insights.Day{{Date: "2024-01-02", Minutes: 30, Pages: 20, Sessions: 1}}; !reflect.DeepEqual(got.Overlap.Days, want) {
			t.Errorf("overlap days: got %+v, want %+v", got.Overlap.Days, want)
		}
	}
	before := readSnapshot(t, f.ts.URL, f.reader, body)
	assertTotals(before)
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compacted=%v", compact), func(t *testing.T) {
			if compact {
				(&Server{St: f.st}).rollupSessionsOnce(t.Context(), 24*time.Hour)
				snap, err := f.st.StatisticsSnapshot(t.Context(), f.user.ID, []string{local.SessionID})
				if err != nil {
					t.Fatal(err)
				}
				if len(snap.Sessions) != 0 || !snap.Archived[local.SessionID].Present {
					t.Fatalf("compaction precondition failed: %+v", snap)
				}
			}
			preReplay := readSnapshot(t, f.ts.URL, f.reader, body)
			assertTotals(preReplay)
			code, out := post(t, f.ts.URL+"/v1/sessions", f.writer,
				map[string]any{"sessions": []map[string]any{snapshotCandidate(local)}})
			if code != http.StatusOK {
				t.Fatalf("identical replay: %d %v", code, out)
			}
			after := readSnapshot(t, f.ts.URL, f.reader, body)
			assertTotals(after)
			if after.Revision != preReplay.Revision {
				t.Errorf("idempotent replay changed revision: %s -> %s", preReplay.Revision, after.Revision)
			}
		})
	}
}

func TestInsightsSnapshotLegacyPayloadSubtractsActualServerContribution(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compacted=%v", compact), func(t *testing.T) {
			f := newSnapshotFixture(t)
			legacy := f.session("legacy-local")
			legacy.ActiveMs = nil
			if err := f.st.AppendSessions(t.Context(), f.user.ID, []store.Session{legacy}); err != nil {
				t.Fatal(err)
			}
			if compact {
				(&Server{St: f.st}).rollupSessionsOnce(t.Context(), 24*time.Hour)
			}
			got := readSnapshot(t, f.ts.URL, f.reader, snapshotBody(legacy))
			if !got.Complete || got.Summary.Sessions != 1 || got.Overlap.Sessions != 1 {
				t.Fatalf("original legacy payload is exact evidence: %+v", got)
			}
			snapshotNear(t, "legacy server minutes", got.Summary.TotalActiveMinutes, 10)
			snapshotNear(t, "actual overlap, not local measured duration", got.Overlap.Minutes, 10)
			// The phone has since measured 30 minutes, but must offer the
			// original payload as evidence for the server's ten-minute row.
			snapshotNear(t, "local + server - actual overlap", 30+got.Summary.TotalActiveMinutes-got.Overlap.Minutes, 30)
			if len(got.Overlap.Works) != 1 || got.Overlap.Works[0].TotalActiveMinutes != 10 ||
				len(got.Overlap.Days) != 1 || got.Overlap.Days[0].Minutes != 10 {
				t.Errorf("legacy overlap dimensions: %+v", got.Overlap)
			}
			changed := legacy
			active := int64(30 * 60 * 1000)
			changed.ActiveMs = &active
			mismatch := readSnapshot(t, f.ts.URL, f.reader, snapshotBody(changed))
			if mismatch.Complete || mismatch.IncompleteReason == "" || mismatch.Overlap.Minutes != 0 {
				t.Errorf("new payload cannot prove the old contribution: %+v", mismatch)
			}
			snapshotNear(t, "mismatch still exposes server totals", mismatch.Summary.TotalActiveMinutes, 10)
		})
	}
}

func TestInsightsSnapshotLegacyArchiveFallsBackWithoutInventingOverlap(t *testing.T) {
	f := newSnapshotFixture(t)
	ses := f.session("legacy-archive")
	ses.ActiveMs = nil
	if err := f.st.AppendSessions(t.Context(), f.user.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.ApplyRollups(t.Context(), f.user.ID, []store.SessionRollup{{
		WorkID: f.work.ID, Day: "2024-01-02", ActiveSeconds: 600, SessionCount: 1,
	}}, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	for _, candidates := range [][]store.Session{nil, {ses}} {
		got := readSnapshot(t, f.ts.URL, f.reader, snapshotBody(candidates...))
		if got.Complete || got.IncompleteReason == "" || got.Summary.Sessions != 1 ||
			got.Overlap.Sessions != 0 || got.Overlap.Minutes != 0 {
			t.Errorf("legacy archive must request fallback: %+v", got)
		}
		snapshotNear(t, "legacy total retained", got.Summary.TotalActiveMinutes, 10)
		if len(got.Days) != 1 || got.Days[0].Minutes != 10 || len(got.Works) != 1 {
			t.Errorf("legacy totals hidden: %+v", got)
		}
	}
}

func TestInsightsSnapshotDeletedContributionsAreNotOverlap(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compacted=%v", compact), func(t *testing.T) {
			f := newSnapshotFixture(t)
			ses := f.session("deleted")
			if err := f.st.AppendSessions(t.Context(), f.user.ID, []store.Session{ses}); err != nil {
				t.Fatal(err)
			}
			if compact {
				(&Server{St: f.st}).rollupSessionsOnce(t.Context(), 24*time.Hour)
			}
			before := readSnapshot(t, f.ts.URL, f.reader, snapshotBody(ses))
			if err := f.st.DeleteWork(t.Context(), f.user.ID, f.work.ID); err != nil {
				t.Fatal(err)
			}
			got := readSnapshot(t, f.ts.URL, f.reader, snapshotBody(ses))
			if !got.Complete || got.Summary.Sessions != 0 || got.Summary.TotalActiveMinutes != 0 ||
				got.Overlap.Sessions != 0 || got.Overlap.Minutes != 0 ||
				len(got.Works) != 0 || len(got.Days) != 0 || len(got.Overlap.Works) != 0 || len(got.Overlap.Days) != 0 {
				t.Errorf("deleted contribution still counted: %+v", got)
			}
			if got.Revision == before.Revision {
				t.Error("deletion did not invalidate snapshot revision")
			}
		})
	}
}

func TestInsightsSnapshotAuthScopesAndTenantIsolation(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, endpoint := range []string{"capabilities", "snapshot"} {
		for _, token := range []struct {
			name, secret string
			want         int
		}{
			{"anonymous", "", http.StatusUnauthorized},
			{"invalid bearer", "not-a-token", http.StatusUnauthorized},
			{"sync only", f.writer, http.StatusForbidden},
			{"read-insights only", f.reader, http.StatusOK},
		} {
			t.Run(endpoint+"/"+token.name, func(t *testing.T) {
				var code int
				var out map[string]any
				if endpoint == "capabilities" {
					code, out = get(t, f.ts.URL+"/v1/insights/capabilities", token.secret)
				} else {
					code, out = post(t, f.ts.URL+"/v1/insights/snapshot", token.secret, snapshotBody())
				}
				if token.want != http.StatusOK {
					requireSnapshotError(t, code, out, token.want)
				} else if code != http.StatusOK {
					t.Errorf("read-insights route: %d %v", code, out)
				}
			})
		}
	}
	code, caps := get(t, f.ts.URL+"/v1/insights/capabilities", f.reader)
	if code != http.StatusOK || caps["version"] != float64(1) || caps["active_ms"] != true ||
		caps["attribution_version"] != float64(2) || caps["timezone"] != "UTC" ||
		caps["account_id"] != f.user.ID || caps["all_time"] != true ||
		caps["max_candidates"] != float64(10_000) || caps["max_calendar_days"] != float64(4000) ||
		caps["max_local_active_days"] != float64(10_000) ||
		caps["max_body_bytes"] != float64(config.Default().Ops.MaxBodyBytes) {
		t.Errorf("capability contract: %d %v", code, caps)
	}
	other := store.User{ID: "other-user", Name: "other-reader", Argon2Hash: f.user.Argon2Hash, Timezone: "Europe/Paris", CreatedAt: time.Now()}
	if err := f.st.CreateUser(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	otherWork := store.Work{ID: "other-work", UserID: other.ID, CreatedAt: time.Now()}
	if err := f.st.CreateWork(t.Context(), otherWork, nil, nil); err != nil {
		t.Fatal(err)
	}
	ses := f.session("other-session")
	ses.UserID, ses.WorkID = other.ID, otherWork.ID
	if err := f.st.AppendSessions(t.Context(), other.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := auth.NewService(f.st).MintToken(t.Context(), other.ID, "other phone", store.ScopeSet{store.ScopeReadInsights}, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, caps = get(t, f.ts.URL+"/v1/insights/capabilities", otherToken)
	if code != http.StatusOK || caps["timezone"] != "Europe/Paris" || caps["account_id"] != other.ID {
		t.Errorf("capabilities use token's account timezone: %d %v", code, caps)
	}
	for _, compact := range []bool{false, true} {
		if compact {
			(&Server{St: f.st}).rollupSessionsOnce(t.Context(), 24*time.Hour)
		}
		got := readSnapshot(t, f.ts.URL, f.reader, snapshotBody(ses))
		if got.AccountID != f.user.ID || !got.Complete || got.Summary.Sessions != 0 ||
			got.Overlap.Sessions != 0 || len(got.Works) != 0 || len(got.Days) != 0 {
			t.Errorf("foreign candidate leaked tenant data (compacted=%v): %+v", compact, got)
		}
		body := snapshotBody(ses)
		body["timezone"] = "Europe/Paris"
		own := readSnapshot(t, f.ts.URL, otherToken, body)
		if own.AccountID != other.ID || own.Summary.TotalActiveMinutes != 30 || own.Overlap.Minutes != 30 {
			t.Errorf("other tenant's own reading missing: %+v", own)
		}
	}
}

func TestInsightsSnapshotWindowAndCalendarClipBothAggregates(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compacted=%v", compact), func(t *testing.T) {
			f := newSnapshotFixture(t)
			loc, err := time.LoadLocation("Europe/Paris")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.st.UpdateUserSettings(t.Context(), f.user.ID, "Europe/Paris", false, false); err != nil {
				t.Fatal(err)
			}
			midnight := time.Date(2024, 1, 2, 0, 0, 0, 0, loc)
			before, cross, zero, next := f.session("before"), f.session("cross"), f.session("zero"), f.session("next")
			before.StartedAt, before.EndedAt = midnight.Add(-time.Hour), midnight.Add(-time.Minute)
			cross.StartedAt, cross.EndedAt = midnight.Add(-10*time.Minute), midnight
			zero.StartedAt, zero.EndedAt = midnight, midnight
			z := int64(0)
			zero.ActiveMs = &z
			next.StartedAt, next.EndedAt = midnight.AddDate(0, 0, 1).Add(-time.Minute), midnight.AddDate(0, 0, 1)
			sessions := []store.Session{before, cross, zero, next}
			if err := f.st.AppendSessions(t.Context(), f.user.ID, sessions); err != nil {
				t.Fatal(err)
			}
			if compact {
				(&Server{St: f.st}).rollupSessionsOnce(t.Context(), 24*time.Hour)
			}
			body := snapshotBody(sessions...)
			delete(body, "range")
			body["timezone"], body["from"], body["to"] = "Europe/Paris", "2024-01-02", "2024-01-02"
			body["calendar_from"], body["calendar_to"] = "2024-01-02", "2024-01-02"
			got := readSnapshot(t, f.ts.URL, f.reader, body)
			if !got.Complete || got.Summary.TotalActiveMinutes != 30 || got.Overlap.Minutes != 30 ||
				got.Summary.Sessions != 2 || got.Overlap.Sessions != 2 {
				t.Errorf("end-day range clipped duration or lost zero-duration sitting: %+v", got)
			}
			want := []insights.Day{{Date: "2024-01-02", Minutes: 30, Sessions: 2}}
			if !reflect.DeepEqual(got.Days, want) || !reflect.DeepEqual(got.Overlap.Days, want) {
				t.Errorf("end-day calendars: server=%+v overlap=%+v", got.Days, got.Overlap.Days)
			}
		})
	}
}

func TestInsightsSnapshotStreakUnionsAllHistoryNotDisplayedDays(t *testing.T) {
	f := newSnapshotFixture(t)
	today := time.Now().UTC()
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	var sessions []store.Session
	for _, offset := range []int{-4, -3, -1} {
		ses := f.session(fmt.Sprintf("day%d", offset))
		ses.StartedAt = today.AddDate(0, 0, offset).Add(time.Hour)
		ses.EndedAt = ses.StartedAt.Add(10 * time.Minute)
		sessions = append(sessions, ses)
	}
	if err := f.st.AppendSessions(t.Context(), f.user.ID, sessions); err != nil {
		t.Fatal(err)
	}
	body := snapshotBody()
	body["local_active_days"] = []string{
		today.Format(insights.DayFormat), today.AddDate(0, 0, -2).Format(insights.DayFormat),
		today.AddDate(0, 0, -2).Format(insights.DayFormat),
	}
	got := readSnapshot(t, f.ts.URL, f.reader, body)
	if got.CombinedStreakDays != 5 || got.Summary.StreakDays != 1 ||
		got.Summary.TotalActiveMinutes != 90 || len(got.Days) != 0 {
		t.Errorf("all-history streak union must not change server totals: %+v", got)
	}
	delete(body, "range")
	body["from"], body["to"] = "2024-01-01", "2024-01-31"
	got = readSnapshot(t, f.ts.URL, f.reader, body)
	if got.CombinedStreakDays != 5 || got.Summary.Sessions != 0 || got.Summary.TotalActiveMinutes != 0 {
		t.Errorf("display range must not truncate combined streak: %+v", got)
	}
}

func TestInsightsSnapshotCalendarChunksRetainRevisionIdentity(t *testing.T) {
	f := newSnapshotFixture(t)
	first, second := f.session("first"), f.session("second")
	second.StartedAt = second.StartedAt.AddDate(0, 1, 0)
	second.EndedAt = second.EndedAt.AddDate(0, 1, 0)
	if err := f.st.AppendSessions(t.Context(), f.user.ID, []store.Session{first, second}); err != nil {
		t.Fatal(err)
	}
	body := snapshotBody(first, second)
	january := readSnapshot(t, f.ts.URL, f.reader, body)
	body["calendar_from"], body["calendar_to"] = "2024-02-01", "2024-02-29"
	february := readSnapshot(t, f.ts.URL, f.reader, body)
	if january.Revision != february.Revision || january.AccountID != february.AccountID ||
		january.SnapshotID != february.SnapshotID || !reflect.DeepEqual(january.Summary, february.Summary) ||
		!reflect.DeepEqual(january.Works, february.Works) || january.Overlap.Minutes != february.Overlap.Minutes {
		t.Errorf("calendar chunk changed snapshot identity/totals: January=%+v February=%+v", january, february)
	}
	if january.Summary.TotalActiveMinutes != 60 || january.Overlap.Minutes != 60 ||
		len(january.Days) != 1 || january.Days[0].Date != "2024-01-02" ||
		len(february.Days) != 1 || february.Days[0].Date != "2024-02-02" ||
		len(january.Overlap.Days) != 1 || january.Overlap.Days[0].Date != "2024-01-02" ||
		len(february.Overlap.Days) != 1 || february.Overlap.Days[0].Date != "2024-02-02" {
		t.Errorf("chunks not independently clipped: January=%+v February=%+v", january, february)
	}
	if err := f.st.AppendSessions(t.Context(), f.user.ID, []store.Session{f.session("new-reading")}); err != nil {
		t.Fatal(err)
	}
	changed := readSnapshot(t, f.ts.URL, f.reader, body)
	if changed.Revision == february.Revision || changed.Summary.TotalActiveMinutes != 90 {
		t.Errorf("new reading must invalidate assembled chunks: %+v", changed)
	}
}

func TestInsightsSnapshotRejectsInvalidEvidenceAndWindows(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want int
	}{
		{"missing snapshot id", func(b map[string]any) { delete(b, "snapshot_id") }, 400},
		{"long snapshot id", func(b map[string]any) { b["snapshot_id"] = strings.Repeat("s", 129) }, 400},
		{"missing timezone", func(b map[string]any) { delete(b, "timezone") }, 409},
		{"invalid timezone", func(b map[string]any) { b["timezone"] = "Not/A_Timezone" }, 409},
		{"stale timezone", func(b map[string]any) { b["timezone"] = "Europe/Paris" }, 409},
		{"missing range", func(b map[string]any) { delete(b, "range") }, 400},
		{"unknown range", func(b map[string]any) { b["range"] = "everything" }, 400},
		{"zero days", func(b map[string]any) { b["range"] = "0d" }, 400},
		{"negative days", func(b map[string]any) { b["range"] = "-1d" }, 400},
		{"excessive days", func(b map[string]any) { b["range"] = "3661d" }, 400},
		{"partial from to", func(b map[string]any) { delete(b, "range"); b["from"] = "2024-01-01" }, 400},
		{"impossible date", func(b map[string]any) {
			delete(b, "range")
			b["from"], b["to"] = "2024-02-30", "2024-03-01"
		}, 400},
		{"non padded date", func(b map[string]any) {
			delete(b, "range")
			b["from"], b["to"] = "2024-1-1", "2024-01-31"
		}, 400},
		{"reversed dates", func(b map[string]any) {
			delete(b, "range")
			b["from"], b["to"] = "2024-02-01", "2024-01-01"
		}, 400},
		{"range and dates", func(b map[string]any) { b["from"], b["to"] = "2024-01-01", "2024-01-31" }, 400},
		{"bad local day", func(b map[string]any) { b["local_active_days"] = []string{"2024-02-30"} }, 400},
		{"non padded local day", func(b map[string]any) { b["local_active_days"] = []string{"2024-1-2"} }, 400},
		{"local day timestamp", func(b map[string]any) { b["local_active_days"] = []string{"2024-01-02T00:00:00Z"} }, 400},
		{"local day whitespace", func(b map[string]any) { b["local_active_days"] = []string{" 2024-01-02"} }, 400},
		{"empty local day", func(b map[string]any) { b["local_active_days"] = []string{""} }, 400},
		{"future local day", func(b map[string]any) {
			b["local_active_days"] = []string{time.Now().UTC().AddDate(0, 0, 2).Format(insights.DayFormat)}
		}, 400},
		{"too many local days", func(b map[string]any) { b["local_active_days"] = make([]string, 10_001) }, 400},
		{"too many candidates", func(b map[string]any) { b["candidates"] = make([]map[string]any, 10_001) }, 400},
		{"duplicate candidates", func(b map[string]any) {
			b["candidates"] = []map[string]any{snapshotCandidate(f.session("same")), snapshotCandidate(f.session("same"))}
		}, 400},
		{"partial calendar", func(b map[string]any) { delete(b, "calendar_to") }, 400},
		{"invalid calendar day", func(b map[string]any) { b["calendar_from"] = "2024-02-30" }, 400},
		{"reversed calendar", func(b map[string]any) { b["calendar_to"] = "2023-12-31" }, 400},
		{"excessive calendar", func(b map[string]any) { b["calendar_from"], b["calendar_to"] = "2000-01-01", "2024-01-31" }, 400},
		{"calendar outside range", func(b map[string]any) {
			delete(b, "range")
			b["from"], b["to"] = "2024-01-02", "2024-01-31"
		}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := snapshotBody()
			tc.edit(body)
			code, out := post(t, f.ts.URL+"/v1/insights/snapshot", f.reader, body)
			requireSnapshotError(t, code, out, tc.want)
		})
	}
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"missing device", "device_id", nil},
		{"empty device", "device_id", ""},
		{"long device", "device_id", strings.Repeat("d", 65)},
		{"empty session", "session_id", ""},
		{"long session", "session_id", strings.Repeat("s", 65)},
		{"empty work", "work_id", ""},
		{"bad start", "started_at", "yesterday"},
		{"backwards times", "ended_at", "2024-01-01T00:00:00Z"},
		{"missing progression", "start_progression", nil},
		{"out of range progression", "end_progression", 1.01},
		{"negative progression", "start_progression", -0.1},
		{"negative idle", "idle_ms", -1},
		{"excess idle", "idle_ms", 600_001},
		{"negative active", "active_ms", -1},
		{"excess active", "active_ms", int64(9007199254740992)},
		{"fractional active", "active_ms", 0.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := snapshotCandidate(f.session("invalid"))
			candidate[tc.field] = tc.value
			body := snapshotBody()
			body["candidates"] = []map[string]any{candidate}
			code, out := post(t, f.ts.URL+"/v1/insights/snapshot", f.reader, body)
			requireSnapshotError(t, code, out, http.StatusBadRequest)
		})
	}
}

type snapshotReadStore struct {
	store.Store
	read func(context.Context, string, []string) (store.StatsSnapshot, error)
}

func (s *snapshotReadStore) StatisticsSnapshot(ctx context.Context, userID string, ids []string) (store.StatsSnapshot, error) {
	return s.read(ctx, userID, ids)
}

func snapshotTestServer(t *testing.T, f *snapshotFixture, st store.Store, maxBody int64) *httptest.Server {
	t.Helper()
	cfg := config.Default()
	cfg.InsecureHTTP = true
	if maxBody != 0 {
		cfg.Ops.MaxBodyBytes = maxBody
	}
	srv := &Server{St: st, Auth: auth.NewService(f.st), Cfg: cfg}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestInsightsSnapshotRejectsMalformedAndOversizedBodies(t *testing.T) {
	f := newSnapshotFixture(t)
	ts := snapshotTestServer(t, f, f.st, 1024)
	valid, err := json.Marshal(snapshotBody())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"empty", "", 400},
		{"malformed", `{"snapshot_id":`, 400},
		{"wrong shape", `[]`, 400},
		{"trailing JSON", string(valid) + `{}`, 400},
		{"trailing garbage", string(valid) + `garbage`, 400},
		{"oversized trailing body", string(valid) + strings.Repeat(" ", 2048) + `{}`, 413},
		{"oversized", `{"snapshot_id":"` + strings.Repeat("x", 2048) + `"}`, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/insights/snapshot", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+f.reader)
			req.Header.Set("Content-Type", "application/json")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var out map[string]any
			if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			requireSnapshotError(t, res.StatusCode, out, tc.want)
		})
	}
}

func TestInsightsSnapshotUsesOneCoherentReadAndExactRevision(t *testing.T) {
	f := newSnapshotFixture(t)
	ses := f.session("coherent")
	sha, pageCount := "snapshot-edition", int64(200)
	ses.EditionSHA = &sha
	snap := store.StatsSnapshot{
		Timezone: "UTC", Revision: 9007199254740993,
		Works: []store.Work{f.work}, Sessions: []store.Session{ses},
		Editions: map[string]store.Edition{sha: {PageCount: &pageCount}},
	}
	var calls atomic.Int32
	st := &snapshotReadStore{
		Store: f.st,
		read: func(_ context.Context, userID string, ids []string) (store.StatsSnapshot, error) {
			calls.Add(1)
			if userID != f.user.ID || !reflect.DeepEqual(ids, []string{ses.SessionID}) {
				t.Errorf("snapshot evidence read under wrong account or IDs: %q %v", userID, ids)
			}
			return snap, nil
		},
	}
	ts := snapshotTestServer(t, f, st, 0)
	got := readSnapshot(t, ts.URL, f.reader, snapshotBody(ses))
	if calls.Load() != 1 || got.Revision != "9007199254740993" || got.Summary.TotalActiveMinutes != 30 ||
		got.Overlap.Minutes != 30 || !got.Complete {
		t.Errorf("one snapshot must supply totals, evidence and lossless revision: calls=%d reply=%+v", calls.Load(), got)
	}
	snapshotNear(t, "pages from coherent edition metadata", got.Summary.TotalPages, 20)
	if len(got.Works) != 1 || got.Works[0].TotalPages != 20 ||
		len(got.Overlap.Works) != 1 || got.Overlap.Works[0].TotalPages != 20 ||
		len(got.Days) != 1 || got.Days[0].Pages != 20 ||
		len(got.Overlap.Days) != 1 || got.Overlap.Days[0].Pages != 20 {
		t.Errorf("pages must use the same snapshot in every dimension: %+v", got)
	}
}

func TestInsightsSnapshotCoherentFailuresNeverBecomePartialSuccess(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, tc := range []struct {
		name string
		edit func(*store.StatsSnapshot) error
	}{
		{"snapshot read failure", func(*store.StatsSnapshot) error { return errors.New("injected snapshot failure") }},
		{"invalid account timezone", func(s *store.StatsSnapshot) error { s.Timezone = "Not/A_Timezone"; return nil }},
		{"missing edition metadata", func(s *store.StatsSnapshot) error {
			sha := "missing"
			s.Sessions[0].EditionSHA = &sha
			return nil
		}},
		{"corrupt rollup day", func(s *store.StatsSnapshot) error {
			s.Rollups = []store.SessionRollup{{WorkID: f.work.ID, Day: "2024-02-30"}}
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := store.StatsSnapshot{
				Timezone: "UTC", Revision: 17, Works: []store.Work{f.work},
				Sessions: []store.Session{f.session("incoherent")},
			}
			failure := tc.edit(&snap)
			st := &snapshotReadStore{Store: f.st, read: func(context.Context, string, []string) (store.StatsSnapshot, error) {
				return snap, failure
			}}
			ts := snapshotTestServer(t, f, st, 0)
			code, out := post(t, ts.URL+"/v1/insights/snapshot", f.reader, snapshotBody())
			requireSnapshotError(t, code, out, http.StatusInternalServerError)
		})
	}
}

func TestInsightsSnapshotMismatchedArchivedProofCannotCertifyOverlap(t *testing.T) {
	f := newSnapshotFixture(t)
	ses := f.session("archived")
	for _, tc := range []struct {
		name string
		edit func(*store.ArchivedSession)
	}{
		{"payload", func(p *store.ArchivedSession) { p.Fingerprint = "different" }},
		{"legacy proof", func(p *store.ArchivedSession) { p.AttributionVersion = 0 }},
		{"timezone", func(p *store.ArchivedSession) { p.Timezone = "Europe/Paris" }},
		{"missing work", func(p *store.ArchivedSession) { p.WorkID = "gone" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proof := store.ArchivedSession{
				Fingerprint: store.SessionFingerprint(ses), WorkID: f.work.ID, Day: "2024-01-02",
				Timezone: "UTC", AttributionVersion: 2, Present: true, ActiveSeconds: 1800,
			}
			tc.edit(&proof)
			st := &snapshotReadStore{Store: f.st, read: func(context.Context, string, []string) (store.StatsSnapshot, error) {
				return store.StatsSnapshot{
					Timezone: "UTC", Revision: 11, Works: []store.Work{f.work},
					Rollups: []store.SessionRollup{{
						WorkID: f.work.ID, Day: "2024-01-02", Timezone: "UTC",
						AttributionVersion: 2, ActiveSeconds: 1800, SessionCount: 1,
					}},
					Archived: map[string]store.ArchivedSession{ses.SessionID: proof},
				}, nil
			}}
			ts := snapshotTestServer(t, f, st, 0)
			got := readSnapshot(t, ts.URL, f.reader, snapshotBody(ses))
			if got.Complete || got.IncompleteReason == "" || got.Overlap.Minutes != 0 ||
				got.Overlap.Sessions != 0 || got.Summary.TotalActiveMinutes != 30 {
				t.Errorf("unverifiable archived contribution must fall back: %+v", got)
			}
		})
	}
}

func TestInsightsSnapshotRawEvidenceRequiresOriginalDeviceAndFullPayload(t *testing.T) {
	f := newSnapshotFixture(t)
	ses := f.session("original")
	if err := f.st.AppendSessions(t.Context(), f.user.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"device", "device_id", "replacement-phone"},
		{"work", "work_id", "another-work"},
		{"edition", "edition_sha", "another-edition"},
		{"start", "started_at", ses.StartedAt.Add(-time.Minute).Format(time.RFC3339Nano)},
		{"end", "ended_at", ses.EndedAt.Add(time.Minute).Format(time.RFC3339Nano)},
		{"start progression", "start_progression", 0.05},
		{"end progression", "end_progression", 0.25},
		{"idle", "idle_ms", 1},
		{"absent active", "active_ms", nil},
		{"zero active", "active_ms", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := snapshotBody(ses)
			body["candidates"].([]map[string]any)[0][tc.field] = tc.value
			got := readSnapshot(t, f.ts.URL, f.reader, body)
			if got.Complete || got.IncompleteReason == "" || got.Overlap.Minutes != 0 ||
				got.Overlap.Sessions != 0 || got.Summary.TotalActiveMinutes != 30 ||
				got.Summary.Sessions != 1 {
				t.Errorf("partial evidence was trusted or changed server totals: %+v", got)
			}
		})
	}
}

func TestInsightsSnapshotEmptyAndCalendarLimitBoundaries(t *testing.T) {
	f := newSnapshotFixture(t)
	body := snapshotBody()
	delete(body, "calendar_from")
	delete(body, "calendar_to")
	code, out := post(t, f.ts.URL+"/v1/insights/snapshot", f.reader, body)
	if code != http.StatusOK || out["calendar_from"] != out["today"] || out["calendar_to"] != out["today"] ||
		out["first_activity_day"] != nil {
		t.Fatalf("empty snapshot's optional calendar: %d %v", code, out)
	}
	got := readSnapshot(t, f.ts.URL, f.reader, body)
	if !got.Complete || got.Summary.Sessions != 0 || got.Overlap.Sessions != 0 ||
		got.Works == nil || got.Days == nil || got.Overlap.Works == nil || got.Overlap.Days == nil {
		t.Errorf("empty successful snapshot must carry empty arrays: %+v", got)
	}
	start := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	body["calendar_from"] = start.Format(insights.DayFormat)
	body["calendar_to"] = start.AddDate(0, 0, 3999).Format(insights.DayFormat)
	readSnapshot(t, f.ts.URL, f.reader, body)
	body["calendar_to"] = start.AddDate(0, 0, 4000).Format(insights.DayFormat)
	code, out = post(t, f.ts.URL+"/v1/insights/snapshot", f.reader, body)
	requireSnapshotError(t, code, out, http.StatusBadRequest)
}

type capabilitiesReadStore struct {
	store.Store
	user store.User
	err  error
}

func (s *capabilitiesReadStore) UserByID(context.Context, string) (store.User, error) {
	return s.user, s.err
}

func TestInsightsSnapshotCapabilitiesReadFailuresAreExplicit(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, tc := range []struct {
		name string
		user store.User
		err  error
	}{
		{"account unavailable", store.User{}, errors.New("injected account read failure")},
		{"invalid account timezone", store.User{Timezone: "Not/A_Timezone"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &capabilitiesReadStore{Store: f.st, user: tc.user, err: tc.err}
			ts := snapshotTestServer(t, f, st, 0)
			code, out := get(t, ts.URL+"/v1/insights/capabilities", f.reader)
			requireSnapshotError(t, code, out, http.StatusInternalServerError)
			if _, ok := out["active_ms"]; ok {
				t.Errorf("failed account read advertised capabilities: %v", out)
			}
		})
	}
}

func TestInsightsSnapshotCapabilitiesAdvertiseConfiguredBodyLimit(t *testing.T) {
	f := newSnapshotFixture(t)
	ts := snapshotTestServer(t, f, f.st, 1024)
	code, caps := get(t, ts.URL+"/v1/insights/capabilities", f.reader)
	if code != http.StatusOK || caps["max_body_bytes"] != float64(1024) {
		t.Fatalf("capabilities must advertise the enforced configuration: %d %v", code, caps)
	}
}

func TestInsightsSnapshotTokenAdvertisesMeasuredSessionsWithoutInsightsScope(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, tc := range []struct {
		name, token string
		scope       store.Scope
	}{
		{"sync only", f.writer, store.ScopeSync},
		{"read-insights only", f.reader, store.ScopeReadInsights},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := get(t, f.ts.URL+"/v1/token", tc.token)
			if code != http.StatusOK || out["session_active_ms"] != true || out["account_id"] != f.user.ID {
				t.Fatalf("token must advertise measured-session support as a boolean: %d %v", code, out)
			}
			if !reflect.DeepEqual(out["scopes"], []any{string(tc.scope)}) {
				t.Errorf("advertising a wire capability must not expand token scopes: %v", out)
			}
		})
	}
	ses := f.session("browser-measured")
	code, out := post(t, f.ts.URL+"/v1/sessions", f.writer,
		map[string]any{"sessions": []map[string]any{snapshotCandidate(ses)}})
	if code != http.StatusOK {
		t.Fatalf("sync-only client cannot use advertised active_ms support: %d %v", code, out)
	}
	got := readSnapshot(t, f.ts.URL, f.reader, snapshotBody(ses))
	if !got.Complete || got.Summary.TotalActiveMinutes != 30 || got.Overlap.Minutes != 30 {
		t.Errorf("advertised measured session was not preserved: %+v", got)
	}
}

func TestInsightsSnapshotWireFixture(t *testing.T) {
	f := newSnapshotFixture(t)
	ses := f.session("wire-measured-30-minutes")
	ses.EndedAt = time.Now().UTC().Truncate(time.Second)
	ses.StartedAt = ses.EndedAt.Add(-10 * time.Minute)
	today := ses.EndedAt.Format(insights.DayFormat)
	code, out := post(t, f.ts.URL+"/v1/sessions", f.writer,
		map[string]any{"sessions": []map[string]any{snapshotCandidate(ses)}})
	if code != http.StatusOK {
		t.Fatalf("upload wire fixture session: %d %v", code, out)
	}
	body := snapshotBody(ses)
	body["calendar_from"], body["calendar_to"] = today, today
	request, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	capture := func(method, path string, body []byte) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, f.ts.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+f.reader)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: HTTP %d: %s", method, path, res.StatusCode, raw)
		}
		return raw
	}
	capabilities := capture(http.MethodGet, "/v1/insights/capabilities", nil)
	response := capture(http.MethodPost, "/v1/insights/snapshot", request)
	var caps map[string]any
	if err := json.Unmarshal(capabilities, &caps); err != nil {
		t.Fatal(err)
	}
	var got snapshotReply
	if err := json.Unmarshal(response, &got); err != nil {
		t.Fatal(err)
	}
	if caps["active_ms"] != true || caps["account_id"] != got.AccountID || caps["timezone"] != got.Timezone ||
		got.AccountID != f.user.ID || got.SnapshotID != "local-generation-7" || !got.Complete ||
		got.Summary.TotalActiveMinutes != 30 || got.Summary.Sessions != 1 ||
		got.Overlap.Minutes != 30 || got.Overlap.Sessions != 1 ||
		len(got.Works) != 1 || got.Works[0].TotalActiveMinutes != 30 ||
		len(got.Overlap.Works) != 1 || got.Overlap.Works[0].TotalActiveMinutes != 30 ||
		len(got.Days) != 1 || got.Days[0].Minutes != 30 || got.Days[0].Date != today ||
		len(got.Overlap.Days) != 1 || got.Overlap.Days[0].Minutes != 30 || got.Overlap.Days[0].Date != today {
		t.Fatalf("wire fixture must represent one exact measured overlap: capabilities=%s response=%s", capabilities, response)
	}
	// Opt-in artifacts carry the exact HTTP bodies for downstream client
	// parser tests, not a hand-written approximation of the Go wire format.
	if dir := os.Getenv("LISEUR_STATS_WIRE_DIR"); dir != "" {
		for _, artifact := range []struct {
			name string
			body []byte
		}{
			{"stats-wire-capabilities.json", capabilities},
			{"stats-wire-request.json", request},
			{"stats-wire-response.json", response},
		} {
			path := filepath.Join(dir, artifact.name)
			if err := os.WriteFile(path, artifact.body, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote actual wire fixture: %s", path)
		}
	}
}
