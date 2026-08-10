package api

import (
	"encoding/json"
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
	if _, ok := s.loginAuth(r); !ok {
		writeError(w, http.StatusUnauthorized, "invalid auth credential")
		return
	}
	var req struct {
		Name      string `json:"name"`
		Scope     string `json:"scope"`
		ExpiresIn int    `json:"expires_in_seconds,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	scope := store.Scope(req.Scope)
	switch scope {
	case store.ScopeSync, store.ScopeReadInsights, store.ScopeAdmin:
	default:
		writeError(w, http.StatusBadRequest, "scope must be sync|read-insights|admin")
		return
	}
	var expires *time.Time
	if req.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresIn) * time.Second)
		expires = &t
	}
	secret, tok, err := s.Auth.CreateToken(r.Context(), mustLoginSecret(r), req.Name, scope, expires)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token creation failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token_id":   tok.ID,
		"device_id":  tok.DeviceID,
		"name":       tok.Name,
		"scope":      tok.Scope,
		"secret":     secret, // shown once
		"expires_at": expires,
	})
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
		ID        string `json:"id"`
		DeviceID  string `json:"device_id"`
		Name      string `json:"name"`
		Scope     string `json:"scope"`
		CreatedAt string `json:"created_at"`
		Revoked   bool   `json:"revoked"`
	}
	out := []tj{}
	for _, t := range toks {
		out = append(out, tj{
			ID: t.ID, DeviceID: t.DeviceID, Name: t.Name, Scope: string(t.Scope),
			CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339), Revoked: t.RevokedAt != nil,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
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
	mux.Handle("GET /v1/insights/works/{id}", insH(s.HandleInsightsWork))
	mux.Handle("GET /v1/insights/calendar", insH(s.HandleInsightsCalendar))

	// Token management: login credential, rate-limited.
	tokH := func(h http.HandlerFunc) http.Handler {
		return auth.RequireSecureTransport(s.Cfg,
			auth.RateLimitIP(s.LoginLimiter, h))
	}
	mux.Handle("POST /v1/tokens", tokH(s.HandleCreateToken))
	mux.Handle("GET /v1/tokens", tokH(s.HandleListTokens))
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
