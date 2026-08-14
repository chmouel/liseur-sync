package webui

import (
	"net/http"

	"github.com/chmouel/liseur-sync/internal/store"
)

// handleLibraryManage is the manager-only home for work around a library:
// uploads, trash, source changes and duplicate decisions. It is separate
// from the catalog page so browsing a shelf does not also mean browsing its
// operational queues.
func (s *Server) handleLibraryManage(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	libs, err := s.St.ListLibraries(r.Context(), u.ID, store.LibraryRoleManage)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}

	v := LibraryView{}
	selected := r.URL.Query().Get("library")
	if selected == "" && len(libs) > 0 {
		selected = libs[0].Library.ID
	}
	for _, l := range libs {
		v.Libraries = append(v.Libraries, LibraryOption{
			ID: l.Library.ID, Name: l.Library.Name, CanWrite: true,
			Selected: l.Library.ID == selected,
		})
	}
	if selected == "" {
		libraryManagePage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
			Render(r.Context(), w)
		return
	}
	for _, l := range libs {
		if l.Library.ID == selected {
			v.Selected, v.CanWrite = selected, true
			break
		}
	}
	if !v.CanWrite {
		http.NotFound(w, r)
		return
	}

	loc := userLoc(u)
	v.Uploads = s.uploadActivity(r, u.ID, v.Selected, loc)
	v.Trash = s.trashActivity(r, u.ID, v.Selected, loc)
	v.Review = s.reviewRows(r, u.ID, v.Selected)
	v.Duplicates = s.duplicateGroups(r, u.ID, v.Selected)
	v.Similar = s.similarGroups(r, u.ID, v.Selected, loc)
	libraryManagePage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
		Render(r.Context(), w)
}
