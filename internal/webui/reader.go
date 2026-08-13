package webui

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

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
