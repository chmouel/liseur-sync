package webui

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
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

// adminUserFoldersMax bounds what the per-user grant form will render.
// The form submits the complete grant set, so a truncated list would
// revoke every folder past the cut the moment somebody saved it. Past
// this many folders the page declines to offer the form at all and the
// admin CLI does the assigning.
const adminUserFoldersMax = 500

// errTooManyFolders is what the grant form answers once the instance
// watches more folders than one page may safely replace.
var errTooManyFolders = errors.New(
	"this instance watches too many folders for the web form to replace the " +
		"whole grant set safely: use the admin CLI (assign-folder, " +
		"unassign-folder) instead",
)

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
	// Self marks the acting admin's own account: demoting or disabling
	// yourself is not offered, since the last-admin guard would be the
	// only thing standing between an operator and a locked instance.
	Self bool
	// Browsers counts the browsers this account has read in, which is
	// not the same as the number of reader tokens it has held.
	Browsers int
	// Base is this server's absolute origin as the browser reached it,
	// for the addresses an operator has to read out to whoever owns the
	// device being paired.
	Base string
	// Folders is every watched folder, not one page. The checkbox form
	// replaces the complete grant set, so rendering a partial list would
	// revoke everything on a later page when it was submitted.
	Folders []adminUserFolderView
	// FoldersUnmanageable says the instance watches more folders than
	// adminUserFoldersMax, so Folders is empty on purpose and the page
	// offers no form rather than a lossy one.
	FoldersUnmanageable bool
}

type adminUserFolderView struct {
	Folder  store.Folder
	Granted bool
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
	userAllowed, ipAllowed := true, true
	if s.AdminReauthUserLimiter != nil {
		userAllowed = s.AdminReauthUserLimiter.Allow(actor.ID)
	}
	if s.AdminReauthIPLimiter != nil {
		ipAllowed = s.AdminReauthIPLimiter.Allow(auth.ClientIP(r, s.Cfg))
	}
	if !userAllowed || !ipAllowed {
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

func (s *Server) renderAdminUsers(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash,
) {
	s.renderSettings(w, r, a, u, settingsAdmin, settingsAdminUsers, "", flash, false, "", false)
}

func (s *Server) handleAdminCreateUser(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := r.FormValue("name")
	if err := s.reauth(r, u); err != nil {
		logAdminAction(r, u, "create-user", name, err)
		if errors.Is(err, errRateLimited) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
		}
		s.renderAdminUsers(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	pw, repeat := r.FormValue("password"), r.FormValue("repeat")
	if err := admin.ValidatePassword(pw, repeat); err != nil {
		logAdminAction(r, u, "create-user", name, err)
		s.renderAdminUsers(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	created, err := admin.CreateUser(r.Context(), s.St, name, pw)
	logAdminAction(r, u, "create-user", name, err)
	if err != nil {
		if isUserError(err) {
			s.renderAdminUsers(w, r, a, u, Flash{Error: err.Error()})
			return
		}
		slog.ErrorContext(r.Context(), "create account failed", "error", err)
		s.renderAdminUsers(w, r, a, u, Flash{Error: "Could not create account."})
		return
	}
	s.renderAdminUsers(w, r, a, u, Flash{
		Notice: "Created " + created.Name + ".",
	})
}

func (s *Server) renderAdminUser(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
	targetID string, flash Flash,
) {
	s.renderSettings(w, r, a, u, settingsAdmin, settingsAdminUser, targetID, flash, false, "", false)
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

func (s *Server) handleAdminSetFolders(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.withTargetNoReauth(w, r, a, u, "set-folder-access", func(target store.User) (string, error) {
		if _, tooMany, err := allFolders(r.Context(), s.St, "", adminUserFoldersMax); err != nil {
			return "", err
		} else if tooMany {
			return "", errTooManyFolders
		}
		// PostForm, not Form: Form merges the URL query into the body, so
		// reading it would let a crafted link decide which folders a saved
		// form grants.
		if err := s.St.ReplaceUserFolders(r.Context(), target.ID, r.PostForm["folders"]); err != nil {
			return "", err
		}
		return "Folder access updated for " + target.Name + ".", nil
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

// The credential-minting actions. Each one hands out a working way into
// somebody else's account, so each is behind the acting administrator's
// own password — the same gate a password reset carries, for the same
// reason. The secret is shown exactly once, on the page that made it,
// and stored hashed.

// handleAdminMintToken creates an API token for another account, with
// the scopes the form asked for. It is the panel's `mint-token`: the
// operator who has just enrolled somebody's e-reader for them should
// not have to reach a shell to produce the token it needs.
func (s *Server) handleAdminMintToken(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.withTargetSecret(w, r, a, u, "mint-token",
		func(target store.User) (string, string, error) {
			scopes, err := formScopes(r)
			if err != nil {
				return "", "", err
			}
			name := strings.TrimSpace(r.FormValue("name"))
			if name == "" {
				return "", "", errors.New("a token needs a name")
			}
			secret, tok, err := s.Auth.MintToken(r.Context(), target.ID, name, scopes, nil)
			if err != nil {
				if errors.Is(err, store.ErrAdminGrantRequiresAdmin) ||
					errors.Is(err, auth.ErrAdminGrantRequiresAdmin) {
					return "", "", errors.New(
						"the admin scope belongs to an admin account: make " +
							target.Name + " an administrator first")
				}
				return "", "", err
			}
			return secret, "Token for " + target.Name + " (" +
				tok.Scopes.String() + "), shown once", nil
		})
}

// handleAdminPairingCode mints a kosync pairing code for another
// account: 128 bits, hashed at rest, single-use, short-lived.
func (s *Server) handleAdminPairingCode(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.withTargetSecret(w, r, a, u, "pairing-code",
		func(target store.User) (string, string, error) {
			code, err := auth.NewSecret()
			if err != nil {
				return "", "", err
			}
			code = code[:32]
			id, err := auth.NewSecret()
			if err != nil {
				return "", "", err
			}
			ttl := time.Duration(s.Cfg.PairingCodeTTLMin) * time.Minute
			if ttl <= 0 {
				ttl = 15 * time.Minute
			}
			if err := s.St.CreatePairingCode(r.Context(), store.PairingCode{
				ID: id, UserID: target.ID, CodeSHA256: auth.HashSecret(code),
				ExpiresAt: time.Now().Add(ttl),
			}); err != nil {
				return "", "", err
			}
			return code, "kosync pairing code for " + target.Name +
				" (" + humanDuration(ttl) + ", single use)", nil
		})
}

// handleAdminCreateKoplugin mints a statistics-plugin capability for
// another account. The capability is the whole credential, so it is
// shown once and stored hashed.
func (s *Server) handleAdminCreateKoplugin(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.withTargetSecret(w, r, a, u, "koplugin-device",
		func(target store.User) (string, string, error) {
			label := strings.TrimSpace(r.FormValue("name"))
			if label == "" {
				return "", "", errors.New("a statistics device needs a name")
			}
			capability, err := auth.NewSecret()
			if err != nil {
				return "", "", err
			}
			id, err := auth.NewSecret()
			if err != nil {
				return "", "", err
			}
			if err := s.St.CreateKopluginDevice(r.Context(), store.KopluginDevice{
				ID: id, UserID: target.ID, TokenSHA256: auth.HashSecret(capability),
				Label: label, DeviceID: "koplugin:" + label,
				CreatedAt: time.Now().UTC(),
			}); err != nil {
				return "", "", err
			}
			return capability, "Capability for " + target.Name +
				": put it in the plugin's server URL, /adapter/koplugin/<capability>", nil
		})
}

// handleAdminBackfillWorks maps every catalog book this account can
// read to a sync work, so that statistics do not wait for each book to
// be opened. It reads and writes the account's own catalog and hands
// out nothing, so it carries no password re-verification; the report is
// counts, which is all the CLI prints too.
func (s *Server) handleAdminBackfillWorks(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.withTargetNoReauth(w, r, a, u, "backfill-works", func(target store.User) (string, error) {
		report, err := admin.BackfillWorks(r.Context(), s.St, target.ID)
		// The report is reported even on failure: a run that stops
		// halfway has still committed everything it counted.
		notice := "Mapped " + target.Name + "'s books to works: " +
			strconv.Itoa(report.Books) + " looked at, " +
			strconv.Itoa(report.Created) + " new, " +
			strconv.Itoa(report.Linked) + " linked, " +
			strconv.Itoa(report.Fuzzy) + " needing confirmation, " +
			strconv.Itoa(report.Conflicted) + " conflicted, " +
			strconv.Itoa(report.Skipped) + " skipped."
		if err != nil {
			return "", errors.New(notice + " It stopped early: " + err.Error())
		}
		return notice, nil
	})
}

// withTargetSecret is withTarget for the actions whose result is a
// secret: the value is put in the flash rather than in the notice, so
// that the one template that knows how to show a secret once is the one
// that shows it.
func (s *Server) withTargetSecret(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
	action string, do func(store.User) (secret string, label string, err error),
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
	if err := s.reauth(r, u); err != nil {
		logAdminAction(r, u, action, targetID, err)
		if errors.Is(err, errRateLimited) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
		}
		s.renderAdminUser(w, r, a, u, targetID, Flash{Error: err.Error()})
		return
	}
	secret, label, err := do(target)
	logAdminAction(r, u, action, targetID, err)
	if err != nil {
		s.renderAdminUser(w, r, a, u, targetID, Flash{Error: err.Error()})
		return
	}
	s.renderAdminUser(w, r, a, u, targetID, Flash{
		Secret: secret, SecretLabel: label,
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

// browserNote says how many browsers the account reads in. It is a
// sentence rather than a table because there is nothing to do to a
// browser from here: the credential behind it is replaced hourly, and
// cutting an account off is what Disable is for.
func browserNote(n int) string {
	switch n {
	case 0:
		return "Has not read in a browser."
	case 1:
		return "Reads in 1 browser."
	default:
		return "Reads in " + strconv.Itoa(n) + " browsers."
	}
}

func moreNote(n int) string {
	if n == 0 {
		return ""
	}
	return "and " + strconv.Itoa(n) + " more not shown"
}
