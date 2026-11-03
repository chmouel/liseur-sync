package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// TestACursorFromASlashedAddressKeepsItsPlace. An earlier build removed
// one trailing slash when it validated a peer address and one more when
// it folded that address into an identity, so a base_url typed with three
// slashes was folded as the root plus one. This build removes every
// slash, which folds that same peer differently: without an alias the
// first pass after an upgrade takes its own cursors for another
// library's and drops the watermarks and the echo mark with them.
func TestACursorFromASlashedAddressKeepsItsPlace(t *testing.T) {
	f := newSyncFixture(t)
	local := f.read("op-1", 0.42, time.Now().Add(-time.Hour))
	peer := newRestampingPeer(t)
	cfg := peerConfig(peer.URL + "///")
	syncer := &Syncer{
		Store: f.st, Peer: "orbit", UserID: f.user.ID,
		Proto:        KosyncProtocol(NewClient(cfg), cfg),
		ActiveWindow: 30 * 24 * time.Hour, MaxPerPass: 100,
	}

	// What that build stored for this address, hashed the way it hashed
	// it: the address with the one slash its validation left on.
	sum := sha256.Sum256([]byte("kosync\n" + peer.URL + "/\nreader"))
	legacy := hex.EncodeToString(sum[:8])
	if legacy == syncer.Proto.Identity() {
		t.Fatal("the earlier folding is the current one, so this proves nothing")
	}
	seen := time.Now().Add(-30 * time.Minute)
	if err := f.st.PutMirrorCursor(context.Background(), f.user.ID, syncer.Peer,
		store.MirrorCursor{
			WorkID: f.work.ID, Document: f.document, PeerIdentity: legacy,
			PushedSeq: local.Seq, RemoteTS: seen.Unix(), PushedMark: "pct:0.42",
		}); err != nil {
		t.Fatal(err)
	}

	rep, err := syncer.Pass(context.Background())
	if err != nil || rep.FirstError != nil {
		t.Fatalf("pass: %s err=%v", rep, err)
	}
	if rep.Pushed != 0 || rep.Pulled != 0 {
		t.Fatalf("an upgraded deployment repeated an exchange: %s", rep)
	}
	cursors, err := f.st.MirrorCursors(context.Background(), f.user.ID, syncer.Peer)
	if err != nil {
		t.Fatal(err)
	}
	cur := cursors[f.work.ID]
	if cur.PeerIdentity != syncer.Proto.Identity() {
		t.Fatalf("the cursor still folds the address the old way: %q", cur.PeerIdentity)
	}
	if cur.PushedSeq != local.Seq || cur.RemoteTS != seen.Unix() || cur.PushedMark != "pct:0.42" {
		t.Fatalf("an upgrade dropped what the cursor remembered: %+v", cur)
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

// gatedPeer is a stub whose first answer is held until the test lets it go,
// so a peer's request can be in flight while another worker of the same
// account finishes its own exchange.
type gatedPeer struct {
	*stubProtocol
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedPeer) Pull(
	ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor,
) (Place, error) {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	return g.stubProtocol.Pull(ctx, c, cur)
}

// TestAPeerAnswerCannotResurrectAStaleLocalPosition is the ordering two
// workers of one account need. Alpha asks its peer before beta has written
// anything and answers after beta has taken a newer position; deciding from
// the snapshot alpha captured would push the old position back over the
// new one. The decision is taken against the op log as it is when the
// answer arrives, under the account lock.
func TestAPeerAnswerCannotResurrectAStaleLocalPosition(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.10, time.Now().Add(-3*time.Hour))

	entered, release := make(chan struct{}), make(chan struct{})
	slow := &gatedPeer{
		stubProtocol: &stubProtocol{who: "peer-a", reply: Place{
			Percentage: 0.30, Device: "web", At: time.Now().Add(-2 * time.Hour),
		}},
		entered: entered, release: release,
	}
	alpha := &Syncer{
		Store: f.st, Peer: "alpha", UserID: f.user.ID, Proto: slow,
		ActiveWindow: 30 * 24 * time.Hour, MaxPerPass: 100,
	}
	beta := &Syncer{
		Store: f.st, Peer: "beta", UserID: f.user.ID,
		Proto: &stubProtocol{who: "peer-b", reply: Place{
			Percentage: 0.60, Device: "web", At: time.Now().Add(-time.Hour),
		}},
		ActiveWindow: 30 * 24 * time.Hour, MaxPerPass: 100,
	}

	done := make(chan Report, 1)
	go func() {
		rep, _ := alpha.Pass(context.Background())
		done <- rep
	}()
	<-entered

	if rep, err := beta.Pass(context.Background()); err != nil || rep.Pulled != 1 {
		t.Fatalf("beta: %s err=%v", rep, err)
	}
	close(release)
	rep := <-done
	if rep.Pulled != 0 {
		t.Fatalf("alpha applied a position over a newer one: %s", rep)
	}
	if rep.Pushed != 1 {
		t.Fatalf("alpha did not offer the newer local position: %s", rep)
	}
	if len(slow.pushed) != 1 || slow.pushed[0].Percentage != 0.60 {
		t.Fatalf("what alpha sent: %+v", slow.pushed)
	}
	ops := f.positions()
	if len(ops) != 2 || ops[0].Progression != 0.60 {
		t.Fatalf("the op log:\n%+v", ops)
	}
	// And the correction is spent, not owed on every pass.
	if rep, err := beta.Pass(context.Background()); err != nil ||
		rep.Pulled+rep.Pushed != 0 {
		t.Fatalf("a settled position crossed again: %s err=%v", rep, err)
	}
	if rep, err := alpha.Pass(context.Background()); err != nil ||
		rep.Pulled+rep.Pushed != 0 {
		t.Fatalf("a settled position crossed again: %s err=%v", rep, err)
	}
}

// gatedPushProtocol holds a peer's first push until the test lets it go,
// so a position can be in flight while another worker of the same account
// finishes its own exchange.
type gatedPushProtocol struct {
	Protocol
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedPushProtocol) Push(
	ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor, place Place,
) error {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	return g.Protocol.Push(ctx, c, cur, place)
}

// TestADelayedEnrichmentPushCannotResurrectAnOlderPosition is the
// interleaving the two-way comparison made newly reachable. Alpha and
// this server hold the same place and only this server names the spot, so
// alpha is owed it. That push is slow. While it is in flight another peer
// moves the reader on, and alpha stamps what it was given with its own
// clock and keeps no device of ours, so the superseded position comes
// back looking like news. Applying it would walk the reader back.
//
// What stops it is the mark: the content of the last push is remembered,
// and a reply that reproduces it is this server's own writing however it
// is stamped and whoever it is attributed to.
func TestADelayedEnrichmentPushCannotResurrectAnOlderPosition(t *testing.T) {
	f := newSyncFixture(t)
	foreign := "/body/DocFragment[3]/body/p[1]"
	op := store.Op{
		OpID: "op-xp", WorkID: f.work.ID, EditionSHA: ptr("sha-w1"),
		ClientTS: time.Now().Add(-3 * time.Hour), Progression: 0.10,
		Origin: store.OriginNative, ForeignPos: &foreign,
	}
	if _, err := f.st.AppendOps(context.Background(), f.user.ID, "phone",
		[]store.Op{op}); err != nil {
		t.Fatal(err)
	}

	alpha := newRestampingPeer(t)
	alpha.set(Position{
		Document: f.document, Progress: "0.1", Percentage: 0.10,
		Device: "peer-web", DeviceID: "peer-web",
		Timestamp: time.Now().Add(-2 * time.Hour).Unix(),
	})
	alphaSync := f.syncerFor("alpha", alpha)
	gate := &gatedPushProtocol{
		Protocol: alphaSync.Proto, entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	alphaSync.Proto = gate
	beta := &Syncer{
		Store: f.st, Peer: "beta", UserID: f.user.ID,
		Proto: &stubProtocol{who: "peer-b", reply: Place{
			Percentage: 0.60, Device: "web", At: time.Now().Add(-time.Hour),
		}},
		ActiveWindow: 30 * 24 * time.Hour, MaxPerPass: 100,
	}

	done := make(chan Report, 1)
	go func() {
		rep, _ := alphaSync.Pass(context.Background())
		done <- rep
	}()
	<-gate.entered

	if rep, err := beta.Pass(context.Background()); err != nil || rep.Pulled != 1 {
		t.Fatalf("beta: %s err=%v", rep, err)
	}
	close(gate.release)
	if rep := <-done; rep.Pushed != 1 || rep.Pulled != 0 {
		t.Fatalf("alpha: %s", rep)
	}
	if pushes := alpha.pushed(t); len(pushes) != 1 || pushes[0].Percentage != 0.10 {
		t.Fatalf("what the slow push carried: %+v", pushes)
	}

	// Alpha now answers with this server's own superseded position,
	// restamped with its clock and attributed to its web reader.
	if rep, err := alphaSync.Pass(context.Background()); err != nil || rep.FirstError != nil {
		t.Fatalf("alpha: %s err=%v", rep, err)
	} else if rep.Pulled != 0 {
		t.Fatalf("a restamped copy of a superseded position was applied: %s", rep)
	} else if rep.Pushed != 1 {
		t.Fatalf("the newer position was not sent to alpha: %s", rep)
	}
	if pushes := alpha.pushed(t); len(pushes) != 2 || pushes[1].Percentage != 0.60 {
		t.Fatalf("what alpha has been told: %+v", pushes)
	}
	ops := f.positions()
	if len(ops) != 2 || ops[0].Progression != 0.60 {
		t.Fatalf("the op log:\n%+v", ops)
	}

	// And the exchange is spent, not repeated on every poll.
	if rep, err := alphaSync.Pass(context.Background()); err != nil ||
		rep.Pulled+rep.Pushed != 0 {
		t.Fatalf("a settled position crossed again: %s err=%v", rep, err)
	}
	if ops := f.positions(); len(ops) != 2 {
		t.Fatalf("polling added ops:\n%+v", ops)
	}
}

// lockWatchingStore holds the account lock's owner to what it is for: the
// database calls that read and commit a decision, and nothing else.
type lockWatchingStore struct {
	store.Store
	t    *testing.T
	lock *sync.Mutex
}

func (s *lockWatchingStore) assertHeld(what string) {
	s.t.Helper()
	if s.lock.TryLock() {
		s.lock.Unlock()
		s.t.Errorf("%s ran outside the account lock", what)
	}
}

func (s *lockWatchingStore) Positions(
	ctx context.Context, userID, workID string, limit int,
) ([]store.Op, error) {
	s.assertHeld("Positions")
	return s.Store.Positions(ctx, userID, workID, limit)
}

func (s *lockWatchingStore) AppendKosyncOp(
	ctx context.Context, userID, partialMD5, deviceID string, op store.Op,
) (store.OpResult, error) {
	s.assertHeld("AppendKosyncOp")
	return s.Store.AppendKosyncOp(ctx, userID, partialMD5, deviceID, op)
}

// lockWatchingProtocol holds the network calls to the other side of the
// same rule: a request may not be made while the account lock is held.
type lockWatchingProtocol struct {
	*stubProtocol
	t    *testing.T
	lock *sync.Mutex
}

func (p *lockWatchingProtocol) assertFree(what string) {
	p.t.Helper()
	if !p.lock.TryLock() {
		p.t.Errorf("%s ran while the account lock was held", what)
		return
	}
	p.lock.Unlock()
}

func (p *lockWatchingProtocol) Pull(
	ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor,
) (Place, error) {
	p.assertFree("Pull")
	return p.stubProtocol.Pull(ctx, c, cur)
}

func (p *lockWatchingProtocol) Push(
	ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor, place Place,
) error {
	p.assertFree("Push")
	return p.stubProtocol.Push(ctx, c, cur, place)
}

// TestTheAccountLockSpansTheDatabaseCallsOnly keeps the lock from becoming
// a serialisation of the network: one slow peer would stall every other
// worker of the account for as long as its timeout.
func TestTheAccountLockSpansTheDatabaseCallsOnly(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.10, time.Now().Add(-time.Hour))
	lock := accountLock(f.user.ID)
	stub := &stubProtocol{
		who: "peer-b", reply: Place{Percentage: 0.60, Device: "web", At: time.Now()},
	}
	f.syncer.Store = &lockWatchingStore{Store: f.st, t: t, lock: lock}
	f.syncer.Proto = &lockWatchingProtocol{stubProtocol: stub, t: t, lock: lock}

	if rep := f.pass(); rep.Pulled != 1 {
		t.Fatalf("report: %s", rep)
	}
	f.read("op-2", 0.90, time.Now().Add(time.Minute))
	if rep := f.pass(); rep.Pushed != 1 {
		t.Fatalf("report: %s", rep)
	}
}

// cfiRestampingPeer keeps what it is sent, stamps it with its own clock and
// answers with it, like a real peer. It can carry a CFI.
type cfiRestampingPeer struct {
	mu     sync.Mutex
	reply  Place
	pushes int
	who    string
}

func (p *cfiRestampingPeer) Name() string                  { return "stub" }
func (p *cfiRestampingPeer) Identity() string              { return p.who }
func (*cfiRestampingPeer) Authorize(context.Context) error { return nil }
func (*cfiRestampingPeer) Close(context.Context)           {}
func (*cfiRestampingPeer) carriesCFI() bool                { return true }

func (p *cfiRestampingPeer) Pull(
	context.Context, store.MirrorCandidate, *store.MirrorCursor,
) (Place, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reply, nil
}

func (p *cfiRestampingPeer) Push(
	_ context.Context, _ store.MirrorCandidate, _ *store.MirrorCursor, place Place,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pushes++
	place.At = time.Now()
	place.Device = "web"
	p.reply = place
	return nil
}

func (p *cfiRestampingPeer) pushCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pushes
}

// TestAFinerLocalSpotCrossesOnceAndSettles is the loop check for the
// two-way comparison: a CFI-capable peer and a kosync peer of one account
// must both end up holding the finer spot, and neither may keep answering
// it as news.
func TestAFinerLocalSpotCrossesOnceAndSettles(t *testing.T) {
	f := newSyncFixture(t)
	cfi := "epubcfi(/6/8!/4/2:41)"
	appendLocatorOp(t, f, cfi, 0.37, time.Now().Add(-time.Hour))

	alpha := &cfiRestampingPeer{who: "peer-a", reply: Place{
		Percentage: 0.37, Device: "web", At: time.Now().Add(-30 * time.Minute),
	}}
	beta := newRestampingPeer(t)
	syncers := []*Syncer{
		{
			Store: f.st, Peer: "alpha", UserID: f.user.ID, Proto: alpha,
			ActiveWindow: 30 * 24 * time.Hour, MaxPerPass: 100,
		},
		f.syncerFor("beta", beta),
	}
	for pass := 0; pass < 10; pass++ {
		s := syncers[pass%2]
		rep, err := s.Pass(context.Background())
		if err != nil || rep.FirstError != nil {
			t.Fatalf("pass %d (%s): %s err=%v", pass, s.Peer, rep, err)
		}
	}
	if ops := f.positions(); len(ops) != 1 {
		t.Fatalf("%d ops for one position after ten passes:\n%+v", len(ops), ops)
	}
	if got := alpha.pushCount(); got != 1 {
		t.Fatalf("the CFI-capable peer was told %d times, want 1", got)
	}
	if got := len(beta.pushed(t)); got != 1 {
		t.Fatalf("the kosync peer was told %d times, want 1", got)
	}
}
