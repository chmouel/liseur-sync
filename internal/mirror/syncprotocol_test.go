package mirror

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// stubProtocol is a peer with no wire at all. It exists for the
// decisions the syncer makes on its own — what a protocol's answer
// means, rather than how any protocol arrives at one — so that those
// can be stated without a server, a credential or a catalog behind
// them.
type stubProtocol struct {
	reply    Place
	pullErr  error
	pushed   []Place
	pushErr  error
	pullSeen int
	// who is which peer this stub claims to be. Empty is the identity
	// a fixture's cursors are already stamped with.
	who string
}

func (s *stubProtocol) Name() string                  { return "stub" }
func (s *stubProtocol) Identity() string              { return s.who }
func (*stubProtocol) Authorize(context.Context) error { return nil }
func (*stubProtocol) Close(context.Context)           {}
func (s *stubProtocol) Pull(
	context.Context, store.MirrorCandidate, *store.MirrorCursor,
) (Place, error) {
	s.pullSeen++
	return s.reply, s.pullErr
}

func (s *stubProtocol) Push(
	_ context.Context, _ store.MirrorCandidate, _ *store.MirrorCursor, p Place,
) error {
	if s.pushErr != nil {
		return s.pushErr
	}
	s.pushed = append(s.pushed, p)
	return nil
}

func (f *syncFixture) with(p Protocol) *syncFixture {
	f.syncer.Proto = p
	return f
}

// TestABookThePeerCannotIdentifyIsNotAFailure. Two libraries overlap
// partially and a protocol that has to find a book by searching for it
// will often not find one. Counting that as a failure would put a
// mirror that is working correctly into permanent backoff and fill the
// log with warnings about books nobody expected to cross.
func TestABookThePeerCannotIdentifyIsNotAFailure(t *testing.T) {
	f := newSyncFixture(t)
	stub := &stubProtocol{pullErr: ErrNotOnPeer}
	f.with(stub)
	f.read("op-1", 0.42, time.Now())

	rep := f.pass()
	if rep.Failed != 0 || rep.FirstError != nil {
		t.Fatalf("a book the peer does not hold counted as a failure: %s", rep)
	}
	if rep.Skipped != 1 || rep.Pushed != 0 {
		t.Fatalf("report: %s", rep)
	}
	if len(stub.pushed) != 0 {
		t.Fatalf("pushed to a peer that cannot name the book: %+v", stub.pushed)
	}
	if got := f.cursor().LastError; got != "" {
		t.Fatalf("recorded an error: %q", got)
	}
}

// TestAStartOfBookPositionNeverDisplacesARealOne. The KOReader path
// recognises a reset because BookOrbit answers it with a synthetic
// reply that says so. A protocol whose reply carries no such tell has
// only the position itself to go on, and the cost of being wrong is a
// reader sent back to page one on every poll for as long as the reset
// stands.
func TestAStartOfBookPositionNeverDisplacesARealOne(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.42, time.Now().Add(-time.Hour))
	f.with(&stubProtocol{reply: Place{
		Percentage: 0, Device: "web", At: time.Now(),
	}})

	rep := f.pass()
	if rep.Pulled != 0 {
		t.Fatalf("a start-of-book position was applied: %s", rep)
	}
	for _, op := range f.positions() {
		if op.Progression == 0 {
			t.Fatalf("page one landed in the log: %+v", op)
		}
	}
	// It is still recorded as seen, so the same reply does not get
	// reconsidered from scratch on every poll.
	if f.cursor().RemoteTS == 0 {
		t.Fatalf("cursor: %+v", f.cursor())
	}
}

// TestAStartOfBookPositionIsTakenWhenThereIsNothingToLose. The guard
// above protects a position; it must not stop a reader who genuinely
// is at the beginning from being where they are.
func TestAStartOfBookPositionIsTakenWhenThereIsNothingToLose(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0, time.Now().Add(-time.Hour))
	f.with(&stubProtocol{reply: Place{
		Percentage: 0, Device: "web", At: time.Now(),
	}})
	if rep := f.pass(); rep.Pulled != 1 {
		t.Fatalf("report: %s", rep)
	}
}

// TestTheExactSpotFromThePeerIsStoredWhereTheReaderLooksForIt. This is
// the whole point of the native protocol on this side. The reader's
// restore path offers a stored spot before anything else, so a position
// that arrives with one reopens on the sentence rather than the
// fraction — and it has to be in the field that path reads.
func TestTheExactSpotFromThePeerIsStoredWhereTheReaderLooksForIt(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.10, time.Now().Add(-time.Hour))
	cfi := "epubcfi(/6/14!/4/2/8:37)"
	f.with(&stubProtocol{reply: Place{
		Percentage: 0.62, CFI: cfi, Device: "web", At: time.Now(),
	}})

	if rep := f.pass(); rep.Pulled != 1 {
		t.Fatalf("report: %s", rep)
	}
	latest := f.positions()[0]
	if got := cfiFromLocator(latest.LocatorJSON); got != cfi {
		t.Fatalf("the exact spot is not where the reader looks: %q in %s",
			got, latest.LocatorJSON)
	}
	if latest.Progression != 0.62 {
		t.Fatalf("progression: %v", latest.Progression)
	}
	// It is still an ordinary kosync-origin op filed under the peer, so
	// statistics, rollups and compaction never learn the mirror exists.
	if latest.Origin != store.OriginKosync {
		t.Fatalf("origin: %q", latest.Origin)
	}
	if !strings.HasPrefix(latest.DeviceID, "orbit:") {
		t.Fatalf("device: %q", latest.DeviceID)
	}
}

// TestTheExactSpotFromHereGoesOut. The local side of the same thing:
// what this server's own reader recorded has to reach the protocol,
// not be dropped on the way to it.
func TestTheExactSpotFromHereGoesOut(t *testing.T) {
	f := newSyncFixture(t)
	cfi := "epubcfi(/6/22!/4/2/2:11)"
	op := store.Op{
		OpID: "op-cfi", WorkID: f.work.ID, EditionSHA: ptr("sha-w1"),
		ClientTS: time.Now(), Progression: 0.44, Origin: store.OriginNative,
		LocatorJSON: []byte(`{"href":"ch3.xhtml","locations":{"fragments":["` +
			cfi + `"],"totalProgression":0.44}}`),
	}
	if _, err := f.st.AppendOps(context.Background(), f.user.ID, "phone",
		[]store.Op{op}); err != nil {
		t.Fatal(err)
	}
	stub := &stubProtocol{}
	f.with(stub)

	if rep := f.pass(); rep.Pushed != 1 {
		t.Fatalf("report: %s", rep)
	}
	if len(stub.pushed) != 1 || stub.pushed[0].CFI != cfi {
		t.Fatalf("the exact spot did not reach the protocol: %+v", stub.pushed)
	}
}

// TestAPositionTakenFromThePeerIsNeverSentBack. Without this the two
// servers would trade one position back and forth forever, each one
// seeing the other's copy as news.
func TestAPositionTakenFromThePeerIsNeverSentBack(t *testing.T) {
	f := newSyncFixture(t)
	// A book is only polled at all because somebody has been reading
	// it here recently, so the crossing always starts from a local
	// position the peer has since overtaken.
	f.read("op-1", 0.20, time.Now().Add(-2*time.Hour))
	stub := &stubProtocol{reply: Place{
		Percentage: 0.55, CFI: "epubcfi(/6/8!/4/2:3)", Device: "web",
		At: time.Now(),
	}}
	f.with(stub)
	if rep := f.pass(); rep.Pulled != 1 {
		t.Fatalf("first pass: %s", rep)
	}
	// Second pass: the peer still says the same thing, and the only
	// local position is the one we took from it.
	if rep := f.pass(); rep.Pushed != 0 {
		t.Fatalf("second pass sent the peer its own position back: %s", rep)
	}
	if len(stub.pushed) != 0 {
		t.Fatalf("pushed: %+v", stub.pushed)
	}
}

// TestAChangeOfPeerIsNotAContinuationOfTheOldOne. A mirror is named
// once by an operator, and that name survives editing the address or
// the account under it. Everything a cursor holds, though, is about one
// particular library: how far the two sides had got, what the peer
// called this book, what was last said to it. Carried across a change
// of peer, each of those is wrong in a way that shows up as silence
// rather than as an error.
func TestAChangeOfPeerIsNotAContinuationOfTheOldOne(t *testing.T) {
	t.Run("a replacement peer that has nothing is told where we are", func(t *testing.T) {
		f := newSyncFixture(t)
		f.read("op-1", 0.42, time.Now())
		first := &stubProtocol{who: "peer-a", pullErr: ErrNoPosition}
		f.with(first)
		if rep := f.pass(); rep.Pushed != 1 {
			t.Fatalf("first peer: %s", rep)
		}

		// Same mirror name, different library. The old cursor says
		// this position has already been sent, which was true of
		// somebody else's server.
		second := &stubProtocol{who: "peer-b", pullErr: ErrNoPosition}
		f.with(second)
		if rep := f.pass(); rep.Pushed != 1 {
			t.Fatalf("a replacement peer was never told the position: %s", rep)
		}
		if len(second.pushed) != 1 || second.pushed[0].Percentage != 0.42 {
			t.Fatalf("pushed: %+v", second.pushed)
		}
		if got := f.cursor().PeerIdentity; got != "peer-b" {
			t.Fatalf("the cursor is still about the old peer: %q", got)
		}
	})

	t.Run("a replacement peer's position is not already spent", func(t *testing.T) {
		f := newSyncFixture(t)
		local := f.read("op-1", 0.10, time.Now().Add(-48*time.Hour))
		// The state a long-running mirror against the old peer leaves
		// behind: it was read on there recently, so the cursor
		// remembers a recent clock, and the local position has already
		// been sent.
		if err := f.st.PutMirrorCursor(
			context.Background(), f.user.ID, f.syncer.Peer, store.MirrorCursor{
				WorkID:       f.work.ID,
				Document:     f.document,
				PeerIdentity: "peer-a",
				PushedSeq:    local.Seq,
				RemoteTS:     time.Now().Unix(),
			}); err != nil {
			t.Fatal(err)
		}

		// The replacement is a different library, and its position is
		// newer than anything read here but older than what the old
		// peer's clock had reached. Measured against that clock it
		// looks like something already taken, and the reader would
		// never see it.
		hourAgo := time.Now().Add(-time.Hour)
		second := &stubProtocol{who: "peer-b", reply: Place{
			Percentage: 0.33, Device: "kobo", At: hourAgo,
		}}
		f.with(second)
		if rep := f.pass(); rep.Pulled != 1 {
			t.Fatalf("a replacement peer's position was thrown away: %s", rep)
		}
		if got := f.positions()[0].Progression; got != 0.33 {
			t.Fatalf("newest position is %v", got)
		}
	})

	t.Run("a peer that does not hold the book yet has not had its turn", func(t *testing.T) {
		f := newSyncFixture(t)
		f.read("op-1", 0.10, time.Now().Add(-2*time.Hour))
		f.with(&stubProtocol{who: "peer-a", reply: Place{
			Percentage: 0.55, Device: "web", At: time.Now(),
		}})
		if rep := f.pass(); rep.Pulled != 1 {
			t.Fatalf("first peer: %s", rep)
		}

		// The replacement library does not hold this book on the day
		// it is switched to. That is the ordinary case and not a
		// failure, but it is also not an exchange: nothing has crossed
		// and the position from the old peer is still owed.
		second := &stubProtocol{who: "peer-b", pullErr: ErrNotOnPeer}
		f.with(second)
		for range 3 {
			if rep := f.pass(); rep.Skipped != 1 {
				t.Fatalf("a book the peer does not hold: %s", rep)
			}
		}
		// A transient failure is no different.
		second.pullErr = errors.New("peer is down")
		if rep, err := f.syncer.Pass(context.Background()); err != nil {
			t.Fatal(err)
		} else if rep.Failed != 1 {
			t.Fatalf("an outage: %s", rep)
		}

		// Somebody adds the book to the new library. It is still owed
		// the position, months of failed lookups later.
		second.pullErr = ErrNoPosition
		if rep := f.pass(); rep.Pushed != 1 {
			t.Fatalf("the position was lost to a first pass that found nothing: %s", rep)
		}
		if len(second.pushed) != 1 || second.pushed[0].Percentage != 0.55 {
			t.Fatalf("pushed: %+v", second.pushed)
		}
		// Exactly once: now it is the peer's own position.
		if rep := f.pass(); rep.Pushed != 0 {
			t.Fatalf("pushed again: %s", rep)
		}
		if len(second.pushed) != 1 {
			t.Fatalf("pushed: %+v", second.pushed)
		}
	})

	t.Run("a push that failed is still owed", func(t *testing.T) {
		f := newSyncFixture(t)
		f.read("op-1", 0.10, time.Now().Add(-4*time.Hour))
		f.with(&stubProtocol{who: "peer-a", reply: Place{
			Percentage: 0.55, Device: "web", At: time.Now().Add(-time.Hour),
		}})
		if rep := f.pass(); rep.Pulled != 1 {
			t.Fatalf("first peer: %s", rep)
		}

		// The replacement library holds the book and has an older
		// position in it, so the local one — which came from peer A —
		// is what should cross. The push fails.
		second := &stubProtocol{
			who:     "peer-b",
			reply:   Place{Percentage: 0.20, Device: "kobo", At: time.Now().Add(-3 * time.Hour)},
			pushErr: errors.New("peer is down"),
		}
		f.with(second)
		rep, err := f.syncer.Pass(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if rep.Failed != 1 {
			t.Fatalf("a failed push: %s", rep)
		}
		if len(second.pushed) != 0 {
			t.Fatalf("pushed: %+v", second.pushed)
		}

		// The peer comes back. The position is still owed: the peer
		// answering is not the same as the two sides having exchanged
		// anything.
		second.pushErr = nil
		if rep := f.pass(); rep.Pushed != 1 {
			t.Fatalf("a position was lost to a failed first push: %s", rep)
		}
		if len(second.pushed) != 1 || second.pushed[0].Percentage != 0.55 {
			t.Fatalf("pushed: %+v", second.pushed)
		}
		if rep := f.pass(); rep.Pushed != 0 {
			t.Fatalf("pushed again: %s", rep)
		}
	})

	t.Run("a position learned from one peer is offered to the next", func(t *testing.T) {
		f := newSyncFixture(t)
		f.read("op-1", 0.10, time.Now().Add(-2*time.Hour))
		f.with(&stubProtocol{who: "peer-a", reply: Place{
			Percentage: 0.55, Device: "web", At: time.Now(),
		}})
		if rep := f.pass(); rep.Pulled != 1 {
			t.Fatalf("first peer: %s", rep)
		}
		// The newest local position is now filed under the peer's
		// device prefix. Sending it back to the peer it came from
		// would be a loop; to a library that has never seen it, it is
		// simply where the reader is.
		if !strings.HasPrefix(f.positions()[0].DeviceID, "orbit:") {
			t.Fatalf("expected a position filed under the peer: %+v", f.positions()[0])
		}
		second := &stubProtocol{who: "peer-b", pullErr: ErrNoPosition}
		f.with(second)
		if rep := f.pass(); rep.Pushed != 1 {
			t.Fatalf("the replacement peer was left behind: %s", rep)
		}
		if len(second.pushed) != 1 || second.pushed[0].Percentage != 0.55 {
			t.Fatalf("pushed: %+v", second.pushed)
		}
		// And it is still not sent back to the peer it came from.
		third := &stubProtocol{who: "peer-b", pullErr: ErrNoPosition}
		f.with(third)
		if rep := f.pass(); rep.Pushed != 0 {
			t.Fatalf("the same peer was told its own position again: %s", rep)
		}
	})
}
