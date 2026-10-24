package webui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/audiobookshelf"
	"github.com/chmouel/liseur-sync/internal/store"
)

type Audiobookshelf interface {
	View(context.Context, string) ([]audiobookshelf.ItemView, error)
	Link(context.Context, string, string, string) error
	Mutate(context.Context, string, string, string, string) error
}

type audiobookPageView struct {
	Items     []audiobookshelf.ItemView
	Choices   []store.CatalogBook
	ItemID    string
	Query     string
	Truncated bool
}

func (s *Server) audiobookAccess(u *store.User) bool {
	return s.Audiobookshelf != nil && s.Cfg.Audiobookshelf.Enabled && u.Name == s.Cfg.Audiobookshelf.Account
}

func (s *Server) handleAudiobookshelf(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.audiobookAccess(u) {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rows, err := s.Audiobookshelf.View(ctx, u.ID)
	if err != nil {
		http.Error(w, "Audiobookshelf is unavailable. Try again later.", http.StatusServiceUnavailable)
		return
	}
	v := audiobookPageView{Items: rows, ItemID: r.URL.Query().Get("item_id"), Query: r.URL.Query().Get("q")}
	if len(v.Query) > 256 {
		http.Error(w, "search is too long", http.StatusBadRequest)
		return
	}
	if v.Query != "" {
		after := ""
		for {
			folders, err := s.St.ListUserFolders(ctx, u.ID, after, 100)
			if err != nil {
				http.Error(w, "internal", 500)
				return
			}
			for _, folder := range folders {
				res, err := s.St.SearchCatalogBooks(ctx, store.SearchQuery{UserID: u.ID, FolderID: folder.ID, Text: v.Query, Limit: 30})
				if err != nil {
					http.Error(w, "internal", 500)
					return
				}
				v.Truncated = v.Truncated || res.Truncated
				for _, book := range res.Books {
					if book.Status == store.BookActive && len(v.Choices) < 30 {
						v.Choices = append(v.Choices, book)
					} else {
						v.Truncated = true
					}
				}
			}
			if len(folders) < 100 {
				break
			}
			after = store.FolderCursor(folders[len(folders)-1])
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	audiobookPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).Render(r.Context(), w)
}

func (s *Server) handleAudiobookMutation(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.audiobookAccess(u) {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var err error
	if r.FormValue("action") == "link" {
		err = s.Audiobookshelf.Link(ctx, u.ID, r.FormValue("item_id"), r.FormValue("book_id"))
	} else {
		err = s.Audiobookshelf.Mutate(ctx, u.ID, r.FormValue("item_id"), r.FormValue("generation"), r.FormValue("action"))
	}
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, audiobookshelf.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrConflict):
		http.Error(w, "This link changed. Reload the page and try again.", http.StatusConflict)
	case errors.Is(err, store.ErrInvalidInput), errors.Is(err, audiobookshelf.ErrNoAlignment):
		http.Error(w, "These chapters cannot be confirmed. Choose another book.", http.StatusBadRequest)
	case err != nil:
		http.Error(w, "Audiobookshelf is unavailable. Try again later.", http.StatusServiceUnavailable)
	default:
		redirectRel(w, relPrefix(r.URL.Path)+"audiobookshelf", http.StatusSeeOther)
	}
}

func audioClock(seconds float64) string {
	return fmt.Sprintf("%d:%02d", int(seconds)/60, int(seconds)%60)
}
func audioCoverage(m audiobookshelf.Mapping) string {
	_, _, start, end := m.Coverage()
	return fmt.Sprintf("%.1f%%–%.1f%% of the ebook", 100*start, 100*end)
}
func audiobookState(l *store.AudiobookLink) string {
	if l == nil {
		return "Unmatched"
	}
	switch l.State {
	case store.AudiobookLinkActive:
		return "Following"
	case store.AudiobookLinkNeedsMapping:
		return "Needs confirmation"
	default:
		return "Paused: ebook unavailable"
	}
}
