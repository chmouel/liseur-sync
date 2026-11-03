package storetest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/infer"
	"github.com/chmouel/liseur-sync/internal/store"
)

func testCrossPointSessions(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "measured")
	other := MkUser(t, s, "other")
	w := MkWork(t, s, u, "w1", "sha1")
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.CreateKosyncDevice(ctx, store.KosyncDevice{UserID: u.ID, DeviceSlot: "x3", KeySHA256: "key"}); err != nil {
		t.Fatal(err)
	}
	cutoff := now.Add(-4 * time.Hour)
	if got, err := s.EnableKosyncStatistics(ctx, u.ID, "x3", cutoff); err != nil || !got.Equal(cutoff) {
		t.Fatalf("enable: %v %v", got, err)
	}
	if got, err := s.EnableKosyncStatistics(ctx, u.ID, "x3", now); err != nil || !got.Equal(cutoff) {
		t.Fatalf("activation moved: %v %v", got, err)
	}
	if _, err := s.EnableKosyncStatistics(ctx, other.ID, "x3", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user enable: %v", err)
	}
	folder := MkFolder(t, s, "measured", store.FolderPlain)
	obs := store.ObservedBook{RelativePath: "one.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha1", PartialMD5: "md5-w1", Title: "One"}
	doReconcile(t, s, folder.ID, []store.ObservedBook{obs}, true, now)
	ses := store.Session{SessionID: "measured-1", StartedAt: now.Add(-time.Hour), EndedAt: now, StartProg: 0, EndProg: .1, ActiveMs: Ptr(int64(60000))}
	upload := func(document, id, hash, want string, session store.Session) {
		t.Helper()
		got, err := s.AppendKosyncSession(ctx, u.ID, "x3", document, id, hash, session)
		if err != nil || got != want {
			t.Fatalf("upload %s: %s %v", id, got, err)
		}
	}
	upload("md5-w1", "e1", "hash1", "stored", ses)
	raw, err := s.SessionsInRange(ctx, u.ID, now.Add(-2*time.Hour), now.Add(time.Hour))
	if err != nil || len(raw) != 1 || raw[0].WorkID != w.ID || raw[0].EditionSHA == nil || *raw[0].EditionSHA != "sha1" {
		t.Fatalf("catalog edition: %+v %v", raw, err)
	}
	if err := s.SplitWork(ctx, u.ID, w.ID, "sha1", []store.Identifier{{Kind: "partial-md5", Value: "md5-w1"}}, store.Work{ID: "split", UserID: u.ID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	upload("md5-w1", "e1", "hash1", "stored", ses)
	if err := s.MergeWorks(ctx, u.ID, "split", w.ID); err != nil {
		t.Fatal(err)
	}
	upload("md5-w1", "e1", "hash1", "stored", ses)
	archived, err := s.SessionsEndedBefore(ctx, u.ID, now.Add(time.Hour), 0)
	if err != nil || len(archived) != 1 {
		t.Fatalf("archive input: %+v %v", archived, err)
	}
	if err := s.ApplyRollups(ctx, u.ID, []store.SessionRollup{{UserID: u.ID, WorkID: w.ID, Day: now.In(time.FixedZone("test", 3600)).Format("2006-01-02"), ActiveSeconds: 60, ProgDelta: .1, SessionCount: 1}}, archived); err != nil {
		t.Fatal(err)
	}
	// Proof must not depend on the current catalog, edition, or live session row.
	if err := s.UnassignUserFolder(ctx, u.ID, folder.ID); err != nil {
		t.Fatal(err)
	}
	upload("md5-w1", "e1", "hash1", "stored", ses)
	if _, err := s.AppendKosyncSession(ctx, u.ID, "x3", "md5-w1", "e1", "changed", ses); !errors.Is(err, store.ErrIDMismatch) {
		t.Fatalf("changed archived event: %v", err)
	}
	raw, err = s.SessionsInRange(ctx, u.ID, now.Add(-2*time.Hour), now.Add(time.Hour))
	if err != nil || len(raw) != 0 {
		t.Fatalf("replay resurrected session: %+v %v", raw, err)
	}
	ses.SessionID = "without-grant"
	upload("md5-w1", "e2", "hash2", "stored", ses)
	if err := s.AssignUserFolder(ctx, u.ID, folder.ID); err != nil {
		t.Fatal(err)
	}
	clash := obs
	clash.RelativePath = "two.epub"
	clash.ContentSHA256 = "sha2"
	doReconcile(t, s, folder.ID, []store.ObservedBook{obs, clash}, true, now)
	ses.SessionID = "collision"
	upload("md5-w1", "e3", "hash3", "stored", ses)
	ses.SessionID = "unknown"
	upload("unknown-md5", "e4", "hash4", "stored", ses)
	raw, err = s.SessionsInRange(ctx, u.ID, now.Add(-2*time.Hour), now.Add(time.Hour))
	if err != nil || len(raw) != 3 {
		t.Fatalf("sessions: %+v %v", raw, err)
	}
	for _, session := range raw {
		if session.EditionSHA != nil {
			t.Fatalf("unproven edition: %+v", session)
		}
	}
	pending, err := s.WorkIDByAlias(ctx, u.ID, "partial-md5", "unknown-md5")
	if err != nil {
		t.Fatal(err)
	}
	if work, err := s.WorkByID(ctx, u.ID, pending); err != nil || !work.Pending {
		t.Fatalf("pending work: %+v %v", work, err)
	}
	old := ses
	old.SessionID = "ignored"
	old.StartedAt = cutoff.Add(-time.Second)
	upload("before-cutoff", "old", "oldhash", "ignored", old)
	if _, err := s.WorkIDByAlias(ctx, u.ID, "partial-md5", "before-cutoff"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ignored event created work: %v", err)
	}
	if _, err := s.AppendKosyncSession(ctx, other.ID, "x3", "md5-w1", "e1", "hash1", ses); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user receipt: %v", err)
	}
	if err := s.CreateKosyncDevice(ctx, store.KosyncDevice{UserID: other.ID, DeviceSlot: "x3", KeySHA256: "other-key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnableKosyncStatistics(ctx, other.ID, "x3", cutoff); err != nil {
		t.Fatal(err)
	}
	ses.SessionID = "measured-1"
	if status, err := s.AppendKosyncSession(ctx, other.ID, "x3", "md5-w1", "e1", "hash1", ses); err != nil || status != "stored" {
		t.Fatalf("other user's own event: %s %v", status, err)
	}
	otherSessions, err := s.SessionsInRange(ctx, other.ID, now.Add(-2*time.Hour), now.Add(time.Hour))
	if err != nil || len(otherSessions) != 1 || otherSessions[0].WorkID == w.ID || otherSessions[0].EditionSHA != nil {
		t.Fatalf("same slot crossed accounts: %+v %v", otherSessions, err)
	}

	if err := s.SetUserDisabled(ctx, other.ID, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendKosyncSession(ctx, other.ID, "x3", "md5-w1", "e1", "hash1", ses); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disabled account receipt: %v", err)
	}

	if err := s.RevokeKosyncDevice(ctx, u.ID, "x3"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendKosyncSession(ctx, u.ID, "x3", "md5-w1", "e1", "hash1", ses); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked upload: %v", err)
	}
}

func testCrossPointInferenceCutoff(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	u := MkUser(t, s, "cutoff")
	MkWork(t, s, u, "w1", "sha1")
	if err := s.CreateKosyncDevice(ctx, store.KosyncDevice{UserID: u.ID, DeviceSlot: "x3", KeySHA256: "key"}); err != nil {
		t.Fatal(err)
	}
	appendOp := func(i int) {
		t.Helper()
		if _, err := s.AppendKosyncOp(ctx, u.ID, "md5-w1", "kosync:x3", store.Op{OpID: fmt.Sprint(i), ClientTS: time.Now(), Progression: float64(i) / 10, Origin: store.OriginKosync}); err != nil {
			t.Fatal(err)
		}
	}
	appendOp(1)
	appendOp(2)
	cutoff := time.Now().UTC()
	appendOp(3)
	appendOp(4)
	appendOp(5)
	stale, err := s.PendingInferenceOps(ctx, u.ID)
	if err != nil || len(stale) != 5 {
		t.Fatalf("snapshot: %+v %v", stale, err)
	}
	if _, err := s.EnableKosyncStatistics(ctx, u.ID, "x3", cutoff); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendInferredSession(ctx, u.ID, store.InferredSessionGroup{Session: infer.Materialize(u.ID, stale), Ops: stale}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale inference committed: %v", err)
	}
	pending, err := s.PendingInferenceOps(ctx, u.ID)
	if err != nil || len(pending) != 2 || pending[0].OpID != "1" || pending[1].OpID != "2" {
		t.Fatalf("cutoff selection: %+v %v", pending, err)
	}
	horizon, err := s.Compact(ctx, u.ID, time.Now().Add(time.Hour))
	if err != nil || horizon != 4 {
		t.Fatalf("post-cutoff compaction: %d %v", horizon, err)
	}
	if err := s.AppendInferredSession(ctx, u.ID, store.InferredSessionGroup{Session: infer.Materialize(u.ID, pending), Ops: pending}); err != nil {
		t.Fatalf("historical inference lost: %v", err)
	}
}
