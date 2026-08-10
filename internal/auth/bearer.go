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
		if !scopeAllowed(tok.Scope, scope) {
			http.Error(w, `{"error":"insufficient scope"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxToken, tok)))
	})
}

func scopeAllowed(have, want store.Scope) bool {
	if have == store.ScopeAdmin {
		return true
	}
	return have == want
}
