package webui

// First-run setup (ADR-0013). A fresh instance has no accounts, and
// until somebody has one there is nobody to authenticate — so the setup
// page is open by necessity, and closes for good the moment the first
// account exists. It is not an invite system and not a registration
// page: it runs exactly once, makes exactly one account, and that
// account is an administrator because otherwise the operator is
// immediately back at a shell prompt looking for grant-admin.

import (
	"strconv"
	"errors"
	"log/slog"
	"net/http"

	"github.com/chmouel/liseur-sync/internal/admin"
)

// instanceEmpty answers "does this instance have any account at all?".
// It reads one row rather than counting, because the answer is only
// ever used to choose a page and the question is asked on every visit
// to the sign-in page.
//
// A false here is advisory: it decides what to render. The decision
// that matters — whether the account may be created — is made by the
// store, inside the transaction that inserts it.
func (s *Server) instanceEmpty(r *http.Request) bool {
	users, err := s.St.ListUsersPage(r.Context(), "", 1)
	return err == nil && len(users) == 0
}

// handleSetupPage renders first-run setup, or sends the visitor to the
// sign-in page when the instance already has an account.
func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	prefix := relPrefix(r.URL.Path)
	if !s.instanceEmpty(r) {
		redirectRel(w, prefix+"login", http.StatusSeeOther)
		return
	}
	setupPage(prefix, uiCtx(r, nil), "").Render(r.Context(), w)
}

// handleSetup creates the first account, makes it an administrator and
// signs it in.
//
// There is no CSRF token: there is no session to bind one to, and
// nothing to forge on behalf of a user who does not exist yet. The
// request is rate limited like sign-in, because the endpoint is open
// and the password hash it computes is deliberately expensive.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	prefix := relPrefix(r.URL.Path)
	fail := func(msg string) {
		setupPage(prefix, uiCtx(r, nil), msg).Render(r.Context(), w)
	}
	if !s.instanceEmpty(r) {
		redirectRel(w, prefix+"login", http.StatusSeeOther)
		return
	}
	name := r.FormValue("username")
	pw, repeat := r.FormValue("password"), r.FormValue("repeat")
	if err := admin.ValidatePassword(pw, repeat); err != nil {
		fail(err.Error())
		return
	}
	u, err := admin.CreateFirstAdmin(r.Context(), s.St, name, pw)
	switch {
	case errors.Is(err, admin.ErrSetupClosed):
		// Somebody else finished setup between the check above and this
		// write. Their account is the one that exists; send this visitor
		// to sign in rather than pretending anything went wrong.
		redirectRel(w, prefix+"login", http.StatusSeeOther)
		return
	case err != nil:
		if isUserError(err) {
			fail(err.Error())
			return
		}
		slog.Error("first-run setup failed", "error", err)
		fail("internal error")
		return
	}
	slog.Info("admin action", "action", "setup.first_admin",
		"target_user", u.ID, "target_name", u.Name)
	if err := s.startSession(w, r, u); err != nil {
		// The account is real; only the convenience of being signed in
		// straight away was lost.
		redirectRel(w, prefix+"login", http.StatusSeeOther)
		return
	}
	redirectRel(w, "./", http.StatusSeeOther)
}

// isUserError reports whether err is one of the validation refusals
// that are safe, and useful, to show verbatim.
func isUserError(err error) bool {
	for _, e := range []error{
		admin.ErrPasswordTooShort, admin.ErrPasswordMismatch,
		admin.ErrNameEmpty, admin.ErrNameTooLong, admin.ErrNameInvalid,
		admin.ErrNameTaken,
	} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// unauthenticatedLanding decides what somebody with no session sees.
// On an instance with accounts that is the sign-in page; on an empty
// one it is setup, so that a first-time operator who opens the web UI
// is never shown a form that no password can satisfy.
func (s *Server) unauthenticatedLanding(w http.ResponseWriter, r *http.Request) {
	prefix := relPrefix(r.URL.Path)
	if s.instanceEmpty(r) {
		redirectRel(w, prefix+"setup", http.StatusSeeOther)
		return
	}
	loginPage(prefix, uiCtx(r, nil), "").Render(r.Context(), w)
}

// minPasswordLength renders the policy into the setup form's minlength
// attribute, so the browser refuses before a round trip does.
var minPasswordLength = strconv.Itoa(admin.MinPasswordLength)
