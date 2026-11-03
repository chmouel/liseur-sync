package mirror

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// restampingPeer is a kosync server that keeps a position per document
// and stamps every write with its own clock, and whose replies do not
// carry the writer's device id — which is what a peer reached through a
// route of its own does. Two of them in front of one account is the
// shape that used to make a mirror write an op on every pass.
type restampingPeer struct {
	*httptest.Server

	mu        sync.Mutex
	positions map[string]Position
	pushes    []Position
}

func newRestampingPeer(t *testing.T) *restampingPeer {
	t.Helper()
	p := &restampingPeer{positions: map[string]Position{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/auth", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"authorized": "OK"})
	})
	mux.HandleFunc("PUT /syncs/progress", func(w http.ResponseWriter, r *http.Request) {
		var got Position
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
			return
		}
		p.mu.Lock()
		got.Timestamp = time.Now().Unix()
		// A route that does not keep the device fields: there is nothing
		// left in the reply to say this server wrote it.
		got.Device, got.DeviceID = "peer-web", "peer-web"
		p.positions[got.Document] = got
		p.pushes = append(p.pushes, got)
		p.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"document": got.Document})
	})
	mux.HandleFunc("GET /syncs/progress/{document}", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		stored, ok := p.positions[r.PathValue("document")]
		p.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
		writeJSON(w, http.StatusOK, stored)
	})
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

// set stores a position as if somebody had read to it there.
func (p *restampingPeer) set(position Position) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.positions[position.Document] = position
}

func (p *restampingPeer) pushed(t *testing.T) []Position {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Position(nil), p.pushes...)
}

// syncerFor builds a second mirror beside the fixture's own: same
// account, same book, a peer of its own.
func (f *syncFixture) syncerFor(name string, peer *restampingPeer) *Syncer {
	return &Syncer{
		Store: f.st, Peer: name, UserID: f.user.ID,
		Proto:        KosyncProtocol(NewClient(peerConfig(peer.URL)), peerConfig(peer.URL)),
		ActiveWindow: 30 * 24 * time.Hour, MaxPerPass: 100,
	}
}

func peerConfig(url string) config.MirrorConfig {
	return config.MirrorConfig{
		Protocol: config.ProtocolKosync, BaseURL: url, RemoteUser: "reader",
		RemoteKey: "0123456789abcdef0123456789abcdef",
		DeviceID:  "liseur-sync", Timeout: config.Duration(20 * time.Second),
	}
}

// TestTwoRestampingPeersDoNotFeedEachOtherForever is the regression that
// several named mirrors makes necessary.
//
// A position filed here goes out to one peer, and a peer that stamps it
// with its own clock and does not keep the writer's device gives the
// mirror nothing to recognise it by. With a second peer holding the same
// library the two keep presenting each other's copy as news, and because
// applying it writes an op that is newer than what the peer has, each
// pass would push it again. The op log grows by one row per pass per
// book until somebody notices.
//
// What stops it is the agreement rule: a peer repeating the place this
// server already holds is not an update, however it is stamped.
func TestTwoRestampingPeersDoNotFeedEachOtherForever(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.37, time.Now())

	alpha, beta := newRestampingPeer(t), newRestampingPeer(t)
	syncers := []*Syncer{f.syncerFor("alpha", alpha), f.syncerFor("beta", beta)}

	for pass := 0; pass < 10; pass++ {
		s := syncers[pass%2]
		rep, err := s.Pass(context.Background())
		if err != nil {
			t.Fatalf("pass %d (%s): %v", pass, s.Peer, err)
		}
		if rep.FirstError != nil {
			t.Fatalf("pass %d (%s): %v", pass, s.Peer, rep.FirstError)
		}
		// The first pass to each peer is the position going out; from
		// the third pass on the two are looking at copies of it.
		if pass > 1 && rep.Pulled+rep.Pushed != 0 {
			t.Fatalf("pass %d (%s) crossed a settled position: %s", pass, s.Peer, rep)
		}
	}

	if ops := f.positions(); len(ops) != 1 {
		t.Fatalf("%d ops for one position after ten passes:\n%+v", len(ops), ops)
	}
	// Each peer was told once, and never again.
	if got := len(alpha.pushed(t)); got != 1 {
		t.Fatalf("%d pushes to alpha, want 1", got)
	}
	if got := len(beta.pushed(t)); got != 1 {
		t.Fatalf("%d pushes to beta, want 1", got)
	}
	// And each keeps its own bookkeeping: a name keys the cursors, so
	// two peers never share one.
	for _, name := range []string{"alpha", "beta"} {
		cursors, err := f.st.MirrorCursors(context.Background(), f.user.ID, name)
		if err != nil {
			t.Fatal(err)
		}
		if cursors[f.work.ID].PushedSeq == 0 {
			t.Fatalf("no cursor for %s: %+v", name, cursors)
		}
	}
}

// TestAPositionCrossesFromOnePeerToTheNext is the reason forwarding is
// kept: two peers of one account stay in step through this server, and
// the position learned from one is offered to the other exactly once.
func TestAPositionCrossesFromOnePeerToTheNext(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.10, time.Now().Add(-3*time.Hour))

	alpha, beta := newRestampingPeer(t), newRestampingPeer(t)
	alpha.set(Position{
		Document: f.document, Progress: "/body/DocFragment[7]/body",
		Percentage: 0.61, Device: "kindle", DeviceID: "kindle-oasis",
		Timestamp: time.Now().Add(-time.Hour).Unix(),
	})
	beta.set(Position{
		Document: f.document, Progress: "/body/DocFragment[2]/body",
		Percentage: 0.20, Device: "kobo", DeviceID: "kobo-libra",
		Timestamp: time.Now().Add(-2 * time.Hour).Unix(),
	})
	fromAlpha := f.syncerFor("alpha", alpha)
	toBeta := f.syncerFor("beta", beta)

	if rep, err := fromAlpha.Pass(context.Background()); err != nil || rep.Pulled != 1 {
		t.Fatalf("alpha: %s err=%v", rep, err)
	}
	if rep, err := toBeta.Pass(context.Background()); err != nil || rep.Pushed != 1 {
		t.Fatalf("beta: %s err=%v", rep, err)
	}
	pushes := beta.pushed(t)
	if len(pushes) != 1 || pushes[0].Percentage != 0.61 {
		t.Fatalf("what beta was told: %+v", pushes)
	}
	if pushes[0].Progress != "/body/DocFragment[7]/body" {
		t.Fatalf("the engine position was not forwarded: %+v", pushes[0])
	}

	// From here the two agree, and nothing else crosses.
	before := len(f.positions())
	for _, s := range []*Syncer{fromAlpha, toBeta, fromAlpha, toBeta} {
		rep, err := s.Pass(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if rep.Pulled+rep.Pushed != 0 {
			t.Fatalf("%s crossed a settled position: %s", s.Peer, rep)
		}
	}
	if after := len(f.positions()); after != before {
		t.Fatalf("op count went from %d to %d", before, after)
	}
	if got := len(beta.pushed(t)); got != 1 {
		t.Fatalf("beta was told %d times", got)
	}
}

// TestTwoEntriesOnOneLibraryAgreeAfterTheFirstCrossing is the other
// shape of the same mistake: both mirrors reach the same logical
// library, so every reply is the other entry's writing.
func TestTwoEntriesOnOneLibraryAgreeAfterTheFirstCrossing(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.42, time.Now())

	library := newRestampingPeer(t)
	first := f.syncerFor("alpha", library)
	second := f.syncerFor("beta", library)

	for pass := 0; pass < 6; pass++ {
		if rep, err := first.Pass(context.Background()); err != nil || rep.FirstError != nil {
			t.Fatalf("alpha pass %d: %s err=%v", pass, rep, err)
		}
		if rep, err := second.Pass(context.Background()); err != nil || rep.FirstError != nil {
			t.Fatalf("beta pass %d: %s err=%v", pass, rep, err)
		}
	}
	if ops := f.positions(); len(ops) != 1 {
		t.Fatalf("%d ops for one position on one library:\n%+v", len(ops), ops)
	}
}

// TestASamePercentageOnADifferentSpotIsStillAnUpdate. The agreement rule
// compares the finest thing both sides hold, not the fraction, because
// two places at the same percent of a book are not the same place. A
// guard that looked only at the fraction would sit on this reply forever.
func TestASamePercentageOnADifferentSpotIsStillAnUpdate(t *testing.T) {
	f := newSyncFixture(t)
	op := store.Op{
		OpID: "op-1", WorkID: f.work.ID, EditionSHA: ptr("sha-w1"),
		ClientTS: time.Now().Add(-time.Hour), Progression: 0.55,
		Origin:      store.OriginNative,
		LocatorJSON: locatorWithCFI("epubcfi(/6/8!/4/2:3)", 0.55),
	}
	if _, err := f.st.AppendOps(context.Background(), f.user.ID, "phone",
		[]store.Op{op}); err != nil {
		t.Fatal(err)
	}
	stub := &stubProtocol{reply: Place{
		Percentage: 0.55, CFI: "epubcfi(/6/8!/4/2:41)", Device: "web", At: time.Now(),
	}}
	f.with(stub)
	if rep := f.pass(); rep.Pulled != 1 {
		t.Fatalf("a different spot at the same percentage was taken as agreement: %s", rep)
	}
}

// TestAnEnginePositionAtTheSamePercentageIsStillAnUpdate is the same
// rule for the protocol that carries an engine position rather than a
// CFI.
func TestAnEnginePositionAtTheSamePercentageIsStillAnUpdate(t *testing.T) {
	f := newSyncFixture(t)
	foreign := "/body/DocFragment[3]/body/p[1]"
	op := store.Op{
		OpID: "op-1", WorkID: f.work.ID, EditionSHA: ptr("sha-w1"),
		ClientTS: time.Now().Add(-time.Hour), Progression: 0.48,
		Origin: store.OriginNative, ForeignPos: &foreign,
	}
	if _, err := f.st.AppendOps(context.Background(), f.user.ID, "phone",
		[]store.Op{op}); err != nil {
		t.Fatal(err)
	}
	f.with(&stubProtocol{reply: Place{
		Percentage: 0.48, Foreign: "/body/DocFragment[4]/body/p[9]",
		Device: "kobo", At: time.Now(),
	}})
	if rep := f.pass(); rep.Pulled != 1 {
		t.Fatalf("a different engine position at the same percentage was dropped: %s", rep)
	}
}

// TestACursorFromTheSingleEntryKeepsItsPlace. A deployment that upgrades
// keeps its [mirror] entry, and the cursor the old process wrote is
// keyed by that entry's name and stamped with its peer, so the first pass
// after the upgrade must find nothing left to do.
func TestACursorFromTheSingleEntryKeepsItsPlace(t *testing.T) {
	f := newSyncFixture(t)
	local := f.read("op-1", 0.42, time.Now().Add(-time.Hour))
	seen := time.Now().Add(-30 * time.Minute)
	stub := &stubProtocol{reply: Place{
		Percentage: 0.42, Foreign: "0.42", Device: "web", At: seen,
	}}
	f.with(stub)
	if err := f.st.PutMirrorCursor(context.Background(), f.user.ID, f.syncer.Peer,
		store.MirrorCursor{
			WorkID: f.work.ID, Document: f.document, PeerIdentity: stub.Identity(),
			PushedSeq: local.Seq, RemoteTS: seen.Unix(),
		}); err != nil {
		t.Fatal(err)
	}
	rep := f.pass()
	if rep.Pushed != 0 || rep.Pulled != 0 {
		t.Fatalf("an upgraded deployment repeated an exchange: %s", rep)
	}
	if got := len(stub.pushed); got != 0 {
		t.Fatalf("an upgraded deployment pushed %d positions", got)
	}
}

// TestATieOnTheFractionStillLosesToTheExactSpot is the ordering the
// agreement rule has to respect: it is checked before newest-wins, so a
// peer that agrees about the place must not have its finer answer
// suppressed by a timestamp comparison.
func TestATieOnTheFractionStillLosesToTheExactSpot(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.30, time.Now().Add(-time.Hour))
	stub := &stubProtocol{}
	f.with(stub)
	f.pass()

	// The reader moves a little on the peer: a fraction that rounds to a
	// different place, so it is news.
	stub.reply = Place{Percentage: 0.31, Device: "web", At: time.Now()}
	if rep := f.pass(); rep.Pulled != 1 {
		t.Fatalf("a moved position was not taken: %s", rep)
	}
	if got := strconv.FormatFloat(f.positions()[0].Progression, 'f', 2, 64); got != "0.31" {
		t.Fatalf("newest progression: %s", got)
	}
}

// TestACFIThePeerJustLearnedIsNewsAtTheSamePercentage. Fraction equality is
// agreement only when neither side names a finer spot. A peer at 0.55 holding
// no CFI here and a CFI there is offering a locator this server does not have;
// dropping it as "the same place" loses it on every poll, which is worse than
// what newest-wins did before the agreement rule existed.
func TestACFIThePeerJustLearnedIsNewsAtTheSamePercentage(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.55, time.Now().Add(-time.Hour))
	exact := "epubcfi(/6/8!/4/2:41)"
	f.with(&stubProtocol{reply: Place{
		Percentage: 0.55, CFI: exact, Device: "web", At: time.Now(),
	}})
	if rep := f.pass(); rep.Pulled != 1 {
		t.Fatalf("a newly available CFI was dropped as agreement: %s", rep)
	}
	ops := f.positions()
	if got := cfiFromLocator(ops[0].LocatorJSON); got != exact {
		t.Fatalf("the locator the peer named was not preserved: %q", got)
	}
	// And with the locator held here the next poll is agreement, so the
	// multi-peer loop still settles.
	if rep := f.pass(); rep.Pulled != 0 || rep.Pushed != 0 {
		t.Fatalf("a position with both locators moved again: %s", rep)
	}
}

// TestAnEnginePositionThePeerJustLearnedIsNewsAtTheSamePercentage is the same
// rule for the protocol that carries an engine position rather than a CFI.
func TestAnEnginePositionThePeerJustLearnedIsNewsAtTheSamePercentage(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.48, time.Now().Add(-time.Hour))
	exact := "/body/DocFragment[4]/body/p[9]"
	f.with(&stubProtocol{reply: Place{
		Percentage: 0.48, Foreign: exact, Device: "kobo", At: time.Now(),
	}})
	if rep := f.pass(); rep.Pulled != 1 {
		t.Fatalf("a newly available engine position was dropped as agreement: %s", rep)
	}
	ops := f.positions()
	if ops[0].ForeignPos == nil || *ops[0].ForeignPos != exact {
		t.Fatalf("the engine position the peer named was not preserved: %+v", ops[0])
	}
	if rep := f.pass(); rep.Pulled != 0 || rep.Pushed != 0 {
		t.Fatalf("a position with both engine positions moved again: %s", rep)
	}
}
