package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// HandleLogin implements POST /v1/login. Returns a short-lived auth
// credential usable only for token management (design §8.1).
func (s *Server) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	secret, err := s.Auth.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"auth_token": secret,
		"expires_in": int(auth.LoginTTL / time.Second),
	})
}

// loginAuth authenticates the token-management endpoints with the
// short-lived login credential (not a device token).
func (s *Server) loginAuth(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	secret, ok := cutBearer(h)
	if !ok {
		return "", false
	}
	userID, err := s.Auth.AuthenticateLogin(r.Context(), secret)
	if err != nil {
		return "", false
	}
	return userID, true
}

func cutBearer(h string) (string, bool) {
	const p = "Bearer "
	if len(h) > len(p) && h[:len(p)] == p {
		return h[len(p):], true
	}
	return "", false
}

// HandleCreateToken implements POST /v1/tokens (login-credential auth).
func (s *Server) HandleCreateToken(w http.ResponseWriter, r *http.Request) {
	_, ok := s.loginAuth(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid auth credential")
		return
	}
	var req struct {
		Name      string          `json:"name"`
		Scope     *store.Scope    `json:"scope,omitempty"`
		Scopes    *store.ScopeSet `json:"scopes,omitempty"`
		ExpiresIn int             `json:"expires_in_seconds,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	scopes, err := requestedScopes(req.Scope, req.Scopes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var expires *time.Time
	if req.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresIn) * time.Second)
		expires = &t
	}
	secret, tok, err := s.Auth.CreateToken(r.Context(), mustLoginSecret(r), req.Name, scopes, expires)
	if err != nil {
		if errors.Is(err, auth.ErrAdminGrantRequiresAdmin) {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "token creation failed")
		return
	}
	response := struct {
		TokenID   string         `json:"token_id"`
		DeviceID  string         `json:"device_id"`
		Name      string         `json:"name"`
		Scope     *store.Scope   `json:"scope,omitempty"`
		Scopes    store.ScopeSet `json:"scopes"`
		Secret    string         `json:"secret"`
		ExpiresAt *time.Time     `json:"expires_at"`
	}{
		TokenID: tok.ID, DeviceID: tok.DeviceID, Name: tok.Name,
		Scope: legacyScope(tok.Scopes), Scopes: tok.Scopes,
		Secret: secret, ExpiresAt: expires,
	}
	writeJSON(w, http.StatusCreated, response)
}

func mustLoginSecret(r *http.Request) string {
	secret, _ := cutBearer(r.Header.Get("Authorization"))
	return secret
}

// HandleListTokens implements GET /v1/tokens (login-credential auth).
func (s *Server) HandleListTokens(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.loginAuth(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid auth credential")
		return
	}
	toks, err := s.St.ListTokens(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	type tj struct {
		ID        string         `json:"id"`
		DeviceID  string         `json:"device_id"`
		Name      string         `json:"name"`
		Scope     *store.Scope   `json:"scope,omitempty"`
		Scopes    store.ScopeSet `json:"scopes"`
		CreatedAt string         `json:"created_at"`
		Revoked   bool           `json:"revoked"`
	}
	out := []tj{}
	for _, t := range toks {
		out = append(out, tj{
			ID: t.ID, DeviceID: t.DeviceID, Name: t.Name,
			Scope: legacyScope(t.Scopes), Scopes: t.Scopes,
			CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339), Revoked: t.RevokedAt != nil,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

// HandleUpdateTokenScopes changes a token's capabilities without replacing
// its secret, device identity, or retry identity.
func (s *Server) HandleUpdateTokenScopes(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.loginAuth(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid auth credential")
		return
	}
	var req struct {
		Scope  *store.Scope    `json:"scope,omitempty"`
		Scopes *store.ScopeSet `json:"scopes,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	scopes, err := requestedScopes(req.Scope, req.Scopes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Auth.CheckScopeGrant(r.Context(), userID, scopes); err != nil {
		if errors.Is(err, auth.ErrAdminGrantRequiresAdmin) {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "token update failed")
		return
	}
	if err := s.St.UpdateTokenScopes(r.Context(), userID, r.PathValue("id"), scopes); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "active token not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "token update failed")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Status string         `json:"status"`
		Scope  *store.Scope   `json:"scope,omitempty"`
		Scopes store.ScopeSet `json:"scopes"`
	}{
		Status: "updated", Scope: legacyScope(scopes), Scopes: scopes,
	})
}

func requestedScopes(scope *store.Scope, scopes *store.ScopeSet) (store.ScopeSet, error) {
	if scope == nil && scopes == nil {
		return nil, errors.New("scope or scopes required")
	}
	var scalarSet, arraySet store.ScopeSet
	var err error
	if scope != nil {
		scalarSet, err = store.NormalizeScopes([]store.Scope{*scope})
		if err != nil {
			return nil, err
		}
	}
	if scopes != nil {
		arraySet, err = store.NormalizeScopes(*scopes)
		if err != nil {
			return nil, err
		}
	}
	if scope == nil {
		return arraySet, nil
	}
	if scopes == nil {
		return scalarSet, nil
	}
	if len(scalarSet) != len(arraySet) {
		return nil, errors.New("scope and scopes must describe the same set")
	}
	for i := range scalarSet {
		if scalarSet[i] != arraySet[i] {
			return nil, errors.New("scope and scopes must describe the same set")
		}
	}
	return scalarSet, nil
}

func legacyScope(scopes store.ScopeSet) *store.Scope {
	scope, ok := scopes.Legacy()
	if !ok {
		return nil
	}
	return &scope
}

// HandleRevokeToken implements DELETE /v1/tokens/{id}.
func (s *Server) HandleRevokeToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.loginAuth(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid auth credential")
		return
	}
	if err := s.St.RevokeToken(r.Context(), userID, r.PathValue("id")); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "token not found or already revoked")
			return
		}
		writeError(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// Routes builds the full route table. The fail-closed policy lives
// here: only /healthz, /v1/login, and (later) invite registration and
// kosync pairing redemption are unauthenticated; everything else passes
// through RequireScope.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Unauthenticated.
	mux.Handle("GET /healthz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	mux.Handle("POST /v1/login",
		auth.RequireSecureTransport(s.Cfg,
			auth.RateLimitIP(s.LoginLimiter, http.HandlerFunc(s.HandleLogin))))
	mux.Handle("POST /v1/register",
		auth.RequireSecureTransport(s.Cfg,
			auth.RateLimitIP(s.LoginLimiter, http.HandlerFunc(s.HandleRegister))))

	syncH := func(h http.HandlerFunc) http.Handler {
		return auth.RequireSecureTransport(s.Cfg,
			auth.RequireScope(s.Auth, store.ScopeSync, h))
	}

	// sync scope.
	mux.Handle("POST /v1/works/resolve", syncH(s.HandleResolve))
	mux.Handle("POST /v1/works/{id}/split", syncH(s.HandleSplit))
	mux.Handle("POST /v1/works/merge", syncH(s.HandleMerge))
	mux.Handle("POST /v1/ops", syncH(s.HandlePushOps))
	mux.Handle("GET /v1/changes", syncH(s.HandleChanges))
	mux.Handle("GET /v1/heads", syncH(s.HandleHeads))
	mux.Handle("GET /v1/works/{id}/positions", syncH(s.HandlePositions))
	mux.Handle("POST /v1/sessions", syncH(s.HandlePushSessions))

	// read-insights scope.
	insH := func(h http.HandlerFunc) http.Handler {
		return auth.RequireSecureTransport(s.Cfg,
			auth.RequireScope(s.Auth, store.ScopeReadInsights, h))
	}
	mux.Handle("GET /v1/insights/summary", insH(s.HandleInsightsSummary))
	mux.Handle("GET /v1/insights/works", insH(s.HandleInsightsWorks))
	mux.Handle("GET /v1/insights/works/{id}", insH(s.HandleInsightsWork))
	mux.Handle("GET /v1/insights/calendar", insH(s.HandleInsightsCalendar))

	// library-manage scope: writing to a library. The store additionally
	// requires manage access to the specific library through its ACL, so
	// the token capability alone never grants access to someone else's
	// books.
	manageH := func(h http.HandlerFunc) http.Handler {
		return auth.RequireSecureTransport(s.Cfg,
			auth.RequireScope(s.Auth, store.ScopeLibraryManage, h))
	}
	mux.Handle("POST /v1/libraries/{library}/upload", manageH(s.HandleUpload))
	// Deleting is a manage capability, and it is reversible until the
	// retention window closes.
	mux.Handle("DELETE /v1/books/{id}", manageH(s.HandleTrashBook))
	mux.Handle("POST /v1/books/{id}/restore", manageH(s.HandleRestoreBook))

	// library-read scope: a job resource describes the caller's own
	// upload, and is user-scoped in the store.
	readH := func(h http.HandlerFunc) http.Handler {
		return auth.RequireSecureTransport(s.Cfg,
			auth.RequireScope(s.Auth, store.ScopeLibraryRead, h))
	}
	// Collection and member paths are kept apart on purpose. A single
	// /v1/library/{library}/... space cannot also hold /v1/library/jobs/{id}
	// or /v1/library/books/{id}: net/http rejects the pair as ambiguous,
	// because "jobs" and "books" are indistinguishable from a library id.
	mux.Handle("GET /v1/ingest/jobs/{id}", readH(s.HandleIngestJob))
	mux.Handle("GET /v1/libraries", readH(s.HandleLibraries))
	mux.Handle("GET /v1/libraries/{library}/books", readH(s.HandleLibraryBooks))
	mux.Handle("GET /v1/libraries/{library}/duplicates", readH(s.HandleLibraryDuplicates))
	mux.Handle("GET /v1/books/{id}", readH(s.HandleBook))
	// Download serves HEAD too: ServeContent handles it, and catalog
	// clients probe with HEAD before fetching.
	mux.Handle("GET /v1/books/{id}/download", readH(s.HandleBookDownload))
	mux.Handle("HEAD /v1/books/{id}/download", readH(s.HandleBookDownload))
	// A cover is catalog data, not content: it needs the same read scope
	// as the book record it illustrates, and no more.
	mux.Handle("GET /v1/books/{id}/cover", readH(s.HandleBookCover))
	mux.Handle("HEAD /v1/books/{id}/cover", readH(s.HandleBookCover))

	// Joining a catalog book to a sync work is the one route that spans
	// both layers, so it demands both capabilities: it reads the catalog
	// and it writes the caller's work graph (ADR-0003).
	mux.Handle("POST /v1/books/{id}/resolve",
		auth.RequireSecureTransport(s.Cfg,
			auth.RequireAllScopes(s.Auth,
				[]store.Scope{store.ScopeLibraryRead, store.ScopeSync},
				http.HandlerFunc(s.HandleResolveBookWork))))

	// OPDS 1.2. Same catalog, same library-read scope, different
	// credential: e-reader catalog clients speak HTTP Basic and nothing
	// else, so the bearer middleware is swapped for the Basic one. The
	// feeds are deliberately catalog-only — they expose no sync state
	// even when the token also carries `sync`, because a reader given a
	// catalog credential should not be able to read reading history.
	opdsH := func(h http.HandlerFunc) http.Handler {
		return auth.RequireSecureTransport(s.Cfg,
			auth.RateLimitIP(s.LoginLimiter,
				auth.RequireBasicScope(s.Auth, store.ScopeLibraryRead, h)))
	}
	mux.Handle("GET /opds/v1.2", opdsH(s.HandleOPDSRoot))
	// {$} rather than a bare trailing slash: a prefix pattern would
	// answer every unknown path under /opds/v1.2 with the root feed,
	// which hides client typos instead of reporting them.
	mux.Handle("GET /opds/v1.2/{$}", opdsH(s.HandleOPDSRoot))
	mux.Handle("GET /opds/v1.2/libraries/{library}", opdsH(s.HandleOPDSLibrary))
	mux.Handle("GET /opds/v1.2/books/{id}/download", opdsH(s.HandleBookDownload))
	mux.Handle("HEAD /opds/v1.2/books/{id}/download", opdsH(s.HandleBookDownload))

	// Token management: login credential, rate-limited.
	tokH := func(h http.HandlerFunc) http.Handler {
		return auth.RequireSecureTransport(s.Cfg,
			auth.RateLimitIP(s.LoginLimiter, h))
	}
	mux.Handle("POST /v1/tokens", tokH(s.HandleCreateToken))
	mux.Handle("GET /v1/tokens", tokH(s.HandleListTokens))
	mux.Handle("PATCH /v1/tokens/{id}", tokH(s.HandleUpdateTokenScopes))
	mux.Handle("DELETE /v1/tokens/{id}", tokH(s.HandleRevokeToken))

	// kosync adapter (feature-gated per instance).
	if s.Kosync != nil && s.Cfg.Adapters.Kosync {
		s.Kosync.Mount(mux, func(h http.Handler) http.Handler {
			return auth.RequireSecureTransport(s.Cfg, h)
		})
	}
	if s.Koplugin != nil && s.Cfg.Adapters.Koplugin {
		s.Koplugin.Mount(mux, func(h http.Handler) http.Handler {
			return auth.RequireSecureTransport(s.Cfg, h)
		})
	}
	if s.WebUI != nil {
		s.WebUI.Mount(mux, func(h http.Handler) http.Handler {
			return auth.RequireSecureTransport(s.Cfg, h)
		})
	}

	return mux
}
