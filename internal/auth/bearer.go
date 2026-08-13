package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

type ctxKey int

const (
	ctxToken ctxKey = iota
)

// TokenFrom extracts the authenticated token from the request context.
func TokenFrom(r *http.Request) (store.Token, bool) {
	t, ok := r.Context().Value(ctxToken).(store.Token)
	return t, ok
}

// RequireScope is the bearer middleware: parses
// "Authorization: Bearer <secret>", authenticates the token, and checks
// its scope. Admin implies every other scope.
func RequireScope(svc *Service, scope store.Scope, next http.Handler) http.Handler {
	return RequireAllScopes(svc, []store.Scope{scope}, next)
}

// RequireAllScopes authenticates the bearer token and requires every
// listed scope, not any of them. A route needs this when it joins two
// capabilities that are deliberately separable: resolving a catalog book
// to a sync work reads the catalog *and* writes the reader's work graph,
// so a catalog-only credential must not perform it, and neither must a
// sync-only one.
func RequireAllScopes(svc *Service, scopes []store.Scope, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		secret, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || secret == "" {
			http.Error(w, `{"error":"missing bearer token"}`, http.StatusUnauthorized)
			return
		}
		tok, err := svc.AuthenticateToken(r.Context(), secret)
		if err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		if !scopeAllowed(tok.Scopes, scopes) {
			http.Error(w, `{"error":"insufficient scope"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxToken, tok)))
	})
}

func scopeAllowed(have store.ScopeSet, want []store.Scope) bool {
	for _, scope := range want {
		if !have.Allows(scope) {
			return false
		}
	}
	return true
}

// BasicRealm is what a client is told to prompt for. OPDS readers show
// it verbatim in their credential dialog.
const BasicRealm = "Liseur catalog"

// RequireBasicScope authenticates HTTP Basic credentials for OPDS, which
// is the only authentication most e-reader catalog clients can perform.
//
// The password is a device token secret, never an account password:
// AuthenticateToken only ever matches token hashes, so a login password
// cannot open the catalog even though the field looks the same to the
// user. The username carries no authority — a token secret is already a
// bearer credential — but it is checked against the token's name so that
// a reader configured for the wrong account fails loudly instead of
// silently browsing someone else's books.
func RequireBasicScope(svc *Service, scope store.Scope, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, secret, ok := r.BasicAuth()
		if !ok || secret == "" {
			challengeBasic(w, "authentication required")
			return
		}
		tok, err := svc.AuthenticateToken(r.Context(), secret)
		if err != nil {
			challengeBasic(w, "invalid credentials")
			return
		}
		if username != "" && username != "token" && username != tok.Name {
			challengeBasic(w, "invalid credentials")
			return
		}
		if !scopeAllowed(tok.Scopes, []store.Scope{scope}) {
			// Not a challenge: the credential is good and retrying with
			// the same one will not help. The token needs a wider scope.
			http.Error(w, `{"error":"insufficient scope"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxToken, tok)))
	})
}

func challengeBasic(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="`+BasicRealm+`", charset="UTF-8"`)
	http.Error(w, `{"error":"`+msg+`"}`, http.StatusUnauthorized)
}
