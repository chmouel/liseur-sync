package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/live"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

// eventsFixture is a server with live notifications on. It is served
// through LogServerErrors, the wrapper production uses: a stream that
// cannot flush through that middleware is the failure a handler-only
// test would miss.
type eventsFixture struct {
	ts  *httptest.Server
	st  store.Store
	hub *live.Hub
}

func newEventsFixture(t *testing.T, maxStreams int, policy EventsPolicy) *eventsFixture {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.InsecureHTTP = true
	hub := live.NewHub(maxStreams)
	st.SetChangeNotifier(hub)
	srv := &Server{
		St:           st,
		Auth:         auth.NewService(st),
		Cfg:          cfg,
		LoginLimiter: auth.NewRateLimiter(1000, time.Minute),
		OPDSLimiter:  auth.NewRateLimiter(1000, time.Minute),
		Live:         hub,
		Events:       policy,
	}
	ts := httptest.NewServer(LogServerErrors(srv.Routes()))
	t.Cleanup(ts.Close)
	t.Cleanup(hub.Close)
	return &eventsFixture{ts: ts, st: st, hub: hub}
}

func (f *eventsFixture) user(t *testing.T, name string, scopes ...store.Scope) string {
	t.Helper()
	hash, err := auth.HashPassword("hunter2hunter")
	if err != nil {
		t.Fatal(err)
	}
	u := store.User{ID: name, Name: name, Argon2Hash: hash, CreatedAt: time.Now()}
	if err := f.st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	return f.mint(t, name, scopes...)
}

func (f *eventsFixture) mint(t *testing.T, userID string, scopes ...store.Scope) string {
	t.Helper()
	secret, err := auth.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	tok := store.Token{
		ID: store.NewID(), UserID: userID, DeviceID: store.NewID(),
		Name: "test", Scopes: store.ScopeSet(scopes), SHA256: auth.HashSecret(secret),
		CreatedAt: time.Now(),
	}
	if err := f.st.CreateToken(t.Context(), tok); err != nil {
		t.Fatal(err)
	}
	return secret
}

// work makes a work directly, so a test about notifications does not
// depend on the resolve endpoint's shape.
func (f *eventsFixture) work(t *testing.T, userID string) string {
	t.Helper()
	id, _, err := f.st.CreatePendingWork(t.Context(), userID, store.NewID())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// stream is an open /v1/events connection.
type stream struct {
	resp *http.Response
	br   *bufio.Reader
	stop context.CancelFunc
}

func (f *eventsFixture) open(t *testing.T, token string) (*stream, int) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.ts.URL+"/v1/events", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		cancel()
		return &stream{resp: resp}, resp.StatusCode
	}
	s := &stream{resp: resp, br: bufio.NewReader(resp.Body), stop: cancel}
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	return s, resp.StatusCode
}

// next reads frames until one is an invalidation, ignoring comments and
// the retry advice, and reports the topics it named.
func (s *stream) next(t *testing.T, within time.Duration) []string {
	t.Helper()
	type result struct {
		topics []string
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		var event, data string
		for {
			line, err := s.br.ReadString('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if event == "invalidate" {
					var payload struct {
						Topics []string `json:"topics"`
					}
					if err := json.Unmarshal([]byte(data), &payload); err != nil {
						ch <- result{err: err}
						return
					}
					ch <- result{topics: payload.Topics}
					return
				}
				event, data = "", ""
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("reading the stream: %v", r.err)
		}
		return r.topics
	case <-time.After(within):
		t.Fatal("no invalidation arrived")
		return nil
	}
}

// silent asserts nothing arrives, which is what a duplicate write and
// another account's write must both look like.
func (s *stream) silent(t *testing.T, within time.Duration) {
	t.Helper()
	done := make(chan []string, 1)
	go func() {
		var event, data string
		for {
			line, err := s.br.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if event == "invalidate" {
					var payload struct {
						Topics []string `json:"topics"`
					}
					_ = json.Unmarshal([]byte(data), &payload)
					done <- payload.Topics
					return
				}
				event, data = "", ""
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	select {
	case topics := <-done:
		t.Fatalf("unexpected invalidation naming %v", topics)
	case <-time.After(within):
	}
}

func op(workID, opID string, prog float64) store.Op {
	return store.Op{
		OpID: opID, WorkID: workID, ClientTS: time.Now().UTC().Truncate(time.Millisecond),
		Progression: prog, Origin: store.OriginNative,
	}
}

func TestEventsOpensWithAnInvalidationAndFollowsCommits(t *testing.T) {
	f := newEventsFixture(t, 4, EventsPolicy{})
	token := f.user(t, "u1", store.ScopeSync)
	workID := f.work(t, "u1")

	s, code := f.open(t, token)
	if code != http.StatusOK {
		t.Fatalf("connect: %d", code)
	}
	if ct := s.resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	// Every connection opens with one, so a client that was offline
	// needs no cursor and no special case.
	if got := s.next(t, 5*time.Second); len(got) != 2 ||
		got[0] != "positions" || got[1] != "annotations" {
		t.Fatalf("initial invalidation named %v", got)
	}

	if _, err := f.st.AppendOps(t.Context(), "u1", "dev", []store.Op{op(workID, "op-1", 0.25)}); err != nil {
		t.Fatal(err)
	}
	if got := s.next(t, 5*time.Second); len(got) != 1 || got[0] != "positions" {
		t.Fatalf("after a commit: %v", got)
	}

	// The identical push again changes no row, and a client that
	// re-read on every retry would never settle.
	if _, err := f.st.AppendOps(t.Context(), "u1", "dev", []store.Op{op(workID, "op-1", 0.25)}); err != nil {
		t.Fatal(err)
	}
	s.silent(t, 300*time.Millisecond)
}

func TestEventsNeverCrossAccounts(t *testing.T) {
	f := newEventsFixture(t, 4, EventsPolicy{})
	mine := f.user(t, "u1", store.ScopeSync)
	f.user(t, "u2", store.ScopeSync)
	theirWork := f.work(t, "u2")

	s, code := f.open(t, mine)
	if code != http.StatusOK {
		t.Fatalf("connect: %d", code)
	}
	s.next(t, 5*time.Second) // the opening invalidation

	if _, err := f.st.AppendOps(t.Context(), "u2", "dev", []store.Op{op(theirWork, "op-1", 0.5)}); err != nil {
		t.Fatal(err)
	}
	s.silent(t, 300*time.Millisecond)
}

func TestEventsTopicsFollowScopes(t *testing.T) {
	f := newEventsFixture(t, 4, EventsPolicy{})

	// An insights credential hears about sessions and nothing else.
	insights := f.user(t, "u1", store.ScopeReadInsights)
	s, code := f.open(t, insights)
	if code != http.StatusOK {
		t.Fatalf("connect: %d", code)
	}
	if got := s.next(t, 5*time.Second); len(got) != 1 || got[0] != "insights" {
		t.Fatalf("insights token was offered %v", got)
	}

	// A catalog-only credential could receive nothing, so it is
	// refused rather than parked on a silent stream.
	catalog := f.user(t, "u2", store.ScopeLibraryRead)
	if _, code := f.open(t, catalog); code != http.StatusForbidden {
		t.Fatalf("library-read token got %d", code)
	}

	if _, code := f.open(t, ""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous got %d", code)
	}
}

func TestEventsSessionsRefreshInsights(t *testing.T) {
	f := newEventsFixture(t, 4, EventsPolicy{})
	token := f.user(t, "u1", store.ScopeSync, store.ScopeReadInsights)
	workID := f.work(t, "u1")

	s, code := f.open(t, token)
	if code != http.StatusOK {
		t.Fatalf("connect: %d", code)
	}
	s.next(t, 5*time.Second)

	ses := store.Session{
		SessionID: store.NewID(), WorkID: workID, DeviceID: "dev",
		StartedAt: time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond),
		EndedAt:   time.Now().UTC().Truncate(time.Millisecond),
		StartProg: 0.1, EndProg: 0.2, Origin: store.OriginNative,
	}
	if err := f.st.AppendSessions(t.Context(), "u1", []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	if got := s.next(t, 5*time.Second); len(got) != 1 || got[0] != "insights" {
		t.Fatalf("after a session: %v", got)
	}
	// The same batch again stores nothing, so it says nothing.
	if err := f.st.AppendSessions(t.Context(), "u1", []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	s.silent(t, 300*time.Millisecond)
}

func TestEventsHeartbeatsThroughTheProductionMiddleware(t *testing.T) {
	// A short heartbeat proves the frames are flushed rather than
	// buffered until the response ends.
	f := newEventsFixture(t, 4, EventsPolicy{Heartbeat: 50 * time.Millisecond})
	token := f.user(t, "u1", store.ScopeSync)
	s, code := f.open(t, token)
	if code != http.StatusOK {
		t.Fatalf("connect: %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	pings := 0
	for time.Now().Before(deadline) && pings < 2 {
		line, err := s.br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading the stream: %v", err)
		}
		if strings.HasPrefix(line, ": ping") {
			pings++
		}
	}
	if pings < 2 {
		t.Fatal("no heartbeats arrived")
	}
}

func TestEventsRefusesTooManyStreams(t *testing.T) {
	f := newEventsFixture(t, 1, EventsPolicy{})
	token := f.user(t, "u1", store.ScopeSync)
	if _, code := f.open(t, token); code != http.StatusOK {
		t.Fatalf("first stream: %d", code)
	}
	s, code := f.open(t, token)
	if code != http.StatusTooManyRequests {
		t.Fatalf("second stream: %d", code)
	}
	if s.resp.Header.Get("Retry-After") == "" {
		t.Fatal("a refusal with no Retry-After leaves the client guessing")
	}
}

func TestEventsEndAtTokenExpiry(t *testing.T) {
	f := newEventsFixture(t, 4, EventsPolicy{Revalidate: 50 * time.Millisecond})
	hash, err := auth.HashPassword("hunter2hunter")
	if err != nil {
		t.Fatal(err)
	}

	if err := f.st.CreateUser(t.Context(), store.User{
		ID: "u1", Name: "u1", Argon2Hash: hash, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	secret, err := auth.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(400 * time.Millisecond)
	tok := store.Token{
		ID: store.NewID(), UserID: "u1", DeviceID: store.NewID(), Name: "short",
		Scopes: store.ScopeSet{store.ScopeSync}, SHA256: auth.HashSecret(secret),
		CreatedAt: time.Now(), ExpiresAt: &expires,
	}
	if err := f.st.CreateToken(t.Context(), tok); err != nil {
		t.Fatal(err)
	}
	s, code := f.open(t, secret)
	if code != http.StatusOK {
		t.Fatalf("connect: %d", code)
	}
	// A stream authenticated now must not still be authorized after the
	// credential behind it has lapsed.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := s.br.ReadString('\n'); err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an expired token kept its stream")
	}
}

func TestEventsRevocationEndsStreamBeforeNextFrame(t *testing.T) {
	for _, trigger := range []string{"heartbeat", "invalidation"} {
		t.Run(trigger, func(t *testing.T) {
			heartbeat := time.Hour
			if trigger == "heartbeat" {
				heartbeat = 50 * time.Millisecond
			}
			f := newEventsFixture(t, 4, EventsPolicy{
				Heartbeat: heartbeat, Revalidate: time.Hour,
			})
			token := f.user(t, "u1", store.ScopeSync)
			s, code := f.open(t, token)
			if code != http.StatusOK {
				t.Fatalf("connect: %d", code)
			}
			s.next(t, 5*time.Second)
			tokens, err := f.st.ListTokens(t.Context(), "u1")
			if err != nil || len(tokens) != 1 {
				t.Fatalf("tokens: %v, %v", tokens, err)
			}
			if err := f.st.RevokeToken(t.Context(), "u1", tokens[0].ID); err != nil {
				t.Fatal(err)
			}
			if trigger == "invalidation" {
				f.hub.Notify("u1", store.TopicPositions)
			}
			done := make(chan string, 1)
			go func() {
				data, _ := io.ReadAll(s.br)
				done <- string(data)
			}()
			select {
			case data := <-done:
				if strings.Contains(data, "event: invalidate") {
					t.Fatalf("revoked credential received an invalidation: %q", data)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("revoked credential kept its stream")
			}
			if _, code := f.open(t, token); code != http.StatusUnauthorized {
				t.Fatalf("reconnect after revocation: %d", code)
			}
		})
	}
}

func TestEventsFlushThroughSubpathProxy(t *testing.T) {
	f := newEventsFixture(t, 4, EventsPolicy{Heartbeat: 25 * time.Millisecond})
	token := f.user(t, "u1", store.ScopeSync)
	upstream, err := url.Parse(f.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.StripPrefix("/sync", httputil.NewSingleHostReverseProxy(upstream)))
	defer proxy.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, proxy.URL+"/sync/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxied connect: %d", resp.StatusCode)
	}
	s := &stream{resp: resp, br: bufio.NewReader(resp.Body)}
	if topics := s.next(t, time.Second); len(topics) != 2 {
		t.Fatalf("proxied opening topics: %v", topics)
	}
	for {
		line, err := s.br.ReadString('\n')
		if err != nil {
			t.Fatalf("proxied heartbeat: %v", err)
		}
		if strings.HasPrefix(line, ": ping") {
			break
		}
	}
	f.hub.Notify("u1", store.TopicAnnotations)
	if topics := s.next(t, time.Second); len(topics) != 1 || topics[0] != "annotations" {
		t.Fatalf("proxied change after idle heartbeat: %v", topics)
	}
}

func TestEventsHubShutdownEndsStreams(t *testing.T) {
	f := newEventsFixture(t, 4, EventsPolicy{})
	token := f.user(t, "u1", store.ScopeSync)
	s, code := f.open(t, token)
	if code != http.StatusOK {
		t.Fatalf("connect: %d", code)
	}
	s.next(t, 5*time.Second)
	f.hub.Close()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, s.br)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("hub shutdown left a stream open")
	}
}
