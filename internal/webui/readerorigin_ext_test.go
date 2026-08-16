package webui_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/api"
	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/webui"
)

// The separate reader origin (ADR-0007 phase 3).
//
// The whole value of the second hostname is what it refuses, so that is
// what these tests are about: nothing authenticated answers there, no
// cookie is honoured there, and the reader still works anyway because
// the credential it needs arrives by a route a server log never sees.

const readerName = "read.example.test"

// splitOriginServer serves the same store from one mux with a reader
// origin configured, exactly as the binary does — through Handler
// rather than Routes, since the origin policies live there.
//
// The listener is opened before the config so the reader origin can name
// the port it will actually be reached on. That matters beyond
// tidiness: a browser treats two ports as two origins, so a reader
// origin without one would be a different origin from the server under
// test.
func splitOriginServer(t *testing.T, f *booksFixture) (*httptest.Server, string) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	readerHost := readerName + ":" +
		strings.TrimPrefix(ts.Listener.Addr().String(), "127.0.0.1:")
	wholeServer(t, f, ts, "http://"+readerHost)
	return ts, readerHost
}

// wholeServer mounts the API and the UI on one listener the way the
// binary does, which is the only way to exercise anything that spans
// them — the reader most of all, since it is a UI page that is an API
// client.
func wholeServer(t *testing.T, f *booksFixture, ts *httptest.Server, readerOrigin string) {
	t.Helper()
	cfg := f.cfg
	cfg.ReaderOrigin = readerOrigin
	cfg.InsecureHTTP = true

	apiSrv := &api.Server{
		St: f.st, Auth: auth.NewService(f.st), Cfg: cfg,
		LoginLimiter: auth.NewRateLimiter(100, time.Minute),
		Files:        content.NewFiles(f.st), Covers: f.cache,
	}
	apiSrv.WebUI = &webui.Server{
		St: f.st, Auth: auth.NewService(f.st), Cfg: cfg,
		LoginLimiter: auth.NewRateLimiter(100, time.Minute),
		Downloads:    apiSrv, Covers: apiSrv,
	}
	ts.Config = &http.Server{Handler: apiSrv.Handler(), ReadHeaderTimeout: time.Minute}
	ts.Start()
	t.Cleanup(ts.Close)
}

// ask sends a request to ts pretending it arrived at host, which is the
// only way one test process can be two origins.
func ask(
	t *testing.T, ts *httptest.Server, method, host, path string, cookie *http.Cookie,
) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

type readerOrigins struct {
	f      *booksFixture
	ts     *httptest.Server
	book   string
	reader string
}

func readerFixture(t *testing.T) readerOrigins {
	t.Helper()
	f := newBooksFixture(t)
	bookID := f.addBook(t, "novel", []byte(strings.Repeat("web-epub", 50)))
	ts, readerHost := splitOriginServer(t, f)
	return readerOrigins{f: f, ts: ts, book: bookID, reader: readerHost}
}

// TestReaderHandoffCarriesTheCredentialInTheFragment checks the one
// mechanism this phase rests on. The credential must not be in the path
// or the query, because those reach access logs, Referer headers and
// the operator's proxy; a fragment reaches only the script on the origin
// that was navigated to.
func TestReaderHandoffCarriesTheCredentialInTheFragment(t *testing.T) {
	o := readerFixture(t)
	f, ts, bookID, readerHost := o.f, o.ts, o.book, o.reader
	cookie := f.loginTo(t, ts, "alice")

	resp, _ := ask(t, ts, http.MethodGet, "main.example.test",
		"/ui/books/"+bookID+"/read", cookie)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reader page on the main origin: got %d, want a handoff", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "http://"+readerHost+"/ui/books/"+bookID+"/read?") {
		t.Fatalf("handoff went somewhere unexpected: %s", loc)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("a redirect carrying a credential must not be cacheable")
	}

	target, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	// The credential is in the fragment and only the credential is. The
	// two addresses beside it are not secret and are in the query, where
	// the server that receives them can act on them.
	handed, err := url.ParseQuery(target.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	token := handed.Get("t")
	if token == "" {
		t.Fatal("no credential in the fragment")
	}
	if strings.Contains(target.RawQuery, token) || strings.Contains(target.Path, token) {
		t.Fatal("the credential is in a part of the URL that reaches the server")
	}
	if got := target.Query().Get("api"); got != "http://main.example.test" {
		t.Errorf("api origin: got %q", got)
	}
	if got := target.Query().Get("back"); got != "http://main.example.test/ui/books/"+bookID {
		t.Errorf("back link: got %q", got)
	}

	// The credential is real and is only as powerful as a reader needs.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/books/"+bookID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	got, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got.Body.Close()
	if got.StatusCode != http.StatusOK {
		t.Fatalf("the handed credential cannot read the book: %d", got.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/v1/books/"+bookID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	got, err = noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got.Body.Close()
	if got.StatusCode == http.StatusOK || got.StatusCode == http.StatusNoContent {
		t.Fatal("the handed credential could delete a book")
	}

	// A book that is not there is refused on the origin that knows who
	// the reader is, rather than handed off and failing later.
	resp, _ = ask(t, ts, http.MethodGet, "main.example.test",
		"/ui/books/does-not-exist/read", cookie)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a book that is not there was handed off anyway: %d", resp.StatusCode)
	}
}

// TestReaderOriginServesOnlyTheReader is the property the second
// hostname exists for. Every route this server has must be absent there
// except the two the reader needs, and a session cookie must buy nothing
// even if a wildcard certificate lets one arrive.
func TestReaderOriginServesOnlyTheReader(t *testing.T) {
	o := readerFixture(t)
	f, ts, bookID, readerHost := o.f, o.ts, o.book, o.reader
	cookie := f.loginTo(t, ts, "alice")

	resp, page := ask(t, ts, http.MethodGet, readerHost,
		"/ui/books/"+bookID+"/read?api=http%3A%2F%2Fmain.example.test", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the reader page needs no session: got %d", resp.StatusCode)
	}
	if !strings.Contains(page, `data-detached="1"`) {
		t.Error("the reader page does not know it is detached")
	}
	if strings.Contains(page, `data-csrf=""`) == false &&
		strings.Contains(page, "data-csrf") {
		t.Error("a CSRF token appeared on an origin that has no session")
	}
	// The API origin the page was told about is the one origin the
	// policy widens for, and it is written from a checked value.
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "connect-src 'self' blob: http://main.example.test") {
		t.Errorf("the detached reader cannot reach the API it was given: %s", csp)
	}

	// A hostile link cannot make the page reach — or trust — anywhere
	// else, and cannot smuggle a directive into the header.
	for _, bad := range []string{
		"javascript:alert(1)",
		"http://evil.example/path",
		"http://evil.example%20;script-src%20*",
		"http://user:pw@evil.example",
	} {
		resp, page := ask(t, ts, http.MethodGet, readerHost,
			"/ui/books/"+bookID+"/read?api="+url.QueryEscape(bad)+
				"&back="+url.QueryEscape(bad), nil)
		csp := resp.Header.Get("Content-Security-Policy")
		if strings.Contains(csp, "evil.example") || strings.Contains(csp, "script-src *") {
			t.Errorf("api=%q reached the policy: %s", bad, csp)
		}
		if strings.Contains(page, "evil.example") || strings.Contains(page, "javascript:") {
			t.Errorf("api=%q reached the page", bad)
		}
	}

	for _, path := range []string{
		"/ui/", "/ui/library", "/ui/books/" + bookID, "/ui/settings", "/ui/admin",
		"/ui/login", "/ui/devices", "/v1/books", "/v1/ops", "/healthz",
		"/ui/books/" + bookID + "/download", "/opds/v1.2",
	} {
		resp, body := ask(t, ts, http.MethodGet, readerHost, path, cookie)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s answered %d on the reader origin", path, resp.StatusCode)
		}
		if strings.Contains(body, "alice") {
			t.Errorf("%s leaked the signed-in user on the reader origin", path)
		}
	}

	// The reader's own assets are the exception, because without them
	// the page is a blank document.
	resp, _ = ask(t, ts, http.MethodGet, readerHost, "/ui/static/reader-app.js", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the reader origin cannot serve its own script: %d", resp.StatusCode)
	}

	// A mutation is refused before a handler can be reached, so a route
	// added later cannot appear here by being registered.
	resp, _ = ask(t, ts, http.MethodPost, readerHost, "/ui/reader/token", cookie)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("the reader origin answered a POST: %d", resp.StatusCode)
	}

	// And the main origin is untouched by any of this.
	resp, _ = ask(t, ts, http.MethodGet, "main.example.test", "/ui/library", cookie)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the main origin lost a page to the split: %d", resp.StatusCode)
	}
}

// TestAPIAnswersTheReaderOriginCrossOrigin covers the other half: the
// detached reader is an ordinary API client on another hostname, so the
// API has to say yes to it — with a bearer token, never with a cookie.
func TestAPIAnswersTheReaderOriginCrossOrigin(t *testing.T) {
	o := readerFixture(t)
	ts, readerHost := o.ts, o.reader

	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/v1/ops", nil)
	req.Host = "main.example.test"
	req.Header.Set("Origin", "http://"+readerHost)
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight from the reader origin: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://"+readerHost {
		t.Errorf("allow-origin: got %q", got)
	}
	if resp.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("the API offered to accept cookies cross-origin")
	}
	if !strings.Contains(resp.Header.Get("Vary"), "Origin") {
		t.Error("a cross-origin answer that does not Vary can be served to the wrong origin")
	}

	// Somebody else's origin gets nothing, including no preflight.
	req, _ = http.NewRequest(http.MethodOptions, ts.URL+"/v1/ops", nil)
	req.Host = "main.example.test"
	req.Header.Set("Origin", "https://evil.example")
	resp, err = noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent ||
		resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("an unlisted origin was allowed: %d %q",
			resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}

	// The web UI is never cross-origin, whoever asks: its defence is a
	// CSRF token on a same-origin form, and a hole here would undo it.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/ui/library", nil)
	req.Host = "main.example.test"
	req.Header.Set("Origin", "http://"+readerHost)
	resp, err = noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("the web UI answered cross-origin")
	}
}

// TestSameOriginDeploymentIsUnchanged makes sure the default deployment
// pays nothing for a feature it did not turn on.
func TestSameOriginDeploymentIsUnchanged(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "novel", []byte(strings.Repeat("web-epub", 50)))

	resp, page := f.get(t, "/ui/books/"+bookID+"/read", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reader page: %d", resp.StatusCode)
	}
	if strings.Contains(page, `data-detached="1"`) {
		t.Error("the same-origin reader thinks it was handed off")
	}
	if !strings.Contains(page, "data-csrf=") {
		t.Error("the same-origin reader lost its CSRF token")
	}
}

// TestReaderOriginMustBeAnOrigin keeps a misconfiguration from becoming
// a reader that silently cannot reach the API: every value below would
// fail to match the Origin header a browser sends, so it is refused at
// startup instead.
func TestReaderOriginMustBeAnOrigin(t *testing.T) {
	for _, bad := range []string{
		"read.example.com",
		"https://read.example.com/reader",
		"https://read.example.com?x=1",
		"ftp://read.example.com",
		"https://",
	} {
		cfg := config.Default()
		cfg.InsecureHTTP = true
		cfg.ReaderOrigin = bad
		if err := cfg.Validate(); err == nil {
			t.Errorf("reader_origin %q was accepted", bad)
		}
	}

	// http is refused unless the operator has already said the whole
	// deployment is plaintext, because the fragment carries a credential.
	cfg := config.Default()
	cfg.ReaderOrigin = "http://read.example.com"
	if err := cfg.Validate(); err == nil {
		t.Error("a plaintext reader origin was accepted silently")
	}
	cfg.InsecureHTTP = true
	if err := cfg.Validate(); err != nil {
		t.Errorf("insecure_http did not permit a plaintext reader origin: %v", err)
	}

	// A trailing slash is a typo, not a different origin.
	cfg = config.Default()
	cfg.ReaderOrigin = "https://read.example.com/"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.ReaderOrigin != "https://read.example.com" {
		t.Errorf("reader_origin was not normalised: %q", cfg.ReaderOrigin)
	}
	if cfg.ReaderOriginHost() != "read.example.com" {
		t.Errorf("reader host: got %q", cfg.ReaderOriginHost())
	}
	// The reader origin is allowed to call the API whether or not the
	// operator also listed it, and listing it twice is not two entries.
	cfg.CORSAllowedOrigins = []string{"https://read.example.com", "https://app.example.com"}
	if got := cfg.BrowserOrigins(); len(got) != 2 {
		t.Errorf("browser origins: got %v", got)
	}
}
