package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func testStatisticsStorage(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "stats")
	w := MkWork(t, s, u, "w-stats", "stats-sha")
	start := time.Date(2026, 9, 4, 22, 30, 0, 0, time.UTC)
	active := int64(30 * 60 * 1000)
	reported := 12.5
	ses := store.Session{
		SessionID: "stats-s1", WorkID: w.ID, EditionSHA: Ptr("stats-sha"), DeviceID: "d1",
		StartedAt: start, EndedAt: start.Add(10 * time.Minute), StartProg: 0.10, EndProg: 0.20,
		IdleMs: 1000, ActiveMs: &active, ReportedPages: &reported, Origin: store.OriginNative,
	}
	legacy := ses
	legacy.SessionID = "stats-legacy"
	legacy.ActiveMs = nil
	legacy.ReportedPages = nil
	if store.SessionFingerprint(legacy) != legacyFingerprint(legacy) {
		t.Fatalf("legacy fingerprint bytes changed")
	}
	withoutReported := ses
	withoutReported.ReportedPages = nil
	if store.SessionFingerprint(ses) == store.SessionFingerprint(withoutReported) || store.SessionFingerprint(ses) == store.SessionFingerprint(legacy) {
		t.Fatalf("optional session fields are not part of the v2 fingerprint")
	}

	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatalf("v2 duplicate was not idempotent: %v", err)
	}
	changed := ses
	changed.ReportedPages = Ptr(13.0)
	if err := s.AppendSessions(ctx, u.ID, []store.Session{changed}); !errors.Is(err, store.ErrIDMismatch) {
		t.Fatalf("changed optional metadata: want ErrIDMismatch, got %v", err)
	}

	snap, err := s.StatisticsSnapshot(ctx, u.ID, []string{"stats-s1"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Timezone != u.Timezone || snap.Revision == 0 || len(snap.Sessions) != 1 {
		t.Fatalf("bad initial snapshot: %+v", snap)
	}
	if snap.Sessions[0].ActiveMs == nil || *snap.Sessions[0].ActiveMs != active || snap.Sessions[0].ReportedPages == nil || *snap.Sessions[0].ReportedPages != reported {
		t.Fatalf("snapshot lost optional session fields: %+v", snap.Sessions[0])
	}
	if _, ok := snap.Editions["stats-sha"]; !ok {
		t.Fatalf("snapshot did not batch editions: %+v", snap.Editions)
	}

	day := ses.EndedAt.In(time.FixedZone("CEST", 2*60*60)).Format("2006-01-02")
	ru := store.SessionRollup{
		UserID: u.ID, WorkID: w.ID, Day: day, Timezone: "Europe/Paris", AttributionVersion: 2,
		ActiveSeconds: 1800, Pages: reported, ProgDelta: 0.10, SessionCount: 1,
		MeasuredActiveSeconds: 1800, MeasuredProgDelta: 0.10,
	}
	if err := s.ApplyRollups(ctx, u.ID, []store.SessionRollup{ru}, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	snap, err = s.StatisticsSnapshot(ctx, u.ID, []string{"stats-s1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sessions) != 0 || len(snap.Rollups) != 1 || snap.Rollups[0].AttributionVersion != 2 || snap.Rollups[0].Timezone != "Europe/Paris" {
		t.Fatalf("snapshot did not expose v2 rollup only: %+v", snap)
	}
	arch, ok := snap.Archived["stats-s1"]
	if !ok || !arch.Present || arch.Fingerprint != store.SessionFingerprint(ses) || arch.WorkID != w.ID || arch.Day != day || arch.Timezone != "Europe/Paris" || arch.AttributionVersion != 2 {
		t.Fatalf("missing archived proof: %#v present=%v", arch, ok)
	}
	if arch.ActiveSeconds != 1800 || arch.Pages != reported || arch.ProgDelta != 0.10 || arch.MeasuredActiveSeconds != 1800 || arch.MeasuredProgDelta != 0.10 {
		t.Fatalf("bad archived contribution: %#v", arch)
	}
	ids, err := s.WorkIDsWithInsights(ctx, u.ID)
	if err != nil || len(ids) != 1 || ids[0] != w.ID {
		t.Fatalf("v2 work insight ids: %+v %v", ids, err)
	}
	if err := s.SplitWork(ctx, u.ID, w.ID, "stats-sha", nil, store.Work{ID: "w-split", UserID: u.ID, CreatedAt: time.Now()}); err != store.ErrConflict {
		t.Fatalf("split with v2 rollup: want ErrConflict, got %v", err)
	}
	if err := s.DeleteWork(ctx, u.ID, w.ID); err != nil {
		t.Fatal(err)
	}
	snap, err = s.StatisticsSnapshot(ctx, u.ID, []string{"stats-s1"})
	if err != nil {
		t.Fatal(err)
	}
	if arch := snap.Archived["stats-s1"]; arch.Present || arch.Fingerprint != store.SessionFingerprint(ses) || arch.WorkID != w.ID {
		t.Fatalf("work deletion did not retain absent proof: %#v", arch)
	}
}

func legacyFingerprint(s store.Session) string {
	edition, alias, source := "", "", ""
	if s.EditionSHA != nil {
		edition = *s.EditionSHA
	}
	if s.OriginAlias != nil {
		alias = *s.OriginAlias
	}
	if s.SourceKey != nil {
		source = *s.SourceKey
	}
	if s.Origin == store.OriginInferred {
		alias = ""
		source = ""
	}
	raw := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%.17g\x00%.17g\x00%d\x00%s\x00%s\x00%s",
		s.WorkID, edition, s.DeviceID, s.StartedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano),
		s.EndedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano), s.SessionID, s.StartProg, s.EndProg,
		s.IdleMs, s.Origin, alias, source)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
