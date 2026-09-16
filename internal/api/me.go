package api

import (
	"net/http"

	"github.com/chmouel/liseur-sync/internal/auth"
)

// HandleMe answers with the account behind the credential — its name,
// its timezone and the reader prompt it keeps.
//
// It exists for a client that holds a token and nothing else. The
// browser reader served from the separate reader origin (ADR-0007) is
// the first of them: that page has no session and no user, so the
// server cannot render the account's reader prompt into it and the
// reader has to ask for it once the token is in hand.
//
// It reads the token's own account and never a path parameter, so there
// is no id here to get wrong and no way to ask about somebody else.
func (s *Server) HandleMe(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	user, err := s.St.UserByID(r.Context(), tok.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account read failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                     user.ID,
		"name":                   user.Name,
		"timezone":               user.Timezone,
		"reader_prompt_template": user.ReaderPromptTemplate,
	})
}
