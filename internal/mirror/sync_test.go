package mirror

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

// syncFixture is one account, one work, one peer, and a real store.
// The store is real on purpose: the mirror's whole job is to turn a
// remote position into a record this server already knows how to read,
// and a fake store would prove only that the mirror agrees with itself.
type syncFixture struct {
	t      *testing.T
	st     store.Store
	peer   *fakePeer
	syncer *Syncer
	user   store.User
	work   store.Work
	// document is the fingerprint both sides know the book by.
	document string
}

const fixtureDocument = "43200deaa6b3a3cd67dc934fabe6f0f2"

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "mirror.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	u := store.User{
		ID: "u-reader", Name: "reader", Argon2Hash: "x", Timezone: "UTC",
		KosyncEnabled: true, CreatedAt: time.Now(),
	}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	w := store.Work{ID: "w1", UserID: u.ID, Title: "A Memory Called Empire", CreatedAt: time.Now()}
	e := &store.Edition{UserID: u.ID, SHA256: "sha-w1", WorkID: w.ID}
	ids := []store.Identifier{
		{Kind: "sha256", Value: "sha-w1"},
		{Kind: "partial-md5", Value: fixtureDocument},
	}
	if err := st.CreateWork(ctx, w, e, ids); err != nil {
		t.Fatal(err)
	}

	peer := newFakePeer(t)
	return &syncFixture{
		t: t, st: st, peer: peer, user: u, work: w, document: fixtureDocument,
		syncer: &Syncer{
			Store: st, Client: peer.client(t), Peer: "orbit", UserID: u.ID,
			ActiveWindow: 30 * 24 * time.Hour, MaxPerPass: 100,
		},
	}
}

// read appends a local position, as reading in this server's own
// reader or in a client against it would.
func (f *syncFixture) read(opID string, progression float64, at time.Time) store.Op {
	f.t.Helper()
	op := store.Op{
		OpID: opID, WorkID: f.work.ID, EditionSHA: ptr("sha-w1"),
		ClientTS: at, Progression: progression, Origin: store.OriginNative,
	}
	res, err := f.st.AppendOps(context.Background(), f.user.ID, "phone", []store.Op{op})
	if err != nil {
		f.t.Fatal(err)
	}
	if res[0].Status != "applied" {
		f.t.Fatalf("%s: %+v", opID, res[0])
	}
	op.Seq = res[0].Seq
	return op
}

func (f *syncFixture) pass() Report {
	f.t.Helper()
	rep, err := f.syncer.Pass(context.Background())
	if err != nil {
		f.t.Fatalf("pass: %v", err)
	}
	if rep.FirstError != nil {
		f.t.Fatalf("pass reported: %v", rep.FirstError)
	}
	return rep
}

// positions is every op for the work, newest first.
func (f *syncFixture) positions() []store.Op {
	f.t.Helper()
	ops, err := f.st.Positions(context.Background(), f.user.ID, f.work.ID, 50)
	if err != nil {
		f.t.Fatal(err)
	}
	return ops
}

func (f *syncFixture) cursor() store.MirrorCursor {
	f.t.Helper()
	cursors, err := f.st.MirrorCursors(context.Background(), f.user.ID, f.syncer.Peer)
	if err != nil {
		f.t.Fatal(err)
	}
	return cursors[f.work.ID]
}

// TestAPositionReadHereGoesToThePeer: the plain outbound case.
func TestAPositionReadHereGoesToThePeer(t *testing.T) {
	f := newSyncFixture(t)
	local := f.read("op-1", 0.37, time.Now())

	rep := f.pass()
	if rep.Pushed != 1 || rep.Considered != 1 {
		t.Fatalf("report: %s", rep)
	}
	pushes := f.peer.pushed(t)
	if len(pushes) != 1 {
		t.Fatalf("pushes: %+v", pushes)
	}
	if pushes[0].Document != f.document || pushes[0].Percentage != 0.37 {
		t.Fatalf("what reached the peer: %+v", pushes[0])
	}
	if f.cursor().PushedSeq != local.Seq {
		t.Fatalf("cursor: %+v", f.cursor())
	}
}

// TestTheSamePositionIsNotPushedTwice. The peer is somebody else's
// server; a mirror that re-sent the same position every five minutes
// would be indistinguishable from a broken client.
func TestTheSamePositionIsNotPushedTwice(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.37, time.Now())

	f.pass()
	second := f.pass()
	if second.Pushed != 0 {
		t.Fatalf("a settled position was pushed again: %s", second)
	}
	if got := len(f.peer.pushed(t)); got != 1 {
		t.Fatalf("%d pushes for one position", got)
	}
}

// TestReadingOnMoreGoesOutAgain: the watermark is a watermark, not a
// latch.
func TestReadingOnMoreGoesOutAgain(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.37, time.Now().Add(-time.Minute))
	f.pass()

	f.read("op-2", 0.51, time.Now())
	rep := f.pass()
	if rep.Pushed != 1 {
		t.Fatalf("report: %s", rep)
	}
	pushes := f.peer.pushed(t)
	if len(pushes) != 2 || pushes[1].Percentage != 0.51 {
		t.Fatalf("pushes: %+v", pushes)
	}
}

// TestAPositionFromThePeerBecomesANativeOp: the plain inbound case, and
// the reason the mirror exists at all.
func TestAPositionFromThePeerBecomesANativeOp(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.10, time.Now().Add(-time.Hour))
	f.peer.set(Position{
		Document: f.document, Progress: "/body/DocFragment[9]/body/p[2]",
		Percentage: 0.64, Device: "kindle", DeviceID: "kindle-oasis",
		Timestamp: time.Now().Unix(),
	})

	rep := f.pass()
	if rep.Pulled != 1 {
		t.Fatalf("report: %s", rep)
	}
	ops := f.positions()
	if len(ops) != 2 {
		t.Fatalf("ops: %+v", ops)
	}
	got := ops[0]
	if got.Progression != 0.64 {
		t.Fatalf("progression: %v", got.Progression)
	}
	// Filed under the peer, so a reader can see where it came from and
	// so the next pass does not mistake it for something read here.
	if got.DeviceID != "orbit:kindle-oasis" {
		t.Fatalf("device: %q", got.DeviceID)
	}
	// It is a kosync-shaped record from a server speaking kosync, which
	// is what it is; nothing new had to be invented to hold it.
	if got.Origin != store.OriginKosync {
		t.Fatalf("origin: %q", got.Origin)
	}
	if got.ForeignPos == nil || *got.ForeignPos != "/body/DocFragment[9]/body/p[2]" {
		t.Fatalf("the engine position was not carried verbatim: %v", got.ForeignPos)
	}
	if got.OriginAlias == nil || *got.OriginAlias != "partial-md5:"+f.document {
		t.Fatalf("origin alias: %v", got.OriginAlias)
	}
	// And nothing went the other way: theirs was newer.
	if len(f.peer.pushed(t)) != 0 {
		t.Fatal("a position was pushed over a newer remote one")
	}
}

// TestTheSameRemotePositionIsTakenOnce. The peer repeats itself on
// every poll until something changes there, and the op log is
// append-only: without a stable id this would grow a duplicate op every
// five minutes forever.
func TestTheSameRemotePositionIsTakenOnce(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.10, time.Now().Add(-time.Hour))
	f.peer.set(Position{
		Document: f.document, Progress: "/body/DocFragment[9]/body",
		Percentage: 0.64, DeviceID: "kindle-oasis", Timestamp: time.Now().Unix(),
	})

	f.pass()
	before := len(f.positions())
	f.pass()
	f.pass()
	if after := len(f.positions()); after != before {
		t.Fatalf("op count went from %d to %d on repeated polls", before, after)
	}
}

// TestOurOwnWritingIsNotTakenBackEvenWhenThePeerRestampsIt. The tie
// rule hides this most of the time, because a peer that stores the
// timestamp we sent answers with it and the two sides compare equal.
// A peer that stamps the reply with its own clock — its own arrival
// time, or a reply synthesised from a row's updated_at — breaks that
// coincidence, and then only the device id stands between the mirror
// and a loop that files this server's own position back into it as
// somebody else's news.
func TestOurOwnWritingIsNotTakenBackEvenWhenThePeerRestampsIt(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.37, time.Now().Add(-time.Hour))
	f.pass()

	pushed := f.peer.pushed(t)
	if len(pushed) != 1 {
		t.Fatalf("pushes: %+v", pushed)
	}
	// The same position, our device, their clock — and comfortably
	// newer than anything here.
	echo := pushed[0]
	echo.Timestamp = time.Now().Add(time.Minute).Unix()
	f.peer.answerWith(&echo)

	before := len(f.positions())
	rep := f.pass()
	if rep.Pulled != 0 {
		t.Fatalf("a restamped echo was taken as news: %s", rep)
	}
	if after := len(f.positions()); after != before {
		t.Fatalf("op count went from %d to %d", before, after)
	}
	for _, op := range f.positions() {
		if strings.HasPrefix(op.DeviceID, "orbit:") {
			t.Fatalf("this server's own position was filed under the peer: %+v", op)
		}
	}
}

// TestOurOwnWritingIsNotTakenBack is echo suppression. Without it the
// mirror would read its own push as news from the peer, append it as a
// remote op, then find that op newer than the local one and push it
// again — a loop that fills the op log with copies of one position.
func TestOurOwnWritingIsNotTakenBack(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.37, time.Now())

	f.pass() // pushes; the fake peer stores it and will serve it back
	before := len(f.positions())

	rep := f.pass()
	if rep.Pulled != 0 {
		t.Fatalf("the mirror took its own writing back: %s", rep)
	}
	if after := len(f.positions()); after != before {
		t.Fatalf("op count went from %d to %d", before, after)
	}
	if got := len(f.peer.pushed(t)); got != 1 {
		t.Fatalf("%d pushes", got)
	}
}

// TestAPositionTakenFromThePeerIsNotSentBackToIt, which is the other
// half of the same loop: an inbound op is the newest thing here, so a
// naive push rule would immediately return it to the server it came
// from, under this server's own device name.
func TestAPositionTakenFromThePeerIsNotSentBackToIt(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.10, time.Now().Add(-time.Hour))
	f.peer.set(Position{
		Document: f.document, Progress: "/body/DocFragment[9]/body",
		Percentage: 0.64, DeviceID: "kindle-oasis", Timestamp: time.Now().Unix(),
	})
	f.pass()

	// Nothing new has been read here, and the peer's own position is
	// now the newest op. It must stay where it is.
	f.peer.answerWith(nil)
	rep := f.pass()
	if rep.Pushed != 0 {
		t.Fatalf("an inbound position was echoed back: %s", rep)
	}
	if got := len(f.peer.pushed(t)); got != 0 {
		t.Fatalf("%d pushes", got)
	}
}

// TestAPositionThePeerNoLongerHoldsIsNotResurrected is the same rule at
// the point where only it can help. When both sides still hold the
// position, the tie rule quietly does the right thing and the device
// prefix is never consulted. When the peer has stopped holding one —
// the book was removed there, its reading cleared — the only position
// left anywhere is the copy this server took from the peer, and it
// looks exactly like something to send. Pushing it would write the
// peer's own abandoned reading back to it under this server's name.
func TestAPositionThePeerNoLongerHoldsIsNotResurrected(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.10, time.Now().Add(-time.Hour))
	f.peer.set(Position{
		Document: f.document, Progress: "/body/DocFragment[9]/body",
		Percentage: 0.64, DeviceID: "kindle-oasis", Timestamp: time.Now().Unix(),
	})
	if rep := f.pass(); rep.Pulled != 1 {
		t.Fatalf("setup did not pull: %s", rep)
	}
	before := len(f.peer.pushed(t))

	f.peer.forget(f.document)
	rep := f.pass()
	if rep.Pushed != 0 {
		t.Fatalf("a position the peer had let go was sent back: %s", rep)
	}
	if got := len(f.peer.pushed(t)); got != before {
		t.Fatalf("pushes went from %d to %d", before, got)
	}
}

// TestNewestWins in the direction that is not the easy one: the peer
// has an older position and must not overwrite what was read here.
func TestNewestWins(t *testing.T) {
	f := newSyncFixture(t)
	now := time.Now()
	f.read("op-1", 0.80, now)
	f.peer.set(Position{
		Document: f.document, Progress: "/body/DocFragment[2]/body",
		Percentage: 0.12, DeviceID: "kindle-oasis",
		Timestamp: now.Add(-2 * time.Hour).Unix(),
	})

	rep := f.pass()
	if rep.Pulled != 0 || rep.Pushed != 1 {
		t.Fatalf("report: %s", rep)
	}
	if got := f.positions()[0].Progression; got != 0.80 {
		t.Fatalf("the older remote position won: %v", got)
	}
	if got := f.peer.pushed(t)[0].Percentage; got != 0.80 {
		t.Fatalf("what the peer was told: %v", got)
	}
}

// TestATieLeavesTheLocalPositionAlone. Two sides agreeing to the second
// is far likelier to be one piece of reading than two.
func TestATieLeavesTheLocalPositionAlone(t *testing.T) {
	f := newSyncFixture(t)
	at := time.Now().Truncate(time.Second)
	f.read("op-1", 0.80, at)
	f.peer.set(Position{
		Document: f.document, Progress: "/body/DocFragment[2]/body",
		Percentage: 0.12, DeviceID: "kindle-oasis", Timestamp: at.Unix(),
	})

	rep := f.pass()
	if rep.Pulled != 0 {
		t.Fatalf("a tie moved the local position: %s", rep)
	}
	if got := f.positions()[0].Progression; got != 0.80 {
		t.Fatalf("progression: %v", got)
	}
}

// TestAResetIsNeverApplied is the trap, end to end. The peer answers
// with a now-stamped position at the start of the book and keeps doing
// it; newest-wins alone would hand it the argument on every poll.
func TestAResetIsNeverApplied(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.80, time.Now().Add(-time.Hour))
	f.pass() // settle, so the reset below is the only thing in play

	for range 3 {
		f.peer.answerWith(&Position{
			Document: f.document, Progress: "/body/DocFragment[1]/body",
			Percentage: 0, Device: "web", DeviceID: "bookorbit-web",
			Timestamp: time.Now().Unix(),
		})
		rep := f.pass()
		if rep.Pulled != 0 {
			t.Fatalf("a reset was applied: %s", rep)
		}
	}
	if got := f.positions()[0].Progression; got != 0.80 {
		t.Fatalf("the reader was sent back to the start: %v", got)
	}
}

// TestAPeerThatFailsDoesNotLoseTheCursor. A mirror is a background
// loop; a failing peer has to leave the account exactly as it found it
// and be retried later, not drop reading on the floor.
func TestAPeerThatFailsDoesNotLoseTheCursor(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.37, time.Now())
	f.peer.failWith(503, "the peer is having a moment")

	rep, err := f.syncer.Pass(context.Background())
	if err != nil {
		t.Fatalf("a failing peer stopped the pass: %v", err)
	}
	if rep.Failed != 1 || rep.FirstError == nil {
		t.Fatalf("report: %s", rep)
	}
	cur := f.cursor()
	if cur.LastError == "" || cur.LastErrorAt == nil {
		t.Fatalf("the failure was not recorded: %+v", cur)
	}
	if cur.PushedSeq != 0 {
		t.Fatalf("a failed push advanced the watermark: %+v", cur)
	}

	// And it recovers by itself once the peer does.
	f.peer.failWith(0, "")
	if rep := f.pass(); rep.Pushed != 1 {
		t.Fatalf("the mirror did not recover: %s", rep)
	}
	if cur := f.cursor(); cur.LastError != "" {
		t.Fatalf("a stale failure survived a good pass: %+v", cur)
	}
}

// TestAChangedFileIsANewBookToThePeerToo. Bytes that changed at a path
// are a new catalog book here and a new fingerprint on both sides, so
// whatever was agreed under the old one says nothing about this one.
func TestAChangedFileIsANewBookToThePeerToo(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.37, time.Now().Add(-time.Minute))
	f.pass()

	// The work now answers to a different fingerprint, as it would
	// after a pass re-read a replaced file.
	ctx := context.Background()
	if err := f.st.AddAliases(ctx, f.user.ID, f.work.ID,
		[]store.Identifier{{Kind: "partial-md5", Value: "ffffffffffffffffffffffffffffffff"}}); err != nil {
		t.Fatal(err)
	}

	rep := f.pass()
	if rep.Pushed == 0 {
		t.Fatalf("the position was not re-sent under the new fingerprint: %s", rep)
	}
	var sawNew bool
	for _, p := range f.peer.pushed(t) {
		if p.Document == "ffffffffffffffffffffffffffffffff" {
			sawNew = true
		}
	}
	if !sawNew {
		t.Fatalf("pushes: %+v", f.peer.pushed(t))
	}
}

// TestAWorkOutsideTheWindowIsLeftAlone. The peer has no push, so the
// window is the entire cost control.
func TestAWorkOutsideTheWindowIsLeftAlone(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.37, time.Now())
	f.syncer.ActiveWindow = time.Nanosecond
	time.Sleep(2 * time.Millisecond)

	rep := f.pass()
	if rep.Considered != 0 {
		t.Fatalf("report: %s", rep)
	}
	f.peer.mu.Lock()
	defer f.peer.mu.Unlock()
	if len(f.peer.requests) != 0 {
		t.Fatalf("the peer was called %d times for a book outside the window", len(f.peer.requests))
	}
}

// TestTheMirrorTouchesOneAccount. Reading state is per reader; a mirror
// belongs to exactly one account and everything else on this server is
// none of the peer's business.
func TestTheMirrorTouchesOneAccount(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	other := store.User{
		ID: "u-other", Name: "other", Argon2Hash: "x", Timezone: "UTC",
		KosyncEnabled: true, CreatedAt: time.Now(),
	}
	if err := f.st.CreateUser(ctx, other); err != nil {
		t.Fatal(err)
	}
	// The same book, the same fingerprint, a different reader.
	w := store.Work{ID: "w1", UserID: other.ID, CreatedAt: time.Now()}
	e := &store.Edition{UserID: other.ID, SHA256: "sha-other", WorkID: w.ID}
	if err := f.st.CreateWork(ctx, w, e, []store.Identifier{
		{Kind: "sha256", Value: "sha-other"},
		{Kind: "partial-md5", Value: fixtureDocument},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.AppendOps(ctx, other.ID, "other-phone", []store.Op{{
		OpID: "other-op", WorkID: w.ID, EditionSHA: ptr("sha-other"),
		ClientTS: time.Now(), Progression: 0.99, Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}

	f.read("op-1", 0.37, time.Now())
	rep := f.pass()
	if rep.Considered != 1 {
		t.Fatalf("the mirror considered another account's reading: %s", rep)
	}
	for _, p := range f.peer.pushed(t) {
		if p.Percentage == 0.99 {
			t.Fatalf("another reader's position reached the peer: %+v", p)
		}
	}
	// And nothing was written into the other account.
	ops, err := f.st.Positions(ctx, other.ID, w.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("the other account's op log was written to: %+v", ops)
	}
	cursors, err := f.st.MirrorCursors(ctx, other.ID, f.syncer.Peer)
	if err != nil {
		t.Fatal(err)
	}
	if len(cursors) != 0 {
		t.Fatalf("a cursor was written for another account: %+v", cursors)
	}
}

// TestAPassIsBounded, so a large library cannot become a burst of
// requests at somebody else's server.
func TestAPassIsBounded(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	for _, id := range []string{"wa", "wb", "wc"} {
		w := store.Work{ID: id, UserID: f.user.ID, CreatedAt: time.Now()}
		e := &store.Edition{UserID: f.user.ID, SHA256: "sha-" + id, WorkID: id}
		if err := f.st.CreateWork(ctx, w, e, []store.Identifier{
			{Kind: "sha256", Value: "sha-" + id},
			{Kind: "partial-md5", Value: "md5-" + id + "00000000000000000000000000"},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.st.AppendOps(ctx, f.user.ID, "phone", []store.Op{{
			OpID: "op-" + id, WorkID: id, EditionSHA: ptr("sha-" + id),
			ClientTS: time.Now(), Progression: 0.5, Origin: store.OriginNative,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	f.read("op-1", 0.37, time.Now())

	f.syncer.MaxPerPass = 2
	rep := f.pass()
	if rep.Considered != 2 {
		t.Fatalf("a pass considered %d works with a bound of 2", rep.Considered)
	}
}

// TestACancelledPassStops, so shutting the server down does not wait
// for a whole library.
func TestACancelledPassStops(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.37, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.syncer.Pass(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled pass returned: %v", err)
	}
}
