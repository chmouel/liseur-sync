package api

import (
	"net/http"
	"strings"

	"github.com/chmouel/liseur-sync/internal/config"
)

// CORS answers cross-origin API calls from the origins an operator
// listed, and from nowhere else (ADR-0007 phase 3).
//
// This exists for one caller: the browser reader served from a separate
// origin, which is an ordinary API client that happens to live on
// another hostname. Everything about the policy follows from that.
//
// **Credentials are never allowed.** The reader authenticates with a
// bearer token it was handed, not with the session cookie, and
// `Access-Control-Allow-Credentials` would let any listed origin make a
// logged-in visitor's browser send that cookie — which is the CSRF
// protection of the entire UI handed to whoever the operator typed into
// a config file. A bearer token has to be obtained; a cookie rides
// along.
//
// **Only /v1 is answered.** The web UI is a server-rendered surface with
// CSRF-protected forms; there is no such thing as another origin
// legitimately posting to it, and `/ui` staying same-origin-only means a
// mistake in this list cannot reach it.
func CORS(cfg config.Config, next http.Handler) http.Handler {
	allowed := cfg.BrowserOrigins()
	if len(allowed) == 0 {
		// Deny by default, and cheaply: with nothing configured this is
		// the identity function rather than a header nobody reads.
		return next
	}
	permitted := make(map[string]bool, len(allowed))
	for _, origin := range allowed {
		permitted[origin] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		// Vary is set even when the origin is refused, because the
		// answer depends on the header either way and a cache that
		// missed that would serve one origin's response to another.
		w.Header().Add("Vary", "Origin")
		if origin == "" || !permitted[origin] || !strings.HasPrefix(r.URL.Path, "/v1/") {
			if r.Method == http.MethodOptions && origin != "" {
				// An unlisted origin's preflight is refused here rather
				// than passed to a mux that has no OPTIONS route and
				// would answer 405, which reads like a bug in the route
				// rather than a policy decision.
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		header := w.Header()
		header.Set("Access-Control-Allow-Origin", origin)
		header.Set("Access-Control-Expose-Headers", "Retry-After")
		if r.Method == http.MethodOptions {
			header.Set("Access-Control-Allow-Methods",
				"GET, HEAD, POST, PUT, PATCH, DELETE")
			header.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			// Ten minutes is Chrome's cap. A longer value is not honoured
			// and a shorter one buys a preflight per request.
			header.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ReaderOrigin confines the reader hostname to what a reader needs
// (ADR-0007 phase 3).
//
// The point of a second origin is that a sandbox escape out of
// publication content lands somewhere holding nothing. That is only true
// if nothing else answers there, and "nothing else" has to be enforced
// rather than assumed: the same mux serves both hostnames, and every
// route added later would otherwise appear on the reader origin for
// free. So the allowance is a list of two things, and the default is
// 404.
//
// A 404 rather than a redirect to the real origin, because a redirect
// would teach a browser that the reader hostname is a way to reach
// authenticated pages, which is the one idea this whole phase exists to
// prevent.
func ReaderOrigin(cfg config.Config, next http.Handler) http.Handler {
	host := cfg.ReaderOriginHost()
	if host == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(hostOf(r), host) {
			next.ServeHTTP(w, r)
			return
		}
		if !readerOriginPath(r) {
			http.NotFound(w, r)
			return
		}
		// A session cookie should never reach this hostname, but a
		// wildcard certificate and a careless proxy can put one there.
		// Dropping it means a handler that forgot which origin it was
		// on still cannot act on a credential.
		r.Header.Del("Cookie")
		next.ServeHTTP(w, r)
	})
}

// readerOriginPath is the whole of what the reader hostname serves: the
// reader shell for one book, and the static assets that render it.
func readerOriginPath(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	if strings.HasPrefix(p, "/ui/static/") {
		return true
	}
	rest, ok := strings.CutPrefix(p, "/ui/books/")
	if !ok {
		return false
	}
	id, ok := strings.CutSuffix(rest, "/read")
	return ok && id != "" && !strings.Contains(id, "/")
}

func hostOf(r *http.Request) string {
	if r.Host != "" {
		return r.Host
	}
	return r.URL.Host
}

// Handler is Routes with the origin policies around it. Callers that
// serve this binary should use it rather than Routes.
func (s *Server) Handler() http.Handler {
	return CORS(s.Cfg, ReaderOrigin(s.Cfg, s.Routes()))
}
