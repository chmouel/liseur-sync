package webui

import (
	"crypto/rand"
	"encoding/base64"
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

	nonce := setReaderPolicy(w, "")

	readerPage(ReaderView{
		BookID:      book.ID,
		Title:       book.Title,
		BackURL:     prefix + "books/" + escaped,
		DownloadURL: prefix + "books/" + escaped + "/download",
		TokenURL:    prefix + "reader/token",
		APIBase:     prefix + "../",
		StaticBase:  prefix + "static/",
		ScriptNonce: nonce,
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

// setReaderPolicy writes the headers that confine publication content.
//
// The policy has to admit two things the rendering engine needs, and
// nothing else.
//
// `blob:` is how foliate-js hands each chapter to its iframe and how
// it rewrites the publication's own images and stylesheets. A blob
// document inherits the policy of whatever created it, so this header
// is what confines the publication as well as the page.
//
// `style-src 'unsafe-inline'` is unavoidable: a book's own markup
// carries style attributes, and there is no nonce to give markup that
// arrived in a zip file. `script-src` gets the opposite treatment — a
// per-response nonce plus 'strict-dynamic', so the only script that
// runs anywhere under this policy is the module tag this server wrote
// into this response and the imports that module makes. A publication
// pointing a script tag at a same-origin URL gets nothing: 'self' is
// listed only for old browsers, and everything that understands
// 'strict-dynamic' ignores it. The frames the engine makes carry
// allow-scripts (it needs its own events inside them), so this
// directive — inherited by every blob: chapter document — is the
// barrier, with the reader stripping script elements from each
// resource besides (ADR-0012).
//
// apiOrigin widens `connect-src` by exactly one origin, and only on the
// separate reader origin, where the API is somewhere else by design. It
// is a checked bare origin or nothing; the caller does that checking.
//
// The returned nonce must be written into the page's own script tag and
// nowhere else.
func setReaderPolicy(w http.ResponseWriter, apiOrigin string) string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		// No randomness, no page: serving without a working nonce would
		// silently downgrade the script policy to 'self'.
		panic(err)
	}
	nonce := base64.StdEncoding.EncodeToString(raw)
	connect := "connect-src 'self' blob:"
	if apiOrigin != "" {
		connect += " " + apiOrigin
	}
	w.Header().Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'none'",
		"script-src 'self' 'nonce-" + nonce + "' 'strict-dynamic'",
		"style-src 'self' 'unsafe-inline' blob:",
		"img-src 'self' data: blob:",
		"font-src data: blob:",
		"media-src data: blob:",
		connect,
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
	return nonce
}

// boolAttr renders a bool as a data attribute the browser can read
// without a second convention: absent is false, "1" is true.
func boolAttr(v bool) string {
	if v {
		return "1"
	}
	return ""
}

// readerTitle keeps the tab from saying " · liseur-sync" before the
// publication has told anybody its name, which is what the detached
// reader sees for the first moment of its life.
func readerTitle(title string) string {
	if title == "" {
		return "liseur-sync"
	}
	return title + " · liseur-sync"
}
