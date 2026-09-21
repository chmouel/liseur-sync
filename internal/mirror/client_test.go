package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
)

// fakePeer is a kosync server the size of the protocol: the three calls
// this client makes, a credential it checks, and a record of what it
// was told. It stands in for BookOrbit in every test here and in the
// mirror's own tests, so the shapes are asserted once, in one place.
type fakePeer struct {
	*httptest.Server

	user, key string

	mu        sync.Mutex
	positions map[string]Position
	pushes    []Position
	requests  []*http.Request
	// answer, when set, replaces the stored position on the next pull.
	answer *Position
	// status, when non-zero, is returned instead of answering at all.
	status int
	// body, when set, is returned verbatim with status 200.
	body string
}

func newFakePeer(t *testing.T) *fakePeer {
	t.Helper()
	p := &fakePeer{
		user:      "reader",
		key:       "0123456789abcdef0123456789abcdef",
		positions: map[string]Position{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/auth", p.guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"authorized": "OK", "username": p.user})
	}))
	mux.HandleFunc("PUT /syncs/progress", p.guard(func(w http.ResponseWriter, r *http.Request) {
		var got Position
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			writeJSON(w, 400, map[string]string{"error": "bad body"})
			return
		}
		p.mu.Lock()
		p.pushes = append(p.pushes, got)
		p.positions[got.Document] = got
		p.mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"document": got.Document, "timestamp": time.Now().Unix(),
		})
	}))
	mux.HandleFunc("GET /syncs/progress/{document}", p.guard(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		forced := p.answer
		stored, ok := p.positions[r.PathValue("document")]
		p.mu.Unlock()
		switch {
		case forced != nil:
			writeJSON(w, 200, forced)
		case ok:
			writeJSON(w, 200, stored)
		default:
			// The real server answers an unknown document with an
			// empty object and a 200, not a 404.
			writeJSON(w, 200, map[string]any{})
		}
	}))
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

// guard is the peer's credential check, and the recorder: every request
// is kept so a test can assert what was on the wire.
func (p *fakePeer) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.requests = append(p.requests, r.Clone(r.Context()))
		status, body := p.status, p.body
		p.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			w.Write([]byte(body))
			return
		}
		if body != "" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
			return
		}
		if r.Header.Get("x-auth-user") != p.user || r.Header.Get("x-auth-key") != p.key {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (p *fakePeer) lastRequest(t *testing.T) *http.Request {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		t.Fatal("the peer was never called")
	}
	return p.requests[len(p.requests)-1]
}

func (p *fakePeer) pushed(t *testing.T) []Position {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Position(nil), p.pushes...)
}

func (p *fakePeer) set(pos Position) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.positions[pos.Document] = pos
}

func (p *fakePeer) answerWith(pos *Position) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answer = pos
}

// forget drops everything the peer holds for a document, so the next
// pull gets the empty answer a server gives for a book nobody has read
// there — or has stopped holding a position for.
func (p *fakePeer) forget(document string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answer = nil
	delete(p.positions, document)
}

func (p *fakePeer) failWith(status int, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status, p.body = status, body
}

func (p *fakePeer) client(t *testing.T) *Client {
	t.Helper()
	return NewClient(config.MirrorConfig{
		Enabled:    true,
		BaseURL:    p.URL,
		Account:    "local",
		RemoteUser: p.user,
		RemoteKey:  p.key,
		DeviceID:   "liseur-sync",
		Timeout:    config.Duration(5 * time.Second),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// TestTheCredentialGoesInTheHeadersKOReaderUses. The peer is somebody
// else's software; the thing that has to be right is the wire.
func TestTheCredentialGoesInTheHeadersKOReaderUses(t *testing.T) {
	peer := newFakePeer(t)
	if err := peer.client(t).Authorize(context.Background()); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	r := peer.lastRequest(t)
	if got := r.Header.Get("x-auth-user"); got != "reader" {
		t.Fatalf("x-auth-user: %q", got)
	}
	if got := r.Header.Get("x-auth-key"); got != peer.key {
		t.Fatalf("x-auth-key: %q", got)
	}
	if r.URL.Path != "/users/auth" {
		t.Fatalf("path: %q", r.URL.Path)
	}
}

// TestTheCredentialIsNeverInTheURL. It would be in the peer's access
// log, in any proxy between here and there, and in this server's own
// error messages.
func TestTheCredentialIsNeverInTheURL(t *testing.T) {
	peer := newFakePeer(t)
	c := peer.client(t)
	ctx := context.Background()
	_ = c.Authorize(ctx)
	_, _ = c.Pull(ctx, "43200deaa6b3a3cd67dc934fabe6f0f2")
	_ = c.Push(ctx, Position{Document: "43200deaa6b3a3cd67dc934fabe6f0f2", Percentage: 0.5})

	peer.mu.Lock()
	defer peer.mu.Unlock()
	for _, r := range peer.requests {
		if strings.Contains(r.URL.String(), peer.key) {
			t.Fatalf("the credential reached the URL: %s", r.URL)
		}
	}
}

// TestARejectedCredentialIsNamed so an operator sees the one failure
// they can actually fix, rather than a generic transport error.
func TestARejectedCredentialIsNamed(t *testing.T) {
	peer := newFakePeer(t)
	c := NewClient(config.MirrorConfig{
		BaseURL: peer.URL, RemoteUser: "reader", RemoteKey: "wrong",
		DeviceID: "liseur-sync", Timeout: config.Duration(5 * time.Second),
	})
	if err := c.Authorize(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a bad credential gave: %v", err)
	}
}

// TestAPushIsAttributedToThisServer. The device id is how this server
// recognises its own writing coming back on the next pull, so it has to
// be ours even when the reading was done on a phone.
func TestAPushIsAttributedToThisServer(t *testing.T) {
	peer := newFakePeer(t)
	c := peer.client(t)
	err := c.Push(context.Background(), Position{
		Document:   "ABCDEF0123456789ABCDEF0123456789",
		Progress:   "/body/DocFragment[7]/body/p[3]",
		Percentage: 0.42,
		Device:     "pixel-8",
		DeviceID:   "android-whatever",
		Timestamp:  1600000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	pushes := peer.pushed(t)
	if len(pushes) != 1 {
		t.Fatalf("pushes: %d", len(pushes))
	}
	got := pushes[0]
	if got.Device != "liseur-sync" || got.DeviceID != "liseur-sync" {
		t.Fatalf("a push was attributed elsewhere: %+v", got)
	}
	// The digest is a hash; the peer stores it lowercased and a pull
	// has to find it again.
	if got.Document != "abcdef0123456789abcdef0123456789" {
		t.Fatalf("document not normalised: %q", got.Document)
	}
	if got.Progress != "/body/DocFragment[7]/body/p[3]" || got.Percentage != 0.42 {
		t.Fatalf("position altered in flight: %+v", got)
	}
	if got.Timestamp != 1600000000 {
		t.Fatalf("timestamp: %d", got.Timestamp)
	}
}

// TestAPushWithNoEnginePositionStillCarriesOne. A position the reader
// cannot act on leaves it where it was, so the percentage goes as the
// position when there is nothing better — which is exactly what this
// server already answers its own non-CRe clients with.
func TestAPushWithNoEnginePositionStillCarriesOne(t *testing.T) {
	peer := newFakePeer(t)
	err := peer.client(t).Push(context.Background(), Position{
		Document: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Percentage: 0.25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := peer.pushed(t)[0].Progress; got != "0.25" {
		t.Fatalf("progress sent as %q", got)
	}
}

// TestAZeroPercentageIsAPositionLikeAnyOther is the falsy-zero rule
// this repository already learned once: the beginning of a book is a
// place somebody is, not a missing value.
func TestAZeroPercentageIsAPositionLikeAnyOther(t *testing.T) {
	peer := newFakePeer(t)
	c := peer.client(t)
	ctx := context.Background()
	if err := c.Push(ctx, Position{
		Document: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Progress: "/body/DocFragment[2]/body", Percentage: 0,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Pull(ctx, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err != nil {
		t.Fatalf("a position at zero came back as: %v", err)
	}
	if got.Percentage != 0 || got.Progress != "/body/DocFragment[2]/body" {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestAnOutOfRangePercentageIsRefusedInBothDirections(t *testing.T) {
	peer := newFakePeer(t)
	c := peer.client(t)
	ctx := context.Background()

	// KOReader's scale is 0..1. A percentage on the 0..100 scale is the
	// single likeliest bug in a bridge between two systems that use
	// both, so it is refused rather than clamped.
	if err := c.Push(ctx, Position{Document: "cccccccccccccccccccccccccccccccc", Percentage: 42}); err == nil {
		t.Fatal("a percentage of 42 was pushed")
	}
	if len(peer.pushed(t)) != 0 {
		t.Fatal("the peer was called with an invalid position")
	}

	peer.answerWith(&Position{
		Document: "cccccccccccccccccccccccccccccccc", Progress: "x",
		Percentage: 42, Timestamp: 1,
	})
	if _, err := c.Pull(ctx, "cccccccccccccccccccccccccccccccc"); err == nil {
		t.Fatal("a percentage of 42 was accepted from the peer")
	}
}

// TestAnUnknownDocumentIsAnOrdinaryAnswer. Most books are known to only
// one side, so "I have nothing" is the common case and must not look
// like a failure worth backing off from.
func TestAnUnknownDocumentIsAnOrdinaryAnswer(t *testing.T) {
	peer := newFakePeer(t)
	_, err := peer.client(t).Pull(context.Background(), "dddddddddddddddddddddddddddddddd")
	if !errors.Is(err, ErrNoPosition) {
		t.Fatalf("unknown document gave: %v", err)
	}
}

// TestAResetMarkerIsRecognisedForWhatItIs. This is the trap: the peer
// answers with a live, now-stamped position at the very start of the
// book while a reading reset is outstanding, attributed to its own web
// reader — the same device a genuine web position carries. Applying it
// would send the reader to page one on every poll, forever.
func TestAResetMarkerIsRecognisedForWhatItIs(t *testing.T) {
	reflowable := Position{
		Document: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Progress: "/body/DocFragment[1]/body", Percentage: 0,
		Device: "web", DeviceID: "bookorbit-web", Timestamp: time.Now().Unix(),
	}
	if !reflowable.IsReset() {
		t.Fatal("a reflowable reset marker was not recognised")
	}
	paged := reflowable
	paged.Progress = "1"
	if !paged.IsReset() {
		t.Fatal("a paged reset marker was not recognised")
	}

	// Everything else is a position somebody read to, including the
	// same device at the same place once there is progress behind it,
	// and the start of a book reached by other means.
	for name, p := range map[string]Position{
		"web reader, mid book": {Progress: "/body/DocFragment[1]/body", Percentage: 0.3, DeviceID: "bookorbit-web"},
		"a device at the start": {
			Progress: "/body/DocFragment[1]/body/p[1]/text()[1].0", Percentage: 0,
			DeviceID: "kindle",
		},
		"page two": {Progress: "2", Percentage: 0, DeviceID: "kindle"},
	} {
		if p.IsReset() {
			t.Fatalf("%s was mistaken for a reset marker: %+v", name, p)
		}
	}
}

// TestTheClientDoesNotFollowARedirect. Following one would re-send the
// credential wherever the peer pointed, which is not the peer's call.
func TestTheClientDoesNotFollowARedirect(t *testing.T) {
	var elsewhereCalled bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhereCalled = true
		writeJSON(w, 200, map[string]string{"authorized": "OK"})
	}))
	defer elsewhere.Close()

	peer := newFakePeer(t)
	peer.failWith(302, "")
	peer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/users/auth", http.StatusFound)
	})

	err := peer.client(t).Authorize(context.Background())
	if err == nil {
		t.Fatal("a redirect was followed silently")
	}
	if elsewhereCalled {
		t.Fatal("the credential was sent to the redirect target")
	}
}

// TestAPeersErrorIsReportedWithoutItsWholeBody. Something is wrong at
// the other end; a line of it is the useful part and the rest is theirs.
func TestAPeersErrorIsReportedWithoutItsWholeBody(t *testing.T) {
	peer := newFakePeer(t)
	peer.failWith(500, "the first line explains it\n"+strings.Repeat("stack frame\n", 400))
	err := peer.client(t).Authorize(context.Background())
	if err == nil {
		t.Fatal("a 500 was accepted")
	}
	if !strings.Contains(err.Error(), "the first line explains it") {
		t.Fatalf("the useful line was dropped: %v", err)
	}
	if strings.Count(err.Error(), "stack frame") != 0 {
		t.Fatalf("the peer's whole body reached the error: %v", err)
	}
}

// TestAPeerThatNeverAnswersDoesNotHangTheMirror. The mirror is a
// background loop; one unresponsive peer must not stop it.
func TestAPeerThatNeverAnswersDoesNotHangTheMirror(t *testing.T) {
	blocked := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	defer func() { close(blocked); slow.Close() }()

	c := NewClient(config.MirrorConfig{
		BaseURL: slow.URL, RemoteUser: "reader", RemoteKey: "k",
		DeviceID: "liseur-sync", Timeout: config.Duration(150 * time.Millisecond),
	})
	start := time.Now()
	if err := c.Authorize(context.Background()); err == nil {
		t.Fatal("a peer that never answered was accepted")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the client waited %s", elapsed)
	}
}

// TestACancelledContextStopsTheRequest, so shutdown does not wait for a
// slow peer.
func TestACancelledContextStopsTheRequest(t *testing.T) {
	blocked := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	defer func() { close(blocked); slow.Close() }()

	c := NewClient(config.MirrorConfig{
		BaseURL: slow.URL, RemoteUser: "reader", RemoteKey: "k",
		DeviceID: "liseur-sync", Timeout: config.Duration(time.Minute),
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if err := c.Authorize(ctx); err == nil {
		t.Fatal("a cancelled request succeeded")
	}
}

// TestAnEnormousReplyIsBounded. The peer is somebody else's software
// and this server reads whatever it sends.
func TestAnEnormousReplyIsBounded(t *testing.T) {
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"document":"f","progress":"`))
		chunk := strings.Repeat("A", 64<<10)
		for range 200 { // ~12 MiB, well past the bound
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
		w.Write([]byte(`","percentage":0.5}`))
	}))
	defer huge.Close()

	c := NewClient(config.MirrorConfig{
		BaseURL: huge.URL, RemoteUser: "reader", RemoteKey: "k",
		DeviceID: "liseur-sync", Timeout: config.Duration(10 * time.Second),
	})
	// Truncated at the bound, so it cannot parse. The point is that it
	// returns rather than reading twelve megabytes into a position.
	if _, err := c.Pull(context.Background(), "ffffffffffffffffffffffffffffffff"); err == nil {
		t.Fatal("an unbounded reply was parsed")
	}
}

// TestAPositionSurvivesTheRoundTrip end to end, which is the whole
// point of the client.
func TestAPositionSurvivesTheRoundTrip(t *testing.T) {
	peer := newFakePeer(t)
	c := peer.client(t)
	ctx := context.Background()
	sent := Position{
		Document:   "43200deaa6b3a3cd67dc934fabe6f0f2",
		Progress:   "/body/DocFragment[11]/body/div/p[42]/text()[1].0",
		Percentage: 0.6180339887,
		Timestamp:  1700000000,
	}
	if err := c.Push(ctx, sent); err != nil {
		t.Fatal(err)
	}
	got, err := c.Pull(ctx, strings.ToUpper(sent.Document))
	if err != nil {
		t.Fatal(err)
	}
	if got.Document != sent.Document || got.Progress != sent.Progress ||
		got.Percentage != sent.Percentage || got.Timestamp != sent.Timestamp {
		t.Fatalf("round trip:\n got %+v\nsent %+v", got, sent)
	}
	if !got.Time().Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("timestamp read back as %s", got.Time())
	}
	if got.DeviceID != c.DeviceID() {
		t.Fatalf("own push came back as %q", got.DeviceID)
	}
}

// TestADocumentWithNothingInItIsRefusedLocally rather than turned into
// a request the peer has to reject.
func TestADocumentWithNothingInItIsRefusedLocally(t *testing.T) {
	peer := newFakePeer(t)
	c := peer.client(t)
	ctx := context.Background()
	if _, err := c.Pull(ctx, "   "); err == nil {
		t.Fatal("an empty document was pulled")
	}
	if err := c.Push(ctx, Position{Percentage: 0.5}); err == nil {
		t.Fatal("an empty document was pushed")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if len(peer.requests) != 0 {
		t.Fatalf("the peer was called %d times for an empty document", len(peer.requests))
	}
}
