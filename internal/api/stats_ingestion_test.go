package api

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/insights"
	"github.com/chmouel/liseur-sync/internal/store"
)

type countingStatsStore struct {
	store.Store
	statisticsSnapshotCalls int
}

func (c *countingStatsStore) StatisticsSnapshot(ctx context.Context, userID string, candidateIDs []string) (store.StatsSnapshot, error) {
	c.statisticsSnapshotCalls++
	return c.Store.StatisticsSnapshot(ctx, userID, candidateIDs)
}

func TestNativeSessionActiveMsOptionalZeroAndBounded(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "u1", Name: "alice", Argon2Hash: hash, Timezone: "UTC", CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	secret, _, err := auth.NewService(st).MintToken(ctx, u.ID, "phone", store.ScopeSet{store.ScopeSync}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := store.Work{ID: "w1", UserID: u.ID, CreatedAt: time.Now()}
	if err := st.CreateWork(ctx, w, nil, nil); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	session := func(id string, active any) map[string]any {
		m := map[string]any{
			"session_id": id, "work_id": w.ID,
			"started_at":        start.Format(time.RFC3339Nano),
			"ended_at":          start.Add(time.Minute).Format(time.RFC3339Nano),
			"start_progression": 0.1, "end_progression": 0.2,
		}
		if active != nil {
			m["active_ms"] = active
		}
		return m
	}
	code, out := post(t, ts.URL+"/v1/sessions", secret, map[string]any{"sessions": []map[string]any{
		session("absent", nil), session("zero", 0),
	}})
	if code != 200 {
		t.Fatalf("push: %d %v", code, out)
	}
	got, err := st.SessionsForWork(ctx, u.ID, w.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]store.Session{}
	for _, ses := range got {
		byID[ses.SessionID] = ses
	}
	if byID["absent"].ActiveMs != nil {
		t.Fatalf("absent active_ms became present: %+v", byID["absent"].ActiveMs)
	}
	if byID["zero"].ActiveMs == nil || *byID["zero"].ActiveMs != 0 {
		t.Fatalf("zero active_ms not preserved: %+v", byID["zero"].ActiveMs)
	}

	if code, out := post(t, ts.URL+"/v1/sessions", secret, map[string]any{"sessions": []map[string]any{
		session("too-active", MaxSessionActiveMs+1),
	}}); code != 400 || out["code"] != errCodeActiveOutOfRange || out["session_id"] != "too-active" {
		t.Fatalf("active_ms bound: %d %v", code, out)
	}
}

func TestParseSessionRequestRejectsPositiveNonfiniteProgression(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sp, ep := 0.1, math.Inf(1)
	_, err := parseSessionRequest(sessionReqJSON{
		SessionID: "s-inf", WorkID: "w1",
		StartedAt: start.Format(time.RFC3339Nano), EndedAt: start.Add(time.Minute).Format(time.RFC3339Nano),
		StartProg: &sp, EndProg: &ep,
	}, "device")
	if reqErr, ok := err.(sessionRequestError); !ok || reqErr.code != errCodeProgression {
		t.Fatalf("want progression refusal, got %#v", err)
	}
}

func TestMaterializerUsesExplicitActiveAndEndDayV2Archive(t *testing.T) {
	_, st := testServer(t)
	ctx := t.Context()
	u := store.User{ID: "rollup-active", Name: "reader", Argon2Hash: "x", Timezone: "Europe/Paris", CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	w := store.Work{ID: "w-active", UserID: u.ID, CreatedAt: time.Now()}
	ed := &store.Edition{UserID: u.ID, SHA256: "ed-active", WorkID: w.ID, PageCount: ptrI64(100)}
	if err := st.CreateWork(ctx, w, ed, nil); err != nil {
		t.Fatal(err)
	}

	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	active := int64(2 * time.Hour / time.Millisecond)
	zero := int64(0)
	crossStart := time.Date(2024, 1, 1, 23, 50, 0, 0, paris)
	zeroAt := time.Date(2024, 1, 2, 1, 0, 0, 0, paris)
	sessions := []store.Session{
		{
			SessionID: "measured-cross", WorkID: w.ID, EditionSHA: &ed.SHA256, DeviceID: "phone",
			StartedAt: crossStart, EndedAt: crossStart.Add(20 * time.Minute),
			StartProg: 0.1, EndProg: 0.2, ActiveMs: &active, Origin: store.OriginNative,
		},
		{
			SessionID: "zero-wall", WorkID: w.ID, EditionSHA: &ed.SHA256, DeviceID: "phone",
			StartedAt: zeroAt, EndedAt: zeroAt,
			StartProg: 0.2, EndProg: 0.3, ActiveMs: &zero, Origin: store.OriginNative,
		},
	}
	if err := st.AppendSessions(ctx, u.ID, sessions); err != nil {
		t.Fatal(err)
	}

	(&Server{St: st}).rollupSessionsOnce(ctx, 24*time.Hour)
	rollups, err := st.RollupsForWork(ctx, u.ID, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rollups) != 1 {
		t.Fatalf("want one end-day rollup, got %+v", rollups)
	}
	ru := rollups[0]
	if ru.Day != "2024-01-02" || ru.Timezone != "Europe/Paris" || ru.AttributionVersion != 2 {
		t.Fatalf("wrong v2 end day: %+v", ru)
	}
	if ru.SessionCount != 2 || ru.ActiveSeconds != 7200 || ru.MeasuredActiveSeconds != 7200 {
		t.Fatalf("wrong active/count: %+v", ru)
	}
	if math.Abs(ru.ProgDelta-0.2) > 0.000001 || math.Abs(ru.MeasuredProgDelta-0.2) > 0.000001 {
		t.Fatalf("wrong measured progression: %+v", ru)
	}
	if math.Abs(ru.Pages-20) > 0.000001 {
		t.Fatalf("wrong pages: %+v", ru)
	}

	snap, err := st.StatisticsSnapshot(ctx, u.ID, []string{"zero-wall", "measured-cross"})
	if err != nil {
		t.Fatal(err)
	}
	arch, ok := snap.Archived["zero-wall"]
	if !ok || !arch.Present || arch.Day != "2024-01-02" || arch.AttributionVersion != 2 {
		t.Fatalf("zero-wall archive: %+v present=%v", arch, ok)
	}
	if arch.MeasuredActiveSeconds != 0 || math.Abs(arch.MeasuredProgDelta-0.1) > 0.000001 {
		t.Fatalf("zero-wall measured archive: %+v", arch)
	}
	if got := insights.ActiveSeconds(sessions[0]); got != 7200 {
		t.Fatalf("explicit active should exceed wall without cap, got %v", got)
	}
}

func TestRollupDoesNotLoadFullStatisticsSnapshot(t *testing.T) {
	_, st := testServer(t)
	wrapped := &countingStatsStore{Store: st}
	ctx := t.Context()
	u := store.User{ID: "rollup-nosnap", Name: "reader", Argon2Hash: "x", Timezone: "UTC", CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	w := store.Work{ID: "w-nosnap", UserID: u.ID, CreatedAt: time.Now()}
	ed := &store.Edition{UserID: u.ID, SHA256: "ed-nosnap", WorkID: w.ID, PageCount: ptrI64(100)}
	if err := st.CreateWork(ctx, w, ed, nil); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	ses := store.Session{
		SessionID: "nosnap-session", WorkID: w.ID, EditionSHA: &ed.SHA256, DeviceID: "phone",
		StartedAt: start, EndedAt: start.Add(30 * time.Minute),
		StartProg: 0.1, EndProg: 0.2, Origin: store.OriginNative,
	}
	if err := st.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	wrapped.statisticsSnapshotCalls = 0
	(&Server{St: wrapped}).rollupSessionsOnce(ctx, 24*time.Hour)
	if wrapped.statisticsSnapshotCalls != 0 {
		t.Fatalf("rollup loaded full snapshot %d times", wrapped.statisticsSnapshotCalls)
	}
	rollups, err := st.RollupsForWork(ctx, u.ID, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rollups) != 1 || rollups[0].SessionCount != 1 {
		t.Fatalf("rollup did not archive session: %+v", rollups)
	}
	// A call count alone would pass against a path that computed the
	// wrong totals, so assert the rollup the narrow reads produced.
	ru := rollups[0]
	if ru.Day != "2024-01-01" || ru.Timezone != "UTC" || ru.AttributionVersion != 2 {
		t.Fatalf("wrong day attribution: %+v", ru)
	}
	if ru.ActiveSeconds != 1800 || ru.MeasuredActiveSeconds != 1800 {
		t.Fatalf("wrong active seconds: %+v", ru)
	}
	if math.Abs(ru.ProgDelta-0.1) > 0.000001 || math.Abs(ru.Pages-10) > 0.000001 {
		t.Fatalf("wrong progression or pages: %+v", ru)
	}
	// The raw session is gone, and its proof remains.
	left, err := st.SessionsEndedBefore(ctx, u.ID, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("rolled-up session was not deleted: %+v", left)
	}
	snap, err := st.StatisticsSnapshot(ctx, u.ID, []string{"nosnap-session"})
	if err != nil {
		t.Fatal(err)
	}
	if arch, ok := snap.Archived["nosnap-session"]; !ok || !arch.Present {
		t.Fatalf("archive proof missing: %+v present=%v", arch, ok)
	}
}

// TestRollupPagesEveryBatchAndKeepsReportedPages drives more sittings
// than one batch holds, so the paging loop has to come back for the
// rest, and mixes a sitting that reports its own page count with ones
// counted from their edition.
func TestRollupPagesEveryBatchAndKeepsReportedPages(t *testing.T) {
	_, st := testServer(t)
	ctx := t.Context()
	u := store.User{ID: "rollup-batches", Name: "reader", Argon2Hash: "x", Timezone: "UTC", CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	w := store.Work{ID: "w-batches", UserID: u.ID, CreatedAt: time.Now()}
	ed := &store.Edition{UserID: u.ID, SHA256: "ed-batches", WorkID: w.ID, PageCount: ptrI64(100)}
	if err := st.CreateWork(ctx, w, ed, nil); err != nil {
		t.Fatal(err)
	}
	// One more than a batch, all on the same day, so the work/day bucket
	// straddles the boundary and must accumulate across both passes.
	const total = rollupBatchSize + 1
	day := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	sessions := make([]store.Session, 0, total)
	for i := range total {
		startedAt := day.Add(time.Duration(i) * time.Second)
		ses := store.Session{
			SessionID: fmt.Sprintf("batch-%04d", i), WorkID: w.ID, EditionSHA: &ed.SHA256,
			DeviceID: "phone", StartedAt: startedAt, EndedAt: startedAt.Add(time.Minute),
			StartProg: 0, EndProg: 0.01, Origin: store.OriginNative,
		}
		if i == total-1 {
			// koplugin names its own page count; the edition is never read.
			ses.ReportedPages = ptrF64(7)
		}
		sessions = append(sessions, ses)
	}
	if err := st.AppendSessions(ctx, u.ID, sessions); err != nil {
		t.Fatal(err)
	}
	(&Server{St: st}).rollupSessionsOnce(ctx, 24*time.Hour)

	left, err := st.SessionsEndedBefore(ctx, u.ID, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("paging stopped early, %d sittings left", len(left))
	}
	rollups, err := st.RollupsForWork(ctx, u.ID, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rollups) != 1 {
		t.Fatalf("one work/day bucket expected across both batches: %+v", rollups)
	}
	ru := rollups[0]
	if ru.SessionCount != total {
		t.Fatalf("batches did not accumulate: got %d, want %d", ru.SessionCount, total)
	}
	if ru.ActiveSeconds != float64(total)*60 {
		t.Fatalf("wrong active seconds across batches: %+v", ru)
	}
	// rollupBatchSize sittings of 1% of a 100-page book, plus the one
	// that reported 7 pages of its own.
	wantPages := float64(rollupBatchSize)*0.01*100 + 7
	if math.Abs(ru.Pages-wantPages) > 0.000001 {
		t.Fatalf("wrong pages: got %v, want %v", ru.Pages, wantPages)
	}
}

// TestRollupDefersWhenTheAccountZoneMoves proves a batch built against
// one zone is kept rather than filed under the wrong day.
func TestRollupDefersWhenTheAccountZoneMoves(t *testing.T) {
	_, st := testServer(t)
	ctx := t.Context()
	u := store.User{ID: "rollup-tz", Name: "reader", Argon2Hash: "x", Timezone: "Europe/Paris", CreatedAt: time.Now()}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	w := store.Work{ID: "w-tz", UserID: u.ID, CreatedAt: time.Now()}
	ed := &store.Edition{UserID: u.ID, SHA256: "ed-tz", WorkID: w.ID, PageCount: ptrI64(100)}
	if err := st.CreateWork(ctx, w, ed, nil); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	ses := store.Session{
		SessionID: "tz-session", WorkID: w.ID, EditionSHA: &ed.SHA256, DeviceID: "phone",
		StartedAt: start, EndedAt: start.Add(30 * time.Minute),
		StartProg: 0.1, EndProg: 0.2, Origin: store.OriginNative,
	}
	if err := st.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	// The zone the materializer reads is the one ApplyRollups checks, so
	// drive the drift from a store that answers the read with a stale one.
	(&Server{St: &staleZoneStore{Store: st, zone: "America/New_York"}}).
		rollupSessionsOnce(ctx, 24*time.Hour)

	left, err := st.SessionsEndedBefore(ctx, u.ID, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Fatalf("a deferred batch must keep its sittings, got %d", len(left))
	}
	rollups, err := st.RollupsForWork(ctx, u.ID, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rollups) != 0 {
		t.Fatalf("a deferred batch must file nothing: %+v", rollups)
	}
}

// staleZoneStore answers UserByID with a zone the account no longer
// keeps, which is what a timezone change mid-pass looks like.
type staleZoneStore struct {
	store.Store
	zone string
}

func (s *staleZoneStore) UserByID(ctx context.Context, userID string) (store.User, error) {
	u, err := s.Store.UserByID(ctx, userID)
	if err != nil {
		return u, err
	}
	u.Timezone = s.zone
	return u, nil
}
