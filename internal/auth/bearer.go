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

// RequireToken authenticates the bearer and requires no scope at all.
//
// It exists for the one route whose subject is the credential itself
// (ADR-0016): a token asking what it is learns nothing it did not
// already possess, because it is holding the credential. Requiring a
// scope here would mean the narrowest tokens — the ones whose limits
// most need discovering — are the ones that cannot ask.
//
// This is not an open route. It authenticates like every other route;
// it simply has no capability to check afterwards.
func RequireToken(svc *Service, next http.Handler) http.Handler {
	return RequireAllScopes(svc, nil, next)
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

// RequireBasicToken authenticates HTTP Basic credentials for OPDS, which
// is the only authentication most e-reader catalog clients can perform.
// It attaches the token and checks no scope; the caller adds
// RequireAttachedScope.
//
// The password is a device token secret, never an account password:
// AuthenticateToken only ever matches token hashes, so a login password
// cannot open the catalog even though the field looks the same to the
// user. The username carries no authority — a token secret is already a
// bearer credential — but it is checked against the token's name so that
// a reader configured for the wrong account fails loudly instead of
// silently browsing someone else's books.
//
// Authentication and authorization are separable because a refusal has to
// be reportable: a scope check folded in here would answer 403 before the
// token existed anywhere a caller could read, and the one line that says
// "insufficient scope" would not say which credential to widen.
func RequireBasicToken(svc *Service, next http.Handler) http.Handler {
	return RequireBasicTokenChallenge(svc, challengeBasic, next)
}

// BasicChallenge answers a request whose Basic credential was absent or
// refused. It must leave a 401 with a Basic challenge behind:
// SetBasicChallenge is the header, and the body is the caller's choice.
type BasicChallenge func(w http.ResponseWriter, r *http.Request, msg string)

// RequireBasicTokenChallenge is RequireBasicToken with the refusal
// written by challenge, for a protocol that describes how to
// authenticate in the body of the 401 itself.
func RequireBasicTokenChallenge(svc *Service, challenge BasicChallenge, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, secret, ok := r.BasicAuth()
		if !ok || secret == "" {
			challenge(w, r, "authentication required")
			return
		}
		tok, err := svc.AuthenticateToken(r.Context(), secret)
		if err != nil {
			challenge(w, r, "invalid credentials")
			return
		}
		if username != "" && username != "token" && username != tok.Name {
			challenge(w, r, "invalid credentials")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxToken, tok)))
	})
}

// RequireAttachedScope requires a scope on the token an authenticating
// middleware already attached to the request.
func RequireAttachedScope(scope store.Scope, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := TokenFrom(r)
		if !ok {
			http.Error(w, `{"error":"missing bearer token"}`, http.StatusUnauthorized)
			return
		}
		if !scopeAllowed(tok.Scopes, []store.Scope{scope}) {
			// Not a challenge: the credential is good and retrying with
			// the same one will not help. The token needs a wider scope.
			http.Error(w, `{"error":"insufficient scope"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func challengeBasic(w http.ResponseWriter, _ *http.Request, msg string) {
	SetBasicChallenge(w)
	http.Error(w, `{"error":"`+msg+`"}`, http.StatusUnauthorized)
}

// SetBasicChallenge sets the header that makes a 401 a Basic challenge.
func SetBasicChallenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="`+BasicRealm+`", charset="UTF-8"`)
}
