package webui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/audiobookshelf"
	"github.com/chmouel/liseur-sync/internal/store"
)

type Audiobookshelf interface {
	View(context.Context, string) ([]audiobookshelf.ItemView, error)
	Link(context.Context, string, string, string) error
	Mutate(context.Context, string, string, string, string) error
}

type audiobookChoiceView struct {
	ID       string
	Title    string
	Subtitle string
	Author   string
}

type audiobookFilterTab struct {
	Key    string
	Label  string
	Count  int
	URL    string
	Active bool
}

type audiobookGroup struct {
	Key   string
	Title string
	Hint  string
	Items []audiobookshelf.ItemView
}

type audiobookPageView struct {
	Items     []audiobookshelf.ItemView
	Groups    []audiobookGroup
	Tabs      []audiobookFilterTab
	Filter    string
	Choices   []audiobookChoiceView
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
	filter := normalizeAudiobookFilter(r.URL.Query().Get("state"))
	v := audiobookPageView{
		Items:  rows,
		Filter: filter,
		ItemID: r.URL.Query().Get("item_id"),
		Query:  r.URL.Query().Get("q"),
	}
	if len(v.Query) > 256 {
		http.Error(w, "search is too long", http.StatusBadRequest)
		return
	}
	if v.Query != "" {
		var books []store.CatalogBook
		var bookIDs []string
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
					if book.Status == store.BookActive && len(books) < 30 {
						books = append(books, book)
						bookIDs = append(bookIDs, book.ID)
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
		var rel store.CatalogBookRelations
		if len(bookIDs) > 0 {
			rel, _ = s.St.CatalogBookRelationsForBooks(ctx, u.ID, bookIDs)
		}
		for _, book := range books {
			_, byline := contributorChips(rel.Contributors[book.ID])
			v.Choices = append(v.Choices, audiobookChoiceView{
				ID:       book.ID,
				Title:    book.Title,
				Subtitle: book.Subtitle,
				Author:   byline,
			})
		}
	}
	v.Tabs, v.Groups = buildAudiobookGroups(rows, filter)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	audiobookPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).Render(r.Context(), w)
}

func normalizeAudiobookFilter(raw string) string {
	switch raw {
	case "needs_mapping", "active", "paused", "unmatched":
		return raw
	default:
		return "all"
	}
}

func audiobookGroupKey(l *store.AudiobookLink) string {
	if l == nil {
		return "unmatched"
	}
	switch l.State {
	case store.AudiobookLinkNeedsMapping:
		return "needs_mapping"
	case store.AudiobookLinkActive:
		return "active"
	default:
		return "paused"
	}
}

func buildAudiobookGroups(rows []audiobookshelf.ItemView, filter string) ([]audiobookFilterTab, []audiobookGroup) {
	buckets := map[string][]audiobookshelf.ItemView{}
	for _, row := range rows {
		k := audiobookGroupKey(row.Link)
		buckets[k] = append(buckets[k], row)
	}
	specs := []struct {
		key   string
		label string
		hint  string
	}{
		{
			key:   "needs_mapping",
			label: "Needs confirmation",
			hint:  "Review how chapters line up before positions cross between your audiobook and ebook.",
		},
		{
			key:   "active",
			label: "Following",
			hint:  "Reading and listening positions stay in step across matched chapters.",
		},
		{
			key:   "paused",
			label: "Paused",
			hint:  "These links are waiting for their ebook or audiobook to become available again.",
		},
		{
			key:   "unmatched",
			label: "Unmatched",
			hint:  "Pair an audiobook with an ebook from your library to sync progress.",
		},
	}
	tabs := []audiobookFilterTab{
		{
			Key:    "all",
			Label:  "All",
			Count:  len(rows),
			URL:    "audiobookshelf",
			Active: filter == "all",
		},
	}
	var groups []audiobookGroup
	for _, sp := range specs {
		items := buckets[sp.key]
		if len(items) == 0 && sp.key == "paused" && filter != "paused" {
			continue
		}
		u := "audiobookshelf?state=" + url.QueryEscape(sp.key)
		tabs = append(tabs, audiobookFilterTab{
			Key:    sp.key,
			Label:  sp.label,
			Count:  len(items),
			URL:    u,
			Active: filter == sp.key,
		})
		if filter != "all" && filter != sp.key {
			continue
		}
		if len(items) == 0 && filter == "all" {
			continue
		}
		groups = append(groups, audiobookGroup{
			Key:   sp.key,
			Title: sp.label,
			Hint:  sp.hint,
			Items: items,
		})
	}
	return tabs, groups
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
	total := int(seconds)
	if total < 0 {
		total = 0
	}
	if total >= 3600 {
		h := total / 3600
		m := (total % 3600) / 60
		s := total % 60
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

func audioDuration(seconds float64) string {
	total := int(math.Round(seconds))
	if total <= 0 {
		return ""
	}
	h := total / 3600
	m := (total % 3600) / 60
	if h > 0 && m > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if h > 0 {
		return fmt.Sprintf("%dh", h)
	}
	if m > 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", total)
}

func audioAuthors(authors []string) string {
	return strings.Join(authors, ", ")
}

func audioCoverage(m audiobookshelf.Mapping) string {
	_, _, start, end := m.Coverage()
	return fmt.Sprintf("%.1f%%–%.1f%% of the ebook", 100*start, 100*end)
}

func audioSegmentBookSpan(seg audiobookshelf.Segment) string {
	return fmt.Sprintf("%.0f%%–%.0f%%", 100*seg.BookStart, 100*seg.BookEnd)
}

func audioCoverageGridClass(m audiobookshelf.Mapping) string {
	_, _, start, end := m.Coverage()
	if start < 0 {
		start = 0
	}
	if end > 1 {
		end = 1
	}
	startCol := int(math.Floor(start*20)) + 1
	if startCol < 1 {
		startCol = 1
	}
	if startCol > 20 {
		startCol = 20
	}
	endCol := int(math.Ceil(end*20)) + 1
	if endCol <= startCol {
		endCol = startCol + 1
	}
	if endCol > 21 {
		endCol = 21
	}
	return fmt.Sprintf("abs-cstart-%d abs-cend-%d", startCol, endCol)
}

func audioProgressFraction(p *audiobookshelf.Progress) float64 {
	if p == nil {
		return 0
	}
	if p.IsFinished {
		return 1
	}
	if p.Progress < 0 {
		return 0
	}
	if p.Progress > 1 {
		return 1
	}
	return p.Progress
}

func audioListeningSummary(p *audiobookshelf.Progress) string {
	if p == nil {
		return ""
	}
	if p.IsFinished {
		return "Finished"
	}
	if p.CurrentTime <= 0 && p.Progress <= 0 {
		return "Not started"
	}
	return fmt.Sprintf("%s · %s", audioClock(p.CurrentTime), pctProgression(p.Progress))
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

func audiobookStateClass(l *store.AudiobookLink) string {
	if l == nil {
		return "abs-badge abs-badge-unmatched"
	}
	switch l.State {
	case store.AudiobookLinkActive:
		return "abs-badge abs-badge-active"
	case store.AudiobookLinkNeedsMapping:
		return "abs-badge abs-badge-pending"
	default:
		return "abs-badge abs-badge-paused"
	}
}

func audiobookCardClass(l *store.AudiobookLink) string {
	if l == nil {
		return "card abs-card abs-card-unmatched"
	}
	switch l.State {
	case store.AudiobookLinkActive:
		return "card abs-card abs-card-active"
	case store.AudiobookLinkNeedsMapping:
		return "card abs-card abs-card-pending"
	default:
		return "card abs-card abs-card-paused"
	}
}

func audiobookQueryFor(v audiobookPageView, itemID string) string {
	if v.ItemID == itemID {
		return v.Query
	}
	return ""
}
