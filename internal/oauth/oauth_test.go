package oauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/oauth"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

const (
	publicURL = "https://sync.example"
	resource  = publicURL + "/mcp"
	redirect  = "https://app.example/cb"
	verifier  = "a-very-long-pkce-verifier-that-is-at-least-43-characters-long"
)

func challenge(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type fixture struct {
	srv    *oauth.Server
	st     store.Store
	user   store.User
	now    time.Time
	secret string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MCP.Enabled = true
	cfg.MCP.PublicURL = publicURL
	f := &fixture{st: st, srv: oauth.New(st, cfg), now: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	f.srv.SetClock(func() time.Time { return f.now })
	f.user = storetest.MkUser(t, st, "reader")
	ctx := context.Background()
	if err := st.CreateOAuthClient(ctx, store.OAuthClient{
		ID: "lsc_public", Kind: store.OAuthClientRegistered, Name: "Public",
		RedirectURIs: []string{redirect}, CreatedAt: f.now,
	}); err != nil {
		t.Fatal(err)
	}
	f.secret = "s3cret"
	if err := st.CreateOAuthClient(ctx, store.OAuthClient{
		ID: "lsc_conf", Kind: store.OAuthClientRegistered, Name: "Confidential",
		RedirectURIs: []string{redirect}, SecretSHA256: auth.HashSecret(f.secret), CreatedAt: f.now,
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func authorizeQuery(client string) url.Values {
	return url.Values{
		"response_type": {"code"}, "client_id": {client}, "redirect_uri": {redirect},
		"code_challenge": {challenge(verifier)}, "code_challenge_method": {"S256"},
		"resource": {resource}, "state": {"xyz"}, "scope": {"mcp:library mcp:text"},
	}
}

// code runs the consent step and returns the issued code.
func (f *fixture) code(t *testing.T, client string, scopes ...string) string {
	t.Helper()
	req, err := f.srv.ParseAuthorize(context.Background(), authorizeQuery(client))
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) == 0 {
		scopes = req.Scopes
	}
	to, err := f.srv.Approve(context.Background(), req, f.user.ID, scopes)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(to)
	q := u.Query()
	if q.Get("state") != "xyz" || q.Get("iss") != publicURL || !strings.HasPrefix(to, redirect+"?") {
		t.Fatalf("redirect %s", to)
	}
	return q.Get("code")
}

func (f *fixture) token(form url.Values, basic ...string) (int, map[string]any) {
	r := httptest.NewRequest(http.MethodPost, oauth.TokenPath, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if len(basic) == 2 {
		r.SetBasicAuth(basic[0], basic[1])
	}
	w := httptest.NewRecorder()
	f.srv.HandleToken(w, r)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Header().Get("Cache-Control") != "no-store" {
		body["_cache"] = "missing no-store"
	}
	return w.Code, body
}

func exchange(code, client string) url.Values {
	return url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client},
		"redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {resource},
	}
}

// mcp reports -1 for a 401 carrying the RFC 9728 challenge, else the status.
func (f *fixture) mcp(token string) int {
	h := f.srv.RequireToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := oauth.AccessFrom(r.Context()); !ok {
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == http.StatusUnauthorized && strings.Contains(w.Header().Get("WWW-Authenticate"), `resource_metadata="`+publicURL+oauth.ResourceMetadataPath) {
		return -1
	}
	return w.Code
}

func TestCodeFlowAndRefreshRotation(t *testing.T) {
	f := newFixture(t)
	code := f.code(t, "lsc_public", "mcp:text")
	status, body := f.token(exchange(code, "lsc_public"))
	if status != http.StatusOK || body["scope"] != "mcp:text" || body["token_type"] != "Bearer" || body["_cache"] != nil {
		t.Fatalf("exchange: %d %v", status, body)
	}
	access := body["access_token"].(string)
	if got := f.mcp(access); got != http.StatusOK {
		t.Fatalf("fresh access token: %d", got)
	}

	if status, _ := f.token(exchange(code, "lsc_public")); status != http.StatusBadRequest {
		t.Fatalf("replayed code: %d", status)
	}
	if got := f.mcp(access); got != -1 {
		t.Fatalf("a replayed code must revoke what it produced: %d", got)
	}

	code = f.code(t, "lsc_public")
	_, body = f.token(exchange(code, "lsc_public"))
	access, refresh := body["access_token"].(string), body["refresh_token"].(string)
	if status, body := f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {"lsc_public"}, "scope": {"mcp:library"}}); status != http.StatusBadRequest || body["error"] != "invalid_scope" {
		t.Fatalf("scoped refresh: %d %v", status, body)
	}
	status, body = f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {"lsc_public"}})
	if status != http.StatusOK || body["scope"] != "mcp:library mcp:text" {
		t.Fatalf("refresh: %d %v", status, body)
	}
	if f.mcp(access) != -1 {
		t.Fatal("rotation must retire the previous access token")
	}
	newAccess := body["access_token"].(string)
	if f.mcp(newAccess) != http.StatusOK {
		t.Fatal("rotated access token refused")
	}
	if status, _ := f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {"lsc_public"}}); status != http.StatusBadRequest {
		t.Fatalf("reused refresh token: %d", status)
	}
	if f.mcp(newAccess) != -1 {
		t.Fatal("refresh token reuse must revoke the whole grant")
	}

	f.now = f.now.Add(2 * time.Hour)
	code = f.code(t, "lsc_public")
	_, body = f.token(exchange(code, "lsc_public"))
	f.now = f.now.Add(time.Hour)
	if f.mcp(body["access_token"].(string)) != -1 {
		t.Fatal("an access token must expire after its TTL")
	}
}

func TestCodeExchangeWithoutRedirectURI(t *testing.T) {
	f := newFixture(t)
	code := f.code(t, "lsc_public")
	form := exchange(code, "lsc_public")
	form.Del("redirect_uri")
	if status, body := f.token(form); status != http.StatusOK || body["access_token"] == nil {
		t.Fatalf("exchange without redirect_uri: %d %v", status, body)
	}
}

func TestExchangeRejects(t *testing.T) {
	f := newFixture(t)
	cases := map[string]func(url.Values) url.Values{
		"wrong verifier":     func(v url.Values) url.Values { v.Set("code_verifier", strings.Repeat("b", 50)); return v },
		"no verifier":        func(v url.Values) url.Values { v.Del("code_verifier"); return v },
		"other redirect":     func(v url.Values) url.Values { v.Set("redirect_uri", "https://app.example/other"); return v },
		"wrong resource":     func(v url.Values) url.Values { v.Set("resource", "https://evil.example/mcp"); return v },
		"other client":       func(v url.Values) url.Values { v.Set("client_id", "lsc_conf"); return v },
		"implicit grant":     func(v url.Values) url.Values { v.Set("grant_type", "password"); return v },
		"client credentials": func(v url.Values) url.Values { v.Set("grant_type", "client_credentials"); return v },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			code := f.code(t, "lsc_public")
			if status, body := f.token(mutate(exchange(code, "lsc_public"))); status < 400 || status >= 500 {
				t.Fatalf("%d %v", status, body)
			}
		})
	}
	t.Run("expired code", func(t *testing.T) {
		code := f.code(t, "lsc_public")
		f.now = f.now.Add(61 * time.Second)
		if status, _ := f.token(exchange(code, "lsc_public")); status != http.StatusBadRequest {
			t.Fatalf("%d", status)
		}
	})
	t.Run("query parameters", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, oauth.TokenPath+"?code=x", strings.NewReader("grant_type=authorization_code"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		f.srv.HandleToken(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%d", w.Code)
		}
	})
}

func TestConfidentialClientMustAuthenticate(t *testing.T) {
	f := newFixture(t)
	code := f.code(t, "lsc_conf")
	if status, _ := f.token(exchange(code, "lsc_conf")); status != http.StatusUnauthorized {
		t.Fatalf("no secret: %d", status)
	}
	if status, _ := f.token(exchange(code, "lsc_conf"), "lsc_conf", "wrong"); status != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", status)
	}
	if status, body := f.token(exchange(code, "lsc_conf"), "lsc_conf", f.secret); status != http.StatusOK {
		t.Fatalf("basic auth: %d %v", status, body)
	}
	if status, _ := f.token(exchange(f.code(t, "lsc_public"), "lsc_public"), "lsc_public", "anything"); status != http.StatusUnauthorized {
		t.Fatalf("a public client presenting a secret: %d", status)
	}
	form := exchange(f.code(t, "lsc_public"), "lsc_public")
	form.Set("client_assertion_type", "urn:ietf:params:oauth:client-assertion-type:jwt-bearer")
	form.Set("client_assertion", "eyJ.e30.sig")
	if status, _ := f.token(form); status != http.StatusUnauthorized {
		t.Fatalf("an unadvertised client assertion: %d", status)
	}
}

func TestParseAuthorize(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	noRedirect := map[string]func(url.Values){
		"unknown client":        func(v url.Values) { v.Set("client_id", "lsc_nobody") },
		"unregistered redirect": func(v url.Values) { v.Set("redirect_uri", "https://evil.example/cb") },
		"prefix redirect":       func(v url.Values) { v.Set("redirect_uri", redirect+"/more") },
		"missing redirect":      func(v url.Values) { v.Del("redirect_uri") },
	}
	for name, mutate := range noRedirect {
		q := authorizeQuery("lsc_public")
		mutate(q)
		_, err := f.srv.ParseAuthorize(ctx, q)
		ae, ok := err.(*oauth.AuthorizeError)
		if !ok || ae.Redirect != "" {
			t.Errorf("%s: must fail without redirecting: %v", name, err)
		}
	}
	redirected := map[string]func(url.Values){
		"plain pkce":             func(v url.Values) { v.Set("code_challenge_method", "plain") },
		"no pkce":                func(v url.Values) { v.Del("code_challenge"); v.Del("code_challenge_method") },
		"short challenge":        func(v url.Values) { v.Set("code_challenge", "abc") },
		"long challenge":         func(v url.Values) { v.Set("code_challenge", strings.Repeat("A", 44)) },
		"verifier alphabet":      func(v url.Values) { v.Set("code_challenge", strings.Repeat("A", 42)+"~") },
		"noncanonical challenge": func(v url.Values) { v.Set("code_challenge", strings.Repeat("A", 42)+"B") },
		"token response":         func(v url.Values) { v.Set("response_type", "token") },
		"other resource":         func(v url.Values) { v.Set("resource", "https://sync.example/v1") },
		"unknown scope":          func(v url.Values) { v.Set("scope", "mcp:library admin") },
		"repeated":               func(v url.Values) { v.Add("scope", "mcp:text") },
	}
	for name, mutate := range redirected {
		q := authorizeQuery("lsc_public")
		mutate(q)
		_, err := f.srv.ParseAuthorize(ctx, q)
		ae, ok := err.(*oauth.AuthorizeError)
		if !ok {
			t.Errorf("%s: accepted", name)
			continue
		}
		if name == "repeated" {
			continue
		}
		if !strings.HasPrefix(ae.Redirect, redirect+"?") || !strings.Contains(ae.Redirect, "state=xyz") {
			t.Errorf("%s: redirect %q", name, ae.Redirect)
		}
	}
	q := authorizeQuery("lsc_public")
	q.Del("scope")
	q.Del("resource")
	req, err := f.srv.ParseAuthorize(ctx, q)
	if err != nil || len(req.Scopes) != len(oauth.Scopes) || req.Resource != resource {
		t.Fatalf("defaults: %+v %v", req, err)
	}
	to, _ := f.srv.Approve(ctx, req, f.user.ID, nil)
	if !strings.Contains(to, "error=access_denied") {
		t.Fatalf("approving nothing is a refusal: %s", to)
	}
}

func TestBearerRefusesOtherTokens(t *testing.T) {
	f := newFixture(t)
	tok, err := auth.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", tok, "lsmcp_at_" + tok, "lsmcp_rt_" + tok} {
		if got := f.mcp(bad); got != -1 {
			t.Errorf("%q: %d", bad, got)
		}
	}
	_, body := f.token(exchange(f.code(t, "lsc_public"), "lsc_public"))
	if f.mcp(body["refresh_token"].(string)) != -1 {
		t.Fatal("a refresh token is not an access token")
	}
	if err := f.st.SetUserDisabled(context.Background(), f.user.ID, true, f.now); err != nil {
		t.Fatal(err)
	}
	if f.mcp(body["access_token"].(string)) != -1 {
		t.Fatal("a disabled account's token must stop working")
	}
}

func TestMetadataClient(t *testing.T) {
	f := newFixture(t)
	var doc any
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect.json":
			http.Redirect(w, r, "/client.json", http.StatusFound)
			return
		case "/big.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"client_id":"` + strings.Repeat("x", 70<<10) + `"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	defer srv.Close()
	id := srv.URL + "/client.json"
	ctx := context.Background()

	// A metadata URL nobody allowlisted is refused before anything is
	// fetched.
	if _, err := f.srv.ResolveClient(ctx, id); err != oauth.ErrUnknownClient {
		t.Fatalf("unlisted: %v", err)
	}
	if err := f.st.CreateOAuthClient(ctx, store.OAuthClient{ID: id, Kind: store.OAuthClientMetadata, CreatedAt: f.now}); err != nil {
		t.Fatal(err)
	}

	// The hardened fetcher never reaches a loopback address.
	if _, err := f.srv.ResolveClient(ctx, id); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("loopback fetch: %v", err)
	}

	f.srv.Fetcher.Client = srv.Client()
	f.srv.Fetcher.Client.CheckRedirect = oauth.NewMetadataFetcher().Client.CheckRedirect
	doc = map[string]any{"client_id": "https://other.example/client.json", "redirect_uris": []string{redirect}}
	if _, err := f.srv.ResolveClient(ctx, id); err == nil {
		t.Fatal("a document naming another client_id was accepted")
	}
	doc = map[string]any{"client_id": id, "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "client_secret_basic"}
	if _, err := f.srv.ResolveClient(ctx, id); err == nil {
		t.Fatal("a metadata client with a shared secret was accepted")
	}
	doc = map[string]any{"client_id": id, "redirect_uris": []string{"http://app.example/cb"}, "token_endpoint_auth_method": "none"}
	if _, err := f.srv.ResolveClient(ctx, id); err == nil {
		t.Fatal("a plain-http redirect was accepted")
	}
	doc = map[string]any{"client_id": id, "client_name": "Example App", "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none"}
	c, err := f.srv.ResolveClient(ctx, id)
	if err != nil || c.Name != "Example App" || c.Host() == "" {
		t.Fatalf("valid document: %+v %v", c, err)
	}
	doc = map[string]any{"client_id": id, "redirect_uris": []string{"https://changed.example/cb"}}
	if c, _ := f.srv.ResolveClient(ctx, id); c.RedirectURIs[0] != redirect {
		t.Fatal("documents are cached")
	}

	for _, path := range []string{"/redirect.json", "/big.json"} {
		other := srv.URL + path
		if err := f.st.CreateOAuthClient(ctx, store.OAuthClient{ID: other, Kind: store.OAuthClientMetadata, CreatedAt: f.now}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.srv.ResolveClient(ctx, other); err == nil {
			t.Errorf("%s accepted", path)
		}
	}
}

func TestMetadataClientAuthenticationMethods(t *testing.T) {
	f := newFixture(t)
	var doc map[string]any
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	defer srv.Close()
	id := srv.URL + "/client.json"
	if err := f.st.CreateOAuthClient(t.Context(), store.OAuthClient{ID: id, Kind: store.OAuthClientMetadata, CreatedAt: f.now}); err != nil {
		t.Fatal(err)
	}
	f.srv.Fetcher.Client = srv.Client()
	for _, tc := range []struct {
		name    string
		method  string
		methods []string
		secret  string
		valid   bool
	}{
		{name: "none", method: "none", valid: true},
		{name: "none alternative", method: "private_key_jwt", methods: []string{"none", "private_key_jwt"}, valid: true},
		{name: "jwt only", method: "private_key_jwt"},
		{name: "unknown", method: "unknown"},
		{name: "omitted"},
		{name: "secret", method: "none", secret: "secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc = map[string]any{"client_id": id, "redirect_uris": []string{redirect},
				"token_endpoint_auth_method": tc.method, "token_endpoint_auth_methods_supported": tc.methods,
				"client_secret": tc.secret}
			f.srv.Fetcher.Forget(id)
			_, err := f.srv.ResolveClient(t.Context(), id)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}

	doc = map[string]any{"client_id": id, "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none"}
	f.srv.Fetcher.Forget(id)
	code := f.code(t, id)
	doc["token_endpoint_auth_method"] = "private_key_jwt"
	f.srv.Fetcher.Forget(id)
	if status, body := f.token(exchange(code, id)); status != http.StatusUnauthorized || body["error"] != "invalid_client" {
		t.Fatalf("exchange with unsupported authentication: %d %v", status, body)
	}
	doc["token_endpoint_auth_method"] = "none"
	f.srv.Fetcher.Forget(id)
	status, body := f.token(exchange(code, id))
	if status != http.StatusOK {
		t.Fatalf("exchange with public authentication: %d %v", status, body)
	}
	refresh := body["refresh_token"].(string)
	doc["token_endpoint_auth_method"] = "private_key_jwt"
	f.srv.Fetcher.Forget(id)
	if status, body := f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {id}}); status != http.StatusUnauthorized || body["error"] != "invalid_client" {
		t.Fatalf("refresh with unsupported authentication: %d %v", status, body)
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "::1": false,
		"fe80::1": false, "fd00::1": false, "::ffff:127.0.0.1": false, "224.0.0.1": false,
	} {
		if got := oauth.PublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}

func TestValidateRedirectURI(t *testing.T) {
	for uri, ok := range map[string]bool{
		"https://a.example/cb": true, "http://127.0.0.1:8123/cb": true, "http://localhost/cb": true,
		"http://a.example/cb": false, "https://a.example/cb#x": false, "javascript:alert(1)": false,
		"custom://cb": false, "https://user@a.example/cb": false,
	} {
		if err := oauth.ValidateRedirectURI(uri); (err == nil) != ok {
			t.Errorf("%s: %v", uri, err)
		}
	}
}
