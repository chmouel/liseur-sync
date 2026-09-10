package webui

// First-run setup (ADR-0013). A fresh instance has no accounts, and
// until somebody has one there is nobody to authenticate — so the setup
// page is open by necessity, and closes for good the moment the first
// account exists. It is not an invite system and not a registration
// page: it runs exactly once, makes exactly one account, and that
// account is an administrator because otherwise the operator is
// immediately back at a shell prompt looking for grant-admin.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/chmouel/liseur-sync/internal/admin"
	"github.com/chmouel/liseur-sync/internal/store"
)

// setupForm is the first-run screen's submission, carried back into the
// page when something is refused. Without it a mistyped password would
// also empty the folder fields, and the operator would retype a path
// they had already got right.
//
// The folder half is optional: an instance with an account and no folder
// is a working instance, and the folders page is still there.
type setupForm struct {
	Username   string
	FolderName string
	FolderRoot string
}

// wantsFolder reports whether the operator filled in the folder half at
// all. Either field is enough to count as intent, so that half a folder
// is refused rather than silently dropped.
func (f setupForm) wantsFolder() bool {
	return f.FolderName != "" || f.FolderRoot != ""
}

// errHalfFolder is what half of a folder gets. Guessing the missing half
// is possible — a name could be taken from the path's last segment — but
// it would mean this form and the folders form disagreeing about what a
// folder is called.
var errHalfFolder = errors.New(
	"a folder needs both a name and a root path, or leave both empty")

// validate checks everything about the folder half that can be checked
// without touching the disk.
//
// That boundary is the point. /ui/setup is open to anyone while the
// instance has no accounts, and resolving a root path stats the
// filesystem: doing it here would answer "does this path exist on your
// server?" for an anonymous visitor. So the shape is checked before the
// account is made and the disk only after it, when the request has an
// administrator behind it.
func (f setupForm) validate() error {
	if !f.wantsFolder() {
		return nil
	}
	if f.FolderName == "" || f.FolderRoot == "" {
		return errHalfFolder
	}
	return admin.ValidateFolderName(f.FolderName)
}

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
	setupPage(prefix, uiCtx(r, nil), setupForm{}, "").Render(r.Context(), w)
}

// handleSetup creates the first account, makes it an administrator,
// signs it in, and watches the folder it was given — if it was given
// one.
//
// The order is the interesting part. Everything that reads the disk
// happens after the account exists and its session is started, because
// until then this is an open endpoint (see setupForm.validate). So a
// visitor who is not the operator learns nothing about the filesystem,
// and the operator gets both halves of first run on one screen.
//
// There is no CSRF token: there is no session to bind one to, and
// nothing to forge on behalf of a user who does not exist yet. The
// request is rate limited like sign-in, because the endpoint is open
// and the password hash it computes is deliberately expensive.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	prefix := relPrefix(r.URL.Path)
	form := setupForm{
		Username:   r.FormValue("username"),
		FolderName: strings.TrimSpace(r.FormValue("folder_name")),
		FolderRoot: strings.TrimSpace(r.FormValue("folder_root")),
	}
	fail := func(msg string) {
		setupPage(prefix, uiCtx(r, nil), form, msg).Render(r.Context(), w)
	}
	if !s.instanceEmpty(r) {
		redirectRel(w, prefix+"login", http.StatusSeeOther)
		return
	}
	pw, repeat := r.FormValue("password"), r.FormValue("repeat")
	if err := admin.ValidatePassword(pw, repeat); err != nil {
		fail(err.Error())
		return
	}
	if err := form.validate(); err != nil {
		fail(err.Error())
		return
	}
	u, err := admin.CreateFirstAdmin(r.Context(), s.St, form.Username, pw)
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
		// straight away was lost. The folder is not attempted: it would
		// be a disk read on behalf of a request that is, once again,
		// nobody.
		redirectRel(w, prefix+"login", http.StatusSeeOther)
		return
	}
	s.finishSetup(w, r, u, form)
}

// finishSetup lands the new administrator: on their library if they
// named a folder, and on the folders page asking for one if they did
// not.
//
// The folder is created through admin.NewFolder like every other folder,
// so its single grant — to the administrator who just named it — is
// written in the same transaction (ADR-0029). A first run that
// registered a folder nobody could read would be the emptiest possible
// library.
func (s *Server) finishSetup(
	w http.ResponseWriter, r *http.Request, u store.User, form setupForm,
) {
	prefix := relPrefix(r.URL.Path)
	if !form.wantsFolder() {
		destination := "./"
		hasFolders, err := s.St.HasAnyFolder(r.Context())
		if err != nil {
			slog.Error("could not determine whether first-run setup has a folder",
				"error", err)
		} else if !hasFolders {
			destination = settingsAdminFoldersOnboardingHref(prefix)
		}
		redirectRel(w, destination, http.StatusSeeOther)
		return
	}
	folder, err := admin.NewFolder(r.Context(), s.St, form.FolderName,
		form.FolderRoot, s.Cfg.Content.FolderRoots, u.ID)
	logAdminAction(r, &u, "add-folder", form.FolderName, err)
	if err != nil {
		// The account is made and they are signed in, so setup has
		// closed behind them: re-rendering that form would be offering
		// something this server will now refuse. Send them to the
		// folders page with the dialog open and the reason showing —
		// which is where a second attempt belongs anyway.
		redirectRel(w, settingsAdminHref(prefix, settingsAdminFolders)+"&"+
			flashQuery(Flash{Error: err.Error(), OpenFolderForm: true}),
			http.StatusSeeOther)
		return
	}
	// Reconcile now rather than waiting for the safety pass, and bound
	// it: a folder nothing has read yet is hashed and parsed book by
	// book. Giving up costs nothing — the folder is registered and
	// watched by then, which is all the notice below promises.
	if s.Watching != nil {
		ctx, cancel := context.WithTimeout(r.Context(), scanBudget)
		defer cancel()
		s.Watching.Add(ctx, folder)
	}
	redirectRel(w, prefix+"library?notice="+
		url.QueryEscape(watchingNotice(folder.Name)), http.StatusSeeOther)
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
