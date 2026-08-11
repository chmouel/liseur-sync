// Package storetest runs the shared store test suite against any
// backend. Both SQLite and PostgreSQL must pass identical behavior.
package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// OpenFunc returns a migrated, empty store for one test.
type OpenFunc func(t *testing.T) store.Store

// Run executes the full suite.
func Run(t *testing.T, open OpenFunc) {
	t.Run("Users", func(t *testing.T) { testUsers(t, open) })
	t.Run("Tokens", func(t *testing.T) { testTokens(t, open) })
	t.Run("ResolveAliases", func(t *testing.T) { testResolveAliases(t, open) })
	t.Run("AppendOpsIdempotencyAndConflict", func(t *testing.T) { testAppendOps(t, open) })
	t.Run("ChangesPaginationAndHeads", func(t *testing.T) { testChangesAndHeads(t, open) })
	t.Run("SplitAndMerge", func(t *testing.T) { testSplitAndMerge(t, open) })
	t.Run("SessionsAppendOnly", func(t *testing.T) { testSessionsAppendOnly(t, open) })
	t.Run("SessionRollups", func(t *testing.T) { testSessionRollups(t, open) })
	t.Run("Housekeeping", func(t *testing.T) { testHousekeeping(t, open) })
	t.Run("ConcurrentAppendGapFreeSeq", func(t *testing.T) { testConcurrentAppend(t, open) })
	t.Run("PairingCodeSingleUse", func(t *testing.T) { testPairingRedeem(t, open) })
	t.Run("KopluginSupersession", func(t *testing.T) { testKopluginUpsert(t, open) })
}

func Ptr[T any](v T) *T { return &v }

func MkUser(t *testing.T, s store.Store, name string) store.User {
	t.Helper()
	u := store.User{
		ID:              "u-" + name,
		Name:            name,
		Argon2Hash:      "x",
		Timezone:        "Europe/Paris",
		KosyncEnabled:   true,
		KopluginEnabled: true,
		CreatedAt:       time.Now(),
	}
	if err := s.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func MkWork(t *testing.T, s store.Store, u store.User, id, sha string) store.Work {
	t.Helper()
	w := store.Work{ID: id, UserID: u.ID, Title: "A Memory Called Empire", Author: "Martine", CreatedAt: time.Now()}
	e := &store.Edition{UserID: u.ID, SHA256: sha, WorkID: id, PageCount: Ptr(int64(462))}
	ids := []store.Identifier{
		{Kind: "sha256", Value: sha},
		{Kind: "partial-md5", Value: "md5-" + id},
		{Kind: "dc", Value: "urn:isbn:9780316419568-" + id},
	}
	if err := s.CreateWork(context.Background(), w, e, ids); err != nil {
		t.Fatal(err)
	}
	return w
}

func testUsers(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "alice")

	got, err := s.UserByName(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID || got.Timezone != "Europe/Paris" || !got.KosyncEnabled {
		t.Fatalf("bad user: %+v", got)
	}
	if _, err := s.UserByName(ctx, "nobody"); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.CreateUser(ctx, u); err != store.ErrConflict {
		t.Fatalf("duplicate user: want ErrConflict, got %v", err)
	}

	// Settings update.
	if err := s.UpdateUserSettings(ctx, u.ID, "Asia/Tokyo", false, true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.UserByID(ctx, u.ID)
	if got.Timezone != "Asia/Tokyo" || got.KosyncEnabled || !got.KopluginEnabled {
		t.Fatalf("settings not saved: %+v", got)
	}
}

func testTokens(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "bob")
	tok := store.Token{
		ID: "t1", UserID: u.ID, DeviceID: "d-boox", Name: "Boox Palma",
		Scope: store.ScopeSync, SHA256: "deadbeef", CreatedAt: time.Now(),
	}
	if err := s.CreateToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	got, err := s.TokenByHash(ctx, u.ID, "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != "d-boox" || got.Scope != store.ScopeSync {
		t.Fatalf("bad token: %+v", got)
	}
	if _, err := s.TokenByHash(ctx, "u-other", "deadbeef"); err != store.ErrNotFound {
		t.Fatalf("cross-user token read: want ErrNotFound, got %v", err)
	}
	if err := s.RevokeToken(ctx, u.ID, "t1"); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeToken(ctx, u.ID, "t1"); err != store.ErrNotFound {
		t.Fatalf("double revoke: want ErrNotFound, got %v", err)
	}
}

func testResolveAliases(t *testing.T, open OpenFunc) {
	s := open(t)
	u := MkUser(t, s, "carol")
	MkWork(t, s, u, "w1", "abc123")

	got, err := s.ResolveAliases(context.Background(), u.ID, []store.Identifier{
		{Kind: "partial-md5", Value: "md5-w1"},
		{Kind: "dc", Value: "urn:isbn:nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["partial-md5:md5-w1"] != "w1" {
		t.Fatalf("want w1, got %v", got)
	}
	if _, ok := got["dc:urn:isbn:nope"]; ok {
		t.Fatalf("unknown alias should be absent: %v", got)
	}
}

func testAppendOps(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "dave")
	w := MkWork(t, s, u, "w1", "abc123")

	op := store.Op{
		OpID: "018e6f1a-0000-7000-8000-000000000001", WorkID: w.ID,
		EditionSHA: Ptr("abc123"), ClientTS: time.Now(), Progression: 0.41,
		Origin: store.OriginNative,
	}
	res, err := s.AppendOps(ctx, u.ID, "d-phone", []store.Op{op})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != "applied" || res[0].Seq != 1 {
		t.Fatalf("want applied seq=1, got %+v", res[0])
	}

	res, err = s.AppendOps(ctx, u.ID, "d-phone", []store.Op{op})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != "duplicate" || res[0].Seq != 1 {
		t.Fatalf("want duplicate seq=1, got %+v", res[0])
	}

	op2 := op
	op2.Progression = 0.99
	res, err = s.AppendOps(ctx, u.ID, "d-phone", []store.Op{op2})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != "conflict" {
		t.Fatalf("want conflict, got %+v", res[0])
	}

	op3 := op
	op3.OpID = "018e6f1a-0000-7000-8000-000000000002"
	op4 := op
	op4.OpID = "018e6f1a-0000-7000-8000-000000000003"
	res, err = s.AppendOps(ctx, u.ID, "d-ereader", []store.Op{op3, op4})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Seq != 2 || res[1].Seq != 3 {
		t.Fatalf("want seq 2,3 got %+v", res)
	}
}

func testChangesAndHeads(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "erin")
	w := MkWork(t, s, u, "w1", "abc123")

	var ops []store.Op
	for i := 0; i < 5; i++ {
		ops = append(ops, store.Op{
			OpID:        "op-" + string(rune('a'+i)),
			WorkID:      w.ID,
			EditionSHA:  Ptr("abc123"),
			ClientTS:    time.Now(),
			Progression: float64(i) / 10,
			Origin:      store.OriginNative,
		})
	}
	if _, err := s.AppendOps(ctx, u.ID, "d1", ops); err != nil {
		t.Fatal(err)
	}

	page, err := s.Changes(ctx, u.ID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Ops) != 2 || !page.HasMore || page.HighWater != 5 || page.ResyncNeeded {
		t.Fatalf("bad page: %+v", page)
	}
	page, err = s.Changes(ctx, u.ID, page.Ops[len(page.Ops)-1].Seq, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Ops) != 3 || page.HasMore {
		t.Fatalf("bad page2: %+v", page)
	}

	heads, err := s.HeadsFor(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if heads.SnapshotSeq != 5 || len(heads.Ops) != 1 || heads.Ops[0].Seq != 5 {
		t.Fatalf("bad heads: %+v", heads)
	}
}

func testSplitAndMerge(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "fred")
	w := MkWork(t, s, u, "w1", "abc123")

	op := store.Op{
		OpID: "op-split-1", WorkID: w.ID, EditionSHA: Ptr("abc123"),
		ClientTS: time.Now(), Progression: 0.5, Origin: store.OriginNative,
	}
	if _, err := s.AppendOps(ctx, u.ID, "d1", []store.Op{op}); err != nil {
		t.Fatal(err)
	}

	newWork := store.Work{ID: "w2", UserID: u.ID, Title: "split", CreatedAt: time.Now()}
	err := s.SplitWork(ctx, u.ID, w.ID, "abc123",
		[]store.Identifier{{Kind: "sha256", Value: "abc123"}, {Kind: "dc", Value: "urn:isbn:9780316419568-w1"}},
		newWork)
	if err != nil {
		t.Fatal(err)
	}

	pos, err := s.Positions(ctx, u.ID, "w2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 || pos[0].OpID != "op-split-1" {
		t.Fatalf("op not moved: %+v", pos)
	}
	pos, err = s.Positions(ctx, u.ID, w.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 0 {
		t.Fatalf("op still on source: %+v", pos)
	}
	got, _ := s.ResolveAliases(ctx, u.ID, []store.Identifier{
		{Kind: "sha256", Value: "abc123"}, {Kind: "partial-md5", Value: "md5-w1"},
	})
	if got["sha256:abc123"] != "w2" || got["partial-md5:md5-w1"] != w.ID {
		t.Fatalf("bad alias split: %v", got)
	}

	if err := s.MergeWorks(ctx, u.ID, "w2", w.ID); err != nil {
		t.Fatal(err)
	}
	pos, err = s.Positions(ctx, u.ID, w.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 {
		t.Fatalf("merge lost op: %+v", pos)
	}
	if _, err := s.WorkByID(ctx, u.ID, "w2"); err != store.ErrNotFound {
		t.Fatalf("source work should be gone: %v", err)
	}
}

func testSessionsAppendOnly(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "gwen")
	w := MkWork(t, s, u, "w1", "abc123")

	ses := store.Session{
		SessionID: "s1", WorkID: w.ID, EditionSHA: Ptr("abc123"), DeviceID: "d1",
		StartedAt: time.Now().Add(-time.Hour), EndedAt: time.Now(),
		StartProg: 0.4, EndProg: 0.45, Origin: store.OriginNative,
	}
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	ses2 := ses
	ses2.EndProg = 0.9
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses2}); err != store.ErrIDMismatch {
		t.Fatalf("want ErrIDMismatch, got %v", err)
	}
}

func testSessionRollups(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "rollup")
	other := MkUser(t, s, "rollup-other")
	w := MkWork(t, s, u, "w1", "rollup-sha")
	old := time.Now().Add(-200 * 24 * time.Hour)
	ses := store.Session{
		SessionID: "old-s1", WorkID: w.ID, EditionSHA: Ptr("rollup-sha"), DeviceID: "d1",
		StartedAt: old, EndedAt: old.Add(time.Hour), StartProg: 0.1, EndProg: 0.2,
		Origin: store.OriginNative,
	}
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	ended, err := s.SessionsEndedBefore(ctx, u.ID, time.Now().Add(-180*24*time.Hour))
	if err != nil || len(ended) != 1 {
		t.Fatalf("sessions for rollup: %d %v", len(ended), err)
	}
	day := old.In(time.FixedZone("test", 3600)).Format("2006-01-02")
	ru := store.SessionRollup{
		UserID: u.ID, WorkID: w.ID, Day: day,
		ActiveSeconds: 3600, Pages: 46.2, ProgDelta: 0.1, SessionCount: 1,
	}
	if err := s.ApplyRollups(ctx, u.ID, []store.SessionRollup{ru}, ended); err != nil {
		t.Fatal(err)
	}
	raw, err := s.SessionsInRange(ctx, u.ID, old.Add(-time.Hour), old.Add(2*time.Hour))
	if err != nil || len(raw) != 0 {
		t.Fatalf("raw session retained: %d %v", len(raw), err)
	}
	got, err := s.RollupsInRange(ctx, u.ID, day, day)
	if err != nil || len(got) != 1 || got[0].SessionCount != 1 || got[0].ActiveSeconds != 3600 {
		t.Fatalf("rollup: %+v %v", got, err)
	}
	if cross, err := s.RollupsInRange(ctx, other.ID, day, day); err != nil || len(cross) != 0 {
		t.Fatalf("cross-user rollup read: %+v %v", cross, err)
	}
	// The compact tombstone preserves append idempotency after deletion.
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatalf("archived duplicate: %v", err)
	}
	changed := ses
	changed.EndProg = 0.3
	if err := s.AppendSessions(ctx, u.ID, []store.Session{changed}); err != store.ErrIDMismatch {
		t.Fatalf("archived mismatch: want ErrIDMismatch, got %v", err)
	}
}

func testHousekeeping(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "cleanup")
	now := time.Now()
	expired := now.Add(-store.TokenPurgeGrace - time.Hour)
	if err := s.CreateToken(ctx, store.Token{
		ID: "expired-token", UserID: u.ID, DeviceID: "d1", Name: "old",
		Scope: store.ScopeSync, SHA256: "expired-token-hash", CreatedAt: expired,
		ExpiresAt: &expired,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAuthSession(ctx, store.AuthSession{
		ID: "expired-auth", UserID: u.ID, SHA256: "expired-auth-hash", Kind: "web",
		CreatedAt: expired, ExpiresAt: expired,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePairingCode(ctx, store.PairingCode{
		ID: "expired-pair", UserID: u.ID, CodeSHA256: "expired-pair-hash", ExpiresAt: expired,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Housekeep(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TokenByHash(ctx, u.ID, "expired-token-hash"); err != store.ErrNotFound {
		t.Fatalf("expired token retained: %v", err)
	}
	if _, err := s.AuthSessionByHash(ctx, "expired-auth-hash"); err != store.ErrNotFound {
		t.Fatalf("expired auth session retained: %v", err)
	}
	if _, err := s.RedeemPairingCode(ctx, "expired-pair-hash", now); err != store.ErrNotFound {
		t.Fatalf("expired pairing code retained: %v", err)
	}
}

func testConcurrentAppend(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "prop")
	w := MkWork(t, s, u, "w1", "abc123")

	const devices = 8
	const opsPerDevice = 25

	var wg sync.WaitGroup
	errs := make(chan error, devices)
	for d := 0; d < devices; d++ {
		wg.Add(1)
		go func(d int) {
			defer wg.Done()
			dev := fmt.Sprintf("dev-%d", d)
			for b := 0; b < opsPerDevice; b += 5 {
				var batch []store.Op
				for i := 0; i < 5 && b+i < opsPerDevice; i++ {
					n := b + i
					batch = append(batch, store.Op{
						OpID:        fmt.Sprintf("%s-op-%d", dev, n),
						WorkID:      w.ID,
						EditionSHA:  Ptr("abc123"),
						ClientTS:    time.Now(),
						Progression: float64(n) / 100,
						Origin:      store.OriginNative,
					})
				}
				res, err := s.AppendOps(ctx, u.ID, dev, batch)
				if err != nil {
					errs <- err
					return
				}
				for _, r := range res {
					if r.Status != "applied" {
						errs <- fmt.Errorf("%s: op %s status %s", dev, r.OpID, r.Status)
						return
					}
				}
			}
		}(d)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	page, err := s.Changes(ctx, u.ID, 0, devices*opsPerDevice+10)
	if err != nil {
		t.Fatal(err)
	}
	want := devices * opsPerDevice
	if len(page.Ops) != want {
		t.Fatalf("want %d ops, got %d", want, len(page.Ops))
	}
	for i, o := range page.Ops {
		if o.Seq != int64(i+1) {
			t.Fatalf("gap at position %d: seq=%d", i, o.Seq)
		}
	}
}

func testPairingRedeem(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "henry")

	pc := store.PairingCode{
		ID: "p1", UserID: u.ID, CodeSHA256: "hash1",
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := s.CreatePairingCode(ctx, pc); err != nil {
		t.Fatal(err)
	}
	got, err := s.RedeemPairingCode(ctx, "hash1", time.Now())
	if err != nil || got.UserID != u.ID {
		t.Fatalf("redeem: %+v %v", got, err)
	}
	// Single use.
	if _, err := s.RedeemPairingCode(ctx, "hash1", time.Now()); err != store.ErrConflict {
		t.Fatalf("double redeem: want ErrConflict, got %v", err)
	}
	// Expired.
	pc2 := store.PairingCode{
		ID: "p2", UserID: u.ID, CodeSHA256: "hash2",
		ExpiresAt: time.Now().Add(-time.Minute),
	}
	if err := s.CreatePairingCode(ctx, pc2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPairingCode(ctx, "hash2", time.Now()); err != store.ErrConflict {
		t.Fatalf("expired redeem: want ErrConflict, got %v", err)
	}
}

func testKopluginUpsert(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "ida")
	w := MkWork(t, s, u, "w1", "abc123")

	key := "d1|md5x|42|1754800000"
	mk := func(sessID string, dur time.Duration) store.Session {
		return store.Session{
			SessionID: sessID, WorkID: w.ID, DeviceID: "koplugin:kobo",
			StartedAt: time.Unix(1754800000, 0), EndedAt: time.Unix(1754800000, 0).Add(dur),
			StartProg: 41.0 / 300, EndProg: 42.0 / 300,
			Origin: store.OriginKoplugin, SourceKey: Ptr(key),
		}
	}

	st, err := s.UpsertKopluginSession(ctx, u.ID, mk("sess-v1", 15*time.Minute))
	if err != nil || st != "inserted" {
		t.Fatalf("insert: %q %v", st, err)
	}
	st, err = s.UpsertKopluginSession(ctx, u.ID, mk("sess-v1", 15*time.Minute))
	if err != nil || st != "duplicate" {
		t.Fatalf("dup: %q %v", st, err)
	}
	st, err = s.UpsertKopluginSession(ctx, u.ID, mk("sess-v2", 20*time.Minute))
	if err != nil || st != "superseded" {
		t.Fatalf("supersede: %q %v", st, err)
	}
	// Re-upload of the first revision is a duplicate, not a new supersession.
	st, err = s.UpsertKopluginSession(ctx, u.ID, mk("sess-v1", 15*time.Minute))
	if err != nil || st != "duplicate" {
		t.Fatalf("old revision re-upload: %q %v", st, err)
	}
}
