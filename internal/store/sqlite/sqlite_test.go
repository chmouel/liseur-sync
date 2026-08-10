package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mkUser(t *testing.T, s *Store, name string) store.User {
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

func TestMigrateAndUsers(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	u := mkUser(t, s, "alice")

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
}

func TestTokens(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	u := mkUser(t, s, "bob")
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

func mkWork(t *testing.T, s *Store, u store.User) store.Work {
	t.Helper()
	w := store.Work{ID: "w1", UserID: u.ID, Title: "A Memory Called Empire", Author: "Martine", CreatedAt: time.Now()}
	e := &store.Edition{UserID: u.ID, SHA256: "abc123", WorkID: w.ID, PageCount: ptr(int64(462))}
	ids := []store.Identifier{
		{Kind: "sha256", Value: "abc123"},
		{Kind: "partial-md5", Value: "ffff"},
		{Kind: "dc", Value: "urn:isbn:9780316419568"},
	}
	if err := s.CreateWork(context.Background(), w, e, ids); err != nil {
		t.Fatal(err)
	}
	return w
}

func ptr[T any](v T) *T { return &v }

func TestResolveAliases(t *testing.T) {
	s := open(t)
	u := mkUser(t, s, "carol")
	mkWork(t, s, u)

	got, err := s.ResolveAliases(context.Background(), u.ID, []store.Identifier{
		{Kind: "partial-md5", Value: "ffff"},
		{Kind: "dc", Value: "urn:isbn:nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["partial-md5:ffff"] != "w1" {
		t.Fatalf("want w1, got %v", got)
	}
	if _, ok := got["dc:urn:isbn:nope"]; ok {
		t.Fatalf("unknown alias should be absent: %v", got)
	}
}

func TestAppendOpsIdempotencyAndConflict(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	u := mkUser(t, s, "dave")
	w := mkWork(t, s, u)

	op := store.Op{
		OpID: "018e6f1a-0000-7000-8000-000000000001", WorkID: w.ID,
		EditionSHA: ptr("abc123"), ClientTS: time.Now(), Progression: 0.41,
		Origin: store.OriginNative,
	}
	res, err := s.AppendOps(ctx, u.ID, "d-phone", []store.Op{op})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != "applied" || res[0].Seq != 1 {
		t.Fatalf("want applied seq=1, got %+v", res[0])
	}

	// Identical retry -> duplicate, same seq.
	res, err = s.AppendOps(ctx, u.ID, "d-phone", []store.Op{op})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != "duplicate" || res[0].Seq != 1 {
		t.Fatalf("want duplicate seq=1, got %+v", res[0])
	}

	// Same op_id, different payload -> conflict, no new op.
	op2 := op
	op2.Progression = 0.99
	res, err = s.AppendOps(ctx, u.ID, "d-phone", []store.Op{op2})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != "conflict" {
		t.Fatalf("want conflict, got %+v", res[0])
	}

	// Gap-free seq across devices.
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

func TestChangesPaginationAndHeads(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	u := mkUser(t, s, "erin")
	w := mkWork(t, s, u)

	var ops []store.Op
	for i := 0; i < 5; i++ {
		ops = append(ops, store.Op{
			OpID:        "op-" + string(rune('a'+i)),
			WorkID:      w.ID,
			EditionSHA:  ptr("abc123"),
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

func TestSplitAndMerge(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	u := mkUser(t, s, "fred")
	w := mkWork(t, s, u)

	op := store.Op{
		OpID: "op-split-1", WorkID: w.ID, EditionSHA: ptr("abc123"),
		ClientTS: time.Now(), Progression: 0.5, Origin: store.OriginNative,
	}
	if _, err := s.AppendOps(ctx, u.ID, "d1", []store.Op{op}); err != nil {
		t.Fatal(err)
	}

	newWork := store.Work{ID: "w2", UserID: u.ID, Title: "split", CreatedAt: time.Now()}
	err := s.SplitWork(ctx, u.ID, w.ID, "abc123",
		[]store.Identifier{{Kind: "sha256", Value: "abc123"}, {Kind: "dc", Value: "urn:isbn:9780316419568"}},
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
	// sha256 alias moved; partial-md5 stayed.
	got, _ := s.ResolveAliases(ctx, u.ID, []store.Identifier{
		{Kind: "sha256", Value: "abc123"}, {Kind: "partial-md5", Value: "ffff"},
	})
	if got["sha256:abc123"] != "w2" || got["partial-md5:ffff"] != w.ID {
		t.Fatalf("bad alias split: %v", got)
	}

	// Merge back.
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

func TestSessionsAppendOnly(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	u := mkUser(t, s, "gwen")
	w := mkWork(t, s, u)

	ses := store.Session{
		SessionID: "s1", WorkID: w.ID, EditionSHA: ptr("abc123"), DeviceID: "d1",
		StartedAt: time.Now().Add(-time.Hour), EndedAt: time.Now(),
		StartProg: 0.4, EndProg: 0.45, Origin: store.OriginNative,
	}
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	// Idempotent duplicate.
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	// Same id, different payload -> mismatch.
	ses2 := ses
	ses2.EndProg = 0.9
	if err := s.AppendSessions(ctx, u.ID, []store.Session{ses2}); err != store.ErrIDMismatch {
		t.Fatalf("want ErrIDMismatch, got %v", err)
	}
}
