package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// fakeOrbit is BookOrbit as this protocol needs it to behave: a
// password, a rotating session, a catalog it can be asked about and one
// reading position per file.
//
// It is written against the peer's observed shapes rather than a
// recording of them, including the two things that are easy to get
// wrong from the outside and expensive to get wrong here — a progress
// read for a file nobody has opened answers 200 with a body missing its
// timestamps rather than 404, and a percentage on the wire counts to a
// hundred.
type fakeOrbit struct {
	t      *testing.T
	server *httptest.Server

	mu sync.Mutex
	// books is what the catalog holds, by book id.
	books map[string]bookDetail
	// progress is the stored position per file id.
	progress map[string]progressReply
	// writes is every position this server sent, in order.
	writes []progressWrite
	// searches is every free-text query the protocol made.
	searches []string
	// access is the token currently good; refresh is the token that
	// would rotate into a new one.
	access, refresh string
	logins          int
	refreshes       int
	// refuseRefresh makes the peer reject the refresh token, as it
	// does for a session that was revoked or rotated past.
	refuseRefresh bool
	// accessLifetime is how long a minted access token lasts. Zero
	// means the fake does not expire them.
	accessLifetime time.Duration
	// envelope wraps a search reply in a named field, the way the real
	// peer's paging does.
	envelope string
	// forgotten makes every progress read answer 404, as the peer does
	// for a file it no longer has.
	forgotten bool
	// reflect makes the peer answer a progress read by failing and
	// quoting back the request headers, the way a misbehaving proxy or
	// a debug error page does. reflectExtra is anything else it should
	// put in that body.
	reflect      bool
	reflectExtra string
	version      string
}

func newFakeOrbit(t *testing.T) *fakeOrbit {
	t.Helper()
	f := &fakeOrbit{
		t:        t,
		books:    map[string]bookDetail{},
		progress: map[string]progressReply{},
		envelope: "items",
		version:  "3.1.0",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", f.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/refresh", f.handleRefresh)
	mux.HandleFunc("POST /api/v1/auth/logout", f.handleLogout)
	mux.HandleFunc("GET /api/v1/app-info", f.guard(f.handleAppInfo))
	mux.HandleFunc("POST /api/v1/books/query", f.guard(f.handleQuery))
	mux.HandleFunc("GET /api/v1/books/files/{file}/progress", f.guard(f.handleReadProgress))
	mux.HandleFunc("POST /api/v1/books/files/{file}/progress", f.guard(f.handleWriteProgress))
	mux.HandleFunc("GET /api/v1/books/{book}", f.guard(f.handleDetail))
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

const (
	orbitUser     = "reader"
	orbitPassword = "correct horse"
)

func (f *fakeOrbit) protocol(t *testing.T) *bookOrbitPeer {
	t.Helper()
	cfg := config.MirrorConfig{
		Protocol:       config.ProtocolBookOrbit,
		BaseURL:        f.server.URL + "/api/v1",
		RemoteUser:     orbitUser,
		RemotePassword: orbitPassword,
		DeviceID:       "liseur-sync",
		Timeout:        config.Duration(10 * time.Second),
	}
	p, ok := BookOrbitProtocol(cfg, nil).(*bookOrbitPeer)
	if !ok {
		t.Fatal("BookOrbitProtocol did not build a BookOrbit peer")
	}
	return p
}

// hold puts a book in the peer's catalog.
func (f *fakeOrbit) hold(bookID, title string, file bookFile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.books[bookID] = bookDetail{
		ID: json.Number(bookID), Title: title, Files: []bookFile{file},
	}
}

func (f *fakeOrbit) place(fileID string, p progressReply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.progress[fileID] = p
}

func (f *fakeOrbit) sent() []progressWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]progressWrite(nil), f.writes...)
}

func (f *fakeOrbit) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.searches...)
}

func (f *fakeOrbit) mint() credentials {
	f.access = "access-" + strconv.Itoa(f.logins+f.refreshes)
	f.refresh = "refresh-" + strconv.Itoa(f.logins+f.refreshes)
	creds := credentials{
		AccessToken:           f.access,
		RefreshToken:          f.refresh,
		RefreshTokenExpiresAt: time.Now().Add(7 * 24 * time.Hour),
		SessionID:             json.Number("77"),
	}
	if f.accessLifetime > 0 {
		creds.AccessTokenExpiresAt = time.Now().Add(f.accessLifetime)
	}
	return creds
}

func (f *fakeOrbit) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		ClientKind  string `json:"clientKind"`
		DeviceLabel string `json:"deviceLabel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if body.Username != orbitUser || body.Password != orbitPassword {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	// The whole point of the native mode is that the refresh token
	// comes back in the body instead of a cookie, so a client that
	// forgets to ask for it gets a web session it cannot keep alive.
	if body.ClientKind != "native" {
		f.t.Errorf("login asked for clientKind %q, not native", body.ClientKind)
	}
	if body.DeviceLabel == "" {
		f.t.Error("login sent no device label")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins++
	writeJSON(w, http.StatusOK, f.mint())
}

func (f *fakeOrbit) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuseRefresh || body.RefreshToken != f.refresh {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	f.refreshes++
	writeJSON(w, http.StatusOK, f.mint())
}

func (f *fakeOrbit) handleLogout(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.access, f.refresh = "", ""
	w.WriteHeader(http.StatusNoContent)
}

// guard is the peer's bearer check. Every route but the auth ones is
// behind it, including app-info.
func (f *fakeOrbit) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		want := f.access
		f.mu.Unlock()
		if want == "" || r.Header.Get("Authorization") != "Bearer "+want {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (f *fakeOrbit) handleAppInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": f.version, "maxUploadSizeMb": 200})
}

func (f *fakeOrbit) handleQuery(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Q string `json:"q"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches = append(f.searches, body.Q)
	// The real search covers title, author, series and narrator. Title
	// is enough to prove the protocol narrows before it decides.
	cards := []bookCard{}
	for _, b := range f.books {
		if body.Q != "" && !strings.Contains(
			strings.ToLower(b.Title), strings.ToLower(body.Q)) {
			continue
		}
		cards = append(cards, bookCard(b))
	}
	if f.envelope == "" {
		writeJSON(w, http.StatusOK, cards)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		f.envelope: cards,
		"page":     1,
		"total":    len(cards),
	})
}

func (f *fakeOrbit) handleDetail(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	book, ok := f.books[r.PathValue("book")]
	if !ok {
		http.Error(w, "no such book", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, book)
}

func (f *fakeOrbit) handleReadProgress(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file := r.PathValue("file")
	if f.reflect {
		http.Error(w, "upstream rejected: "+r.Header.Get("Authorization")+
			" "+f.reflectExtra, http.StatusBadGateway)
		return
	}
	if f.forgotten {
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	if p, ok := f.progress[file]; ok {
		id := json.Number(file)
		p.BookFileID = &id
		writeJSON(w, http.StatusOK, p)
		return
	}
	// A file nobody has opened: 200 with a hand-written default that
	// has no bookFileId and, crucially, no timestamps.
	writeJSON(w, http.StatusOK, map[string]any{
		"source": "text", "percentage": 0, "cfi": nil, "pageNumber": nil,
	})
}

func (f *fakeOrbit) handleWriteProgress(w http.ResponseWriter, r *http.Request) {
	var body progressWrite
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, body)
	now := time.Now().UTC()
	stored := progressReply{
		CFI: body.CFI, PageNumber: body.PageNumber,
		Percentage: body.Percentage, KoreaderProgress: body.KoreaderProgress,
		LastReadAt: &now, UpdatedAt: &now,
	}
	f.progress[r.PathValue("file")] = stored
	w.WriteHeader(http.StatusOK)
}

// orbitBook is the local side of a book both servers hold.
func orbitBook() store.MirrorCandidate {
	return store.MirrorCandidate{
		WorkID:       "w1",
		Document:     fixtureDocument,
		Title:        "A Memory Called Empire",
		RelativePath: "Arkady Martine/A Memory Called Empire.epub",
		RootPath:     "/srv/library",
		SizeBytes:    918_273,
	}
}

func orbitFile() bookFile {
	return bookFile{
		ID: "41", Format: "epub", SizeBytes: 918_273,
		Filename:     "A Memory Called Empire.epub",
		AbsolutePath: "/books/Arkady Martine/A Memory Called Empire.epub",
	}
}

// TestTheNativeSessionIsKeptAlive. The access token lasts fifteen
// minutes on the real peer, so a mirror that polls for a week does most
// of its work on refreshed tokens rather than on the one it logged in
// with. Signing in again for every pass would burn through the sign-in
// rate limit and leave a session behind each time.
func TestTheNativeSessionIsKeptAlive(t *testing.T) {
	orbit := newFakeOrbit(t)
	// Every token is already past its skew window when it is minted,
	// so each call has to go and get another one.
	orbit.accessLifetime = accessSkew / 2
	peer := orbit.protocol(t)
	ctx := context.Background()

	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := peer.api.appInfo(ctx); err != nil {
			t.Fatal(err)
		}
	}
	orbit.mu.Lock()
	logins, refreshes := orbit.logins, orbit.refreshes
	orbit.mu.Unlock()
	if logins != 1 {
		t.Fatalf("signed in %d times, expected once", logins)
	}
	if refreshes < 3 {
		t.Fatalf("refreshed %d times, expected one per call", refreshes)
	}
	if peer.version != "3.1.0" {
		t.Fatalf("peer version not read: %q", peer.version)
	}
}

// TestARefusedRefreshSignsInAgain. BookOrbit rotates the refresh token
// on every use, so a reply lost in flight leaves this server holding a
// token the peer has already retired. The password is still good, and
// the alternative to using it is a mirror that stops until somebody
// restarts it.
func TestARefusedRefreshSignsInAgain(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.accessLifetime = accessSkew / 2
	peer := orbit.protocol(t)
	ctx := context.Background()

	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	orbit.mu.Lock()
	orbit.refuseRefresh = true
	orbit.mu.Unlock()

	if _, err := peer.api.appInfo(ctx); err != nil {
		t.Fatalf("a refused refresh should have signed in again: %v", err)
	}
	orbit.mu.Lock()
	logins := orbit.logins
	orbit.mu.Unlock()
	if logins != 2 {
		t.Fatalf("signed in %d times, expected a second after the refusal", logins)
	}
}

// TestABadPasswordIsRefusedAtTheDoor. The admin page tests the
// credential before writing it, and that test is this call.
func TestABadPasswordIsRefusedAtTheDoor(t *testing.T) {
	orbit := newFakeOrbit(t)
	peer := orbit.protocol(t)
	peer.api.password = "wrong"
	if err := peer.Authorize(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a wrong password gave %v", err)
	}
}

// TestSigningOutClosesThePeerSession. Editing the mirror page or
// restarting the server should not leave a login behind on somebody's
// BookOrbit account every time.
func TestSigningOutClosesThePeerSession(t *testing.T) {
	orbit := newFakeOrbit(t)
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	peer.Close(ctx)
	orbit.mu.Lock()
	live := orbit.refresh
	orbit.mu.Unlock()
	if live != "" {
		t.Fatal("the session is still open on the peer")
	}
}

// TestTheExactSpotCrossesInBothDirections is the whole reason this
// protocol exists. The KOReader bridge carries a fraction; this one
// carries the sentence.
func TestTheExactSpotCrossesInBothDirections(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}

	c := orbitBook()
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}
	cfi := "epubcfi(/6/14!/4/2/8:37)"
	if err := peer.Push(ctx, c, cur, Place{Percentage: 0.3472, CFI: cfi}); err != nil {
		t.Fatal(err)
	}

	sent := orbit.sent()
	if len(sent) != 1 {
		t.Fatalf("writes: %+v", sent)
	}
	if sent[0].CFI == nil || *sent[0].CFI != cfi {
		t.Fatalf("the exact spot did not reach the peer: %+v", sent[0])
	}
	// The peer counts to a hundred and this server counts to one.
	if sent[0].Percentage != 34.72 {
		t.Fatalf("percentage reached the peer as %v, expected 34.72", sent[0].Percentage)
	}
	if sent[0].Source != "text" {
		t.Fatalf("source: %q", sent[0].Source)
	}

	// And back: what the peer now holds is what we sent.
	back, err := peer.Pull(ctx, c, cur)
	if err != nil {
		t.Fatal(err)
	}
	if back.CFI != cfi {
		t.Fatalf("the exact spot did not come back: %+v", back)
	}
	if back.Percentage != 0.3472 {
		t.Fatalf("percentage came back as %v, expected 0.3472", back.Percentage)
	}
}

// TestOurOwnPositionComingBackIsRecognised. BookOrbit's progress row
// has no device on it, so the echo cannot be recognised the way the
// KOReader path recognises it. It is recognised by content instead, and
// this is the case that matters: what we just wrote, read back.
func TestOurOwnPositionComingBackIsRecognised(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	c := orbitBook()
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}

	cfi := "epubcfi(/6/14!/4/2/8:37)"
	if err := peer.Push(ctx, c, cur, Place{Percentage: 0.3472, CFI: cfi}); err != nil {
		t.Fatal(err)
	}
	if cur.PushedMark == "" {
		t.Fatal("the push remembered nothing to recognise itself by")
	}
	back, err := peer.Pull(ctx, c, cur)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Echo {
		t.Fatalf("our own position was not recognised: %+v", back)
	}

	// Somebody reading on further is not an echo.
	further := time.Now().UTC()
	orbit.place("41", progressReply{
		CFI: ptr("epubcfi(/6/22!/4/2/2:11)"), Percentage: 61.5, LastReadAt: &further,
	})
	moved, err := peer.Pull(ctx, c, cur)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Echo {
		t.Fatalf("a position somebody else read was taken for ours: %+v", moved)
	}
}

// TestABookNobodyHasOpenedOnThePeerIsNotAPosition. The peer answers a
// never-opened file with 200 and a default body rather than a 404, so
// the missing read time is the only tell. Reading that body as a real
// position would send every reader back to the start of every book the
// peer merely holds.
func TestABookNobodyHasOpenedOnThePeerIsNotAPosition(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	c := orbitBook()
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}
	if _, err := peer.Pull(ctx, c, cur); !errors.Is(err, ErrNoPosition) {
		t.Fatalf("an unopened book gave %v", err)
	}
}

// TestNewestWinsOnTheReadTimeNotTheRowTime. BookOrbit freezes
// updatedAt on its own KOReader sync path on purpose, so that column is
// stale for exactly the positions a mirror is there to carry.
func TestNewestWinsOnTheReadTimeNotTheRowTime(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	read := time.Now().UTC().Truncate(time.Second)
	stale := read.Add(-72 * time.Hour)
	orbit.place("41", progressReply{
		Percentage: 12, LastReadAt: &read, UpdatedAt: &stale,
	})
	got, err := peer.Pull(ctx, orbitBook(),
		&store.MirrorCursor{WorkID: "w1", Document: fixtureDocument})
	if err != nil {
		t.Fatal(err)
	}
	if !got.At.Equal(read) {
		t.Fatalf("took %s as the time of the position, expected %s", got.At, read)
	}
}

// TestAKoreaderPositionSurvivesTheCrossing. A position this server took
// from a real KOReader device reaches the peer in the shape the peer
// would have received it in directly, rather than flattened to a
// number.
func TestAKoreaderPositionSurvivesTheCrossing(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	c := orbitBook()
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}
	xpointer := "/body/DocFragment[11]/body/div/p[27]/text().112"
	if err := peer.Push(ctx, c, cur, Place{Percentage: 0.5, Foreign: xpointer}); err != nil {
		t.Fatal(err)
	}
	sent := orbit.sent()[0]
	if sent.KoreaderProgress == nil || *sent.KoreaderProgress != xpointer {
		t.Fatalf("the engine position did not survive: %+v", sent)
	}
	if sent.PageNumber != nil {
		t.Fatalf("an xpointer was turned into a page number: %+v", sent)
	}

	back, err := peer.Pull(ctx, c, cur)
	if err != nil {
		t.Fatal(err)
	}
	if back.Foreign != xpointer {
		t.Fatalf("the engine position did not come back: %+v", back)
	}
}

// TestAPagedPositionCrossesAsAPage. The other thing a foreign position
// can be is a bare page number, which is a page number on both sides.
func TestAPagedPositionCrossesAsAPage(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	c := orbitBook()
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}
	if err := peer.Push(ctx, c, cur, Place{Percentage: 0.5, Foreign: "132"}); err != nil {
		t.Fatal(err)
	}
	sent := orbit.sent()[0]
	if sent.PageNumber == nil || *sent.PageNumber != 132 {
		t.Fatalf("a page did not cross as a page: %+v", sent)
	}
	back, err := peer.Pull(ctx, c, cur)
	if err != nil {
		t.Fatal(err)
	}
	if back.Foreign != "132" {
		t.Fatalf("the page did not come back: %+v", back)
	}
}

// TestAPercentageOutsideThePeersScaleIsRefused. A peer that started
// answering in fractions would otherwise land every reader on page one
// and look precise doing it.
func TestAPercentageOutsideThePeersScaleIsRefused(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	orbit.place("41", progressReply{Percentage: 140, LastReadAt: &now})
	_, err := peer.Pull(ctx, orbitBook(),
		&store.MirrorCursor{WorkID: "w1", Document: fixtureDocument})
	if err == nil || !strings.Contains(err.Error(), "0..100") {
		t.Fatalf("an impossible percentage gave %v", err)
	}
}

// TestTheBookIsFoundOnceAndRemembered. Resolution costs a search and a
// detail read and the answer does not change, so paying it every five
// minutes would spend the mirror's budget on bookkeeping.
func TestTheBookIsFoundOnceAndRemembered(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	c := orbitBook()
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}
	for range 3 {
		if _, err := peer.Pull(ctx, c, cur); !errors.Is(err, ErrNoPosition) {
			t.Fatal(err)
		}
	}
	if cur.RemoteFileID != "41" {
		t.Fatalf("the file id was not remembered: %+v", cur)
	}
	if asked := orbit.asked(); len(asked) != 1 {
		t.Fatalf("searched the peer %d times: %v", len(asked), asked)
	}
}

// TestABookThePeerDoesNotHoldIsNotSearchedForAgain. Two libraries
// overlap partially; a book only this side holds is the normal case and
// must not cost a search every poll forever.
func TestABookThePeerDoesNotHoldIsNotSearchedForAgain(t *testing.T) {
	orbit := newFakeOrbit(t)
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	c := orbitBook()
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}
	for range 3 {
		if _, err := peer.Pull(ctx, c, cur); !errors.Is(err, ErrNotOnPeer) {
			t.Fatalf("a book the peer does not hold gave %v", err)
		}
	}
	if len(orbit.asked()) != 1 {
		t.Fatalf("searched %d times for a book the peer does not hold", len(orbit.asked()))
	}
	if cur.RemoteCheckedAt == nil {
		t.Fatal("nothing recorded that the book was looked for")
	}
}

// TestAFailedSearchIsNotAnAnswerAboutTheBook. A peer that was down when
// the book was looked for has said nothing about whether it holds it,
// and remembering the outage as "not there" would hide the book for a
// day after the peer came back.
func TestAFailedSearchIsNotAnAnswerAboutTheBook(t *testing.T) {
	orbit := newFakeOrbit(t)
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	orbit.server.Close()

	c := orbitBook()
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}
	if _, err := peer.Pull(ctx, c, cur); err == nil || errors.Is(err, ErrNotOnPeer) {
		t.Fatalf("an unreachable peer gave %v", err)
	}
	if cur.RemoteCheckedAt != nil {
		t.Fatal("an outage was recorded as an answer about the book")
	}
}

// TestTwoBooksOfTheSameSizeAreRefusedRatherThanGuessed. The cost of
// guessing wrong is a reader's place in the wrong book, which is the
// same reason ADR-0047 refuses an ambiguous fingerprint.
func TestTwoBooksOfTheSameSizeAreRefusedRatherThanGuessed(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	twin := orbitFile()
	twin.ID = "42"
	orbit.hold("8", "A Memory Called Empire (proof)", twin)

	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	c := orbitBook()
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}
	if _, err := peer.Pull(ctx, c, cur); !errors.Is(err, ErrNotOnPeer) {
		t.Fatalf("an ambiguous match gave %v", err)
	}
	if cur.RemoteFileID != "" {
		t.Fatalf("a guess was remembered: %+v", cur)
	}
}

// TestADifferentBookOfTheSameSizeIsNotOurs. The search narrows by
// title and the size shortlists, but the filename is what confirms.
func TestADifferentBookOfTheSameSizeIsNotOurs(t *testing.T) {
	orbit := newFakeOrbit(t)
	other := orbitFile()
	other.Filename = "A Desolation Called Peace.epub"
	other.AbsolutePath = "/books/Arkady Martine/A Desolation Called Peace.epub"
	orbit.hold("7", "A Memory Called Empire", other)

	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	cur := &store.MirrorCursor{WorkID: "w1", Document: fixtureDocument}
	if _, err := peer.Pull(ctx, orbitBook(), cur); !errors.Is(err, ErrNotOnPeer) {
		t.Fatalf("a different book of the same size gave %v", err)
	}
}

// TestTheSharedDirectoryConfirmsTheBook. When both servers read the
// same disk the peer's path is the strongest confirmation available,
// once translated: BookOrbit reports the path it sees from inside its
// own container, which is not this server's path for the same bytes.
func TestTheSharedDirectoryConfirmsTheBook(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	peer.cfg.PeerPathPrefix = "/books"
	peer.cfg.LocalPathPrefix = "/srv/library"
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	cur := &store.MirrorCursor{WorkID: "w1", Document: fixtureDocument}
	if _, err := peer.Pull(ctx, orbitBook(), cur); !errors.Is(err, ErrNoPosition) {
		t.Fatalf("a book in the shared directory was not accepted: %v", err)
	}
	if cur.RemoteFileID != "41" {
		t.Fatalf("cursor: %+v", cur)
	}
}

// TestABookElsewhereOnTheSharedDiskIsRefused. Same name, same size, a
// different directory: with the mapping configured that is a different
// book, and without it there is nothing to go on and it is accepted.
func TestABookElsewhereOnTheSharedDiskIsRefused(t *testing.T) {
	orbit := newFakeOrbit(t)
	elsewhere := orbitFile()
	elsewhere.AbsolutePath = "/other/Arkady Martine/A Memory Called Empire.epub"
	orbit.hold("7", "A Memory Called Empire", elsewhere)

	peer := orbit.protocol(t)
	peer.cfg.PeerPathPrefix = "/books"
	peer.cfg.LocalPathPrefix = "/srv/library"
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	cur := &store.MirrorCursor{WorkID: "w1", Document: fixtureDocument}
	if _, err := peer.Pull(ctx, orbitBook(), cur); !errors.Is(err, ErrNotOnPeer) {
		t.Fatalf("a book in the wrong directory gave %v", err)
	}
}

// TestAWorkWithNoCatalogBookIsNotSearchedFor. A fingerprint can arrive
// from a KOReader device for a book this server does not hold, and
// there is nothing to search the peer for.
func TestAWorkWithNoCatalogBookIsNotSearchedFor(t *testing.T) {
	orbit := newFakeOrbit(t)
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	c := store.MirrorCandidate{WorkID: "w1", Document: fixtureDocument, Title: "Unknown"}
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}
	if _, err := peer.Pull(ctx, c, cur); !errors.Is(err, ErrNotOnPeer) {
		t.Fatalf("a work with no file gave %v", err)
	}
	if len(orbit.asked()) != 0 {
		t.Fatal("searched the peer for a book with no file")
	}
}

// TestTheSearchReplyEnvelopeCanMove. BookOrbit's catalog routes change
// in most of its releases, and the page wrapper around the results is
// the least interesting thing that could change. Pinning its name would
// break the mirror over a rename.
func TestTheSearchReplyEnvelopeCanMove(t *testing.T) {
	for _, envelope := range []string{"items", "data", "results", "somethingNew", ""} {
		t.Run("envelope="+envelope, func(t *testing.T) {
			orbit := newFakeOrbit(t)
			orbit.envelope = envelope
			orbit.hold("7", "A Memory Called Empire", orbitFile())
			peer := orbit.protocol(t)
			ctx := context.Background()
			if err := peer.Authorize(ctx); err != nil {
				t.Fatal(err)
			}
			cur := &store.MirrorCursor{WorkID: "w1", Document: fixtureDocument}
			if _, err := peer.Pull(ctx, orbitBook(), cur); !errors.Is(err, ErrNoPosition) {
				t.Fatalf("the book was not found: %v", err)
			}
			if cur.RemoteFileID != "41" {
				t.Fatalf("cursor: %+v", cur)
			}
		})
	}
}

// TestAFileThePeerHasForgottenIsLookedForAgain. A remembered file id
// that now answers 404 is worthless, and keeping it would mean the book
// never resolves again even after the peer re-adds it.
func TestAFileThePeerHasForgottenIsLookedForAgain(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	orbit.mu.Lock()
	orbit.forgotten = true
	orbit.mu.Unlock()

	cur := &store.MirrorCursor{
		WorkID: "w1", Document: fixtureDocument, RemoteFileID: "41",
	}
	if _, err := peer.Pull(ctx, orbitBook(), cur); !errors.Is(err, ErrNotOnPeer) {
		t.Fatalf("a forgotten file gave %v", err)
	}
	if cur.RemoteFileID != "" {
		t.Fatalf("a dead file id was kept: %+v", cur)
	}

	// And with the file back, the book resolves again rather than
	// staying invisible because of what happened last time.
	orbit.mu.Lock()
	orbit.forgotten = false
	orbit.mu.Unlock()
	if _, err := peer.Pull(ctx, orbitBook(), cur); !errors.Is(err, ErrNoPosition) {
		t.Fatalf("the book did not come back: %v", err)
	}
	if cur.RemoteFileID != "41" {
		t.Fatalf("cursor: %+v", cur)
	}
}

// TestAMarkIsTheSpotWhenThereIsOneAndTheFractionOtherwise. The mark is
// how this protocol recognises its own writing, so what it is made of
// is the whole of that judgement.
func TestAMarkIsTheSpotWhenThereIsOneAndTheFractionOtherwise(t *testing.T) {
	cfi := "epubcfi(/6/14!/4/2/8:37)"
	withCFI := placeMark(Place{Percentage: 0.5, CFI: cfi})
	// Two readings of the same spot that disagree slightly on how far
	// through the book it is are the same position.
	if withCFI != placeMark(Place{Percentage: 0.51, CFI: cfi}) {
		t.Fatal("the mark of a known spot depended on the fraction")
	}
	if withCFI == placeMark(Place{Percentage: 0.5}) {
		t.Fatal("a position with a spot and one without share a mark")
	}
	// Without a spot the fraction is all there is, and it has to
	// survive a database and a JSON number unchanged.
	if placeMark(Place{Percentage: 0.3472}) != placeMark(Place{Percentage: 0.34720000001}) {
		t.Fatal("a float that came back from the peer did not match itself")
	}
	if placeMark(Place{Percentage: 0.3472}) == placeMark(Place{Percentage: 0.3473}) {
		t.Fatal("two different places share a mark")
	}
}

// TestAMarkIsSpentOnceThePeerMovesOn. The mark says what the peer's
// row holds because of this server. Keeping it after the peer has
// moved elsewhere would call a reader's eventual return to that spot
// our own writing, and refuse to follow them back to it.
func TestAMarkIsSpentOnceThePeerMovesOn(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	c := orbitBook()
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}
	cfi := "epubcfi(/6/14!/4/2/8:37)"
	if err := peer.Push(ctx, c, cur, Place{Percentage: 0.3472, CFI: cfi}); err != nil {
		t.Fatal(err)
	}

	// The reader moves on.
	later := time.Now().UTC()
	orbit.place("41", progressReply{
		CFI: ptr("epubcfi(/6/22!/4/2/2:11)"), Percentage: 61.5, LastReadAt: &later,
	})
	if _, err := peer.Pull(ctx, c, cur); err != nil {
		t.Fatal(err)
	}
	if cur.PushedMark != "" {
		t.Fatalf("the mark outlived what it described: %+v", cur)
	}

	// And back to where they were. That is their reading, not ours.
	back := time.Now().UTC()
	orbit.place("41", progressReply{CFI: &cfi, Percentage: 34.72, LastReadAt: &back})
	got, err := peer.Pull(ctx, c, cur)
	if err != nil {
		t.Fatal(err)
	}
	if got.Echo {
		t.Fatal("a reader returning to a spot was mistaken for our own writing")
	}
}

// TestACrowdedSearchIdentifiesNothing. A full page of results is not a
// shortlist: the match picked from it may have a twin on the page
// nobody asked for, and the rule is to refuse rather than guess.
func TestACrowdedSearchIdentifiesNothing(t *testing.T) {
	orbit := newFakeOrbit(t)
	for i := range searchPageSize + 5 {
		file := orbitFile()
		file.ID = json.Number(strconv.Itoa(1000 + i))
		if i > 0 {
			// Only one of them could be ours on size alone; the point
			// is that the shortlist was never complete.
			file.SizeBytes += int64(i)
		}
		orbit.hold(strconv.Itoa(i), "A Memory Called Empire "+strconv.Itoa(i), file)
	}
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	cur := &store.MirrorCursor{WorkID: "w1", Document: fixtureDocument}
	if _, err := peer.Pull(ctx, orbitBook(), cur); !errors.Is(err, ErrNotOnPeer) {
		t.Fatalf("a crowded search gave %v", err)
	}
	if cur.RemoteFileID != "" {
		t.Fatalf("a guess from an incomplete shortlist was kept: %+v", cur)
	}
}

// TestASearchReplyWithNoBooksInItIsAFailure. An envelope this does not
// understand is the peer having changed, which belongs in the log and
// in the backoff. Reading it as an empty library would hide every book
// for a day and say nothing about why.
func TestASearchReplyWithNoBooksInItIsAFailure(t *testing.T) {
	if _, err := decodeCards(json.RawMessage(`{"unexpected":"shape"}`)); err == nil {
		t.Fatal("a reply with no books in it passed as an empty library")
	}
	// An empty page is still an answer, and a real one.
	cards, err := decodeCards(json.RawMessage(`{"items":[],"total":0}`))
	if err != nil || len(cards) != 0 {
		t.Fatalf("an empty page gave %v, %v", cards, err)
	}
}

// TestReAuthorizingKeepsTheSessionItHas. The mirror rebuilds itself
// after every outage and re-authorizes each time. BookOrbit keeps a
// session for a week, so signing in afresh on each rebuild would leave
// a week of dead logins on somebody's account.
func TestReAuthorizingKeepsTheSessionItHas(t *testing.T) {
	orbit := newFakeOrbit(t)
	peer := orbit.protocol(t)
	ctx := context.Background()
	for range 4 {
		if err := peer.Authorize(ctx); err != nil {
			t.Fatal(err)
		}
	}
	orbit.mu.Lock()
	logins := orbit.logins
	orbit.mu.Unlock()
	if logins != 1 {
		t.Fatalf("signed in %d times over four rebuilds", logins)
	}
}

// TestASignInFailureIsNotQuotedBack. The request carried the password,
// and this error is logged and stored on the cursor. A peer or a proxy
// in front of it that echoes what it was sent must not put a password
// there through this path.
func TestASignInFailureIsNotQuotedBack(t *testing.T) {
	echoing := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			http.Error(w, "rejected: "+string(body), http.StatusBadRequest)
		}))
	t.Cleanup(echoing.Close)

	api := newBookOrbitAPI(config.MirrorConfig{
		BaseURL:        echoing.URL,
		RemoteUser:     orbitUser,
		RemotePassword: orbitPassword,
		Timeout:        config.Duration(10 * time.Second),
	}, nil)
	err := api.login(context.Background())
	if err == nil {
		t.Fatal("a rejected sign-in looked like a success")
	}
	if strings.Contains(err.Error(), orbitPassword) {
		t.Fatalf("the password came back in an error: %v", err)
	}
}

// TestTwoKoreaderPositionsAtOnePercentageAreNotTheSamePlace. A
// percentage is a coarse thing: a chapter heading and the paragraph
// after it round to the same number. So if the mark fell back to the
// fraction whenever there was no CFI, a KOReader device moving between
// two such spots would be reading this server's own last write, and
// the mirror would stop following it.
func TestTwoKoreaderPositionsAtOnePercentageAreNotTheSamePlace(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	c := orbitBook()
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document}

	const ours = "/body/DocFragment[11]/body/div/p[42]/text().0"
	const theirs = "/body/DocFragment[11]/body/div/p[47]/text().0"
	if err := peer.Push(ctx, c, cur, Place{Percentage: 0.4172, Foreign: ours}); err != nil {
		t.Fatal(err)
	}

	// The same xpointer coming back is ours.
	read := time.Now().UTC()
	orbit.place("41", progressReply{
		KoreaderProgress: ptr(ours), Percentage: 41.72, LastReadAt: &read,
	})
	back, err := peer.Pull(ctx, c, cur)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Echo {
		t.Fatalf("our own xpointer was not recognised: %+v", back)
	}

	// A different xpointer at a percentage that rounds the same is a
	// different place, and the reader went there.
	if err := peer.Push(ctx, c, cur, Place{Percentage: 0.4172, Foreign: ours}); err != nil {
		t.Fatal(err)
	}
	orbit.place("41", progressReply{
		KoreaderProgress: ptr(theirs), Percentage: 41.72, LastReadAt: &read,
	})
	moved, err := peer.Pull(ctx, c, cur)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Echo {
		t.Fatalf("a device's new position was thrown away as our echo: %+v", moved)
	}
	if moved.Foreign != theirs {
		t.Fatalf("the xpointer did not survive the crossing: %+v", moved)
	}
}

// TestAPeerIsItsAddressAndAccount. A cursor remembers what the peer
// calls a book, which on this protocol is a small integer. That number
// means a different book on a different installation, so the cursor
// records who answered and the syncer throws the lot away when it
// stops matching. The two halves of that are tested apart: the
// identity here, the forgetting in the syncer tests.
func TestAPeerIsItsAddressAndAccount(t *testing.T) {
	orbit := newFakeOrbit(t)
	peer := orbit.protocol(t)
	base := peer.Identity()
	if base == "" {
		t.Fatal("a peer with no identity")
	}
	if again := orbit.protocol(t).Identity(); again != base {
		t.Fatal("the same peer is not the same identity twice")
	}

	moved := orbit.protocol(t)
	moved.cfg.BaseURL = "https://elsewhere.example/api/v1"
	if moved.Identity() == base {
		t.Fatal("a different address is the same identity")
	}

	other := orbit.protocol(t)
	other.cfg.RemoteUser = "somebody-else"
	if other.Identity() == base {
		t.Fatal("a different account on the same server is the same identity")
	}

	// A rotated password is the same library, and the cursor should
	// survive it: nothing it remembers stops being true.
	rotated := orbit.protocol(t)
	rotated.cfg.RemotePassword = "a different password"
	if rotated.Identity() != base {
		t.Fatal("changing the password made it a different peer")
	}

	// And the two protocols are never each other, even pointed at one
	// server under one account.
	kos := KosyncProtocol(nil, rotated.cfg)
	if kos.Identity() == base {
		t.Fatal("the two protocols share an identity")
	}
}

// TestAPeerReflectingTheTokenCannotPutItInTheError. A failure is
// quoted because the peer's own words are usually the answer, and it
// is also logged and written to mirror_cursors.last_error. A peer or a
// proxy that echoes the request would otherwise put a live access
// token in this server's database and log file.
func TestAPeerReflectingTheTokenCannotPutItInTheError(t *testing.T) {
	orbit := newFakeOrbit(t)
	orbit.hold("7", "A Memory Called Empire", orbitFile())
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	orbit.mu.Lock()
	orbit.reflect = true
	token := orbit.access
	orbit.mu.Unlock()
	if token == "" {
		t.Fatal("the fake peer minted no token")
	}

	c := orbitBook()
	cur := &store.MirrorCursor{WorkID: c.WorkID, Document: c.Document, RemoteFileID: "41"}
	cur.PeerIdentity = peer.Identity()
	_, err := peer.Pull(ctx, c, cur)
	if err == nil {
		t.Fatal("a reflecting peer was not a failure")
	}
	text := err.Error()
	if strings.Contains(text, token) {
		t.Fatalf("the access token reached an error string: %q", text)
	}
	if strings.Contains(text, orbitPassword) {
		t.Fatalf("the password reached an error string: %q", text)
	}
	if !strings.Contains(text, "[redacted]") {
		t.Fatalf("nothing was redacted, so the guard did not run: %q", text)
	}
	// The rest of the peer's complaint still comes through, because
	// that is the whole point of quoting it.
	if !strings.Contains(text, "upstream rejected") {
		t.Fatalf("the peer's message was lost: %q", text)
	}
}

// TestAShortPasswordIsRedactedToo. Nothing stops an operator's
// BookOrbit account having a six-letter password, and the peer knows
// it: it was sent at sign-in. Skipping short secrets to keep error
// messages readable would put that password in the log and in
// mirror_cursors.last_error, which is the thing that must not happen.
func TestAShortPasswordIsRedactedToo(t *testing.T) {
	const short = "sesame"
	orbit := newFakeOrbit(t)
	peer := orbit.protocol(t)
	ctx := context.Background()
	if err := peer.Authorize(ctx); err != nil {
		t.Fatal(err)
	}
	peer.api.password = short
	orbit.mu.Lock()
	orbit.reflect, orbit.reflectExtra = true, "rejected password "+short
	orbit.mu.Unlock()

	c := orbitBook()
	cur := &store.MirrorCursor{
		WorkID: c.WorkID, Document: c.Document, RemoteFileID: "41",
		PeerIdentity: peer.Identity(),
	}
	_, err := peer.Pull(ctx, c, cur)
	if err == nil {
		t.Fatal("a reflecting peer was not a failure")
	}
	if strings.Contains(err.Error(), short) {
		t.Fatalf("a short password survived redaction: %q", err.Error())
	}
}
