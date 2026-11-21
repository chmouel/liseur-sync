package webui

import (
	"context"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/oauth"
	"github.com/chmouel/liseur-sync/internal/store"
)

const consentRedirect = "https://client.example/callback"

type failingOAuthClientStore struct{ store.Store }

func (s failingOAuthClientStore) OAuthClient(context.Context, string) (store.OAuthClient, error) {
	return store.OAuthClient{}, errors.New("private database failure marker")
}

func assistantsServer(t *testing.T) (*httptest.Server, store.Store) {
	t.Helper()
	ts, st := testServerCfg(t, func(c *config.Config) {
		c.MCP.Enabled = true
		c.MCP.PublicURL = "https://books.example"
	}, func(s *Server) {
		s.OAuth = oauth.New(s.St, s.Cfg)
	})
	if err := st.CreateOAuthClient(t.Context(), store.OAuthClient{
		ID: "lsc_test", Kind: store.OAuthClientRegistered, Name: "Test Assistant",
		RedirectURIs: []string{consentRedirect}, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return ts, st
}

func authorizeQuery(mutate func(url.Values)) string {
	q := url.Values{
		"response_type": {"code"}, "client_id": {"lsc_test"}, "redirect_uri": {consentRedirect},
		"code_challenge": {strings.Repeat("A", 43)}, "code_challenge_method": {"S256"},
		"state": {"xyz"}, "scope": {"mcp:library mcp:text"},
	}
	if mutate != nil {
		mutate(q)
	}
	return q.Encode()
}

func getWith(t *testing.T, ts *httptest.Server, cookie *http.Cookie, path string, header map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func postRaw(t *testing.T, ts *httptest.Server, cookie *http.Cookie, path string, form url.Values) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestOAuthConsentFlow(t *testing.T) {
	ts, st := assistantsServer(t)
	path := "/ui/oauth/authorize?" + authorizeQuery(nil)

	// Arriving from the assistant's site, the Strict cookie is withheld:
	// the page offers a same-site hop rather than a password form.
	resp := getWith(t, ts, nil, path, map[string]string{"Sec-Fetch-Site": "cross-site"})
	body := readBody(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "Continue") || strings.Contains(body, `name="password"`) {
		t.Fatalf("cross-site arrival: %d %s", resp.StatusCode, body)
	}

	// Same-site without a session: sign in, and come back to consent.
	resp = getWith(t, ts, nil, path, nil)
	body = readBody(t, resp)
	if !strings.Contains(body, `name="password"`) || !strings.Contains(body, `name="next"`) {
		t.Fatalf("no login form with next: %s", body)
	}
	next := oauthNextPrefix + authorizeQuery(func(q url.Values) { q.Set("resource", "https://books.example/mcp") })
	if !strings.Contains(html.UnescapeString(body), `name="next" value="`+next+`"`) {
		t.Fatalf("login continuation missing validated parameters: %s", body)
	}
	resp = postRaw(t, ts, nil, "/ui/login", url.Values{
		"username": {"alice"}, "password": {"hunter2hunter"}, "next": {next},
	})
	readBody(t, resp)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != next {
		t.Fatalf("login with next: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			cookie = c
		}
	}

	resp = getWith(t, ts, cookie, path, nil)
	body = readBody(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "Connect Test Assistant?") {
		t.Fatalf("consent page: %d %s", resp.StatusCode, body)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "form-action 'self' https://client.example;") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("consent CSP: %q", csp)
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("consent page is framable or cacheable")
	}
	if strings.Contains(body, `value="mcp:reading"`) {
		t.Fatal("consent offers a scope that was not requested")
	}
	if !strings.Contains(body, "including text beyond your reading position") {
		t.Fatal("consent does not explain book-text access")
	}
	csrf := extractCSRF(t, body)
	form, _ := url.ParseQuery(authorizeQuery(nil))
	form.Set("decision", "allow")
	form["grant"] = []string{"mcp:library", "mcp:reading"}

	resp = postRaw(t, ts, cookie, "/ui/oauth/authorize", form)
	readBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("consent without CSRF: %d", resp.StatusCode)
	}

	form.Set("csrf", csrf)
	resp = postRaw(t, ts, cookie, "/ui/oauth/authorize", form)
	readBody(t, resp)
	loc, err := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || err != nil ||
		!strings.HasPrefix(loc.String(), consentRedirect+"?") {
		t.Fatalf("allow: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if loc.Query().Get("state") != "xyz" || loc.Query().Get("iss") != "https://books.example" {
		t.Fatalf("allow redirect: %s", loc)
	}
	g, err := st.ExchangeOAuthCode(t.Context(), store.OAuthCodeRedemption{
		SHA256: auth.HashSecret(loc.Query().Get("code")), ClientID: "lsc_test",
		RedirectURI: consentRedirect, CodeChallenge: strings.Repeat("A", 43), Resource: "https://books.example/mcp",
		GrantID: "grant-consent",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Only what was both requested and ticked: mcp:reading was ticked
	// but never asked for, and mcp:text was asked for but unticked.
	if g.UserID != "u1" || strings.Join(g.Scopes, " ") != "mcp:library" {
		t.Fatalf("grant: %+v", g)
	}

	form.Set("decision", "deny")
	resp = postRaw(t, ts, cookie, "/ui/oauth/authorize", form)
	readBody(t, resp)
	loc, _ = url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("error") != "access_denied" || loc.Query().Get("code") != "" {
		t.Fatalf("deny: %s", loc)
	}
}

func TestOAuthConsentInternationalizedRedirect(t *testing.T) {
	ts, st := assistantsServer(t)
	redirect := "https://b%C3%BCcher.example:8443/callback"
	if err := st.CreateOAuthClient(t.Context(), store.OAuthClient{
		ID: "lsc_idn", Kind: store.OAuthClientRegistered, Name: "International client",
		RedirectURIs: []string{redirect}, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	query := authorizeQuery(func(q url.Values) { q.Set("client_id", "lsc_idn"); q.Set("redirect_uri", redirect) })
	cookie := loginCookie(t, ts)
	resp := getWith(t, ts, cookie, "/ui/oauth/authorize?"+query, nil)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "form-action 'self' https://xn--bcher-kva.example:8443;") {
		t.Fatalf("consent CSP: %d %q", resp.StatusCode, resp.Header.Get("Content-Security-Policy"))
	}
	form, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	form.Set("csrf", extractCSRF(t, body))
	form.Set("decision", "allow")
	form.Set("grant", "mcp:library")
	resp = postRaw(t, ts, cookie, "/ui/oauth/authorize", form)
	readBody(t, resp)
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), redirect+"?") {
		t.Fatalf("callback: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestOAuthRedirectOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"https://b%C3%BCcher.example:8443/cb": "https://xn--bcher-kva.example:8443",
		"https://xn--bcher-kva.example/cb":    "https://xn--bcher-kva.example",
		"http://[::1]:8123/cb":                "http://[::1]:8123",
		"https://a_b.example/cb":              "",
		"https://client.example:0/cb":         "",
		"https://client.example:65536/cb":     "",
		"https://client.example:/cb":          "",
		"https://[fe80::1%25eth0]/cb":         "",
	} {
		err := oauth.ValidateRedirectURI(raw)
		if got := cspOrigin(raw); got != want || (err == nil) != (want != "") {
			t.Errorf("redirect %q: origin %q, error %v", raw, got, err)
		}
	}
}

func TestOAuthConsentContinuationBounds(t *testing.T) {
	ts, st := assistantsServer(t)
	query := authorizeQuery(func(q url.Values) { q.Set("ignored", strings.Repeat("x", 9000)) })
	for _, crossSite := range []bool{false, true} {
		headers := map[string]string{}
		if crossSite {
			headers["Sec-Fetch-Site"] = "cross-site"
		}
		resp := getWith(t, ts, nil, "/ui/oauth/authorize?"+query, headers)
		body := html.UnescapeString(readBody(t, resp))
		if resp.StatusCode != http.StatusOK || strings.Contains(body, "ignored=") {
			t.Fatalf("cross-site %t: %d, continuation contains ignored parameters", crossSite, resp.StatusCode)
		}
		if crossSite && !strings.Contains(body, `href="authorize?`) || !crossSite && !strings.Contains(body, `name="next" value="oauth/authorize?`) {
			t.Fatalf("cross-site %t: missing continuation", crossSite)
		}
	}

	redirect := "https://client.example/" + strings.Repeat("a", 8500)
	if err := st.CreateOAuthClient(t.Context(), store.OAuthClient{
		ID: "lsc_long", Kind: store.OAuthClientRegistered, Name: "Long redirect",
		RedirectURIs: []string{redirect}, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	query = authorizeQuery(func(q url.Values) { q.Set("client_id", "lsc_long"); q.Set("redirect_uri", redirect) })
	for _, crossSite := range []bool{false, true} {
		headers := map[string]string{}
		if crossSite {
			headers["Sec-Fetch-Site"] = "cross-site"
		}
		resp := getWith(t, ts, nil, "/ui/oauth/authorize?"+query, headers)
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "authorization request is too long") {
			t.Fatalf("cross-site %t: %d, expected explicit length error", crossSite, resp.StatusCode)
		}
	}
}

func TestOAuthConsentRefusesUntrustedRequests(t *testing.T) {
	ts, _ := assistantsServer(t)
	cookie := loginCookie(t, ts)
	for name, mutate := range map[string]func(url.Values){
		"unknown client": func(q url.Values) { q.Set("client_id", "lsc_nobody") },
		"wrong redirect": func(q url.Values) { q.Set("redirect_uri", "https://evil.example/cb") },
	} {
		resp := getWith(t, ts, cookie, "/ui/oauth/authorize?"+authorizeQuery(mutate), nil)
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" ||
			!strings.Contains(body, "cannot continue") {
			t.Errorf("%s: %d %q", name, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	// Once client and redirect are trusted, errors go back to the client.
	resp := getWith(t, ts, cookie, "/ui/oauth/authorize?"+authorizeQuery(func(q url.Values) {
		q.Set("code_challenge_method", "plain")
	}), nil)
	readBody(t, resp)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("plain PKCE: %d %s", resp.StatusCode, loc)
	}

	// A login next that is not the consent page is dropped.
	for _, next := range []string{"https://evil.example/", "//evil.example/", "oauth/authorize?\\@evil", "settings"} {
		resp := postRaw(t, ts, nil, "/ui/login", url.Values{
			"username": {"alice"}, "password": {"hunter2hunter"}, "next": {next},
		})
		readBody(t, resp)
		if got := resp.Header.Get("Location"); got != "./" {
			t.Errorf("next %q: redirected to %q", next, got)
		}
	}
}

func TestOAuthConsentHidesLookupErrors(t *testing.T) {
	ts, _ := testServerCfg(t, func(c *config.Config) {
		c.MCP.Enabled = true
		c.MCP.PublicURL = "https://books.example"
	}, func(s *Server) {
		s.OAuth = oauth.New(failingOAuthClientStore{s.St}, s.Cfg)
	})
	resp := getWith(t, ts, nil, "/ui/oauth/authorize?"+authorizeQuery(nil), nil)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusInternalServerError || resp.Header.Get("Location") != "" ||
		strings.Contains(body, "private database failure marker") || !strings.Contains(body, "could not be checked") {
		t.Fatalf("lookup failure: %d %q %s", resp.StatusCode, resp.Header.Get("Location"), body)
	}
}

func TestOAuthConsentDisabled(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	for _, c := range []*http.Cookie{nil, cookie} {
		if code, _ := page(t, ts, c, "/ui/oauth/authorize?"+authorizeQuery(nil)); code != http.StatusNotFound {
			t.Fatalf("consent GET with MCP off: %d", code)
		}
		for _, path := range []string{
			"/ui/oauth/authorize", "/ui/oauth/grants/g1/revoke", "/ui/admin/oauth/chatgpt",
			"/ui/admin/oauth/clients", "/ui/admin/oauth/clients/delete", "/ui/admin/oauth/grants/g1/revoke",
		} {
			resp := postRaw(t, ts, c, path, url.Values{"decision": {"allow"}})
			readBody(t, resp)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("POST %s with MCP off: %d", path, resp.StatusCode)
			}
		}
	}
	if _, body := page(t, ts, cookie, "/ui/settings?section=devices&view=assistants"); !strings.Contains(body, "has not turned on") {
		t.Fatal("assistants page does not say the feature is off")
	}
	_, admin := page(t, ts, cookie, "/ui/settings?section=admin&view=assistants")
	if !strings.Contains(admin, "restart before allowing applications") || strings.Contains(admin, "admin/oauth/") {
		t.Fatalf("disabled administration: %s", admin)
	}
}

func TestConnectedAssistantsAreTheReadersOwn(t *testing.T) {
	ts, st := assistantsServer(t)
	hash, _ := auth.HashPassword("hunter2hunter")
	if err := st.CreateUser(t.Context(), store.User{ID: "u2", Name: "bob", Argon2Hash: hash, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	client, err := st.OAuthClient(t.Context(), "lsc_test")
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"u1", "u2"} {
		sessionID := "oauth-fixture-" + owner
		if err := st.CreateAuthSession(t.Context(), store.AuthSession{ID: sessionID, UserID: owner, SHA256: "hash-" + sessionID, Kind: "web", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateOAuthCode(t.Context(), store.OAuthCode{
			SHA256: "code-" + owner, UserID: owner, ClientID: "lsc_test", ClientGeneration: client.Generation, ClientName: "Test Assistant",
			RedirectURI: consentRedirect, CodeChallenge: strings.Repeat("A", 43),
			Resource: "https://books.example/mcp", Scopes: []string{"mcp:library"},
			CreatedAt: now, ExpiresAt: now.Add(time.Minute),
		}, sessionID); err != nil {
			t.Fatal(err)
		}
		if _, err := st.ExchangeOAuthCode(t.Context(), store.OAuthCodeRedemption{
			SHA256: "code-" + owner, ClientID: "lsc_test", RedirectURI: consentRedirect,
			CodeChallenge: strings.Repeat("A", 43), Resource: "https://books.example/mcp", GrantID: "grant-" + owner,
			Tokens: []store.OAuthToken{
				{SHA256: "at-" + owner, Kind: store.OAuthTokenAccess, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
				{SHA256: "rt-" + owner, Kind: store.OAuthTokenRefresh, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)},
			},
		}, now); err != nil {
			t.Fatal(err)
		}
	}

	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=devices&view=assistants")
	if !strings.Contains(body, "https://books.example/mcp") || !strings.Contains(body, "grant-u1") || strings.Contains(body, "grant-u2") {
		t.Fatalf("assistants page: %s", body)
	}
	csrf := extractCSRF(t, body)
	// Bob's grant cannot be revoked from Alice's session.
	postForm(t, ts, cookie, "/ui/oauth/grants/grant-u2/revoke", url.Values{"csrf": {csrf}})
	if _, err := st.OAuthAccess(t.Context(), "at-u2", time.Now()); err != nil {
		t.Fatalf("alice revoked bob's grant: %v", err)
	}
	_, body = postForm(t, ts, cookie, "/ui/oauth/grants/grant-u1/revoke", url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "Assistant disconnected.") {
		t.Fatalf("revoke: %s", body)
	}
	if _, err := st.OAuthAccess(t.Context(), "at-u1", time.Now()); err == nil {
		t.Fatal("revoked grant still authenticates")
	}
}

func TestAdminAssistants(t *testing.T) {
	ts, st := assistantsServer(t)
	hash, _ := auth.HashPassword("hunter2hunter")
	if err := st.CreateUser(t.Context(), store.User{ID: "u2", Name: "bob", Argon2Hash: hash, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	bob := loginCookieAs(t, ts, "bob", "hunter2hunter")
	_, body := page(t, ts, bob, "/ui/settings")
	if code, _ := postForm(t, ts, bob, "/ui/admin/oauth/chatgpt", url.Values{
		"csrf": {extractCSRF(t, body)}, "allow": {"on"},
	}); code != http.StatusForbidden {
		t.Fatalf("non-admin toggled ChatGPT: %d", code)
	}

	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/settings?section=admin&view=assistants")
	if code != 200 || !strings.Contains(body, "Allow ChatGPT") {
		t.Fatalf("admin assistants page: %d", code)
	}
	csrf := extractCSRF(t, body)
	if _, body = postForm(t, ts, cookie, "/ui/admin/oauth/chatgpt", url.Values{"csrf": {csrf}, "allow": {"on"}}); !strings.Contains(body, "ChatGPT is allowed.") {
		t.Fatalf("allow ChatGPT: %s", body)
	}
	if c, err := st.OAuthClient(t.Context(), oauth.ChatGPTMetadataURL); err != nil || c.Kind != store.OAuthClientMetadata {
		t.Fatalf("ChatGPT client: %+v %v", c, err)
	}
	postForm(t, ts, cookie, "/ui/admin/oauth/chatgpt", url.Values{"csrf": {csrf}, "allow": {"off"}})
	if _, err := st.OAuthClient(t.Context(), oauth.ChatGPTMetadataURL); err == nil {
		t.Fatal("ChatGPT still allowed")
	}
	for _, kind := range []string{"registered", "metadata"} {
		for _, name := range []string{"bad\x00name", "bad\xffname"} {
			_, body = postForm(t, ts, cookie, "/ui/admin/oauth/clients", url.Values{
				"csrf": {csrf}, "kind": {kind}, "name": {name},
				"metadata_url": {"https://client.example/metadata"}, "redirect_uris": {"https://client.example/cb"},
			})
			if !strings.Contains(body, "client name contains invalid text") {
				t.Fatalf("accepted invalid %s client name", kind)
			}
		}
	}

	code, body = postForm(t, ts, cookie, "/ui/admin/oauth/clients", url.Values{
		"csrf": {csrf}, "kind": {"registered"}, "name": {"Desk"},
		"redirect_uris": {"http://evil.example/cb"},
	})
	if !strings.Contains(body, "must be https") {
		t.Fatalf("plain http redirect accepted: %d", code)
	}
	code, body = postForm(t, ts, cookie, "/ui/admin/oauth/clients", url.Values{
		"csrf": {csrf}, "kind": {"registered"}, "name": {"Desk"}, "confidential": {"on"},
		"redirect_uris": {"http://127.0.0.1:8765/cb\nhttps://desk.example/cb\n"},
	})
	secret := secretFromPage(t, body)
	clients, _ := st.ListOAuthClients(t.Context())
	var desk store.OAuthClient
	for _, c := range clients {
		if c.Name == "Desk" {
			desk = c
		}
	}
	if code != 200 || desk.SecretSHA256 != auth.HashSecret(secret) || len(desk.RedirectURIs) != 2 {
		t.Fatalf("registered client: %d %+v", code, desk)
	}

	postForm(t, ts, cookie, "/ui/admin/oauth/clients/delete", url.Values{"csrf": {csrf}, "client_id": {desk.ID}})
	if _, err := st.OAuthClient(t.Context(), desk.ID); err == nil {
		t.Fatal("client not removed")
	}
}
