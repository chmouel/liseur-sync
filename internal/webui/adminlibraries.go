package webui

import (
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/admin"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// The libraries page (ADR-0013 phase 4).
//
// It shows every library on the instance — its kind, owner, root and
// grants — and never a book. Creating a *watched* library names a path
// on the server's filesystem, which is a privilege this page
// deliberately does not hand to a browser; that stays a subcommand and
// the page says so.

// adminLibrariesPerPage is how many libraries one page shows.
const adminLibrariesPerPage = 25

// adminGrantsPerLibrary bounds the grant list rendered under each
// library. Unlike an account's own devices, this list is as long as an
// administrator chose to make it, so it is asked for with a limit
// rather than read whole (ADR-0013).
const adminGrantsPerLibrary = 25

// adminLibraryView is one library as the page shows it.
type adminLibraryView struct {
	Library    store.Library
	OwnerName  string
	Grants     []store.LibraryGrant
	MoreGrants bool
	// Layouts is how this library's filenames are read, and Configured
	// says whether that is a choice or the default.
	Layouts    []metadata.PathPattern
	Configured bool
	// LayoutError carries a configuration document this server cannot
	// parse. That is the reason the library's uploads are not being
	// described the way its owner expects, so it belongs on the page
	// rather than in a log nobody is reading.
	LayoutError string
}

// libraryAxes renders a library's three axes as one line: where its
// books come from, where their bytes live, and how often the source is
// read again. The three are independent, so the page shows all three
// rather than the single word `kind` used to be.
func libraryAxes(l store.Library) string {
	refresh := string(l.Refresh)
	if l.Refresh == store.LibraryRefreshInterval {
		refresh = "every " + humanDuration(l.RefreshInterval)
	}
	return string(l.Source) + " · " + string(l.Storage) + " · " + refresh
}

// RefreshState is the sentence the library card shows about the last
// time this library's source was read. A library with no source has
// nothing to say, and a library that has never been refreshed says so
// rather than showing a blank.
func (v adminLibraryView) RefreshState() string {
	l := v.Library
	if l.RootPath == nil || *l.RootPath == "" {
		return ""
	}
	switch {
	case l.RefreshRequestedAt != nil:
		return "A refresh is queued and starts on the next tick."
	case l.LastRefreshAt != nil:
		return "Last refreshed " + l.LastRefreshAt.Format("2006-01-02 15:04") + "."
	case l.LastRefreshAttemptAt != nil:
		return "Tried " + l.LastRefreshAttemptAt.Format("2006-01-02 15:04") +
			" and has never completed."
	default:
		return "Never refreshed."
	}
}

// RefreshFailure is why the last refresh did not work, in words, or ""
// when it did.
//
// The catalog stores a bounded code and this turns it into a sentence.
// Neither is the underlying error: that names paths, mount points and
// database URLs, which ADR-0013 keeps out of the browser entirely, so it
// goes to the log where an operator can read it next to the code.
func (v adminLibraryView) RefreshFailure() string {
	switch v.Library.LastRefreshCode {
	case store.RefreshCodeNone:
		return ""
	case store.RefreshCodeRootUnavailable:
		return "this library's directory could not be read; the catalog " +
			"was left exactly as it was"
	case store.RefreshCodeNoRootPath:
		return "this library has no directory to read"
	case store.RefreshCodeUnreadableDatabase:
		return "this library's Calibre database could not be read"
	case store.RefreshCodeUnsupportedSchema:
		return "this library's Calibre database is of a version this " +
			"server does not understand"
	case store.RefreshCodeIncompleteScan:
		return "the scan did not finish, so this library is only partly " +
			"accounted for"
	case store.RefreshCodeLeaseLost:
		return "another refresh of this library took over; the next one " +
			"finishes the work"
	default:
		return "the last refresh failed; the server log has the detail"
	}
}

// Refreshable says whether this library has a source to read again.
func (v adminLibraryView) Refreshable() bool {
	return v.Library.RootPath != nil && *v.Library.RootPath != ""
}

// Root reports a root-backed library's root path, or "" for a managed
// one, which has none by construction.
func (v adminLibraryView) Root() string {
	if v.Library.RootPath == nil {
		return ""
	}
	return *v.Library.RootPath
}

func (s *Server) handleAdminLibraries(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.renderAdminLibraries(w, r, a, u, Flash{})
}

func (s *Server) renderAdminLibraries(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash,
) {
	after := r.URL.Query().Get("after")
	// One row more than the page shows: the extra row is the answer to
	// "is there another page".
	libs, err := s.St.AdminListLibraries(r.Context(), after, adminLibrariesPerPage+1)
	if err != nil {
		http.Error(w, "library list unavailable", http.StatusInternalServerError)
		return
	}
	var next string
	if len(libs) > adminLibrariesPerPage {
		libs = libs[:adminLibrariesPerPage]
		next = store.LibraryCursor(libs[len(libs)-1])
	}
	views := make([]adminLibraryView, 0, len(libs))
	names := map[string]string{}
	for _, l := range libs {
		views = append(views, s.libraryView(r, l, names))
	}
	prefix := relPrefix(r.URL.Path)
	adminPage("Libraries", prefix, uiCtx(r, u), csrfFor(a), "libraries",
		adminLibrariesBody(prefix, csrfFor(a), views, next,
			s.Cfg.Content.LibraryRoots, flash)).
		Render(r.Context(), w)
}

// libraryView fills in what the page shows around one library row. The
// owner names are resolved through a per-request cache: one account
// commonly owns several libraries, and the alternative is a join that
// would have to be written twice, once per backend.
func (s *Server) libraryView(r *http.Request, l store.Library, names map[string]string) adminLibraryView {
	v := adminLibraryView{Library: l}
	name, ok := names[l.OwnerUserID]
	if !ok {
		if owner, err := s.St.UserByID(r.Context(), l.OwnerUserID); err == nil {
			name = owner.Name
		}
		names[l.OwnerUserID] = name
	}
	v.OwnerName = name
	grants, err := s.St.AdminLibraryGrants(r.Context(), l.ID, adminGrantsPerLibrary+1)
	if err == nil {
		if len(grants) > adminGrantsPerLibrary {
			grants, v.MoreGrants = grants[:adminGrantsPerLibrary], true
		}
		v.Grants = grants
	}
	layouts, configured, err := admin.LibraryLayouts(l)
	if err != nil {
		v.LayoutError = err.Error()
	}
	v.Layouts, v.Configured = layouts, configured
	return v
}

func (s *Server) handleAdminCreateLibrary(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.runLibraryMutation(w, r, a, u, "create-library", func() (string, error) {
		owner, err := s.St.UserByName(r.Context(), strings.TrimSpace(r.FormValue("owner")))
		if err != nil {
			return "", errNoSuchUser
		}
		lib, err := admin.NewManagedLibrary(
			r.Context(), s.St, owner.ID, r.FormValue("name"))
		if err != nil {
			return "", err
		}
		return "Created " + lib.Name + " for " + owner.Name + ".", nil
	})
}

// handleAdminLibraryAccess grants a role on a library, or takes one
// away. "none" is a role in the form and nil at the store, which is the
// same control doing both jobs rather than two buttons that can
// disagree about which library they are pointed at.
func (s *Server) handleAdminLibraryAccess(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.runLibraryMutation(w, r, a, u, "set-library-access", func() (string, error) {
		target, err := s.St.UserByName(r.Context(), strings.TrimSpace(r.FormValue("user")))
		if err != nil {
			return "", errNoSuchUser
		}
		var role *store.LibraryRole
		switch r.FormValue("role") {
		case "none":
		case "read":
			read := store.LibraryRoleRead
			role = &read
		case "manage":
			manage := store.LibraryRoleManage
			role = &manage
		default:
			return "", errors.New("choose read, manage or no access")
		}
		err = admin.SetLibraryAccessAsAdmin(
			r.Context(), s.St, u.ID, r.PathValue("id"), target.ID, role)
		switch {
		case errors.Is(err, store.ErrNotFound) && role == nil:
			return "", errors.New(target.Name + " had no access to this library")
		case errors.Is(err, store.ErrNotFound):
			return "", admin.ErrGrantToOwner
		case err != nil:
			return "", err
		}
		if role == nil {
			return target.Name + " can no longer reach this library.", nil
		}
		return target.Name + " can now " + string(*role) + " this library.", nil
	})
}

// handleAdminLibraryLayout sets how one library's filenames are read.
func (s *Server) handleAdminLibraryLayout(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.runLibraryMutation(w, r, a, u, "set-library-layout", func() (string, error) {
		lib, err := s.St.AdminLibraryByID(r.Context(), r.PathValue("id"))
		if err != nil {
			return "", err
		}
		var patterns []metadata.PathPattern
		if list := strings.Join(r.Form["layout"], ","); list != "" {
			patterns, err = metadata.ParsePathPatterns(list)
			if err != nil {
				return "", err
			}
		}
		// No layout selected means "back to the default", which is a
		// nil slice — not an empty one, which would mean "read no
		// filename at all".
		if err := admin.SetLibraryLayoutAsAdmin(r.Context(), s.St, u.ID, lib, patterns); err != nil {
			return "", err
		}
		if patterns == nil {
			return lib.Name + " is back to the default filename layouts.", nil
		}
		return lib.Name + " now reads filenames as " +
			metadata.FormatPathPatterns(patterns) + ".", nil
	})
}

// handleAdminRefreshLibrary asks for a refresh of one library now. It
// queues rather than sweeps: the refresh worker holds the claim that
// stops two sweeps of one root, and a request handler that walked a
// disk would hold a browser connection open for as long as the disk
// took. No re-authentication — it reads a directory the administrator
// already configured, and changes no credential (ADR-0013).
func (s *Server) handleAdminRefreshLibrary(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.runLibraryMutation(w, r, a, u, "refresh-library", func() (string, error) {
		lib, err := s.St.AdminLibraryByID(r.Context(), r.PathValue("id"))
		if err != nil {
			return "", err
		}
		if lib.RootPath == nil || *lib.RootPath == "" {
			return "", errors.New(
				"a managed library has no source to refresh from")
		}
		if err := s.St.AdminRequestLibraryRefresh(
			r.Context(), u.ID, lib.ID, time.Now().UTC()); err != nil {
			return "", err
		}
		return "Queued a refresh of " + lib.Name +
			"; the server picks it up on its next tick.", nil
	})
}

// handleAdminCreateRootLibrary creates a library over a directory that
// already exists on this server — a plain tree or a Calibre library —
// with all three of ADR-0014's axes on the form.
//
// ADR-0013 kept this a subcommand because naming a server path from a
// browser is a filesystem-existence oracle and a way to make the
// scanner ingest any readable tree on the host. That reasoning is
// unchanged; what changed is that an operator who has to reach a shell
// to attach the Calibre library the whole instance exists to serve is
// an operator who runs the shell as root anyway. So the privilege is
// offered with the guards the reasoning asks for:
//
//   - the acting administrator types their own password, rate-limited
//     per account and per address, so a stolen session cannot probe;
//   - `content.library_roots`, when set, is the only place a root may
//     be; and
//   - every attempt, refused or not, is one audit line.
func (s *Server) handleAdminCreateRootLibrary(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	check := r.FormValue("action") == "check"
	action := "add-library"
	if check {
		action = "check-library-root"
	}
	s.runLibraryMutationReauth(w, r, a, u, action, func() (string, error) {
		opts, err := rootLibraryOptions(r)
		if err != nil {
			return "", err
		}
		root, err := admin.ResolveLibraryRoot(r.FormValue("root"))
		if err != nil {
			return "", errUnusableRoot
		}
		if !s.rootAllowed(root) {
			return "", errRootNotAllowed
		}
		if err := admin.CheckLibraryRoot(root, opts.Source); err != nil {
			return "", err
		}
		if check {
			return root + " is readable and can be used as a " +
				string(opts.Source) + " library.", nil
		}
		owner, err := s.St.UserByName(r.Context(), strings.TrimSpace(r.FormValue("owner")))
		if err != nil {
			return "", errNoSuchUser
		}
		lib, err := admin.NewRootLibrary(
			r.Context(), s.St, owner.ID, r.FormValue("name"), root, opts)
		if err != nil {
			return "", err
		}
		return "Created the " + string(lib.Source) + " library " + lib.Name +
			" for " + owner.Name + " over " + *lib.RootPath +
			". The server reads that directory and never writes below it.", nil
	})
}

// rootLibraryOptions reads the three axes off the form. A blank axis is
// left blank rather than defaulted here, so that the defaults are the
// subcommand's defaults — one implementation, in the admin package.
func rootLibraryOptions(r *http.Request) (admin.RootLibraryOptions, error) {
	opts := admin.RootLibraryOptions{
		Source:  store.LibrarySource(r.FormValue("source")),
		Storage: store.LibraryStorage(strings.ReplaceAll(r.FormValue("storage"), "-", "_")),
		Refresh: store.LibraryRefresh(r.FormValue("refresh")),
	}
	if value := strings.TrimSpace(r.FormValue("interval")); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil {
			return opts, errors.New(
				"a refresh interval reads like 15m, 2h or 24h")
		}
		opts.Interval = d
	}
	// Normalized here rather than at the store call, so that the page
	// can say which kind of library it just checked a directory for.
	// The defaults are the subcommand's, filled in by the same code.
	err := opts.Normalize()
	return opts, err
}

// The two refusals a root can meet. Neither repeats what the server
// found on disk: the administrator typed the path, so echoing it back
// tells them nothing they did not know, but the reason a stat failed
// can describe a tree they were guessing at.
var (
	errUnusableRoot = errors.New(
		"that path is not a directory this server can read")
	errRootNotAllowed = errors.New(
		"that path is not under any directory this instance allows " +
			"libraries in (content.library_roots)")
)

// rootAllowed applies the configured allowlist. Empty means anywhere,
// which is what the subcommand has always allowed.
func (s *Server) rootAllowed(root string) bool {
	allowed := s.Cfg.Content.LibraryRoots
	if len(allowed) == 0 {
		return true
	}
	for _, prefix := range allowed {
		prefix = filepath.Clean(prefix)
		if root == prefix {
			return true
		}
		if strings.HasPrefix(root, prefix+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// adminReviewLimit bounds one library's review listing on the panel. A
// queue longer than this is a sign something is wrong with the root
// rather than with the queue.
const adminReviewLimit = 200

// adminReviewRow is one book awaiting a decision, as the panel shows
// it: an id, why it is here, and when it happened.
//
// No title, no author, no path. The queue belongs to somebody else's
// library and ADR-0013 keeps their books off these pages; the library's
// own manager sees the titles under Manage. What an administrator needs
// here is whether the queue is draining and the ability to say "the
// copy being served is fine".
type adminReviewRow struct {
	BookID  string
	Reason  string
	Updated string
}

type adminReviewView struct {
	Library store.Library
	Rows    []adminReviewRow
	Capped  bool
}

func (s *Server) handleAdminLibraryReview(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.renderAdminLibraryReview(w, r, a, u, Flash{})
}

func (s *Server) renderAdminLibraryReview(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash,
) {
	lib, err := s.St.AdminLibraryByID(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "no such library", http.StatusNotFound)
		return
	}
	view := adminReviewView{Library: lib}
	// Read as the owner rather than as the administrator: the owner
	// always manages their own library, so this needs no store method
	// that crosses users to answer a question about one library.
	books, err := s.St.ListBooksInReview(
		r.Context(), lib.OwnerUserID, lib.ID, adminReviewLimit)
	if err != nil {
		flash.Error = "This library's review queue could not be read."
	}
	for _, b := range books {
		view.Rows = append(view.Rows, adminReviewRow{
			BookID:  b.ID,
			Reason:  b.ReviewReason,
			Updated: b.UpdatedAt.UTC().Format("2006-01-02 15:04"),
		})
	}
	view.Capped = len(books) == adminReviewLimit
	prefix := relPrefix(r.URL.Path)
	adminPage("Review", prefix, uiCtx(r, u), csrfFor(a), "libraries",
		adminLibraryReviewBody(prefix, csrfFor(a), view, flash)).
		Render(r.Context(), w)
}

// handleAdminClearReview accepts the copy the catalog is serving for
// one book. It changes no credential and reveals nothing about the
// book, so it carries no password re-verification (ADR-0013).
func (s *Server) handleAdminClearReview(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	bookID := r.PathValue("bookID")
	changed, err := admin.ClearBookReview(
		r.Context(), s.St, r.PathValue("id"), bookID)
	logAdminAction(r, u, "clear-review", bookID, err)
	switch {
	case err != nil:
		s.renderAdminLibraryReview(w, r, a, u, Flash{Error: err.Error()})
	case !changed:
		s.renderAdminLibraryReview(w, r, a, u, Flash{
			Error: "That book was not awaiting review."})
	default:
		s.renderAdminLibraryReview(w, r, a, u, Flash{
			Notice: "Cleared. The book returns to the catalog on the next " +
				"availability pass if it still has a servable file."})
	}
}

var errNoSuchUser = errors.New("no account by that name")

// runLibraryMutation is the shape every library POST shares: CSRF,
// then the change, then the list page again carrying the outcome. None
// of these re-verify the admin's password — they change who can reach a
// library, not who can sign in as somebody (ADR-0013).
func (s *Server) runLibraryMutation(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
	action string, do func() (string, error),
) {
	s.libraryMutation(w, r, a, u, action, false, do)
}

// runLibraryMutationReauth is the same shape for the one library action
// that is a privilege rather than a permission: naming a directory on
// this machine. It costs the acting administrator their own password,
// on the same two budgets the account actions use.
func (s *Server) runLibraryMutationReauth(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
	action string, do func() (string, error),
) {
	s.libraryMutation(w, r, a, u, action, true, do)
}

func (s *Server) libraryMutation(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
	action string, reverify bool, do func() (string, error),
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if reverify {
		if err := s.reauth(r, u); err != nil {
			logAdminAction(r, u, action, r.PathValue("id"), err)
			if errors.Is(err, errRateLimited) {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusTooManyRequests)
			}
			s.renderAdminLibraries(w, r, a, u, Flash{Error: err.Error()})
			return
		}
	}
	notice, err := do()
	logAdminAction(r, u, action, r.PathValue("id"), err)
	if err != nil {
		s.renderAdminLibraries(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	s.renderAdminLibraries(w, r, a, u, Flash{Notice: notice})
}

// hasLayout reports whether a pattern is in the effective list, for the
// checkbox that shows what is set now.
func hasLayout(list []metadata.PathPattern, p metadata.PathPattern) bool {
	return slices.Contains(list, p)
}

// adminUserLibrary is one library an account can reach, as the per-user
// page shows it.
type adminUserLibrary struct {
	Library store.Library
	Role    store.LibraryRole
	// Owner distinguishes "owns it" from "was granted manage on it".
	// Both read as manage everywhere else, and only one of them can be
	// taken away.
	Owner bool
}

// userLibraries is what the per-user page shows under Libraries: one
// page of them, and whether there are more.
func (s *Server) userLibraries(r *http.Request, userID string) ([]adminUserLibrary, bool) {
	rows, err := s.St.AdminUserLibraries(
		r.Context(), userID, "", adminLibrariesPerPage+1)
	if err != nil {
		return nil, false
	}
	more := len(rows) > adminLibrariesPerPage
	if more {
		rows = rows[:adminLibrariesPerPage]
	}
	out := make([]adminUserLibrary, 0, len(rows))
	for _, row := range rows {
		out = append(out, adminUserLibrary{
			Library: row.Library,
			Role:    row.Role,
			Owner:   row.Library.OwnerUserID == userID,
		})
	}
	return out, more
}
