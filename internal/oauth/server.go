// Package oauth is the authorization server in front of the assistant
// endpoint (ADR-0069). It issues opaque, hashed, audience-bound tokens
// through the authorization code flow with PKCE, to clients an
// administrator allowed, after a signed-in reader consented.
package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Scope names. Every one of them is read-only.
const (
	ScopeLibrary    = "mcp:library"
	ScopeReading    = "mcp:reading"
	ScopeInsights   = "mcp:insights"
	ScopeVocabulary = "mcp:vocabulary"
	ScopeText       = "mcp:text"
)

// ScopeInfo describes a scope on the consent screen.
type ScopeInfo struct {
	Name        string
	Title       string
	Description string
}

// Scopes lists every scope in the order the consent screen shows them.
var Scopes = []ScopeInfo{
	{ScopeLibrary, "Your library", "Search the books you have access to and read their titles, authors and series."},
	{ScopeReading, "Your reading", "See what you are reading, where you are in each book, and your highlights, notes and bookmarks."},
	{ScopeInsights, "Your statistics", "See your reading time, pace, streaks and calendar."},
	{ScopeVocabulary, "Your vocabulary", "See saved words, definitions, notes and example sentences."},
	{ScopeText, "Book text", "Read any chapter or passage from your books, including text beyond your reading position."},
}

// AllScopes returns every scope name.
func AllScopes() []string {
	out := make([]string, len(Scopes))
	for i, s := range Scopes {
		out[i] = s.Name
	}
	return out
}

func knownScope(name string) bool {
	return slices.ContainsFunc(Scopes, func(s ScopeInfo) bool { return s.Name == name })
}

const (
	codeTTL         = 60 * time.Second
	accessPrefix    = "lsmcp_at_"
	refreshPrefix   = "lsmcp_rt_"
	codePrefix      = "lsmcp_ac_"
	registeredIDPre = "lsc_"
)

// Server is the authorization server. Its issuer and resource come from
// configuration, never from a request's Host header.
type Server struct {
	st         store.Store
	cfg        config.Config
	issuer     string
	resource   string
	accessTTL  time.Duration
	refreshTTL time.Duration
	Fetcher    *MetadataFetcher
	now        func() time.Time
}

func New(st store.Store, cfg config.Config) *Server {
	return &Server{
		st:         st,
		cfg:        cfg,
		issuer:     cfg.MCP.PublicURL,
		resource:   cfg.MCPResource(),
		accessTTL:  time.Duration(cfg.MCP.AccessTokenTTL),
		refreshTTL: time.Duration(cfg.MCP.RefreshTokenTTL),
		Fetcher:    NewMetadataFetcher(),
		now:        time.Now,
	}
}

// Issuer is the authorization server's identifier.
func (s *Server) Issuer() string { return s.issuer }

// Resource is the canonical URL of the protected MCP endpoint.
func (s *Server) Resource() string { return s.resource }

// SetClock replaces the clock; for tests.
func (s *Server) SetClock(now func() time.Time) { s.now = now }

// Client is a resolved client: the stored row, plus for a metadata
// client what its document says right now.
type Client struct {
	ID           string
	Kind         string
	Name         string
	RedirectURIs []string
	Confidential bool
	generation   string
	secretSHA256 string
}

// Host is what the consent screen shows as the client's verified
// origin: the metadata document's host, or nothing for a registered
// client.
func (c Client) Host() string {
	if c.Kind != store.OAuthClientMetadata {
		return ""
	}
	u, err := url.Parse(c.ID)
	if err != nil {
		return ""
	}
	return u.Host
}

// ErrUnknownClient is returned for a client_id nobody allowed.
var ErrUnknownClient = errors.New("this application is not allowed on this server")

var errInvalidClientMetadata = errors.New("the application's metadata could not be verified")

func validClientID(id string) bool {
	return id != "" && len(id) <= maxClientIDBytes && utf8.ValidString(id) && !strings.ContainsRune(id, 0)
}

// ResolveClient looks a client up. A metadata client is trusted only
// because an administrator allowlisted its exact URL; its redirect URIs
// come from the document itself.
func (s *Server) ResolveClient(ctx context.Context, clientID string) (Client, error) {
	if !validClientID(clientID) {
		return Client{}, ErrUnknownClient
	}
	row, err := s.st.OAuthClient(ctx, clientID)
	if errors.Is(err, store.ErrNotFound) {
		return Client{}, ErrUnknownClient
	}
	if err != nil {
		return Client{}, err
	}
	c := Client{ID: row.ID, Kind: row.Kind, Name: row.Name, RedirectURIs: row.RedirectURIs,
		Confidential: row.SecretSHA256 != "", generation: row.Generation, secretSHA256: row.SecretSHA256}
	if row.Kind == store.OAuthClientMetadata {
		doc, err := s.Fetcher.Fetch(ctx, row.ID, row.Generation)
		if err != nil {
			return Client{}, fmt.Errorf("%w: %w", errInvalidClientMetadata, err)
		}
		c.RedirectURIs = doc.RedirectURIs
		if doc.ClientName != "" {
			c.Name = doc.ClientName
		}
	}
	if c.Name == "" {
		c.Name = c.Host()
	}
	return c, nil
}

// AuthorizeRequest is a validated authorization request.
type AuthorizeRequest struct {
	Client        Client
	RedirectURI   string
	State         string
	CodeChallenge string
	Resource      string
	Scopes        []string
}

// Params returns the request as the query parameters it came from, so
// the consent form can carry it through a POST and have it validated
// again.
func (a *AuthorizeRequest) Params() url.Values {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", a.Client.ID)
	v.Set("redirect_uri", a.RedirectURI)
	v.Set("code_challenge", a.CodeChallenge)
	v.Set("code_challenge_method", "S256")
	v.Set("resource", a.Resource)
	v.Set("scope", strings.Join(a.Scopes, " "))
	if a.State != "" {
		v.Set("state", a.State)
	}
	return v
}

// RedirectHost is the host the browser is sent back to.
func (a *AuthorizeRequest) RedirectHost() string {
	u, err := url.Parse(a.RedirectURI)
	if err != nil {
		return ""
	}
	return u.Host
}

// AuthorizeError is a failed authorization request. When Redirect is
// empty the client or its redirect URI could not be trusted, and the
// error must be shown to the user rather than sent anywhere.
type AuthorizeError struct {
	Code        string
	Description string
	Redirect    string
}

func (e *AuthorizeError) Error() string { return e.Code + ": " + e.Description }

// ParseAuthorize validates an authorization request. The client and the
// redirect URI are checked first; until both are trusted, nothing is
// redirected.
func (s *Server) ParseAuthorize(ctx context.Context, q url.Values) (*AuthorizeRequest, error) {
	for _, k := range []string{"client_id", "redirect_uri", "state", "code_challenge", "resource", "scope", "response_type", "code_challenge_method"} {
		if len(q[k]) > 1 {
			return nil, &AuthorizeError{Code: "invalid_request", Description: "parameter " + k + " is repeated"}
		}
	}
	client, err := s.ResolveClient(ctx, q.Get("client_id"))
	if err != nil {
		if errors.Is(err, ErrUnknownClient) {
			return nil, &AuthorizeError{Code: "unauthorized_client", Description: ErrUnknownClient.Error()}
		}
		if errors.Is(err, errInvalidClientMetadata) {
			return nil, &AuthorizeError{Code: "unauthorized_client", Description: errInvalidClientMetadata.Error()}
		}
		return nil, err
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" || !slices.Contains(client.RedirectURIs, redirect) {
		return nil, &AuthorizeError{Code: "invalid_request", Description: "the redirect URI is not registered for this application"}
	}
	req := &AuthorizeRequest{Client: client, RedirectURI: redirect, State: q.Get("state")}
	fail := func(code, desc string) error {
		return &AuthorizeError{Code: code, Description: desc, Redirect: s.errorRedirect(req, code, desc)}
	}
	if len(req.State) > 1024 {
		req.State = ""
		return nil, fail("invalid_request", "state is too long")
	}
	if q.Get("response_type") != "code" {
		return nil, fail("unsupported_response_type", "only the authorization code flow is supported")
	}
	if q.Get("code_challenge_method") != "S256" {
		return nil, fail("invalid_request", "PKCE with code_challenge_method=S256 is required")
	}
	req.CodeChallenge = q.Get("code_challenge")
	decodedChallenge, challengeErr := base64.RawURLEncoding.Strict().DecodeString(req.CodeChallenge)
	if len(req.CodeChallenge) != 43 || challengeErr != nil || len(decodedChallenge) != sha256.Size {
		return nil, fail("invalid_request", "code_challenge is malformed")
	}
	req.Resource = q.Get("resource")
	if req.Resource == "" {
		req.Resource = s.resource
	}
	if req.Resource != s.resource {
		return nil, fail("invalid_target", "this server only issues tokens for "+s.resource)
	}
	scopes, ok := parseScopes(q.Get("scope"))
	if !ok {
		return nil, fail("invalid_scope", "unknown scope requested")
	}
	req.Scopes = scopes
	return req, nil
}

// parseScopes keeps the scopes this server knows, in canonical order.
// An empty request means every scope; the reader narrows it at consent.
func parseScopes(raw string) ([]string, bool) {
	fields := strings.Fields(raw)
	if len(fields) > 32 {
		return nil, false
	}
	if len(fields) == 0 {
		return AllScopes(), true
	}
	var out []string
	for _, f := range fields {
		if !knownScope(f) {
			return nil, false
		}
	}
	for _, s := range Scopes {
		if slices.Contains(fields, s.Name) {
			out = append(out, s.Name)
		}
	}
	return out, true
}

// validPKCE checks the RFC 7636 verifier alphabet and length.
func validPKCE(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '.' && r != '_' && r != '~' {
			return false
		}
	}
	return true
}

func (s *Server) errorRedirect(req *AuthorizeRequest, code, desc string) string {
	v := url.Values{}
	v.Set("error", code)
	if desc != "" {
		v.Set("error_description", desc)
	}
	return s.redirectWith(req, v)
}

func (s *Server) redirectWith(req *AuthorizeRequest, v url.Values) string {
	if req.State != "" {
		v.Set("state", req.State)
	}
	v.Set("iss", s.issuer)
	u, _ := url.Parse(req.RedirectURI)
	q := u.Query()
	for k, vals := range v {
		q[k] = vals
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Deny returns where to send the browser when the reader refuses.
func (s *Server) Deny(req *AuthorizeRequest) string {
	return s.errorRedirect(req, "access_denied", "the reader declined")
}

// Approve issues a single-use code for the scopes the reader kept and
// returns where to send the browser. granted must be a subset of what
// was requested; an empty set is a refusal.
func (s *Server) Approve(ctx context.Context, req *AuthorizeRequest, userID, sessionID string, granted []string) (string, error) {
	var scopes []string
	for _, sc := range req.Scopes {
		if slices.Contains(granted, sc) {
			scopes = append(scopes, sc)
		}
	}
	if len(scopes) == 0 {
		return s.Deny(req), nil
	}
	secret, err := auth.NewSecret()
	if err != nil {
		return "", err
	}
	code := codePrefix + secret
	now := s.now().UTC()
	if err := s.st.CreateOAuthCode(ctx, store.OAuthCode{
		SHA256: auth.HashSecret(code), UserID: userID, ClientID: req.Client.ID, ClientGeneration: req.Client.generation,
		ClientName: req.Client.Name, RedirectURI: req.RedirectURI,
		CodeChallenge: req.CodeChallenge, Resource: req.Resource, Scopes: scopes,
		CreatedAt: now, ExpiresAt: now.Add(codeTTL),
	}, sessionID); err != nil {
		return "", err
	}
	return s.redirectWith(req, url.Values{"code": {code}}), nil
}

// pkceChallenge is the S256 challenge a verifier must match.
func pkceChallenge(verifier string) (string, bool) {
	if !validPKCE(verifier) {
		return "", false
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]), true
}

// NewRegisteredClientID mints an identifier for an admin-registered
// client.
func NewRegisteredClientID() (string, error) {
	s, err := auth.NewSecret()
	if err != nil {
		return "", err
	}
	return registeredIDPre + s[:24], nil
}
