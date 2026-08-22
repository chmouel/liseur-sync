package webui

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// The separate reader origin (ADR-0007 phase 3).
//
// A book is laid out by an engine that has to reach into the laid-out
// document, so the frame holding publication content is same-origin
// with the reader page. No script inside a book runs — the page CSP's
// nonce-gated script-src is inherited by every chapter document, and
// the reader strips script elements from each resource besides
// (ADR-0012) — but that is a policy and a transform standing between
// publisher markup and the page it was laid out in. An operator who
// does not want to bet a session cookie on them names a second
// hostname, and the reader moves there.
//
// What the second hostname is worth comes entirely from what it does not
// have. It serves the reader shell and the static assets, and nothing
// else: no session cookie is ever set on it, no authenticated route
// answers on it, and there is nothing there to steal. The reader reaches
// the API cross-origin with a short-lived token carrying library-read
// and sync, which is the same credential it always used.
//
// The handoff is a redirect with the credential in the URL fragment. A
// fragment is not sent to the server, does not appear in a log or a
// Referer, and is readable only by script on the origin that was
// navigated to — which is exactly the delivery this needs. The page
// erases it from the address bar on arrival.

// onReaderOrigin reports whether this request arrived at the configured
// reader hostname. Port included: two origins on one host that differ
// only by port are still two origins to a browser, and treating them as
// one here would hand reader pages to the wrong one.
func (s *Server) onReaderOrigin(r *http.Request) bool {
	want := s.Cfg.ReaderOriginHost()
	if want == "" {
		return false
	}
	return strings.EqualFold(requestHost(r), want)
}

// requestHost is the host the browser asked for. Host is used rather
// than the forwarded headers a proxy may add, because this decides only
// which of this server's own two faces answered, and a proxy that
// rewrites Host between them has already broken the split.
func requestHost(r *http.Request) string {
	if r.Host != "" {
		return r.Host
	}
	return r.URL.Host
}

// handleReaderRoute is GET /ui/books/{id}/read in all three deployments:
// same-origin (render), the main origin with a reader origin configured
// (hand off), and the reader origin itself (render detached).
func (s *Server) handleReaderRoute(w http.ResponseWriter, r *http.Request) {
	if s.Cfg.ReaderOrigin == "" {
		s.requireAuth(s.handleReaderPage)(w, r)
		return
	}
	if s.onReaderOrigin(r) {
		s.handleDetachedReaderPage(w, r)
		return
	}
	s.requireAuth(s.handleReaderHandoff)(w, r)
}

// handleReaderHandoff sends a reader to the other origin with a
// credential it can use when it gets there.
//
// The book is authorised here, on the origin that has the session, so an
// unreadable book fails as a 404 on the page the user came from rather
// than as a broken reader on a hostname they have never seen.
//
// A GET that mints a credential would normally want a CSRF token. It
// does not need one because of where the credential goes: into a
// fragment on another origin, which the site that caused the navigation
// cannot read. What a cross-site link can do is cause a token to be
// minted, and the reader token is deliberately cheap — one stable device
// identity per user, a short expiry, and two scopes.
func (s *Server) handleReaderHandoff(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	bookID := r.PathValue("id")
	book, err := s.St.CatalogBookByID(r.Context(), u.ID, bookID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	secret, _, err := s.Auth.MintReaderToken(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	escaped := url.PathEscape(book.ID)
	here := s.originOf(r)

	// The credential travels in the fragment and nothing else does. A
	// fragment is not sent to the server, so it is the one part of a URL
	// that cannot reach an access log or a proxy — and it is also
	// invisible to the page that receives it until its own script reads
	// it, which is why the rest is in the query instead. This server
	// does not know its own public address behind a proxy, and the
	// reader origin needs it both to call the API and to name it in a
	// policy, so it is told rather than left to guess.
	q := url.Values{}
	q.Set("api", here)
	q.Set("back", here+strings.TrimSuffix(r.URL.Path, "/read"))
	target := s.Cfg.ReaderOrigin + "/ui/books/" + escaped + "/read?" + q.Encode() +
		"#t=" + url.QueryEscape(secret)

	// no-store because the Location carries a live credential, and a
	// cache that kept it would hand one browser another's token.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// originOf reconstructs the origin this request was made to. The scheme
// comes from auth.IsSecure rather than from the forwarded header
// directly, so the reader is told to call back over https on exactly the
// deployments where the rest of the server already believes it is behind
// TLS — one rule about trusting a proxy, not two.
func (s *Server) originOf(r *http.Request) string {
	scheme := "http"
	if auth.IsSecure(r, s.Cfg) {
		scheme = "https"
	}
	return scheme + "://" + requestHost(r)
}

// handleDetachedReaderPage serves the reader shell on the reader origin.
//
// It is the one page in this server that answers without a session, and
// it can be because it says nothing. It knows a book id, which the URL
// already contained; it has no title until the publication supplies one,
// no library, and no user. The credential arrives in the fragment, in
// the browser, after this response is written.
func (s *Server) handleDetachedReaderPage(w http.ResponseWriter, r *http.Request) {
	bookID := r.PathValue("id")

	// Where the API is comes from the URL, so it is checked here as
	// hostilely as any other input: it ends up in a response header and
	// in an anchor, and an unchecked value in either is a hole rather
	// than a broken page. Anything that is not a bare origin is dropped,
	// and the reader then says it cannot reach the API — which is true.
	apiOrigin := safeOrigin(r.URL.Query().Get("api"))
	back := ""
	if apiOrigin != "" {
		back = safeURLOn(apiOrigin, r.URL.Query().Get("back"))
	}
	nonce := setReaderPolicy(w, apiOrigin)
	// Nothing here is per-user, but a cache between the two origins
	// still must not treat one reader's shell as another's, since the
	// URL is the only thing that distinguishes them.
	w.Header().Set("Cache-Control", "no-store")
	// The base is the server root, not /v1: every call the reader makes
	// spells out its own path from there, exactly as it does when the
	// two origins are one.
	apiBase := ""
	if apiOrigin != "" {
		apiBase = apiOrigin + "/"
	}
	readerPage(ReaderView{
		BookID:      bookID,
		Detached:    true,
		BackURL:     back,
		APIBase:     apiBase,
		DownloadURL: apiBase + "v1/books/" + url.PathEscape(bookID) + "/download",
		StaticBase:  relPrefix(r.URL.Path) + "static/",
		ScriptNonce: nonce,
	}, "").Render(r.Context(), w)
}

// safeOrigin returns raw when it is a bare http(s) origin and "" when it
// is anything else. The strictness is not decoration: this value is
// written into a Content-Security-Policy header, where a space or a
// newline is a way to add a directive somebody else chose.
func safeOrigin(raw string) string {
	if raw == "" || len(raw) > 253 || strings.ContainsAny(raw, " \t\r\n;,\"'") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return ""
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// safeURLOn returns raw only when it is an absolute URL on origin. The
// back link is the one thing on this page a user is invited to click, so
// it is not allowed to lead anywhere but the library it came from.
func safeURLOn(origin, raw string) string {
	if raw == "" || !strings.HasPrefix(raw, origin+"/") {
		return ""
	}
	if u, err := url.Parse(raw); err != nil || u.User != nil {
		return ""
	}
	return raw
}
