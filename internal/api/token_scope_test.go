package api

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

func TestTokenScopeSetCompatibilityAndUpdate(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	hash, _ := auth.HashPassword("hunter2hunter")
	if err := st.CreateUser(ctx, store.User{
		ID: "u1", Name: "alice", Argon2Hash: hash, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	code, out := post(t, ts.URL+"/v1/login", "", map[string]string{
		"username": "alice", "password": "hunter2hunter",
	})
	if code != http.StatusOK {
		t.Fatalf("login: %d %v", code, out)
	}
	login := out["auth_token"].(string)

	// The scalar request remains accepted and receives both wire fields.
	code, out = post(t, ts.URL+"/v1/tokens", login, map[string]any{
		"name": "legacy", "scope": "sync",
	})
	if code != http.StatusCreated || out["scope"] != "sync" {
		t.Fatalf("legacy token create: %d %v", code, out)
	}
	if scopes, ok := out["scopes"].([]any); !ok || len(scopes) != 1 || scopes[0] != "sync" {
		t.Fatalf("legacy scopes response: %v", out)
	}

	// A scope array is deduplicated and returned in canonical order. Multi-
	// scope responses omit the deprecated scalar field.
	code, out = post(t, ts.URL+"/v1/tokens", login, map[string]any{
		"name": "reader", "scopes": []string{"library-read", "sync", "library-read"},
	})
	if code != http.StatusCreated {
		t.Fatalf("multi-scope token create: %d %v", code, out)
	}
	if _, exists := out["scope"]; exists {
		t.Fatalf("multi-scope response exposed scalar scope: %v", out)
	}
	scopes := out["scopes"].([]any)
	if len(scopes) != 2 || scopes[0] != "sync" || scopes[1] != "library-read" {
		t.Fatalf("canonical scopes response: %v", out)
	}
	tokenID := out["token_id"].(string)
	deviceID := out["device_id"].(string)
	secret := out["secret"].(string)

	// Equal scalar and array forms may coexist; contradictory forms fail.
	if code, out = post(t, ts.URL+"/v1/tokens", login, map[string]any{
		"name": "same", "scope": "sync", "scopes": []string{"sync", "sync"},
	}); code != http.StatusCreated {
		t.Fatalf("equivalent compatibility fields: %d %v", code, out)
	}
	if code, out = post(t, ts.URL+"/v1/tokens", login, map[string]any{
		"name": "conflict", "scope": "sync", "scopes": []string{"read-insights"},
	}); code != http.StatusBadRequest ||
		out["error"] != "scope and scopes must describe the same set" {
		t.Fatalf("contradictory compatibility fields: %d %v", code, out)
	}
	if code, _ = post(t, ts.URL+"/v1/tokens", login, map[string]any{
		"name": "empty", "scopes": []string{},
	}); code != http.StatusBadRequest {
		t.Fatalf("empty scope set: want 400, got %d", code)
	}
	if code, out = patch(t, ts.URL+"/v1/tokens/"+tokenID, login, map[string]any{
		"scopes": []string{"sync", "admin"},
	}); code != http.StatusForbidden {
		t.Fatalf("scope update self-granted admin: want 403, got %d %v", code, out)
	}

	// In-place updates preserve the token secret and device identity.
	code, out = patch(t, ts.URL+"/v1/tokens/"+tokenID, login, map[string]any{
		"scopes": []string{"read-insights", "library-read"},
	})
	if code != http.StatusOK {
		t.Fatalf("scope update: %d %v", code, out)
	}
	if _, exists := out["scope"]; exists {
		t.Fatalf("multi-scope update exposed scalar scope: %v", out)
	}
	updatedScopes := out["scopes"].([]any)
	if len(updatedScopes) != 2 ||
		updatedScopes[0] != "read-insights" ||
		updatedScopes[1] != "library-read" {
		t.Fatalf("scope update response: %v", out)
	}
	if code, _ = get(t, ts.URL+"/v1/insights/summary", secret); code != http.StatusOK {
		t.Fatalf("updated secret lost read-insights access: %d", code)
	}
	if code, _ = get(t, ts.URL+"/v1/changes?since=0", secret); code != http.StatusForbidden {
		t.Fatalf("removed sync scope still authorized: %d", code)
	}
	code, out = get(t, ts.URL+"/v1/tokens", login)
	if code != http.StatusOK {
		t.Fatalf("list tokens: %d %v", code, out)
	}
	var updated map[string]any
	for _, raw := range out["tokens"].([]any) {
		candidate := raw.(map[string]any)
		if candidate["id"] == tokenID {
			updated = candidate
			break
		}
	}
	if updated == nil || updated["device_id"] != deviceID {
		t.Fatalf("scope update changed token identity: %v", out)
	}
	if _, exists := updated["scope"]; exists {
		t.Fatalf("listed multi-scope token exposed scalar scope: %v", updated)
	}
}

// routeGate names which credential a registered route demands, so the
// matrix below can assert the right thing for each kind rather than
// pretending they are all the same shape.
type routeGate int

const (
	gatePublic        routeGate = iota // no credential at all
	gateSync                           // bearer, scope sync
	gateInsights                       // bearer, scope read-insights
	gateLibraryRead                    // bearer, scope library-read
	gateLibraryManage                  // bearer, scope library-manage
	gateResolveBoth                    // bearer, library-read AND sync
	gateOPDS                           // HTTP Basic, scope library-read
	gateTokenSelf                      // bearer, no scope beyond authenticating
	gateLoginCred                      // the short-lived login credential, not a device token
)

// registeredRouteGates is the hand-kept half of the matrix: what each
// route in routes.go demands. routePattern below reads routes.go itself
// so a route added there without an entry here fails the test instead of
// silently going unchecked — the mistake the /v1/libraries → /v1/folders
// rename would have made easy to repeat if this table were static.
var registeredRouteGates = map[string]routeGate{
	"GET /healthz":      gatePublic,
	"POST /v1/login":    gatePublic,
	"POST /v1/register": gatePublic,

	"POST /v1/works/resolve":       gateSync,
	"POST /v1/works/{id}/split":    gateSync,
	"POST /v1/works/merge":         gateSync,
	"POST /v1/ops":                 gateSync,
	"GET /v1/changes":              gateSync,
	"GET /v1/heads":                gateSync,
	"GET /v1/works/{id}/positions": gateSync,
	"POST /v1/sessions":            gateSync,

	"GET /v1/insights/summary":    gateInsights,
	"GET /v1/insights/works":      gateInsights,
	"GET /v1/insights/works/{id}": gateInsights,
	"GET /v1/insights/calendar":   gateInsights,

	"GET /v1/folders":                        gateLibraryRead,
	"GET /v1/folders/{folder}/books":         gateLibraryRead,
	"GET /v1/folders/{folder}/search":        gateLibraryRead,
	"GET /v1/books/{id}":                     gateLibraryRead,
	"GET /v1/entities/{kind}":                gateLibraryRead,
	"GET /v1/entities/{kind}/{entity}/books": gateLibraryRead,
	"GET /v1/books/{id}/download":            gateLibraryRead,
	"HEAD /v1/books/{id}/download":           gateLibraryRead,
	"GET /v1/books/{id}/cover":               gateLibraryRead,
	"HEAD /v1/books/{id}/cover":              gateLibraryRead,
	"GET /v1/books/{id}/series":              gateLibraryRead,

	"PUT /v1/books/{id}/series":                gateLibraryManage,
	"DELETE /v1/books/{id}/series":             gateLibraryManage,
	"PUT /v1/entities/{kind}/{entity}/order":   gateLibraryManage,
	"PUT /v1/entities/{kind}/{entity}/name":    gateLibraryManage,
	"DELETE /v1/entities/{kind}/{entity}/name": gateLibraryManage,

	"POST /v1/books/{id}/resolve": gateResolveBoth,

	"GET /opds/v1.2":                             gateOPDS,
	"GET /opds/v1.2/{$}":                         gateOPDS,
	"GET /opds/v1.2/folders/{folder}":            gateOPDS,
	"GET /opds/v1.2/folders/{folder}/search.xml": gateOPDS,
	"GET /opds/v1.2/folders/{folder}/search":     gateOPDS,
	"GET /opds/v1.2/folders/{folder}/recent":     gateOPDS,
	"GET /opds/v1.2/entities/{kind}":             gateOPDS,
	"GET /opds/v1.2/entities/{kind}/{entity}":    gateOPDS,
	"GET /opds/v1.2/books/{id}/download":         gateOPDS,
	"HEAD /opds/v1.2/books/{id}/download":        gateOPDS,
	"GET /opds/v1.2/books/{id}/cover":            gateOPDS,
	"HEAD /opds/v1.2/books/{id}/cover":           gateOPDS,

	"GET /v1/token": gateTokenSelf,

	"POST /v1/tokens":        gateLoginCred,
	"GET /v1/tokens":         gateLoginCred,
	"PATCH /v1/tokens/{id}":  gateLoginCred,
	"DELETE /v1/tokens/{id}": gateLoginCred,
}

// routePatternRE pulls every literal "METHOD pattern" string handed to
// mux.Handle in routes.go. The kosync, koplugin and web UI mounts are
// deliberately invisible to it — they call Mount(mux, ...) rather than
// mux.Handle with a literal pattern, and each has its own scope tests in
// its own package.
var routePatternRE = regexp.MustCompile(`mux\.Handle\("([A-Z]+ [^"]+)"`)

// registeredRoutes re-reads routes.go so this test fails the moment a
// route is added or renamed there without a matching entry in
// registeredRouteGates, rather than only when someone remembers to
// update this file by hand.
func registeredRoutes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	matches := routePatternRE.FindAllStringSubmatch(string(src), -1)
	if len(matches) == 0 {
		t.Fatal("routePatternRE matched nothing in routes.go — did mux.Handle's shape change?")
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// placeholderRE finds a {name} or {$} path segment so a route pattern can
// be turned into a concrete URL. {$} (the exact-match-of-root marker) is
// dropped rather than replaced, since it names an empty final segment.
var placeholderRE = regexp.MustCompile(`\{[^}]*\}`)

func concretePath(pattern string) string {
	return placeholderRE.ReplaceAllStringFunc(pattern, func(m string) string {
		if m == "{$}" {
			return ""
		}
		return "x"
	})
}

// TestScopeMatrixCoversEveryRegisteredRoute is the ADR-0017 obligation
// that replaces the old library-manage scope: with folder management
// gone from the API entirely and the catalog shared by every reader,
// the only scope boundary left worth policing by hand is "does this
// route ask for the credential routes.go says it asks for" — and this
// test is honest about that boundary by reading routes.go rather than
// guessing at it, so a route added without a matching gate here fails
// loudly instead of shipping unchecked.
func TestScopeMatrixCoversEveryRegisteredRoute(t *testing.T) {
	found := registeredRoutes(t)
	seen := map[string]bool{}
	for _, route := range found {
		seen[route] = true
		if _, ok := registeredRouteGates[route]; !ok {
			t.Errorf("routes.go registers %q with no entry in registeredRouteGates", route)
		}
	}
	for route := range registeredRouteGates {
		if !seen[route] {
			t.Errorf("registeredRouteGates names %q, which routes.go no longer registers", route)
		}
	}
	if t.Failed() {
		return
	}

	ts, st := testServer(t)
	ctx := t.Context()
	if err := st.CreateUser(ctx, store.User{
		ID: "matrix-user", Name: "matrix", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	svc := auth.NewService(st)
	mint := func(scopes ...store.Scope) string {
		t.Helper()
		secret, _, err := svc.MintToken(ctx, "matrix-user", "matrix-token", store.ScopeSet(scopes), nil)
		if err != nil {
			t.Fatal(err)
		}
		return secret
	}
	syncOnly := mint(store.ScopeSync)
	insightsOnly := mint(store.ScopeReadInsights)
	libraryReadOnly := mint(store.ScopeLibraryRead)
	both := mint(store.ScopeLibraryRead, store.ScopeSync)
	libraryManageOnly := mint(store.ScopeLibraryManage)

	// A real login credential, minted the same way HandleLogin mints one
	// — through Login itself, in-process, since this matrix is about
	// scope gates and not about password verification (which
	// TestFullSyncFlow and register_test.go already cover end to end).
	hash, err := auth.HashPassword("matrix-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(ctx, store.User{
		ID: "matrix-login-user", Name: "matrix-login", Argon2Hash: hash, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	login, err := svc.Login(ctx, "matrix-login", "matrix-password")
	if err != nil {
		t.Fatal(err)
	}

	bearer := func(method, path, secret string) int {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, nil)
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	basic := func(method, path, secret string) int {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, nil)
		if secret != "" {
			req.SetBasicAuth("token", secret)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	for route, gate := range registeredRouteGates {
		t.Run(route, func(t *testing.T) {
			parts := strings.SplitN(route, " ", 2)
			method, path := parts[0], concretePath(parts[1])
			switch gate {
			case gatePublic:
				// Covered by healthz_test.go and register_test.go, which
				// exercise the actual bodies these routes expect; this
				// matrix only has to know they exist and are not
				// scope-gated, which the completeness check above
				// already established.
			case gateSync:
				if got := bearer(method, path, ""); got != http.StatusUnauthorized {
					t.Errorf("no token: %d, want 401", got)
				}
				if got := bearer(method, path, insightsOnly); got != http.StatusForbidden {
					t.Errorf("read-insights token on a sync route: %d, want 403", got)
				}
				if got := bearer(method, path, syncOnly); got == http.StatusUnauthorized || got == http.StatusForbidden {
					t.Errorf("sync token on a sync route: %d, should have passed the gate", got)
				}
			case gateInsights:
				if got := bearer(method, path, ""); got != http.StatusUnauthorized {
					t.Errorf("no token: %d, want 401", got)
				}
				if got := bearer(method, path, syncOnly); got != http.StatusForbidden {
					t.Errorf("sync token on a read-insights route: %d, want 403", got)
				}
				if got := bearer(method, path, insightsOnly); got == http.StatusUnauthorized || got == http.StatusForbidden {
					t.Errorf("read-insights token on a read-insights route: %d, should have passed the gate", got)
				}
			case gateLibraryRead:
				if got := bearer(method, path, ""); got != http.StatusUnauthorized {
					t.Errorf("no token: %d, want 401", got)
				}
				if got := bearer(method, path, syncOnly); got != http.StatusForbidden {
					t.Errorf("sync token on a library-read route: %d, want 403", got)
				}
				if got := bearer(method, path, libraryReadOnly); got == http.StatusUnauthorized || got == http.StatusForbidden {
					t.Errorf("library-read token on a library-read route: %d, should have passed the gate", got)
				}
			case gateLibraryManage:
				if got := bearer(method, path, ""); got != http.StatusUnauthorized {
					t.Errorf("no token: %d, want 401", got)
				}
				// Reading the catalog is not permission to restate it.
				if got := bearer(method, path, libraryReadOnly); got != http.StatusForbidden {
					t.Errorf("library-read token on a library-manage route: %d, want 403", got)
				}
				if got := bearer(method, path, libraryManageOnly); got == http.StatusUnauthorized || got == http.StatusForbidden {
					t.Errorf("library-manage token on a library-manage route: %d, should have passed the gate", got)
				}
			case gateResolveBoth:
				if got := bearer(method, path, ""); got != http.StatusUnauthorized {
					t.Errorf("no token: %d, want 401", got)
				}
				if got := bearer(method, path, syncOnly); got != http.StatusForbidden {
					t.Errorf("sync-only token on the combined route: %d, want 403", got)
				}
				if got := bearer(method, path, libraryReadOnly); got != http.StatusForbidden {
					t.Errorf("library-read-only token on the combined route: %d, want 403", got)
				}
				if got := bearer(method, path, both); got == http.StatusUnauthorized || got == http.StatusForbidden {
					t.Errorf("token with both scopes on the combined route: %d, should have passed the gate", got)
				}
			case gateOPDS:
				if got := basic(method, path, ""); got != http.StatusUnauthorized {
					t.Errorf("no credential: %d, want 401", got)
				}
				if got := basic(method, path, syncOnly); got != http.StatusForbidden {
					t.Errorf("sync-scoped credential on an OPDS route: %d, want 403", got)
				}
				if got := basic(method, path, libraryReadOnly); got == http.StatusUnauthorized || got == http.StatusForbidden {
					t.Errorf("library-read credential on an OPDS route: %d, should have passed the gate", got)
				}
			case gateTokenSelf:
				if got := bearer(method, path, ""); got != http.StatusUnauthorized {
					t.Errorf("no token: %d, want 401", got)
				}
				// ADR-0016: the narrowest token there is can still ask
				// what it is — that is the entire point of the route.
				if got := bearer(method, path, syncOnly); got != http.StatusOK {
					t.Errorf("a scoped token describing itself: %d, want 200", got)
				}
			case gateLoginCred:
				if got := bearer(method, path, ""); got != http.StatusUnauthorized {
					t.Errorf("no login credential: %d, want 401", got)
				}
				// A device token, however wide its scopes, is not a
				// login credential and must not substitute for one:
				// otherwise a stolen sync token could mint or revoke
				// other tokens on the account.
				if got := bearer(method, path, both); got != http.StatusUnauthorized {
					t.Errorf("a device token used as a login credential: %d, want 401", got)
				}
				if got := bearer(method, path, login); got == http.StatusUnauthorized {
					t.Errorf("the login credential itself: %d, should have passed the gate", got)
				}
			}
		})
	}
}
