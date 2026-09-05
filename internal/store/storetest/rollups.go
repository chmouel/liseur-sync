package storetest

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// RollupsRejectStaleEditionPageCount needs a backend-specific metadata write:
// the public store API currently registers editions without updating them.
func RollupsRejectStaleEditionPageCount(t *testing.T, s store.Store, updatePages func(string, string, *int64) error) {
	ctx := t.Context()
	user := MkUser(t, s, "stale-edition")
	work := MkWork(t, s, user, "stale-edition-work", "stale-edition-sha")
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		old, fresh *int64
	}{
		{"changed-page-count", Ptr(int64(100)), Ptr(int64(200))},
		{"previously-unknown-page-count", nil, Ptr(int64(200))},
		{"now-unknown-page-count", Ptr(int64(100)), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := updatePages(user.ID, "stale-edition-sha", tc.old); err != nil {
				t.Fatal(err)
			}
			sessions := []store.Session{
				{
					SessionID: tc.name + "-native", WorkID: work.ID, EditionSHA: Ptr("stale-edition-sha"),
					DeviceID: "reader", StartedAt: now, EndedAt: now.Add(10 * time.Minute),
					StartProg: 0, EndProg: 0.25, ActiveMs: Ptr(int64(300000)), Origin: store.OriginNative,
				},
				{
					SessionID: tc.name + "-inferred", WorkID: work.ID, EditionSHA: Ptr("stale-edition-sha"),
					DeviceID: "reader", StartedAt: now, EndedAt: now.Add(10 * time.Minute),
					StartProg: 0.25, EndProg: 0.375, Origin: store.OriginInferred,
				},
				{
					SessionID: tc.name + "-reported", WorkID: work.ID, EditionSHA: Ptr("stale-edition-sha"),
					DeviceID: "reader", StartedAt: now, EndedAt: now.Add(time.Minute),
					StartProg: 0.375, EndProg: 0.625, ReportedPages: Ptr(12.5), Origin: store.OriginNative,
				},
			}
			if err := s.AppendSessions(ctx, user.ID, sessions); err != nil {
				t.Fatal(err)
			}
			ids := []string{sessions[0].SessionID, sessions[1].SessionID, sessions[2].SessionID}
			before, err := s.StatisticsSnapshot(ctx, user.ID, ids)
			if err != nil {
				t.Fatal(err)
			}
			edition := before.Editions["stale-edition-sha"]
			pages := 12.5
			if edition.PageCount != nil {
				pages += 0.375 * float64(*edition.PageCount)
			}
			rollup := store.SessionRollup{
				UserID: user.ID, WorkID: work.ID, Day: now.Format("2006-01-02"), Timezone: "UTC",
				AttributionVersion: 2, ActiveSeconds: 960, Pages: pages, ProgDelta: 0.625,
				SessionCount: 3, MeasuredActiveSeconds: 360, MeasuredProgDelta: 0.5,
			}
			if err := updatePages(user.ID, edition.SHA256, tc.fresh); err != nil {
				t.Fatal(err)
			}
			if err := s.ApplyRollups(ctx, user.ID, []store.SessionRollup{rollup}, sessions); !errors.Is(err, store.ErrConflict) {
				t.Fatalf("stale edition must refuse compaction, got %v", err)
			}
			after, err := s.StatisticsSnapshot(ctx, user.ID, ids)
			if err != nil {
				t.Fatal(err)
			}
			if len(after.Sessions) != 3 || !reflect.DeepEqual(after.Rollups, before.Rollups) || len(after.Archived) != 0 {
				t.Fatalf("refused compaction changed history: %+v", after)
			}

			rollup.Pages = 12.5
			if tc.fresh != nil {
				rollup.Pages += 0.375 * float64(*tc.fresh)
			}
			if err := s.ApplyRollups(ctx, user.ID, []store.SessionRollup{rollup}, sessions); err != nil {
				t.Fatalf("fresh metadata retry: %v", err)
			}
			after, err = s.StatisticsSnapshot(ctx, user.ID, ids)
			if err != nil {
				t.Fatal(err)
			}
			if len(after.Sessions) != 0 || len(after.Archived) != 3 || len(after.Rollups) != len(before.Rollups)+1 {
				t.Fatalf("successful retry lost archive proofs: %+v", after)
			}
			var proofs []store.ArchivedSession
			for _, ses := range sessions {
				proof := after.Archived[ses.SessionID]
				if !proof.Present || proof.Fingerprint != store.SessionFingerprint(ses) {
					t.Fatalf("invalid archive proof for %s: %+v", ses.SessionID, proof)
				}
				proofs = append(proofs, proof)
			}
			if err := store.ValidateRollupContributions([]store.SessionRollup{rollup}, proofs); err != nil {
				t.Fatalf("fresh bucket differs from archived contributions: %v", err)
			}
			got := after.Rollups[len(after.Rollups)-1]
			if got != rollup {
				t.Fatalf("fresh bucket: got %+v, want %+v", got, rollup)
			}
			now = now.Add(24 * time.Hour)
		})
	}
}

func testV2RollupsRejectMismatchedContributions(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	user := MkUser(t, s, "rollup-contributions")
	work := MkWork(t, s, user, "contribution-work", "contribution-sha")
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	ses := store.Session{
		SessionID: "contribution-session", WorkID: work.ID, DeviceID: "reader",
		StartedAt: now, EndedAt: now.Add(10001 * time.Microsecond), IdleMs: 1,
		StartProg: 0, EndProg: 0.25, ReportedPages: Ptr(12.5), Origin: store.OriginNative,
	}
	if err := s.AppendSessions(ctx, user.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	active := ses.EndedAt.Sub(ses.StartedAt).Seconds() - float64(ses.IdleMs)/1000
	rollup := store.SessionRollup{
		UserID: user.ID, WorkID: work.ID, Day: "2026-09-04", Timezone: "UTC", AttributionVersion: 2,
		ActiveSeconds: active, Pages: 12.5, ProgDelta: 0.25, SessionCount: 1,
		MeasuredActiveSeconds: active, MeasuredProgDelta: 0.25,
	}
	for _, tc := range []struct {
		name   string
		change func(*store.SessionRollup)
	}{
		{"active-seconds", func(r *store.SessionRollup) { r.ActiveSeconds++ }},
		{"pages", func(r *store.SessionRollup) { r.Pages++ }},
		{"progression", func(r *store.SessionRollup) { r.ProgDelta++ }},
		{"session-count", func(r *store.SessionRollup) { r.SessionCount++ }},
		{"measured-active-seconds", func(r *store.SessionRollup) { r.MeasuredActiveSeconds++ }},
		{"measured-progression", func(r *store.SessionRollup) { r.MeasuredProgDelta++ }},
		{"work", func(r *store.SessionRollup) { r.WorkID = "other-work" }},
		{"day", func(r *store.SessionRollup) { r.Day = "2026-09-03" }},
		{"nonfinite-pages", func(r *store.SessionRollup) { r.Pages = math.Inf(1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := rollup
			tc.change(&changed)
			if err := s.ApplyRollups(ctx, user.ID, []store.SessionRollup{changed}, []store.Session{ses}); !errors.Is(err, store.ErrConflict) {
				t.Fatalf("mismatched bucket must conflict, got %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		rollups []store.SessionRollup
	}{
		{"duplicate-bucket", []store.SessionRollup{rollup, rollup}},
		{"unbacked-bucket", []store.SessionRollup{rollup, {
			UserID: user.ID, WorkID: work.ID, Day: "2026-09-05", Timezone: "UTC",
			AttributionVersion: 2, SessionCount: 1,
		}}},
		{"unbacked-timezone", []store.SessionRollup{rollup, {
			UserID: user.ID, WorkID: work.ID, Day: rollup.Day, Timezone: "Europe/Paris",
			AttributionVersion: 2, SessionCount: 1,
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.ApplyRollups(ctx, user.ID, tc.rollups, []store.Session{ses}); !errors.Is(err, store.ErrConflict) {
				t.Fatalf("unbacked bucket must conflict, got %v", err)
			}
		})
	}
	before, err := s.StatisticsSnapshot(ctx, user.ID, []string{ses.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Sessions) != 1 || len(before.Rollups) != 0 || len(before.Archived) != 0 {
		t.Fatalf("refused compaction changed history: %+v", before)
	}
	if err := s.ApplyRollups(ctx, user.ID, []store.SessionRollup{rollup}, []store.Session{ses}); err != nil {
		t.Fatalf("matching contributions must compact: %v", err)
	}
}
