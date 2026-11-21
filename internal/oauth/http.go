package oauth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

const (
	// AuthorizePath is where the consent screen lives. It is under /ui so
	// it shares the browser session and its CSRF protection.
	AuthorizePath = "/ui/oauth/authorize"
	TokenPath     = "/oauth/token"
	// ASMetadataPath is RFC 8414 authorization server metadata.
	ASMetadataPath = "/.well-known/oauth-authorization-server"
	// ResourceMetadataPath is RFC 9728 protected resource metadata for
	// /mcp, at the path-suffixed well-known location.
	ResourceMetadataPath = "/.well-known/oauth-protected-resource/mcp"
	// ResourceMetadataRootPath serves the same document for clients that
	// only look at the root well-known location.
	ResourceMetadataRootPath = "/.well-known/oauth-protected-resource"

	tokenBodyMax = 16 << 10
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// HandleASMetadata serves the authorization server metadata. It
// advertises no registration endpoint: clients are allowed by an
// administrator, never by themselves.
func (s *Server) HandleASMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         s.issuer,
		"authorization_endpoint":                         s.issuer + AuthorizePath,
		"token_endpoint":                                 s.issuer + TokenPath,
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none", "client_secret_basic", "client_secret_post"},
		"scopes_supported":                               AllScopes(),
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
	})
}

// HandleResourceMetadata serves the protected resource metadata for /mcp.
func (s *Server) HandleResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.resource,
		"authorization_servers":    []string{s.issuer},
		"scopes_supported":         AllScopes(),
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "liseur-sync",
		"resource_documentation":   "https://chmouel.github.io/liseur-sync/assistants/",
	})
}

type tokenError struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// HandleToken is the token endpoint: the authorization code grant with
// PKCE, and refresh token rotation. Nothing else.
func (s *Server) HandleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, tokenError{Error: "invalid_request", Description: "POST only"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, tokenBodyMax)
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_request", Description: "the body must be form-encoded"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_request", Description: "malformed form"})
		return
	}
	// Credentials and codes belong in the body, never the URL, where
	// they would end up in logs.
	if r.URL.RawQuery != "" {
		writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_request", Description: "parameters must be sent in the body"})
		return
	}
	for k, v := range r.PostForm {
		if len(v) > 1 {
			writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_request", Description: "parameter " + k + " is repeated"})
			return
		}
	}
	client, ok := s.authenticateClient(w, r)
	if !ok {
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.exchangeCode(w, r, client)
	case "refresh_token":
		s.refresh(w, r, client)
	default:
		writeJSON(w, http.StatusBadRequest, tokenError{Error: "unsupported_grant_type"})
	}
}

// authenticateClient identifies the client. A confidential registered
// client must prove its secret; a public client (every metadata client,
// and registered clients without a secret) is bound instead by PKCE and
// the exact redirect URI.
func (s *Server) authenticateClient(w http.ResponseWriter, r *http.Request) (Client, bool) {
	invalid := func() (Client, bool) {
		w.Header().Set("WWW-Authenticate", `Basic realm="liseur-sync"`)
		writeJSON(w, http.StatusUnauthorized, tokenError{Error: "invalid_client"})
		return Client{}, false
	}
	// private_key_jwt is not advertised, so an assertion is refused
	// rather than ignored.
	if r.PostForm.Has("client_assertion") || r.PostForm.Has("client_assertion_type") {
		return invalid()
	}
	id, secret, basic := r.BasicAuth()
	if basic {
		if r.PostForm.Get("client_id") != "" && r.PostForm.Get("client_id") != id {
			writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_request", Description: "client_id differs from the Authorization header"})
			return Client{}, false
		}
		if r.PostForm.Has("client_secret") {
			writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_request", Description: "use one client authentication method"})
			return Client{}, false
		}
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if !validClientID(id) {
		return invalid()
	}
	row, err := s.st.OAuthClient(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return invalid()
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, tokenError{Error: "server_error"})
		return Client{}, false
	}
	if row.Kind == store.OAuthClientMetadata {
		if _, err := s.Fetcher.Fetch(r.Context(), row.ID, row.Generation); err != nil {
			return invalid()
		}
	}
	c := Client{ID: row.ID, Kind: row.Kind, Name: row.Name, Confidential: row.SecretSHA256 != "", secretSHA256: row.SecretSHA256}
	if c.Confidential {
		if secret == "" || subtle.ConstantTimeCompare([]byte(auth.HashSecret(secret)), []byte(c.secretSHA256)) != 1 {
			return invalid()
		}
	} else if secret != "" {
		return invalid()
	}
	return c, true
}

func (s *Server) newTokens() (access, refresh string, tokens []store.OAuthToken, err error) {
	a, err := auth.NewSecret()
	if err != nil {
		return "", "", nil, err
	}
	rt, err := auth.NewSecret()
	if err != nil {
		return "", "", nil, err
	}
	access, refresh = accessPrefix+a, refreshPrefix+rt
	now := s.now().UTC()
	tokens = []store.OAuthToken{
		{SHA256: auth.HashSecret(access), Kind: store.OAuthTokenAccess, CreatedAt: now, ExpiresAt: now.Add(s.accessTTL)},
		{SHA256: auth.HashSecret(refresh), Kind: store.OAuthTokenRefresh, CreatedAt: now, ExpiresAt: now.Add(s.refreshTTL)},
	}
	return access, refresh, tokens, nil
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request, client Client) {
	f := r.PostForm
	code, verifier := f.Get("code"), f.Get("code_verifier")
	if code == "" || verifier == "" {
		writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_request", Description: "code and code_verifier are required"})
		return
	}
	if res := f.Get("resource"); res != "" && res != s.resource {
		writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_target"})
		return
	}
	now := s.now().UTC()
	// The verifier never reaches the store: it decides which S256
	// challenge the request presents, and the store matches that inside
	// the transaction that spends the code.
	challenge, ok := pkceChallenge(verifier)
	if !ok {
		writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_grant"})
		return
	}
	access, refresh, tokens, err := s.newTokens()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, tokenError{Error: "server_error"})
		return
	}
	g, err := s.st.ExchangeOAuthCode(r.Context(), store.OAuthCodeRedemption{
		SHA256: auth.HashSecret(code), ClientID: client.ID, RedirectURI: f.Get("redirect_uri"),
		CodeChallenge: challenge, Resource: s.resource,
		GrantID: store.NewID(), Tokens: tokens,
	}, now)
	if errors.Is(err, store.ErrConflict) {
		slog.Warn("oauth: authorization code replayed; grant revoked", "client", client.ID)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrConflict) {
			writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_grant"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, tokenError{Error: "server_error"})
		return
	}
	slog.Info("oauth: grant created", "grant", g.ID, "user", g.UserID, "client", g.ClientID, "scopes", strings.Join(g.Scopes, " "))
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: int64(s.accessTTL.Seconds()),
		RefreshToken: refresh, Scope: strings.Join(g.Scopes, " "),
	})
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request, client Client) {
	f := r.PostForm
	rt := f.Get("refresh_token")
	if rt == "" {
		writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_request", Description: "refresh_token is required"})
		return
	}
	if res := f.Get("resource"); res != "" && res != s.resource {
		writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_target"})
		return
	}
	if f.Has("scope") {
		writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_scope", Description: "omit scope to refresh the existing grant"})
		return
	}
	access, refresh, tokens, err := s.newTokens()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, tokenError{Error: "server_error"})
		return
	}
	g, err := s.st.RotateOAuthRefresh(r.Context(), auth.HashSecret(rt), client.ID, s.resource, s.now().UTC(), tokens)
	if errors.Is(err, store.ErrConflict) {
		slog.Warn("oauth: refresh token reused; grant revoked", "client", client.ID)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrConflict) {
			writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_grant"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, tokenError{Error: "server_error"})
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: int64(s.accessTTL.Seconds()),
		RefreshToken: refresh, Scope: strings.Join(g.Scopes, " "),
	})
}

type accessKey struct{}

// WithAccess stores a verified access in a context.
func WithAccess(ctx context.Context, a store.OAuthAccess) context.Context {
	return context.WithValue(ctx, accessKey{}, a)
}

// AccessFrom returns the verified access a request carries.
func AccessFrom(ctx context.Context) (store.OAuthAccess, bool) {
	a, ok := ctx.Value(accessKey{}).(store.OAuthAccess)
	return a, ok
}

// HasScope reports whether a grant covers a scope.
func HasScope(a store.OAuthAccess, scope string) bool {
	for _, s := range a.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// RequireToken admits a request only with a live access token issued
// for this resource. Ordinary API tokens are a different class and are
// never found here; these tokens are likewise unknown to /v1.
func (s *Server) RequireToken(next http.Handler) http.Handler {
	challenge := `Bearer resource_metadata="` + s.issuer + ResourceMetadataPath + `"`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
			w.Header().Set("WWW-Authenticate", challenge)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		tok := strings.TrimSpace(h[len(prefix):])
		var a store.OAuthAccess
		err := store.ErrNotFound
		if strings.HasPrefix(tok, accessPrefix) && len(tok) <= 256 {
			a, err = s.st.OAuthAccess(r.Context(), auth.HashSecret(tok), s.now().UTC())
		}
		if err == nil && a.Resource != s.resource {
			err = store.ErrNotFound
		}
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				return
			}
			w.Header().Set("WWW-Authenticate", challenge+`, error="invalid_token"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
			return
		}
		if err := s.st.TouchOAuthGrant(r.Context(), a.GrantID, s.now().UTC()); err != nil {
			slog.Warn("oauth: recording grant use", "grant", a.GrantID, "err", err)
		}
		next.ServeHTTP(w, r.WithContext(WithAccess(r.Context(), a)))
	})
}
