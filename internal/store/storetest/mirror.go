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

// testMirrorCandidatesPickOneAliasPerWork covers a work that owns more
// than one KOReader fingerprint. That can happen when bytes changed at
// a path and the work learns the new fingerprint before the old one is
// forgotten. The mirror cursor is keyed by work, so the candidate list
// must still offer one row for that work, not one row per alias.
func testMirrorCandidatesPickOneAliasPerWork(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "mirror-multi-alias")
	w := MkWork(t, s, u, "w1", "sha-w1")
	if err := s.AddAliases(ctx, u.ID, w.ID,
		[]store.Identifier{{Kind: "partial-md5", Value: "ffffffffffffffffffffffffffffffff"}}); err != nil {
		t.Fatal(err)
	}
	mirrorOp(t, s, u, w.ID, "sha-w1", "op-1", "phone", 0.55)

	got, err := s.MirrorCandidates(ctx, u.ID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("one work with two aliases produced %d candidates: %+v", len(got), got)
	}
	if got[0].WorkID != w.ID || got[0].Document != "ffffffffffffffffffffffffffffffff" {
		t.Fatalf("candidate: %+v", got[0])
	}
}

func testMirrorCandidatesPreferSafeCatalogAlias(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "mirror-safe-alias")
	w := MkWork(t, s, u, "w1", "sha-w1")
	const (
		ambiguous = "ffffffffffffffffffffffffffffffff"
		safe      = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	)
	if err := s.AddAliases(ctx, u.ID, w.ID, []store.Identifier{
		{Kind: "partial-md5", Value: ambiguous},
		{Kind: "partial-md5", Value: safe},
	}); err != nil {
		t.Fatal(err)
	}
	mirrorOp(t, s, u, w.ID, "sha-w1", "op-1", "phone", 0.55)

	folder := MkFolder(t, s, "safe-alias", store.FolderPlain)
	now := time.Now().UTC()
	if _, err := s.ReconcileFolder(ctx, folder.ID, []store.ObservedBook{
		{RelativePath: "safe.epub", SizeBytes: 100, MTime: now,
			ContentSHA256: "sha-safe", PartialMD5: safe, Title: "Safe"},
		{RelativePath: "ambiguous-one.epub", SizeBytes: 101, MTime: now,
			ContentSHA256: "sha-ambiguous-one", PartialMD5: ambiguous, Title: "Ambiguous One"},
		{RelativePath: "ambiguous-two.epub", SizeBytes: 102, MTime: now,
			ContentSHA256: "sha-ambiguous-two", PartialMD5: ambiguous, Title: "Ambiguous Two"},
	}, true, now); err != nil {
		t.Fatal(err)
	}

	got, err := s.MirrorCandidates(ctx, u.ID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates: %+v", got)
	}
	if got[0].Document != safe || got[0].Title != "Safe" {
		t.Fatalf("candidate chose the unsafe alias: %+v", got[0])
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

// testMirrorCandidatesRefuseAmbiguousFingerprints covers ADR-0047's
// explicit collision rule: a fingerprint matching two catalog books
// mirrors neither. The aliases table only guarantees one alias row per
// fingerprint, not that the catalog agrees on which book it names, so
// the candidate query has to check the catalog itself.
func testMirrorCandidatesRefuseAmbiguousFingerprints(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "mirror-ambiguous")
	w := MkWork(t, s, u, "w1", "sha-w1")
	mirrorOp(t, s, u, w.ID, "sha-w1", "op-1", "phone", 0.3)

	// A work with no catalog book at all still mirrors: the ambiguity
	// this guard refuses is specifically two active catalog books
	// sharing a fingerprint, not the absence of one.
	got, err := s.MirrorCandidates(ctx, u.ID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].WorkID != w.ID {
		t.Fatalf("candidate missing before any catalog book exists: %+v", got)
	}

	// Two active catalog books observed with the same KOReader
	// fingerprint (a real collision KOReader itself accepts, per
	// ADR-0046).
	folder := MkFolder(t, s, "ambiguous-mirror", store.FolderPlain)
	now := time.Now().UTC()
	const shared = "md5-w1"
	if _, err := s.ReconcileFolder(ctx, folder.ID, []store.ObservedBook{
		{RelativePath: "one.epub", SizeBytes: 10, MTime: now,
			ContentSHA256: "sha-one", PartialMD5: shared, Title: "One"},
		{RelativePath: "two.epub", SizeBytes: 10, MTime: now,
			ContentSHA256: "sha-two", PartialMD5: shared, Title: "Two"},
	}, true, now); err != nil {
		t.Fatal(err)
	}

	got, err = s.MirrorCandidates(ctx, u.ID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("an ambiguous fingerprint was offered as a candidate: %+v", got)
	}
}

// testMirrorCandidatesRespectFolderGrants. A fingerprint is a reader's
// own alias and says only that they hold these bytes. It is not a
// claim on a shelf nobody gave them, so the catalog facts a candidate
// carries — the title, the filename, the size, the folder's root —
// come only from a book in a folder they were granted.
//
// This matters beyond the usual isolation rule because those facts
// leave the building: the BookOrbit protocol searches the peer by
// title (ADR-0048), so an ungranted title would be spoken aloud to
// another server, and a position could be filed against a book the
// reader cannot see.
func testMirrorCandidatesRespectFolderGrants(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "mirror-grants")
	w := MkWork(t, s, u, "w1", "sha-w1")
	mirrorOp(t, s, u, w.ID, "sha-w1", "op-1", "phone", 0.3)

	// A folder this reader is not in, holding a book whose bytes
	// happen to carry their fingerprint.
	now := time.Now().UTC()
	private := store.Folder{
		ID: "f-private", Name: "private", RootPath: "/srv/private",
		Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateFolder(ctx, private); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileFolder(ctx, private.ID, []store.ObservedBook{{
		RelativePath: "secret/Confidential Report.epub", SizeBytes: 4242,
		MTime: now, ContentSHA256: "sha-secret", PartialMD5: "md5-w1",
		Title: "Confidential Report",
	}}, true, now); err != nil {
		t.Fatal(err)
	}

	got, err := s.MirrorCandidates(ctx, u.ID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("the reader's own work stopped being a candidate: %+v", got)
	}
	// The work still mirrors — the reader's reading is theirs — but it
	// carries nothing from a folder they were never given.
	c := got[0]
	if c.Title != "" || c.RelativePath != "" || c.SizeBytes != 0 || c.RootPath != "" {
		t.Fatalf("a book from an ungranted folder leaked into the mirror: %+v", c)
	}

	// Granted, it is an ordinary catalog book again.
	if err := s.AssignUserFolder(ctx, u.ID, private.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.MirrorCandidates(ctx, u.ID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Confidential Report" {
		t.Fatalf("a granted book did not reach the mirror: %+v", got)
	}
	if got[0].RootPath != "/srv/private" || got[0].SizeBytes != 4242 {
		t.Fatalf("candidate: %+v", got[0])
	}

	// And revoked again, it goes back to being invisible.
	if err := s.UnassignUserFolder(ctx, u.ID, private.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.MirrorCandidates(ctx, u.ID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "" {
		t.Fatalf("a revoked grant left the book behind: %+v", got)
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
	if got.RemoteFileID != "" || got.RemoteCheckedAt != nil || got.PushedMark != "" {
		t.Fatalf("a cursor invented a book on the peer: %+v", got)
	}

	// The second exchange replaces the first. There is no history here:
	// this is a watermark, not a log.
	second := first
	second.PushedSeq = 11
	second.RemoteTS = 1700000000
	second.PulledAt = Ptr(time.Now().UTC().Truncate(time.Second))
	second.LastError = "peer answered 502"
	second.LastErrorAt = Ptr(time.Now().UTC().Truncate(time.Second))
	// What the peer calls this book, when it was last looked for, and
	// what was last sent: the bookkeeping a protocol whose replies
	// carry no device needs to recognise its own writing.
	second.RemoteFileID = "41"
	second.RemoteCheckedAt = Ptr(time.Now().UTC().Truncate(time.Second))
	second.PushedMark = "cfi:epubcfi(/6/14!/4/2/8:37)"
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
	if got.RemoteFileID != "41" || got.PushedMark != second.PushedMark {
		t.Fatalf("what the peer calls the book was not kept: %+v", got)
	}
	if got.RemoteCheckedAt == nil || !got.RemoteCheckedAt.Equal(*second.RemoteCheckedAt) {
		t.Fatalf("remote_checked_at: %v", got.RemoteCheckedAt)
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
