package webui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/chmouel/liseur-sync/internal/buildinfo"
	"github.com/chmouel/liseur-sync/internal/store"
)

const (
	settingsProfile = "profile"
	settingsDevices = "devices"
	settingsAdmin   = "admin"

	settingsAdminOverview    = "overview"
	settingsAdminUsers       = "users"
	settingsAdminUser        = "user"
	settingsAdminFolders     = "folders"
	settingsAdminMaintenance = "maintenance"
)

type settingsDevicesView struct {
	Tokens   []store.Token
	Browsers []browserSession
	Kosync   []store.KosyncDevice
	Koplugin []store.KopluginDevice
	Base     string
}

type settingsAdminView struct {
	View string

	Counts store.AdminCounts
	Build  buildinfo.Info
	Config configFacts

	Folders     []adminFolderView
	FoldersNext string
	Roots       []string

	Users     []store.User
	UsersNext string
	Invites   []store.Invite

	User    adminUserView
	HasUser bool

	Maintenance maintenanceView
}

type settingsView struct {
	Section string
	Saved   bool
	Zones   []string

	PasswordMessage string
	PasswordError   bool

	Devices settingsDevicesView
	Admin   settingsAdminView
	Flash   Flash
}

func settingsHref(prefix, section string) string {
	if section == "" || section == settingsProfile {
		return prefix + "settings"
	}
	return prefix + "settings?section=" + url.QueryEscape(section)
}

func settingsAdminHref(prefix, view string) string {
	href := prefix + "settings?section=" + settingsAdmin
	if view != "" && view != settingsAdminOverview {
		href += "&view=" + url.QueryEscape(view)
	}
	return href
}

func settingsAdminUserHref(prefix, userID string) string {
	return settingsAdminHref(prefix, settingsAdminUser) +
		"&user=" + url.QueryEscape(userID)
}

func settingsBack(section, view, userID string) string {
	if section != settingsAdmin {
		return settingsHref("", section)
	}
	if view == settingsAdminUser && userID != "" {
		return settingsAdminUserHref("", userID)
	}
	return settingsAdminHref("", view)
}

// settingsTarget is the settings page a mutation belongs to, relative to
// the URL the browser posted to.
func settingsTarget(r *http.Request, section, view, userID string) string {
	prefix := relPrefix(r.URL.Path)
	if section != settingsAdmin {
		return settingsHref(prefix, section)
	}
	if view == settingsAdminUser && userID != "" {
		return settingsAdminUserHref(prefix, userID)
	}
	return settingsAdminHref(prefix, view)
}

// settingsRedirect finishes a settings mutation with Post/Redirect/Get,
// and says whether it did.
//
// Rendering the page under the URL a form posted to leaves the browser
// somewhere the page was never meant to live: /ui/admin/folders/{id}/uploads
// is three segments deeper than /ui/settings, so every relative link on
// it — the stylesheet first of all — resolves against the wrong
// directory, and a refresh is a GET that route does not answer. The
// notice travels in the query string instead, which is also what makes
// the reload harmless.
//
// A flash carrying a one-time secret is the exception: a token or a
// pairing code goes in a page, never in a URL that lands in history, a
// proxy log and the next request's Referer. Those render in place.
func settingsRedirect(
	w http.ResponseWriter, r *http.Request,
	section, view, userID string, flash Flash,
) bool {
	if r.Method != http.MethodPost || flash.Secret != "" {
		return false
	}
	target := settingsTarget(r, section, view, userID)
	if q := flashQuery(flash); q != "" {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + q
	}
	redirectRel(w, target, http.StatusSeeOther)
	return true
}

// flashQuery encodes what a redirect has to carry across. It uses the
// same notice/problem pair the library and reader pages already use.
func flashQuery(flash Flash) string {
	switch {
	case flash.Error != "":
		return "problem=" + url.QueryEscape(flash.Error)
	case flash.Notice != "":
		return "notice=" + url.QueryEscape(flash.Notice)
	}
	return ""
}

// flashFromQuery reads back what settingsRedirect sent.
func flashFromQuery(r *http.Request) Flash {
	return Flash{
		Notice: r.URL.Query().Get("notice"),
		Error:  r.URL.Query().Get("problem"),
	}
}

func settingsSelection(r *http.Request) (section, view, userID string) {
	section = r.URL.Query().Get("section")
	switch section {
	case settingsDevices:
	case settingsAdmin:
		view = r.URL.Query().Get("view")
		switch view {
		case settingsAdminUsers, settingsAdminFolders, settingsAdminMaintenance:
		case settingsAdminUser:
			userID = r.URL.Query().Get("user")
			if userID == "" {
				view = settingsAdminUsers
			}
		default:
			view = settingsAdminOverview
		}
	default:
		section = settingsProfile
	}
	return section, view, userID
}

func settingsContext(r *http.Request, u *store.User, section, view, userID string) userCtx {
	ctx := uiCtx(r, u)
	ctx.Section = settingsSection
	ctx.Back = settingsBack(section, view, userID)
	return ctx
}

const settingsSection = "settings"

func (s *Server) renderSettings(
	w http.ResponseWriter,
	r *http.Request,
	a store.AuthSession,
	u *store.User,
	section string,
	adminView string,
	targetID string,
	flash Flash,
	saved bool,
	passwordMessage string,
	passwordError bool,
) {
	if section == "" {
		section = settingsProfile
	}
	if section == settingsAdmin && !isAdmin(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		forbiddenPage(relPrefix(r.URL.Path),
			settingsContext(r, u, section, adminView, targetID), csrfFor(a)).
			Render(r.Context(), w)
		return
	}

	view := settingsView{
		Section:         section,
		Saved:           saved,
		Zones:           commonZones,
		PasswordMessage: passwordMessage,
		PasswordError:   passwordError,
		Flash:           flash,
	}

	switch section {
	case settingsDevices:
		view.Devices = s.settingsDevices(r, u)
	case settingsAdmin:
		if adminView == "" {
			adminView = settingsAdminOverview
		}
		view.Admin.View = adminView
		if err := s.settingsAdmin(r, u, &view.Admin, adminView, targetID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "no such user", http.StatusNotFound)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	default:
		view.Section = settingsProfile
	}

	settingsPage(
		relPrefix(r.URL.Path),
		settingsContext(r, u, view.Section, view.Admin.View, targetID),
		csrfFor(a),
		view,
	).Render(r.Context(), w)
}

func (s *Server) settingsDevices(r *http.Request, u *store.User) settingsDevicesView {
	toks, _ := s.St.ListTokens(r.Context(), u.ID)
	kosyncDevs, _ := s.St.ListKosyncDevices(r.Context(), u.ID)
	kopluginDevs, _ := s.St.ListKopluginDevices(r.Context(), u.ID)
	toks, browsers := splitReaderTokens(toks)
	sortTokens(toks)
	sortKosyncDevices(kosyncDevs)
	sortKopluginDevices(kopluginDevs)
	return settingsDevicesView{
		Tokens: toks, Browsers: browsers,
		Kosync: kosyncDevs, Koplugin: kopluginDevs,
		Base: s.serverBaseURL(r),
	}
}

func (s *Server) settingsAdmin(
	r *http.Request,
	u *store.User,
	v *settingsAdminView,
	view string,
	targetID string,
) error {
	switch view {
	case settingsAdminFolders:
		after := r.URL.Query().Get("after")
		folders, err := s.St.ListFolders(r.Context(), "", after, adminFoldersPerPage+1)
		if err != nil {
			return err
		}
		if len(folders) > adminFoldersPerPage {
			folders = folders[:adminFoldersPerPage]
			v.FoldersNext = settingsAdminHref(relPrefix("/ui/settings"), settingsAdminFolders) +
				"&after=" + url.QueryEscape(store.FolderCursor(folders[len(folders)-1]))
		}
		v.Folders = make([]adminFolderView, 0, len(folders))
		for _, folder := range folders {
			v.Folders = append(v.Folders, adminFolderView{Folder: folder})
		}
		v.Roots = s.Cfg.Content.FolderRoots
	case settingsAdminUsers:
		after := r.URL.Query().Get("after")
		users, err := s.St.ListUsersPage(r.Context(), after, adminUsersPerPage+1)
		if err != nil {
			return err
		}
		if len(users) > adminUsersPerPage {
			users = users[:adminUsersPerPage]
			v.UsersNext = settingsAdminHref(relPrefix("/ui/settings"), settingsAdminUsers) +
				"&after=" + url.QueryEscape(users[len(users)-1].Name)
		}
		v.Users = users
		v.Invites, _ = s.St.ListInvites(r.Context(), u.ID)
	case settingsAdminUser:
		target, err := s.St.UserByID(r.Context(), targetID)
		if err != nil {
			return err
		}
		view := adminUserView{
			User: target, Self: target.ID == u.ID,
			Base: s.serverBaseURL(r),
		}
		ownTokens, browsers := splitReaderTokens(
			listOrNil(s.St.ListTokens(r.Context(), target.ID)),
		)
		view.Tokens, view.MoreTokens = capSlice(ownTokens)
		view.Browsers = len(browsers)
		view.Kosync, view.MoreKosync = capSlice(
			listOrNil(s.St.ListKosyncDevices(r.Context(), target.ID)),
		)
		view.Koplugin, view.MoreKoplugin = capSlice(
			listOrNil(s.St.ListKopluginDevices(r.Context(), target.ID)),
		)
		all, tooMany, err := allFolders(r.Context(), s.St, "", adminUserFoldersMax)
		if err != nil {
			return err
		}
		if tooMany {
			view.FoldersUnmanageable = true
		} else {
			granted, _, err := allFolders(r.Context(), s.St, target.ID, adminUserFoldersMax)
			if err != nil {
				return err
			}
			grantSet := make(map[string]bool, len(granted))
			for _, folder := range granted {
				grantSet[folder.ID] = true
			}
			view.Folders = make([]adminUserFolderView, 0, len(all))
			for _, folder := range all {
				view.Folders = append(view.Folders, adminUserFolderView{
					Folder: folder, Granted: grantSet[folder.ID],
				})
			}
		}
		v.User = view
		v.HasUser = true
	case settingsAdminMaintenance:
		counts, err := s.St.AdminCounts(r.Context())
		if err != nil {
			return err
		}
		v.Maintenance = maintenanceView{
			Counts: counts,
			Kinds:  countRows(counts.FoldersByKind, folderKindOrder),
			Books:  countRows(counts.BooksByStatus, bookStatusOrder),
		}
	default:
		counts, err := s.St.AdminCounts(r.Context())
		if err != nil {
			return err
		}
		v.Counts = counts
		v.Build = buildinfo.Get()
		v.Config = describeConfig(s.Cfg)
		v.View = settingsAdminOverview
	}
	return nil
}

// allFolders walks the whole folder list a page at a time, up to max
// folders. It stops and reports the overflow rather than returning a
// prefix: every caller needs the complete set, and a silent prefix here
// would become a silent revocation on the grant form.
func allFolders(
	ctx context.Context, st store.Store, viewerID string, limit int,
) (folders []store.Folder, tooMany bool, err error) {
	var after string
	for {
		page, err := st.ListFolders(ctx, viewerID, after, adminFoldersPerPage)
		if err != nil {
			return nil, false, err
		}
		folders = append(folders, page...)
		if len(folders) > limit {
			return nil, true, nil
		}
		if len(page) < adminFoldersPerPage {
			return folders, false, nil
		}
		after = store.FolderCursor(page[len(page)-1])
	}
}
