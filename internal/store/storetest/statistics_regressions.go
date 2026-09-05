package storetest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func testSessionRangeAndCompactionReadsPreserveOptionalMeasurements(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	user := MkUser(t, s, "session-measurements")
	work := MkWork(t, s, user, "measured-work", "measured-sha")
	start := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		active *int64
		pages  *float64
	}{
		{name: "legacy-null"},
		{name: "explicit-zero", active: Ptr(int64(0)), pages: Ptr(0.0)},
		{name: "active-only", active: Ptr(int64(123456))},
		{name: "pages-only", pages: Ptr(12.5)},
		{name: "both-measured", active: Ptr(int64(234567)), pages: Ptr(23.75)},
	}
	var sessions []store.Session
	for i, tc := range cases {
		at := start.Add(time.Duration(i) * time.Hour)
		sessions = append(sessions, store.Session{
			SessionID: tc.name, WorkID: work.ID, EditionSHA: Ptr("measured-sha"), DeviceID: "reader",
			StartedAt: at, EndedAt: at.Add(10 * time.Minute), StartProg: 0.25, EndProg: 0.5,
			IdleMs: 1000, ActiveMs: tc.active, ReportedPages: tc.pages,
			Origin: store.OriginNative, OriginAlias: Ptr("sha256:measured-sha"),
		})
	}
	if err := s.AppendSessions(ctx, user.ID, sessions); err != nil {
		t.Fatal(err)
	}
	until := start.Add(24 * time.Hour)
	for _, read := range []struct {
		name string
		run  func() ([]store.Session, error)
	}{
		{"SessionsInRange", func() ([]store.Session, error) {
			return s.SessionsInRange(ctx, user.ID, start, until)
		}},
		{"SessionsEndedBefore", func() ([]store.Session, error) {
			return s.SessionsEndedBefore(ctx, user.ID, until)
		}},
	} {
		t.Run(read.name, func(t *testing.T) {
			got, err := read.run()
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(sessions) {
				t.Fatalf("want %d sessions, got %d", len(sessions), len(got))
			}
			for i, want := range sessions {
				if !reflect.DeepEqual(got[i].ActiveMs, want.ActiveMs) ||
					!reflect.DeepEqual(got[i].ReportedPages, want.ReportedPages) ||
					store.SessionFingerprint(got[i]) != store.SessionFingerprint(want) {
					t.Errorf("%s: session payload changed: got %+v, want %+v", want.SessionID, got[i], want)
				}
				if got[i].UserID != user.ID || got[i].ReceivedAt.IsZero() {
					t.Errorf("%s: missing stored session metadata: %+v", want.SessionID, got[i])
				}
			}
		})
	}
}

func testReconcileCalibrePreservesV2RollupHistoryAndArchiveProof(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	user := MkUser(t, s, "calibre-v2-history")
	folder := MkFolder(t, s, "calibre-v2-history", store.FolderCalibre)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	observed := []store.ObservedBook{{
		CalibreID: Ptr(int64(1)), RelativePath: "Kept (1)/book.epub", SizeBytes: 1,
		MTime: now, ContentSHA256: "kept-sha", Title: "Kept",
	}}
	doReconcile(t, s, folder.ID, observed, true, now)
	work := MkWork(t, s, user, "unmapped-history", "history-sha")
	ses := store.Session{
		SessionID: "archived-history", WorkID: work.ID, EditionSHA: Ptr("history-sha"), DeviceID: "reader",
		StartedAt: now, EndedAt: now.Add(10 * time.Minute), StartProg: 0.25, EndProg: 0.5,
		ActiveMs: Ptr(int64(300000)), ReportedPages: Ptr(12.5), Origin: store.OriginNative,
	}
	if err := s.AppendSessions(ctx, user.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	doReconcile(t, s, folder.ID, observed, true, now.Add(time.Hour))
	if _, err := s.WorkByID(ctx, user.ID, work.ID); err != nil {
		t.Fatalf("unmapped work with raw history was collected: %v", err)
	}
	rollup := store.SessionRollup{
		UserID: user.ID, WorkID: work.ID, Day: "2026-09-04", Timezone: "Europe/Paris",
		AttributionVersion: 2, ActiveSeconds: 300, Pages: 12.5, ProgDelta: 0.25,
		SessionCount: 1, MeasuredActiveSeconds: 300, MeasuredProgDelta: 0.25,
	}
	if err := s.ApplyRollups(ctx, user.ID, []store.SessionRollup{rollup}, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	wantProof := store.ArchivedSession{
		Fingerprint: store.SessionFingerprint(ses), WorkID: work.ID, Day: rollup.Day,
		Timezone: rollup.Timezone, AttributionVersion: 2, Present: true,
		ActiveSeconds: 300, Pages: 12.5, ProgDelta: 0.25,
		MeasuredActiveSeconds: 300, MeasuredProgDelta: 0.25,
	}
	assertHistory := func() {
		t.Helper()
		snap, err := s.StatisticsSnapshot(ctx, user.ID, []string{ses.SessionID})
		if err != nil {
			t.Fatal(err)
		}
		if len(snap.Sessions) != 0 || len(snap.Rollups) != 1 || snap.Rollups[0] != rollup {
			t.Fatalf("want only the intact v2 rollup, got raw=%+v rollups=%+v", snap.Sessions, snap.Rollups)
		}
		if proof, ok := snap.Archived[ses.SessionID]; !ok || proof != wantProof {
			t.Fatalf("archive proof changed: got %+v (exists=%v), want %+v", proof, ok, wantProof)
		}
		if _, err := s.WorkByID(ctx, user.ID, work.ID); err != nil {
			t.Fatalf("unmapped work with v2 history was collected: %v", err)
		}
	}
	assertHistory()

	// The guard must match both the reader and the work, not protect all
	// empty works whenever any reader has a v2 rollup.
	empty := MkWork(t, s, user, "empty-work", "empty-sha")
	other := MkUser(t, s, "calibre-v2-other-reader")
	MkWork(t, s, other, work.ID, "other-sha")
	result := doReconcile(t, s, folder.ID, observed, true, now.Add(2*time.Hour))
	if result.Purged != 0 {
		t.Fatalf("cleanup-only pass unexpectedly purged a book: %+v", result)
	}
	for _, orphan := range []struct{ userID, workID string }{
		{user.ID, empty.ID}, {other.ID, work.ID},
	} {
		if _, err := s.WorkByID(ctx, orphan.userID, orphan.workID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("empty work %s/%s was not collected: %v", orphan.userID, orphan.workID, err)
		}
	}
	assertHistory()
	if err := s.AppendSessions(ctx, user.ID, []store.Session{ses}); err != nil {
		t.Fatalf("archived replay after reconciliation: %v", err)
	}
	assertHistory()
}
