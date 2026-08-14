package webui

import (
	"errors"
	"net/http"
	"slices"
	"strings"

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
		adminLibrariesBody(prefix, csrfFor(a), views, next, flash)).
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

var errNoSuchUser = errors.New("no account by that name")

// runLibraryMutation is the shape every library POST shares: CSRF,
// then the change, then the list page again carrying the outcome. None
// of these re-verify the admin's password — they change who can reach a
// library, not who can sign in as somebody (ADR-0013).
func (s *Server) runLibraryMutation(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
	action string, do func() (string, error),
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
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
