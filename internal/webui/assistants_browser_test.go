package webui

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/oauth"
	"github.com/chmouel/liseur-sync/internal/store"
)

func TestOAuthConsentInRealBrowser(t *testing.T) {
	chrome := os.Getenv("LISEUR_CHROME")
	if chrome == "" {
		t.Skip("set LISEUR_CHROME to run the browser check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to drive the browser")
	}
	var authorizationURL string
	callback := make(chan url.Values, 1)
	client := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/callback" {
			callback <- r.URL.Query()
			_, _ = fmt.Fprint(w, "Callback received")
			return
		}
		_, _ = fmt.Fprintf(w, `<a id="connect" href="%s">Connect</a>`, html.EscapeString(authorizationURL))
	}))
	defer client.Close()
	_, clientPort, err := net.SplitHostPort(client.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	redirect := "https://b%C3%BCcher.test:" + clientPort + "/callback"
	st := NewTestStore(t)
	hash, err := auth.HashPassword("hunter2hunter")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(t.Context(), store.User{ID: "u1", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateOAuthClient(t.Context(), store.OAuthClient{ID: "lsc_browser", Kind: store.OAuthClientRegistered,
		Name: "Browser Assistant", RedirectURIs: []string{redirect}, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MCP.Enabled = true
	issuer := httptest.NewUnstartedServer(nil)
	_, issuerPort, err := net.SplitHostPort(issuer.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cfg.MCP.PublicURL = "https://issuer.test:" + issuerPort
	s := &Server{St: st, Auth: auth.NewService(st), Cfg: cfg, OAuth: oauth.New(st, cfg)}
	mux := http.NewServeMux()
	s.Mount(mux, func(h http.Handler) http.Handler { return auth.RequireSecureTransport(cfg, h) })
	mux.HandleFunc("POST /oauth/token", s.OAuth.HandleToken)
	mux.Handle("POST /mcp", s.OAuth.RequireToken(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	issuer.Config.Handler = mux
	issuer.StartTLS()
	defer issuer.Close()
	verifier := strings.Repeat("a", 64)
	challenge := sha256.Sum256([]byte(verifier))
	query := authorizeQuery(func(q url.Values) {
		q.Set("client_id", "lsc_browser")
		q.Set("redirect_uri", redirect)
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	})
	authorizationURL = cfg.MCP.PublicURL + "/ui/oauth/authorize?" + query
	client.StartTLS()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join("testdata", "oauthbrowser.mjs"))
	cmd.Env = append(os.Environ(), "OAUTH_CHROME="+chrome, "OAUTH_ISSUER="+cfg.MCP.PublicURL,
		"OAUTH_CLIENT=https://xn--bcher-kva.test:"+clientPort)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser consent flow: %v\n%s", err, out)
	}
	var code string
	select {
	case q := <-callback:
		if q.Get("state") != "xyz" || q.Get("code") == "" || q.Get("iss") != cfg.MCP.PublicURL {
			t.Fatal("callback is missing the authorization response")
		}
		code = q.Get("code")
	case <-time.After(time.Second):
		t.Fatal("client did not receive the callback")
	}
	type tokenResponse struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
	}
	tokenRequest := func(form url.Values) (int, tokenResponse) {
		t.Helper()
		resp, err := issuer.Client().PostForm(issuer.URL+oauth.TokenPath, form)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body tokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, body
	}
	status, tokens := tokenRequest(url.Values{
		"grant_type": {"authorization_code"}, "client_id": {"lsc_browser"}, "code": {code},
		"code_verifier": {verifier}, "redirect_uri": {redirect},
	})
	if status != http.StatusOK || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("code exchange failed: status %d", status)
	}
	refresh := tokens.RefreshToken
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {"lsc_browser"}, "refresh_token": {refresh}}
	status, rotated := tokenRequest(form)
	if status != http.StatusOK || rotated.AccessToken == "" || rotated.RefreshToken == "" || rotated.RefreshToken == refresh {
		t.Fatalf("refresh rotation failed: status %d", status)
	}
	accessStatus := func(token string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, issuer.URL+"/mcp", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := issuer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if status := accessStatus(rotated.AccessToken); status != http.StatusNoContent {
		t.Fatalf("rotated access token: %d", status)
	}
	status, replay := tokenRequest(form)
	if status != http.StatusBadRequest || replay.Error != "invalid_grant" {
		t.Fatalf("refresh replay: status %d", status)
	}
	if status := accessStatus(rotated.AccessToken); status != http.StatusUnauthorized {
		t.Fatalf("replayed refresh did not revoke the access token: %d", status)
	}
}
