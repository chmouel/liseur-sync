package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/buildinfo"
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

// HandleToken implements GET /v1/token — what the bearer that presented
// it is allowed to do (ADR-0016).
//
// Singular against the plural /v1/tokens on purpose: one is the token
// you are, the other is the tokens you have. They are different
// resources with different credentials, and the URL says so.
//
// The field names are the ones GET /v1/tokens already uses for the same
// things, so a client parses one token shape. Nothing else is returned:
// not the secret, not its hash, not the other tokens on the account, not
// the user's identity beyond what the caller already proved.
func (s *Server) HandleToken(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ID       string         `json:"id"`
		DeviceID string         `json:"device_id"`
		Name     string         `json:"name"`
		Scope    *store.Scope   `json:"scope,omitempty"`
		Scopes   store.ScopeSet `json:"scopes"`
	}{
		ID: tok.ID, DeviceID: tok.DeviceID, Name: tok.Name,
		Scope: legacyScope(tok.Scopes), Scopes: tok.Scopes,
	})
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
		// The build is here and not only on the admin overview page
		// because the operator who most needs it — someone whose
		// container is serving an image older than they think — cannot
		// always log in to find out, and the image carries no OCI
		// labels to read from outside. A struct rather than a map so
		// `status` stays first for anything grepping the first line.
		b := buildinfo.Get()
		writeJSON(w, http.StatusOK, struct {
			Status   string `json:"status"`
			Version  string `json:"version"`
			Revision string `json:"revision,omitempty"`
		}{"ok", b.Short(), b.ShortRevision()})
	}))
	mux.Handle("POST /v1/login",
		auth.RequireSecureTransport(s.Cfg,
			auth.RateLimitIP(s.LoginLimiter, s.Cfg, http.HandlerFunc(s.HandleLogin))))
	mux.Handle("POST /v1/register",
		auth.RequireSecureTransport(s.Cfg,
			auth.RateLimitIP(s.LoginLimiter, s.Cfg, http.HandlerFunc(s.HandleRegister))))

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
	// Annotations (ADR-0028) are reading state, they travel with
	// positions, and the reader token already carries what they need.
	mux.Handle("POST /v1/annotations", syncH(s.HandlePushAnnotations))
	mux.Handle("GET /v1/annotations/changes", syncH(s.HandleAnnotationChanges))
	mux.Handle("DELETE /v1/annotations/{id}", syncH(s.HandleDeleteAnnotation))
	mux.Handle("GET /v1/works/{id}/annotations", syncH(s.HandleWorkAnnotations))

	// read-insights scope.
	insH := func(h http.HandlerFunc) http.Handler {
		return auth.RequireSecureTransport(s.Cfg,
			auth.RequireScope(s.Auth, store.ScopeReadInsights, h))
	}
	mux.Handle("GET /v1/insights/summary", insH(s.HandleInsightsSummary))
	mux.Handle("GET /v1/insights/works", insH(s.HandleInsightsWorks))
	mux.Handle("GET /v1/insights/works/{id}", insH(s.HandleInsightsWork))
	mux.Handle("GET /v1/insights/calendar", insH(s.HandleInsightsCalendar))

	// library-read scope: the catalog. Folder grants select the shared
	// catalog rows this account may see (ADR-0027), while reading state
	// stays strictly per user.
	//
	// There is no metadata-editing scope beyond series claims: with
	// uploads gone, nothing else writes to the catalog but a reconcile
	// pass, and a pass answers to the disk rather than to a token.
	// Managing folders is an admin capability because it names paths on
	// this machine.
	readH := func(h http.HandlerFunc) http.Handler {
		return auth.RequireSecureTransport(s.Cfg,
			auth.RequireScope(s.Auth, store.ScopeLibraryRead, h))
	}
	// Collection and member paths are kept apart on purpose. A single
	// /v1/folders/{folder}/... space cannot also hold /v1/folders/books/{id}:
	// net/http rejects the pair as ambiguous, because "books" is
	// indistinguishable from a folder id.
	mux.Handle("GET /v1/folders", readH(s.HandleFolders))
	mux.Handle("GET /v1/folders/{folder}/books", readH(s.HandleFolderBooks))
	// Finding one book among many is a read of the catalog, so it asks
	// for nothing beyond the scope that lists it.
	mux.Handle("GET /v1/folders/{folder}/search", readH(s.HandleFolderSearch))
	mux.Handle("GET /v1/books/{id}", readH(s.HandleBook))
	// Entities are library-wide (ADR-0019), so they hang off the root
	// rather than off the folder a book happened to be found in.
	mux.Handle("GET /v1/entities/{kind}", readH(s.HandleEntities))
	mux.Handle("GET /v1/entities/{kind}/{entity}/books",
		readH(s.HandleEntityBooks))
	// Download serves HEAD too: ServeContent handles it, and catalog
	// clients probe with HEAD before fetching.
	mux.Handle("GET /v1/books/{id}/download", readH(s.HandleBookDownload))
	mux.Handle("HEAD /v1/books/{id}/download", readH(s.HandleBookDownload))
	// A cover is catalog data, not content: it needs the same read scope
	// as the book record it illustrates, and no more.
	mux.Handle("GET /v1/books/{id}/cover", readH(s.HandleBookCover))
	mux.Handle("HEAD /v1/books/{id}/cover", readH(s.HandleBookCover))
	// Reading the layers under a book's series is a catalog read; it
	// shows what the folder said beneath any claim, which is what an
	// editor needs to know whether a reset would change anything.
	mux.Handle("GET /v1/books/{id}/series", readH(s.HandleBookSeriesLayers))

	// library-manage scope: stating what the folder got wrong
	// (ADR-0018). These write to a layer beside the catalog, never into
	// it and never under a watched folder. The shared layer additionally
	// takes admin, checked in the handler, because it speaks for
	// everybody; the personal layer speaks only for its writer.
	manageH := func(h http.HandlerFunc) http.Handler {
		return auth.RequireSecureTransport(s.Cfg,
			auth.RequireScope(s.Auth, store.ScopeLibraryManage, h))
	}
	mux.Handle("PUT /v1/books/{id}/series", manageH(s.HandleSetBookSeries))
	mux.Handle("DELETE /v1/books/{id}/series", manageH(s.HandleClearBookSeries))
	mux.Handle("PUT /v1/entities/{kind}/{entity}/order",
		manageH(s.HandleReorderSeries))
	// Renaming a series (ADR-0020). Same two layers as a claim, and the
	// same rule: the name a scan observed is never touched.
	mux.Handle("PUT /v1/entities/{kind}/{entity}/name",
		manageH(s.HandleSetSeriesName))
	mux.Handle("DELETE /v1/entities/{kind}/{entity}/name",
		manageH(s.HandleClearSeriesName))
	// Merging and splitting a series (ADR-0021). Admin is checked in the
	// handler: unlike a rename, neither has a personal layer to fall
	// back to, because both change what a shelf is rather than what one
	// reader calls it.
	mux.Handle("POST /v1/entities/{kind}/{entity}/merge",
		manageH(s.HandleMergeSeries))
	mux.Handle("POST /v1/entities/{kind}/{entity}/split",
		manageH(s.HandleSplitSeries))
	mux.Handle("GET /v1/entities/{kind}/{entity}/bindings",
		manageH(s.HandleSeriesBindings))
	mux.Handle("DELETE /v1/entities/{kind}/{entity}/bindings/{binding}",
		manageH(s.HandleDeleteSeriesBinding))

	// library-upload scope: putting a book into a folder that accepts
	// one (ADR-0023). It is separate from library-manage because
	// tidying your own series and writing to this server's disk are
	// different questions, and one token should not answer both.
	mux.Handle("POST /v1/folders/{folder}/books",
		auth.RequireSecureTransport(s.Cfg,
			auth.RequireScope(s.Auth, store.ScopeLibraryUpload,
				http.HandlerFunc(s.HandleUploadBook))))

	// library-delete scope: taking a book back out of a folder that
	// accepts one (ADR-0025). Separate from library-upload for the
	// reason upload is separate from library-manage — adding your own
	// book and removing everyone's are different questions.
	mux.Handle("DELETE /v1/books/{id}",
		auth.RequireSecureTransport(s.Cfg,
			auth.RequireScope(s.Auth, store.ScopeLibraryDelete,
				http.HandlerFunc(s.HandleDeleteBook))))

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
	//
	// Rate-limited on OPDSLimiter, not LoginLimiter: this surface covers
	// covers and downloads as well as feeds, so one screen of a folder
	// is a request per visible book plus the feed itself, routinely
	// outnumbering a budget sized for password attempts.
	opdsH := func(h http.HandlerFunc) http.Handler {
		return auth.RequireSecureTransport(s.Cfg,
			auth.RateLimitIP(s.OPDSLimiter, s.Cfg,
				auth.RequireBasicScope(s.Auth, store.ScopeLibraryRead, h)))
	}
	mux.Handle("GET /opds/v1.2", opdsH(s.HandleOPDSRoot))
	// {$} rather than a bare trailing slash: a prefix pattern would
	// answer every unknown path under /opds/v1.2 with the root feed,
	// which hides client typos instead of reporting them.
	mux.Handle("GET /opds/v1.2/{$}", opdsH(s.HandleOPDSRoot))
	mux.Handle("GET /opds/v1.2/folders/{folder}", opdsH(s.HandleOPDSFolder))
	// A reader discovers these through links in the feeds above rather
	// than being configured with them, which is the point of OPDS.
	mux.Handle("GET /opds/v1.2/folders/{folder}/search.xml",
		opdsH(s.HandleOPDSSearchDescription))
	mux.Handle("GET /opds/v1.2/folders/{folder}/search", opdsH(s.HandleOPDSSearch))
	mux.Handle("GET /opds/v1.2/folders/{folder}/recent", opdsH(s.HandleOPDSRecent))
	mux.Handle("GET /opds/v1.2/entities/{kind}", opdsH(s.HandleOPDSEntities))
	mux.Handle("GET /opds/v1.2/entities/{kind}/{entity}",
		opdsH(s.HandleOPDSEntityBooks))
	mux.Handle("GET /opds/v1.2/books/{id}/download", opdsH(s.HandleBookDownload))
	mux.Handle("HEAD /opds/v1.2/books/{id}/download", opdsH(s.HandleBookDownload))
	// Readers render covers in the feed, and they send the feed's Basic
	// credential to do it. Without this the images in an acquisition feed
	// would all fail to load.
	mux.Handle("GET /opds/v1.2/books/{id}/cover", opdsH(s.HandleBookCover))
	mux.Handle("HEAD /opds/v1.2/books/{id}/cover", opdsH(s.HandleBookCover))

	// Authenticated, no scope required. A token describing itself is the
	// one route whose subject is the credential presenting it, so there
	// is no capability left to check (ADR-0016). It is emphatically not
	// an open route: an absent, revoked or expired token gets 401 here
	// like anywhere else.
	mux.Handle("GET /v1/token",
		auth.RequireSecureTransport(s.Cfg,
			auth.RequireToken(s.Auth, http.HandlerFunc(s.HandleToken))))

	// Token management: login credential, rate-limited.
	tokH := func(h http.HandlerFunc) http.Handler {
		return auth.RequireSecureTransport(s.Cfg,
			auth.RateLimitIP(s.LoginLimiter, s.Cfg, h))
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
