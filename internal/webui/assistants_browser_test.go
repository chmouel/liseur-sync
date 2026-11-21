package webui

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	issuer.Config.Handler = mux
	issuer.StartTLS()
	defer issuer.Close()
	query := authorizeQuery(func(q url.Values) { q.Set("client_id", "lsc_browser"); q.Set("redirect_uri", redirect) })
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
	select {
	case q := <-callback:
		if q.Get("state") != "xyz" || q.Get("code") == "" || q.Get("iss") != cfg.MCP.PublicURL {
			t.Fatal("callback is missing the authorization response")
		}
	case <-time.After(time.Second):
		t.Fatal("client did not receive the callback")
	}
}
