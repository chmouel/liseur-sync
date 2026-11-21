package webui

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/oauth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// oauthParams are the authorization request parameters the consent form
// carries from the GET that showed it to the POST that answers it. The
// POST validates them again from scratch: nothing the page held is
// trusted because the page held it.
var oauthParams = []string{
	"response_type", "client_id", "redirect_uri", "state",
	"code_challenge", "code_challenge_method", "resource", "scope",
}

const oauthNextPrefix = "oauth/authorize?"

// oauthNext accepts a post-login destination only when it is the
// consent page, relative to /ui/login. Anything else, including a value
// that would leave the origin, is dropped.
func oauthNext(raw string) string {
	if len(raw) > 8192 || !strings.HasPrefix(raw, oauthNextPrefix) {
		return ""
	}
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c <= 0x20 || c >= 0x7f || c == '\\' || c == '#' {
			return ""
		}
	}
	return raw
}

// consentPolicy is uiPolicy with the redirect URI's origin added to
// form-action. Browsers apply form-action to the redirect that answers
// a form submission, so without it the 303 back to the client would be
// blocked after the code was already minted.
func consentPolicy(redirectURI string) string {
	origin := cspOrigin(redirectURI)
	if origin == "" {
		return uiPolicy
	}
	return strings.Replace(uiPolicy, "form-action 'self'", "form-action 'self' "+origin, 1)
}

// cspOrigin returns scheme://host[:port] when every character of it is
// safe inside a CSP source list, and nothing otherwise.
func cspOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	for _, c := range u.Host {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '-' || c == ':' || c == '[' || c == ']'
		if !ok {
			return ""
		}
	}
	return u.Scheme + "://" + u.Host
}

// scopeTitle is the consent screen's name for a scope.
func scopeTitle(name string) string {
	for _, s := range oauth.Scopes {
		if s.Name == name {
			return s.Title
		}
	}
	return name
}

func requestedScopes(names []string) []oauth.ScopeInfo {
	var out []oauth.ScopeInfo
	for _, s := range oauth.Scopes {
		if slices.Contains(names, s.Name) {
			out = append(out, s)
		}
	}
	return out
}

func oauthHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
}

// handleOAuthAuthorize is the authorization endpoint (ADR-0069). The
// session cookie is SameSite=Strict, so an assistant's link from its own
// site arrives without it. That arrival gets a page that navigates once
// more from this origin, which brings the cookie along; only a browser
// that still has no session is asked to sign in.
func (s *Server) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.OAuth == nil {
		http.NotFound(w, r)
		return
	}
	oauthHeaders(w)
	prefix := relPrefix(r.URL.Path)
	req, err := s.OAuth.ParseAuthorize(r.Context(), r.URL.Query())
	if err != nil {
		s.oauthFail(w, r, err)
		return
	}
	a, u, ok := s.session(r)
	if !ok {
		query := req.Params().Encode()
		next := oauthNext(oauthNextPrefix + query)
		if next == "" {
			s.oauthFail(w, r, &oauth.AuthorizeError{Code: "invalid_request", Description: "the authorization request is too long"})
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			oauthContinuePage(prefix, uiCtx(r, nil), "authorize?"+query, req.Client.Name).Render(r.Context(), w)
			return
		}
		loginPage(prefix, uiCtx(r, nil), "", next).Render(r.Context(), w)
		return
	}
	s.renewSession(w, r, a)
	w.Header().Set("Content-Security-Policy", consentPolicy(req.RedirectURI))
	consentPage(prefix, uiCtx(r, u), csrfFor(a), consentView{
		Request: req, Fields: hiddenParams(req.Params()), Scopes: requestedScopes(req.Scopes),
		Account: u.Name,
	}).Render(r.Context(), w)
}

type hiddenField struct{ Name, Value string }

func hiddenParams(v url.Values) []hiddenField {
	var out []hiddenField
	for _, k := range oauthParams {
		if val := v.Get(k); val != "" {
			out = append(out, hiddenField{k, val})
		}
	}
	return out
}

type consentView struct {
	Request *oauth.AuthorizeRequest
	Fields  []hiddenField
	Scopes  []oauth.ScopeInfo
	Account string
}

// handleOAuthDecision answers the consent form.
func (s *Server) handleOAuthDecision(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if s.OAuth == nil {
		http.NotFound(w, r)
		return
	}
	oauthHeaders(w)
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	q := url.Values{}
	for _, k := range oauthParams {
		if vals, ok := r.PostForm[k]; ok {
			q[k] = vals
		}
	}
	req, err := s.OAuth.ParseAuthorize(r.Context(), q)
	if err != nil {
		s.oauthFail(w, r, err)
		return
	}
	target := s.OAuth.Deny(req)
	if r.PostForm.Get("decision") == "allow" {
		target, err = s.OAuth.Approve(r.Context(), req, u.ID, a.ID, r.PostForm["grant"])
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "authorization expired; connect again", http.StatusUnauthorized)
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "oauth approve failed", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	slog.InfoContext(r.Context(), "oauth consent", "user", u.ID, "client", req.Client.ID,
		"decision", r.PostForm.Get("decision"))
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// oauthFail shows an error the client cannot be trusted with, and sends
// any other back to the client's registered redirect URI.
func (s *Server) oauthFail(w http.ResponseWriter, r *http.Request, err error) {
	var ae *oauth.AuthorizeError
	if errors.As(err, &ae) && ae.Redirect != "" {
		http.Redirect(w, r, ae.Redirect, http.StatusSeeOther)
		return
	}
	msg := "The request could not be checked. Try connecting again from the assistant."
	status := http.StatusBadRequest
	if ae != nil {
		msg = ae.Description
	} else {
		slog.ErrorContext(r.Context(), "oauth authorize failed", "error", err)
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	oauthErrorPage(relPrefix(r.URL.Path), uiCtx(r, nil), msg).Render(r.Context(), w)
}

// assistantsView is what a reader sees of the assistants connected to
// their account.
type assistantsView struct {
	Enabled bool
	MCPURL  string
	Grants  []store.OAuthGrant
}

func activeGrants(gs []store.OAuthGrant) []store.OAuthGrant {
	out := gs[:0:0]
	for _, g := range gs {
		if g.RevokedAt == nil {
			out = append(out, g)
		}
	}
	return out
}

func (s *Server) assistantsFor(r *http.Request, userID string) assistantsView {
	v := assistantsView{Enabled: s.OAuth != nil}
	if s.OAuth == nil {
		return v
	}
	v.MCPURL = s.OAuth.Resource()
	gs, err := s.St.ListOAuthGrants(r.Context(), userID)
	if err != nil {
		slog.ErrorContext(r.Context(), "listing oauth grants", "error", err)
	}
	v.Grants = activeGrants(gs)
	return v
}

func (s *Server) handleRevokeOAuthGrant(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	flash := Flash{Notice: "Assistant disconnected."}
	if err := s.St.RevokeOAuthGrant(r.Context(), u.ID, r.PathValue("id"), time.Now()); err != nil {
		flash = Flash{Error: "Could not disconnect that assistant."}
	}
	s.renderDevices(w, r, a, u, settingsDevicesAssistants, flash)
}

// adminAssistantsView is the administrator's page: which assistants may
// ask, and every connection any reader made.
type adminAssistantsView struct {
	Enabled bool
	MCPURL  string
	ChatGPT bool
	Clients []store.OAuthClient
	Grants  []store.OAuthGrant
}

func (s *Server) adminAssistants(r *http.Request) (adminAssistantsView, error) {
	v := adminAssistantsView{Enabled: s.OAuth != nil}
	if s.OAuth != nil {
		v.MCPURL = s.OAuth.Resource()
	}
	clients, err := s.St.ListOAuthClients(r.Context())
	if err != nil {
		return v, err
	}
	for _, c := range clients {
		if c.ID == oauth.ChatGPTMetadataURL {
			v.ChatGPT = true
			continue
		}
		v.Clients = append(v.Clients, c)
	}
	gs, err := s.St.ListOAuthGrants(r.Context(), "")
	if err != nil {
		return v, err
	}
	v.Grants = activeGrants(gs)
	return v, nil
}

func (s *Server) renderAdminAssistants(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash) {
	if settingsRedirect(w, r, settingsAdmin, settingsAdminAssistants, "", flash) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.renderSettings(w, r, a, u, settingsAdmin, settingsAdminAssistants, "", flash, false, "", false)
}

func (s *Server) handleAdminChatGPT(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var err error
	flash := Flash{}
	if r.FormValue("allow") == "on" {
		err = s.St.CreateOAuthClient(r.Context(), store.OAuthClient{
			ID: oauth.ChatGPTMetadataURL, Kind: store.OAuthClientMetadata,
			Name: "ChatGPT", CreatedAt: time.Now().UTC(),
		})
		if errors.Is(err, store.ErrConflict) {
			err = nil
		}
		flash.Notice = "ChatGPT can now ask readers to connect."
	} else {
		err = s.St.DeleteOAuthClient(r.Context(), oauth.ChatGPTMetadataURL)
		if errors.Is(err, store.ErrNotFound) {
			err = nil
		}
		if s.OAuth != nil {
			s.OAuth.Fetcher.Forget(oauth.ChatGPTMetadataURL)
		}
		flash.Notice = "ChatGPT is no longer allowed. Every ChatGPT connection was cut."
	}
	logAdminAction(r, u, "oauth.chatgpt", r.FormValue("allow"), err)
	if err != nil {
		slog.ErrorContext(r.Context(), "toggling chatgpt", "error", err)
		flash = Flash{Error: "Could not save that."}
	}
	s.renderAdminAssistants(w, r, a, u, flash)
}

func (s *Server) handleAdminCreateOAuthClient(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	client, secret, err := s.newOAuthClient(r)
	if err != nil {
		logAdminAction(r, u, "oauth.client.create", "", err)
		s.renderAdminAssistants(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	err = s.St.CreateOAuthClient(r.Context(), client)
	logAdminAction(r, u, "oauth.client.create", client.ID, err)
	switch {
	case errors.Is(err, store.ErrConflict):
		s.renderAdminAssistants(w, r, a, u, Flash{Error: "That application is already allowed."})
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "creating oauth client", "error", err)
		s.renderAdminAssistants(w, r, a, u, Flash{Error: "Could not add that application."})
		return
	}
	if s.OAuth != nil {
		s.OAuth.Fetcher.Forget(client.ID)
	}
	flash := Flash{Notice: "Added " + client.Name + "."}
	if secret != "" {
		flash = Flash{
			Secret: secret, SecretLabel: "Client secret for " + client.Name,
			SecretHint: "Client ID: " + client.ID + ". Give both to the application. The secret is not stored here and cannot be shown again.",
		}
	} else if client.Kind == store.OAuthClientRegistered {
		flash.Notice = "Added " + client.Name + ". Its client ID is " + client.ID + "."
	}
	s.renderAdminAssistants(w, r, a, u, flash)
}

// newOAuthClient reads the add-an-application form: either the URL of a
// Client ID Metadata Document, or a name and redirect URIs for a client
// registered here.
func (s *Server) newOAuthClient(r *http.Request) (store.OAuthClient, string, error) {
	now := time.Now().UTC()
	if err := oauth.ValidateClientName(r.FormValue("name")); err != nil {
		return store.OAuthClient{}, "", err
	}
	if r.FormValue("kind") == store.OAuthClientMetadata {
		docURL := strings.TrimSpace(r.FormValue("metadata_url"))
		if err := oauth.ValidateMetadataURL(docURL); err != nil {
			return store.OAuthClient{}, "", err
		}
		return store.OAuthClient{
			ID: docURL, Kind: store.OAuthClientMetadata,
			Name: clampFormName(r.FormValue("name")), CreatedAt: now,
		}, "", nil
	}
	name := clampFormName(r.FormValue("name"))
	if name == "" {
		return store.OAuthClient{}, "", errors.New("give the application a name")
	}
	var uris []string
	for line := range strings.Lines(r.FormValue("redirect_uris")) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if err := oauth.ValidateRedirectURI(line); err != nil {
			return store.OAuthClient{}, "", err
		}
		if !slices.Contains(uris, line) {
			uris = append(uris, line)
		}
	}
	if len(uris) == 0 || len(uris) > 10 {
		return store.OAuthClient{}, "", errors.New("list between one and ten redirect URIs, one per line")
	}
	id, err := oauth.NewRegisteredClientID()
	if err != nil {
		return store.OAuthClient{}, "", err
	}
	c := store.OAuthClient{ID: id, Kind: store.OAuthClientRegistered, Name: name, RedirectURIs: uris, CreatedAt: now}
	var secret string
	if r.FormValue("confidential") == "on" {
		if secret, err = s.generateSecret(); err != nil {
			return store.OAuthClient{}, "", err
		}
		c.SecretSHA256 = auth.HashSecret(secret)
	}
	return c, secret, nil
}

func clampFormName(name string) string {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80])
	}
	return name
}

func (s *Server) handleAdminDeleteOAuthClient(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := r.FormValue("client_id")
	err := s.St.DeleteOAuthClient(r.Context(), id)
	logAdminAction(r, u, "oauth.client.delete", id, err)
	if s.OAuth != nil {
		s.OAuth.Fetcher.Forget(id)
	}
	flash := Flash{Notice: "Application removed, and every connection it had was cut."}
	if err != nil {
		flash = Flash{Error: "Could not remove that application."}
	}
	s.renderAdminAssistants(w, r, a, u, flash)
}

func (s *Server) handleAdminRevokeOAuthGrant(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	err := s.St.RevokeOAuthGrant(r.Context(), "", id, time.Now())
	logAdminAction(r, u, "oauth.grant.revoke", id, err)
	flash := Flash{Notice: "Connection cut."}
	if err != nil {
		flash = Flash{Error: "Could not cut that connection."}
	}
	s.renderAdminAssistants(w, r, a, u, flash)
}
