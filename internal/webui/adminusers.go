package webui

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/chmouel/liseur-sync/internal/admin"
	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// The users pages (ADR-0013 phase 3).
//
// Everything here administers accounts, never their contents: there is
// no route below that reads another user's works, sessions, positions
// or books, and the page renders a name, an id, a device label and a
// timestamp — nothing that came out of somebody's EPUB.

// adminUsersPerPage is how many accounts one page shows. The list is
// cursor-paginated because it grows with the instance.
const adminUsersPerPage = 50

// adminListCap bounds the per-account lists (tokens, kosync slots,
// koplugin devices). Those grow with what one person did rather than
// with the instance, so they are read in full and rendered capped, with
// the exact remainder stated — the handler knows it because it holds
// the slice (ADR-0013).
const adminListCap = 200

// adminUserView is one account as the per-user page shows it.
type adminUserView struct {
	User     store.User
	Tokens   []store.Token
	Kosync   []store.KosyncDevice
	Koplugin []store.KopluginDevice
	// Extra counts what the caps hid, so the page can say "and 40 more"
	// rather than quietly showing a prefix.
	MoreTokens   int
	MoreKosync   int
	MoreKoplugin int
	// Libraries is what this account owns or was granted, one page of
	// it; the whole instance's list lives on the libraries page.
	Libraries     []adminUserLibrary
	MoreLibraries bool
	// Self marks the acting admin's own account: demoting or disabling
	// yourself is not offered, since the last-admin guard would be the
	// only thing standing between an operator and a locked instance.
	Self bool
}

// reauth is the shared gate in front of every high-impact mutation: the
// acting admin types their own password, and it is checked against two
// independent budgets before it is checked against the hash.
//
// A verifier reachable from a stolen session is an online password
// oracle. One budget is keyed on the acting admin's account, so an
// attacker who moves between addresses still runs out; the other on the
// remote address, so one account cannot spend the whole instance's.
// Both must allow the attempt.
func (s *Server) reauth(r *http.Request, actor *store.User) error {
	if s.AdminReauthUserLimiter != nil && !s.AdminReauthUserLimiter.Allow(actor.ID) {
		return errRateLimited
	}
	if s.AdminReauthIPLimiter != nil && !s.AdminReauthIPLimiter.Allow(remoteHost(r)) {
		return errRateLimited
	}
	ok, err := auth.CheckPassword(r.FormValue("admin_password"), actor.Argon2Hash)
	if err != nil || !ok {
		return errBadAdminPassword
	}
	return nil
}

var (
	errRateLimited      = errors.New("too many attempts; wait a minute and try again")
	errBadAdminPassword = errors.New("your password is wrong")
)

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// logAdminAction is the audit trail: one structured line per cross-user
// mutation, actor and target by id, outcome named. No persisted table
// (that would need a migration, a retention policy and a page of its
// own); no secret ever reaches it.
func logAdminAction(r *http.Request, actor *store.User, action, targetID string, err error) {
	outcome := "ok"
	if err != nil {
		outcome = err.Error()
	}
	slog.InfoContext(r.Context(), "admin action",
		"actor_id", actor.ID, "action", action, "target_id", targetID,
		"outcome", outcome)
}

func (s *Server) handleAdminUsers(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.renderAdminUsers(w, r, a, u, Flash{})
}

func (s *Server) renderAdminUsers(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash,
) {
	after := r.URL.Query().Get("after")
	// One row more than the page shows: the extra row is the answer to
	// "is there another page", with no second counting query.
	users, err := s.St.ListUsersPage(r.Context(), after, adminUsersPerPage+1)
	if err != nil {
		http.Error(w, "user list unavailable", http.StatusInternalServerError)
		return
	}
	var next string
	if len(users) > adminUsersPerPage {
		users = users[:adminUsersPerPage]
		next = users[len(users)-1].Name
	}
	invites, _ := s.St.ListInvites(r.Context(), u.ID)
	prefix := relPrefix(r.URL.Path)
	adminPage("Users", prefix, uiCtx(r, u), csrfFor(a), "users",
		adminUsersBody(prefix, csrfFor(a), users, next, invites, flash)).
		Render(r.Context(), w)
}

func (s *Server) handleAdminCreateUser(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := r.FormValue("name")
	pw, repeat := r.FormValue("password"), r.FormValue("repeat")
	if err := admin.ValidatePassword(pw, repeat); err != nil {
		s.renderAdminUsers(w, r, a, u, Flash{Notice: "", Error: err.Error()})
		return
	}
	created, err := admin.CreateUser(r.Context(), s.St, name, pw)
	logAdminAction(r, u, "create-user", name, err)
	if err != nil {
		s.renderAdminUsers(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	s.renderAdminUsers(w, r, a, u, Flash{
		Notice: "Created " + created.Name + ".",
	})
}

func (s *Server) handleAdminUser(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.renderAdminUser(w, r, a, u, r.PathValue("id"), Flash{})
}

func (s *Server) renderAdminUser(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
	targetID string, flash Flash,
) {
	target, err := s.St.UserByID(r.Context(), targetID)
	if err != nil {
		http.Error(w, "no such user", http.StatusNotFound)
		return
	}
	view := adminUserView{User: target, Self: target.ID == u.ID}
	view.Tokens, view.MoreTokens = capSlice(listOrNil(s.St.ListTokens(r.Context(), target.ID)))
	view.Kosync, view.MoreKosync = capSlice(listOrNil(s.St.ListKosyncDevices(r.Context(), target.ID)))
	view.Koplugin, view.MoreKoplugin = capSlice(listOrNil(s.St.ListKopluginDevices(r.Context(), target.ID)))
	view.Libraries, view.MoreLibraries = s.userLibraries(r, target.ID)

	prefix := relPrefix(r.URL.Path)
	adminPage(target.Name, prefix, uiCtx(r, u), csrfFor(a), "users",
		adminUserBody(prefix, csrfFor(a), view, flash)).
		Render(r.Context(), w)
}

// listOrNil drops the error from a per-account list: an account whose
// devices cannot be read still has a page worth rendering, and the
// empty table says as much as an error page would.
func listOrNil[T any](v []T, _ error) []T { return v }

func capSlice[T any](v []T) ([]T, int) {
	if len(v) <= adminListCap {
		return v, 0
	}
	return v[:adminListCap], len(v) - adminListCap
}

// handleAdminSetPassword resets another account's password. Web and
// login sessions go with it; tokens and devices deliberately do not.
func (s *Server) handleAdminSetPassword(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.withTarget(w, r, a, u, "reset-password", func(target store.User) (string, error) {
		pw, repeat := r.FormValue("password"), r.FormValue("repeat")
		if err := admin.ValidatePassword(pw, repeat); err != nil {
			return "", err
		}
		if err := admin.SetPassword(r.Context(), s.St, target.ID, pw, ""); err != nil {
			return "", err
		}
		return "Password changed. " + target.Name +
			"'s web and login sessions were revoked; their devices still work.", nil
	})
}

func (s *Server) handleAdminSetRole(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.withTarget(w, r, a, u, "set-admin", func(target store.User) (string, error) {
		grant := r.FormValue("admin") == "on"
		if !grant && target.ID == u.ID {
			return "", errors.New("give up your own administrator rights from another account")
		}
		if _, err := admin.SetAdmin(r.Context(), s.St, target.Name, grant); err != nil {
			return "", err
		}
		if grant {
			return target.Name + " is now an administrator.", nil
		}
		return target.Name + " is no longer an administrator; their admin-scoped tokens were revoked.", nil
	})
}

// handleAdminSetDisabled stops an account or starts it again. It is
// behind the admin's own password: it is how somebody is locked out of
// everything at once, and it is the action an attacker with a stolen
// session would reach for to keep an operator from undoing them.
func (s *Server) handleAdminSetDisabled(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.withTarget(w, r, a, u, "set-disabled", func(target store.User) (string, error) {
		disable := r.FormValue("disabled") == "on"
		if disable && target.ID == u.ID {
			return "", errors.New("disable your own account from another administrator's")
		}
		if _, err := admin.SetDisabled(r.Context(), s.St, target.Name, disable); err != nil {
			return "", err
		}
		if disable {
			return target.Name + " is disabled: every credential is refused and " +
				"their sessions were revoked. Nothing was deleted.", nil
		}
		return target.Name + " is enabled again. Their tokens and devices work; " +
			"their web sessions do not, so they sign in again.", nil
	})
}

func (s *Server) handleAdminRevokeCredentials(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.withTarget(w, r, a, u, "revoke-credentials", func(target store.User) (string, error) {
		if err := admin.RevokeAllCredentials(r.Context(), s.St, target.ID); err != nil {
			return "", err
		}
		return "Every credential for " + target.Name +
			" was revoked: tokens, sessions, kosync slots, koplugin devices and unused pairing codes.", nil
	})
}

// The three single-credential revocations. They are not behind the
// admin's password: removing one device is the reversible, low-blast
// action an operator takes while somebody is on the phone, and the
// account keeps every other way in.
func (s *Server) handleAdminRevokeToken(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.withTargetNoReauth(w, r, a, u, "revoke-token", func(target store.User) (string, error) {
		if err := s.St.RevokeToken(r.Context(), target.ID, r.PathValue("tokenID")); err != nil {
			return "", err
		}
		return "Token revoked.", nil
	})
}

func (s *Server) handleAdminRevokeKosync(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.withTargetNoReauth(w, r, a, u, "revoke-kosync", func(target store.User) (string, error) {
		if err := s.St.RevokeKosyncDevice(r.Context(), target.ID, r.PathValue("slot")); err != nil {
			return "", err
		}
		return "kosync device revoked.", nil
	})
}

func (s *Server) handleAdminRevokeKoplugin(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.withTargetNoReauth(w, r, a, u, "revoke-koplugin", func(target store.User) (string, error) {
		if err := s.St.RevokeKopluginDevice(r.Context(), target.ID, r.PathValue("deviceID")); err != nil {
			return "", err
		}
		return "Statistics device revoked.", nil
	})
}

func (s *Server) withTarget(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
	action string, do func(store.User) (string, error),
) {
	s.runUserMutation(w, r, a, u, action, true, do)
}

func (s *Server) withTargetNoReauth(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
	action string, do func(store.User) (string, error),
) {
	s.runUserMutation(w, r, a, u, action, false, do)
}

func (s *Server) runUserMutation(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
	action string, reverify bool, do func(store.User) (string, error),
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	targetID := r.PathValue("id")
	target, err := s.St.UserByID(r.Context(), targetID)
	if err != nil {
		http.Error(w, "no such user", http.StatusNotFound)
		return
	}
	if reverify {
		if err := s.reauth(r, u); err != nil {
			logAdminAction(r, u, action, targetID, err)
			if errors.Is(err, errRateLimited) {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusTooManyRequests)
			}
			s.renderAdminUser(w, r, a, u, targetID, Flash{Error: err.Error()})
			return
		}
	}
	notice, err := do(target)
	logAdminAction(r, u, action, targetID, err)
	if err != nil {
		s.renderAdminUser(w, r, a, u, targetID, Flash{Error: err.Error()})
		return
	}
	s.renderAdminUser(w, r, a, u, targetID, Flash{Notice: notice})
}

// stamp renders a timestamp for a table cell, or an em dash for a thing
// that has not happened.
func stamp(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04")
}

func moreNote(n int) string {
	if n == 0 {
		return ""
	}
	return "and " + strconv.Itoa(n) + " more not shown"
}
