package webui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// handleReaderPage serves the browser reader for one book (ADR-0007).
//
// The page carries no publication bytes. It fetches the EPUB and unpacks
// it in the browser, so the only thing this handler decides is whether
// the caller may read the book at all — and the only thing it
// contributes to how the book is rendered is the policy below.
func (s *Server) handleReaderPage(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	bookID := r.PathValue("id")
	book, err := s.St.CatalogBookByID(r.Context(), u.ID, bookID, store.LibraryRoleRead)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	prefix := relPrefix(r.URL.Path)
	escaped := url.PathEscape(bookID)

	// The policy has to admit two things the rendering engine needs, and
	// nothing else.
	//
	// `blob:` is how epub.js hands each chapter to its iframe and how it
	// rewrites the publication's own images and stylesheets. A blob
	// document inherits the policy of whatever created it, so this
	// header is what confines the publication as well as the page.
	//
	// `style-src 'unsafe-inline'` is unavoidable: a book's own markup
	// carries style attributes, and there is no nonce to give markup
	// that arrived in a zip file. `script-src` deliberately has no such
	// hole — the engine is served from here, and the publication's
	// scripts never run at all, because the iframe is sandboxed without
	// allow-scripts.
	w.Header().Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'none'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline' blob:",
		"img-src 'self' data: blob:",
		"font-src data: blob:",
		"media-src data: blob:",
		"connect-src 'self' blob:",
		"frame-src blob:",
		"child-src blob:",
		// The engine gives each chapter a <base> so that the
		// publication's own relative links resolve against where the
		// document actually came from. 'self' permits that and still
		// refuses a base pointing at somebody else's origin, which is
		// the attack this directive exists for.
		"base-uri 'self'",
		"form-action 'none'",
	}, "; "))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")

	readerPage(ReaderView{
		BookID:      book.ID,
		Title:       book.Title,
		BackURL:     prefix + "books/" + escaped,
		DownloadURL: prefix + "books/" + escaped + "/download",
		TokenURL:    prefix + "reader/token",
		APIBase:     prefix + "../",
		StaticBase:  prefix + "static/",
	}, csrfFor(a)).Render(r.Context(), w)
}

// readerTokenResponse is the browser reader's credential. The secret is
// returned in the body rather than set as a cookie on purpose: the
// reader is an ordinary API client sending Authorization headers, and a
// credential the browser attaches automatically is one that publication
// content might one day ride along with.
type readerTokenResponse struct {
	Token     string   `json:"token"`
	DeviceID  string   `json:"device_id"`
	ExpiresAt string   `json:"expires_at"`
	Scopes    []string `json:"scopes"`
}

// handleReaderToken mints the short-lived API token the browser reader
// uses for /v1/ops, /v1/changes and /v1/sessions (ADR-0007).
//
// It is authenticated by the session cookie and guarded by the same CSRF
// token as every other mutation here — without that check any site could
// make a logged-in visitor's browser hand out a working sync credential.
//
// The token's scopes are fixed at library-read and sync. They are not
// copied from what the user is otherwise allowed, so a librarian reading
// a book in a tab is not carrying a credential that can delete one.
func (s *Server) handleReaderToken(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	secret, tok, err := s.Auth.MintReaderToken(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	scopes := make([]string, 0, len(tok.Scopes))
	for _, scope := range tok.Scopes {
		scopes = append(scopes, string(scope))
	}
	expires := ""
	if tok.ExpiresAt != nil {
		expires = tok.ExpiresAt.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(readerTokenResponse{
		Token:     secret,
		DeviceID:  tok.DeviceID,
		ExpiresAt: expires,
		Scopes:    scopes,
	})
}
