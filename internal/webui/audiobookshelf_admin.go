package webui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/audiobookshelf"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// The Audiobookshelf page edits the one [audiobookshelf] section
// (ADR-0055, ADR-0062). Like the mirror page it reads the file on every
// request, so a setting saved and left disabled is still there to edit
// while the running worker keeps what it booted with until a restart.

// audiobookshelfTestTimeout bounds the connection test a save makes. It
// is a check, not a sync pass: one /api/me round trip.
const audiobookshelfTestTimeout = 10 * time.Second

// audiobookshelfAdminView is the page: what the saved section says, the
// status this process reports, and who owns the credential. It carries
// no configuration value: the saved key never leaves the handler, and
// the address is here only as the sanitised text the page may show.
type audiobookshelfAdminView struct {
	// SavedURL is the saved address as the page may show it: the
	// credential, query and fragment a file can carry are stripped, and
	// an address that does not parse is not echoed at all.
	SavedURL string
	// SavedEnabled and SavedAccount are the saved settings the page
	// shows.
	SavedEnabled bool
	SavedAccount string
	Status       audiobookshelf.Status
	// Accounts is the local account list the form offers.
	Accounts []string
	// HasKey says the file already holds a key, so the form can offer to
	// keep it. The key itself never reaches the page.
	HasKey bool
	// CredentialSource names where the effective key comes from: the
	// environment, the file, or nowhere yet.
	CredentialSource string
	// CredentialFromEnv says LISEUR_ABS_API_KEY owns the effective key.
	CredentialFromEnv bool
	// EnvManaged names the non-secret LISEUR_ABS_* variables the
	// environment sets. Any one of them makes the section read-only,
	// because the next start would override what a form saved.
	EnvManaged []string
	// NoConfigPath is a server started without a writable file, which
	// can show the settings and change none of them.
	NoConfigPath bool
	// FileError is why the file could not be read; the view then falls
	// back to the running configuration and hides the form.
	FileError string
	// RestartRequired says the file and the running process disagree.
	RestartRequired bool
}

// Editable reports whether the form can be offered at all.
func (v audiobookshelfAdminView) Editable() bool {
	return !v.NoConfigPath && v.FileError == "" && len(v.EnvManaged) == 0
}

func (v audiobookshelfAdminView) KeyPlaceholder() string {
	if v.HasKey {
		return "saved; leave blank to keep it"
	}
	return "required when enabled"
}

// audiobookshelfEnvManagedList spells the variables that own the section
// the way the page says them.
func audiobookshelfEnvManagedList(names []string) string {
	return strings.Join(names, ", ")
}

// audiobookshelfView builds the page from the file and the running
// configuration.
func (s *Server) audiobookshelfView(users []store.User) audiobookshelfAdminView {
	v := audiobookshelfAdminView{
		// The runner reports what this process is doing; a server
		// started without one shows what its configuration says.
		Status: audiobookshelf.Status{Enabled: s.Cfg.Audiobookshelf.Enabled},
	}
	if s.AudiobookRunner != nil {
		v.Status = s.AudiobookRunner.Status()
		v.Status.Enabled = s.Cfg.Audiobookshelf.Enabled
	}
	saved, err := config.ReadAudiobookshelf(s.ConfigPath)
	if err != nil {
		v.FileError = err.Error()
		saved = s.Cfg.Audiobookshelf
	}
	v.SavedEnabled, v.SavedAccount = saved.Enabled, saved.Account
	v.SavedURL = audiobookshelfCleanURL(saved.BaseURL)
	v.NoConfigPath = strings.TrimSpace(s.ConfigPath) == ""
	v.HasKey = saved.APIKey != ""
	v.CredentialSource = audiobookshelfCredentialSource(saved.APIKey)
	_, v.CredentialFromEnv = os.LookupEnv("LISEUR_ABS_API_KEY")
	v.EnvManaged = config.AudiobookshelfEnvironmentManaged()
	v.RestartRequired = audiobookshelfRestartRequired(saved, s.Cfg.Audiobookshelf)
	v.Accounts = make([]string, 0, len(users)+1)
	for _, u := range users {
		v.Accounts = append(v.Accounts, u.Name)
	}
	if saved.Account != "" && !containsString(v.Accounts, saved.Account) {
		v.Accounts = append(v.Accounts, saved.Account)
	}
	return v
}

// audiobookshelfCredentialSource says where the effective key lives. The
// file is written by this page; an environment variable is not, and a
// variable set but empty refuses an enabled integration rather than
// falling back to a stale key.
func audiobookshelfCredentialSource(fileKey string) string {
	if value, ok := os.LookupEnv("LISEUR_ABS_API_KEY"); ok {
		if value == "" {
			return "LISEUR_ABS_API_KEY (set but empty: this integration will not start)"
		}
		return "environment: LISEUR_ABS_API_KEY"
	}
	if fileKey != "" {
		return "the configuration file"
	}
	return "missing"
}

// audiobookshelfRestartRequired compares the configuration a restart
// would use — the file with the environment overlaid and normalised the
// way validation leaves it — with the one this process loaded. The
// running side is compared as it is: startup already normalised it, and
// normalising it here again would trim a second trailing slash and hide
// a difference a restart would really apply. An environment-managed
// setting that a restart would not change is therefore not reported, and
// a file change to any setting the worker reads, including polling and
// timeout, is.
func audiobookshelfRestartRequired(saved, runtime config.AudiobookshelfConfig) bool {
	return config.EffectiveAudiobookshelf(saved) != runtime
}

// audiobookshelfCleanURL is the address as the page shows it: a scheme,
// a host and a path, never the credential, query or fragment somebody
// pasted into a file. Trailing slashes are left alone because the form
// posts this string back: rewriting them here would change the address
// startup derives the peer identity from on a save that changed nothing.
// The validator refuses those parts, but the page can read a file this
// process never started with. An address that cannot be parsed is shown
// as nothing rather than echoed.
func audiobookshelfCleanURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	u.User = nil
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

// audiobookshelfFromForm is the entry the form describes. A field the
// form does not carry keeps what the file held, and a blank key field
// keeps the saved key: this is an edit, not a replacement.
func audiobookshelfFromForm(r *http.Request, previous config.AudiobookshelfConfig) config.AudiobookshelfConfig {
	a := previous
	a.Enabled = r.FormValue("enabled") == "on"
	a.BaseURL = strings.TrimSpace(r.FormValue("base_url"))
	a.Account = strings.TrimSpace(r.FormValue("account"))
	if key := strings.TrimSpace(r.FormValue("api_key")); key != "" {
		a.APIKey = key
	}
	return a
}

// saveAudiobookshelf writes the section, tests it when it ends up
// enabled, and returns what to tell the administrator.
//
// The test costs a request to somebody else's server, so it runs only
// for an enabled integration: saving it disabled must work with the peer
// switched off and no key at all.
func (s *Server) saveAudiobookshelf(r *http.Request) (string, error) {
	var tested bool
	err := config.UpdateAudiobookshelf(s.ConfigPath, func(a *config.AudiobookshelfConfig) error {
		next := audiobookshelfFromForm(r, *a)
		candidate, err := config.AudiobookshelfCandidate(s.ConfigPath, next)
		if err != nil {
			return err
		}
		// The address is written as the operator typed it: startup is
		// what normalises it. Writing the candidate's normalised value
		// instead would make each save trim the address again and move
		// the peer identity the stored history is keyed by. Only the
		// fields the form owns are written; the credential is not among
		// them, since an environment key is the effective one and copying
		// it here would put a secret on disk.
		next.Enabled = candidate.Enabled
		*a = next
		if !candidate.Enabled {
			return nil
		}
		if _, err := s.St.UserByName(r.Context(), candidate.Account); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("there is no local account named %q", candidate.Account)
			}
			return err
		}
		ctx, cancel := context.WithTimeout(r.Context(), audiobookshelfTestTimeout)
		defer cancel()
		if _, err := audiobookshelf.NewClient(candidate).Me(ctx); err != nil {
			return audiobookshelfConnectionError(err, candidate.APIKey)
		}
		tested = true
		return nil
	})
	if err != nil {
		return "", err
	}
	if tested {
		return "Audiobookshelf saved and connection tested. Restart liseur-sync to apply it.", nil
	}
	return "Audiobookshelf saved and disabled. Restart liseur-sync to stop or apply it.", nil
}

// audiobookshelfConnectionError keeps the key out of a refusal. The
// client already scrubs the server's own text; this covers anything else
// a transport error might carry.
func audiobookshelfConnectionError(err error, key string) error {
	msg := err.Error()
	if key != "" {
		msg = strings.ReplaceAll(msg, key, "[redacted]")
	}
	return fmt.Errorf("Audiobookshelf refused the connection: %s", msg)
}

// handleSaveAudiobookshelf saves the one section. The gate is the same
// as every other cross-user mutation: per-session CSRF token, the acting
// administrator's own password against both reauth budgets, and one
// audit line. It never writes a credential the environment owns, and
// never renders one.
func (s *Server) handleSaveAudiobookshelf(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		logAdminAction(r, u, "audiobookshelf.save", "audiobookshelf", errForgedCSRF)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if managed := config.AudiobookshelfEnvironmentManaged(); len(managed) > 0 {
		err := fmt.Errorf("%s comes from the environment; change the variable and restart",
			audiobookshelfEnvManagedList(managed))
		logAdminAction(r, u, "audiobookshelf.save", "audiobookshelf", err)
		s.renderAudiobookshelf(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	if err := s.reauth(r, u); err != nil {
		logAdminAction(r, u, "audiobookshelf.save", "audiobookshelf", err)
		s.audiobookshelfRefusal(w, r, a, u, err)
		return
	}
	notice, err := s.saveAudiobookshelf(r)
	logAdminAction(r, u, "audiobookshelf.save", "audiobookshelf", err)
	if err != nil {
		s.renderAudiobookshelf(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	s.renderAudiobookshelf(w, r, a, u, Flash{Notice: notice})
}

// audiobookshelfRefusal answers a failed reauth in place rather than by
// redirect: a redirect would erase the status the browser has to see,
// which is the 429 the reauth budget answers with.
func (s *Server) audiobookshelfRefusal(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, err error,
) {
	if errors.Is(err, errRateLimited) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		s.renderSettings(w, r, a, u, settingsAdmin, settingsAdminAudiobookshelf, "",
			Flash{Error: err.Error()}, false, "", false)
		return
	}
	s.renderAudiobookshelf(w, r, a, u, Flash{Error: err.Error()})
}

// renderAudiobookshelf lands the browser back on the page, carrying the
// flash in the query string.
func (s *Server) renderAudiobookshelf(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash,
) {
	if settingsRedirect(w, r, settingsAdmin, settingsAdminAudiobookshelf, "", flash) {
		return
	}
	s.renderSettings(w, r, a, u, settingsAdmin, settingsAdminAudiobookshelf, "", flash, false, "", false)
}
