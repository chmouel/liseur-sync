package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// mirrorOp appends one position for a work and returns its sequence.
func mirrorOp(t *testing.T, s store.Store, u store.User, workID, sha, opID, device string, progression float64) int64 {
	t.Helper()
	res, err := s.AppendOps(context.Background(), u.ID, device, []store.Op{{
		OpID: opID, WorkID: workID, EditionSHA: Ptr(sha),
		ClientTS: time.Now(), Progression: progression,
		Origin: store.OriginNative,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != "applied" {
		t.Fatalf("op %s: %+v", opID, res[0])
	}
	return res[0].Seq
}

// testMirrorCandidates covers the read the mirror loop is built on: the
// bounded set of works that carry a KOReader fingerprint and have been
// read recently, each with the newest thing this server knows about
// where the reader is.
func testMirrorCandidates(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "mirror-candidates")
	w := MkWork(t, s, u, "w1", "sha-w1")
	// MkWork gives every work a partial-md5 alias of md5-<id>, which is
	// the shape a folder pass and a KOReader device both produce.
	const document = "md5-w1"

	// A work nobody has read is not a candidate: there is no position
	// to send and nothing to ask the peer about on its behalf.
	got, err := s.MirrorCandidates(ctx, u.ID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("an unread work was offered as a candidate: %+v", got)
	}

	mirrorOp(t, s, u, w.ID, "sha-w1", "op-1", "phone", 0.2)
	seq := mirrorOp(t, s, u, w.ID, "sha-w1", "op-2", "phone", 0.55)

	got, err = s.MirrorCandidates(ctx, u.ID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates: %+v", got)
	}
	c := got[0]
	if c.WorkID != w.ID || c.Document != document {
		t.Fatalf("candidate identity: %+v", c)
	}
	// The newest op, not the first: the mirror sends where the reader
	// is, and the op log behind it is not the peer's business.
	if c.Latest.Seq != seq || c.Latest.OpID != "op-2" || c.Latest.Progression != 0.55 {
		t.Fatalf("candidate carried the wrong position: %+v", c.Latest)
	}
	if c.Latest.DeviceID != "phone" {
		t.Fatalf("device id lost: %q", c.Latest.DeviceID)
	}
	if c.Latest.ClientTS.IsZero() || c.Latest.ReceivedAt.IsZero() {
		t.Fatalf("timestamps lost: %+v", c.Latest)
	}

	// The window is the cost control: a library read years ago is not
	// asked about every five minutes.
	got, err = s.MirrorCandidates(ctx, u.ID, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a work outside the window was offered: %+v", got)
	}

	// A work with no fingerprint has no join key, so there is nothing
	// the two servers could agree it is.
	unnamed := store.Work{ID: "w-unnamed", UserID: u.ID, CreatedAt: time.Now()}
	edition := &store.Edition{UserID: u.ID, SHA256: "sha-unnamed", WorkID: unnamed.ID}
	if err := s.CreateWork(ctx, unnamed, edition,
		[]store.Identifier{{Kind: "sha256", Value: "sha-unnamed"}}); err != nil {
		t.Fatal(err)
	}
	mirrorOp(t, s, u, unnamed.ID, "sha-unnamed", "op-unnamed", "phone", 0.7)

	got, err = s.MirrorCandidates(ctx, u.ID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].WorkID != w.ID {
		t.Fatalf("a work with no fingerprint was offered: %+v", got)
	}
}

// testMirrorCandidatesAreBoundedAndOrdered. The mirror asks a peer one
// document at a time, so the order decides what gets exchanged when
// there is more than a pass can do.
func testMirrorCandidatesAreBoundedAndOrdered(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "mirror-order")

	for i, id := range []string{"wa", "wb", "wc"} {
		w := MkWork(t, s, u, id, "sha-"+id)
		mirrorOp(t, s, u, w.ID, "sha-"+id, "op-"+id, "phone", float64(i)/10)
	}

	got, err := s.MirrorCandidates(ctx, u.ID, time.Now().Add(-time.Hour), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("limit ignored: %d candidates", len(got))
	}
	// Newest first, so a bound truncates the books nobody has touched
	// lately rather than the one being read right now.
	if got[0].WorkID != "wc" || got[1].WorkID != "wb" {
		t.Fatalf("order: %s then %s", got[0].WorkID, got[1].WorkID)
	}
	if none, err := s.MirrorCandidates(ctx, u.ID, time.Now().Add(-time.Hour), 0); err != nil || len(none) != 0 {
		t.Fatalf("a limit of zero returned %v, %v", none, err)
	}
}

// testMirrorCandidatesAreScopedToTheAccount. Reading state is per
// reader, and a mirror belongs to exactly one account; a candidate list
// that crossed accounts would push one reader's position to another's
// peer.
func testMirrorCandidatesAreScopedToTheAccount(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	alice := MkUser(t, s, "mirror-alice")
	bob := MkUser(t, s, "mirror-bob")
	// The same book, the same fingerprint, read by both.
	wa := MkWork(t, s, alice, "shared", "sha-shared-a")
	wb := MkWork(t, s, bob, "shared", "sha-shared-b")
	mirrorOp(t, s, alice, wa.ID, "sha-shared-a", "op-alice", "alice-phone", 0.1)
	mirrorOp(t, s, bob, wb.ID, "sha-shared-b", "op-bob", "bob-phone", 0.9)

	got, err := s.MirrorCandidates(ctx, alice.ID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates: %+v", got)
	}
	if got[0].Latest.Progression != 0.1 || got[0].Latest.DeviceID != "alice-phone" {
		t.Fatalf("another reader's position was offered: %+v", got[0].Latest)
	}
}

// testMirrorCursors covers the bookkeeping: what has already crossed,
// per work and per peer.
func testMirrorCursors(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "mirror-cursors")
	w := MkWork(t, s, u, "w1", "sha-w1")

	empty, err := s.MirrorCursors(ctx, u.ID, "orbit")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("a peer nobody has talked to has cursors: %+v", empty)
	}

	// A first exchange: pushed, never pulled. The nil timestamps are
	// the point — "not yet" has to survive the round trip, because the
	// loop tells it apart from "at the epoch".
	first := store.MirrorCursor{
		WorkID: w.ID, Document: "md5-w1", PushedSeq: 7,
		PushedAt: Ptr(time.Now().UTC().Truncate(time.Second)),
	}
	if err := s.PutMirrorCursor(ctx, u.ID, "orbit", first); err != nil {
		t.Fatal(err)
	}
	cursors, err := s.MirrorCursors(ctx, u.ID, "orbit")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cursors[w.ID]
	if !ok {
		t.Fatalf("cursor not found among %+v", cursors)
	}
	if got.Document != "md5-w1" || got.PushedSeq != 7 {
		t.Fatalf("cursor: %+v", got)
	}
	if got.PushedAt == nil || !got.PushedAt.Equal(*first.PushedAt) {
		t.Fatalf("pushed_at: %v", got.PushedAt)
	}
	if got.PulledAt != nil || got.RemoteTS != 0 || got.LastError != "" || got.LastErrorAt != nil {
		t.Fatalf("a cursor invented a pull that never happened: %+v", got)
	}

	// The second exchange replaces the first. There is no history here:
	// this is a watermark, not a log.
	second := first
	second.PushedSeq = 11
	second.RemoteTS = 1700000000
	second.PulledAt = Ptr(time.Now().UTC().Truncate(time.Second))
	second.LastError = "peer answered 502"
	second.LastErrorAt = Ptr(time.Now().UTC().Truncate(time.Second))
	if err := s.PutMirrorCursor(ctx, u.ID, "orbit", second); err != nil {
		t.Fatal(err)
	}
	cursors, err = s.MirrorCursors(ctx, u.ID, "orbit")
	if err != nil {
		t.Fatal(err)
	}
	if len(cursors) != 1 {
		t.Fatalf("an upsert made a second row: %+v", cursors)
	}
	got = cursors[w.ID]
	if got.PushedSeq != 11 || got.RemoteTS != 1700000000 {
		t.Fatalf("cursor after upsert: %+v", got)
	}
	if got.LastError != "peer answered 502" || got.LastErrorAt == nil {
		t.Fatalf("the last failure was not kept: %+v", got)
	}
	if got.PulledAt == nil || !got.PulledAt.Equal(*second.PulledAt) {
		t.Fatalf("pulled_at: %v", got.PulledAt)
	}
}

// testMirrorCursorsAreScopedToPeerAndAccount. Two peers disagreeing
// about a book is normal; one peer's watermark answering for the other
// would silently skip a push.
func testMirrorCursorsAreScopedToPeerAndAccount(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	alice := MkUser(t, s, "cursor-alice")
	bob := MkUser(t, s, "cursor-bob")
	wa := MkWork(t, s, alice, "w1", "sha-a")
	wb := MkWork(t, s, bob, "w1", "sha-b")

	put := func(u store.User, workID, peer string, seq int64) {
		t.Helper()
		if err := s.PutMirrorCursor(ctx, u.ID, peer, store.MirrorCursor{
			WorkID: workID, Document: "md5-w1", PushedSeq: seq,
		}); err != nil {
			t.Fatal(err)
		}
	}
	put(alice, wa.ID, "orbit", 1)
	put(alice, wa.ID, "other-peer", 2)
	put(bob, wb.ID, "orbit", 3)

	for _, tc := range []struct {
		user store.User
		peer string
		want int64
	}{
		{alice, "orbit", 1},
		{alice, "other-peer", 2},
		{bob, "orbit", 3},
	} {
		cursors, err := s.MirrorCursors(ctx, tc.user.ID, tc.peer)
		if err != nil {
			t.Fatal(err)
		}
		if len(cursors) != 1 {
			t.Fatalf("%s/%s: %+v", tc.user.Name, tc.peer, cursors)
		}
		if got := cursors["w1"].PushedSeq; got != tc.want {
			t.Fatalf("%s/%s: pushed_seq %d, want %d", tc.user.Name, tc.peer, got, tc.want)
		}
	}
}

// testMirrorCursorsFollowTheWorkOut. A reader deleting a work deletes
// the whole graph (ADR-0024); a cursor left behind would be a row
// naming a work that no longer exists.
func testMirrorCursorsFollowTheWorkOut(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "cursor-cascade")
	w := MkWork(t, s, u, "w1", "sha-w1")
	if err := s.PutMirrorCursor(ctx, u.ID, "orbit", store.MirrorCursor{
		WorkID: w.ID, Document: "md5-w1", PushedSeq: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWork(ctx, u.ID, w.ID); err != nil && !errors.Is(err, store.ErrInvalidInput) {
		t.Fatal(err)
	} else if err != nil {
		t.Skip("this work is backed by a catalog book and cannot be deleted here")
	}
	cursors, err := s.MirrorCursors(ctx, u.ID, "orbit")
	if err != nil {
		t.Fatal(err)
	}
	if len(cursors) != 0 {
		t.Fatalf("a cursor outlived its work: %+v", cursors)
	}
}
