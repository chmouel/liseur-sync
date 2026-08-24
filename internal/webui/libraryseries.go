package webui

// The grouped library follows Liseur's Android shelf: one mixed grid of
// standalone books and series piles, never a series section above a book
// section. The store expands only the primary series of books already in
// play, so a paged catalog stays paged and a reader's personal claims and
// folder grants stay inside that reader's view.

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// LibraryEntry is one card in the grouped grid. Exactly one side is set.
type LibraryEntry struct {
	Row    *LibraryRow
	Series *LibrarySeries
}

// LibrarySeries is the compact pile standing in for two or more books in
// every folder the reader may see. Its position on a paged folder shelf is
// still determined by a volume in that folder.
type LibrarySeries struct {
	ID                   string
	Name                 string
	Author               string
	Volumes              []LibrarySeriesVolume
	CoverBookID          string
	BackBookIDs          []string
	RepresentativeBookID string
	FinishedCount        int
	StartedCount         int
	Complete             bool
	AddedAt              int64
	LastActiveAt         int64
}

// LibrarySeriesVolume carries the per-reader state painted into one
// segment of a pile's progress rail.
type LibrarySeriesVolume struct {
	Row       LibraryRow
	Position  *float64
	FolderID  string
	createdAt time.Time
}

func (s LibrarySeries) Summary() string {
	parts := []string{plural(len(s.Volumes), "book")}
	if s.FinishedCount > 0 {
		parts = append(parts, strconv.Itoa(s.FinishedCount)+" read")
	}
	if s.StartedCount > 0 {
		parts = append(parts, strconv.Itoa(s.StartedCount)+" in progress")
	}
	return strings.Join(parts, " · ")
}

func rowEntries(rows []LibraryRow) []LibraryEntry {
	out := make([]LibraryEntry, 0, len(rows))
	for i := range rows {
		row := rows[i]
		out = append(out, LibraryEntry{Row: &row})
	}
	return out
}

// groupedCatalogEntries folds one cursor page. A pile is emitted only
// where its newest-added volume occurs in the underlying catalog order;
// every other volume is suppressed. That makes the existing book cursor
// a stable shelf cursor without repeating a series across pages.
func (s *Server) groupedCatalogEntries(
	r *http.Request, userID string, v LibraryView, rows []LibraryRow,
	byBook map[string]store.WorkSummary, loc *time.Location,
) ([]LibraryEntry, error) {
	groups, byVolume, err := s.librarySeriesForRows(
		r, userID, v.Selected, rows, byBook, loc)
	if err != nil {
		return nil, err
	}
	eligible := make(map[string]bool, len(groups))
	for id, group := range groups {
		eligible[id] = groupMatches(*group, v.Filter)
	}

	entries := make([]LibraryEntry, 0, len(rows))
	for i := range rows {
		row := rows[i]
		group := byVolume[row.BookID]
		if group == nil {
			if keepRow(row, v.Filter) {
				entries = append(entries, LibraryEntry{Row: &row})
			}
			continue
		}
		if eligible[group.ID] && row.BookID == group.RepresentativeBookID {
			entries = append(entries, LibraryEntry{Series: group})
		}
	}
	return entries, nil
}

// groupedReadingEntries folds the complete reading-side answer. Unlike
// catalog pages it can sort the resulting entries directly because that
// side is already complete in memory.
func (s *Server) groupedReadingEntries(
	r *http.Request, userID string, v LibraryView, rows []LibraryRow,
	byBook map[string]store.WorkSummary, loc *time.Location,
) ([]LibraryEntry, error) {
	groups, byVolume, err := s.librarySeriesForRows(
		r, userID, v.Selected, rows, byBook, loc)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(groups))
	entries := make([]LibraryEntry, 0, len(rows))
	for i := range rows {
		row := rows[i]
		group := byVolume[row.BookID]
		if group == nil {
			entries = append(entries, LibraryEntry{Row: &row})
			continue
		}
		if !groupMatches(*group, v.Filter) {
			continue
		}
		if !seen[group.ID] {
			seen[group.ID] = true
			entries = append(entries, LibraryEntry{Series: group})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entryLastActive(entries[i]), entryLastActive(entries[j])
		if left == right {
			return entryName(entries[i]) < entryName(entries[j])
		}
		if v.Dir == sortDirAsc {
			return left < right
		}
		return left > right
	})
	return entries, nil
}

func (s *Server) librarySeriesForRows(
	r *http.Request, userID, folderID string, seeds []LibraryRow,
	byBook map[string]store.WorkSummary, loc *time.Location,
) (map[string]*LibrarySeries, map[string]*LibrarySeries, error) {
	bookIDs := make([]string, 0, len(seeds))
	for _, row := range seeds {
		if row.BookID != "" {
			bookIDs = append(bookIDs, row.BookID)
		}
	}
	volumes, err := s.St.CatalogSeriesVolumesForBooks(
		r.Context(), userID, folderID, bookIDs)
	if err != nil {
		return nil, nil, err
	}
	volumeIDs := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		volumeIDs = append(volumeIDs, volume.BookID)
	}
	authors, _ := s.St.CatalogAuthorsForBooks(r.Context(), userID, volumeIDs)

	groups := make(map[string]*LibrarySeries)
	for _, volume := range volumes {
		group := groups[volume.SeriesID]
		if group == nil {
			group = &LibrarySeries{ID: volume.SeriesID, Name: volume.SeriesName}
			groups[volume.SeriesID] = group
		}
		row := LibraryRow{
			BookID:  volume.BookID,
			Title:   volume.Title,
			Author:  credit(authors[volume.BookID]),
			Added:   volume.CreatedAt.In(loc).Format("Jan 2, 2006"),
			CanGet:  true,
			CanRead: isEPUB(volume.MediaType),
		}
		if ws, ok := byBook[volume.BookID]; ok {
			row.WorkID = ws.Work.ID
			row.Progression = ws.Progression
			row.Pending = ws.Pending
			if row.Author == "" {
				row.Author = ws.Work.Author
			}
			if ws.LastActive != nil {
				row.LastActive = ws.LastActive.In(loc).Format("Jan 2")
				row.lastActiveAt = ws.LastActive.Unix()
			}
		}
		group.Volumes = append(group.Volumes, LibrarySeriesVolume{
			Row: row, Position: volume.Position, FolderID: volume.FolderID,
			createdAt: volume.CreatedAt,
		})
	}

	byVolume := make(map[string]*LibrarySeries)
	for id, group := range groups {
		if len(group.Volumes) < 2 {
			delete(groups, id)
			continue
		}
		sort.SliceStable(group.Volumes, func(i, j int) bool {
			left, right := group.Volumes[i], group.Volumes[j]
			if (left.Position == nil) != (right.Position == nil) {
				return left.Position != nil
			}
			if left.Position != nil && *left.Position != *right.Position {
				return *left.Position < *right.Position
			}
			leftTitle := androidShelfSortKey(left.Row.Title)
			rightTitle := androidShelfSortKey(right.Row.Title)
			if leftTitle != rightTitle {
				return leftTitle < rightTitle
			}
			return left.Row.BookID < right.Row.BookID
		})
		finalizeLibrarySeries(group, folderID)
		group.Author = commonSeriesAuthor(group.Volumes)
		for _, volume := range group.Volumes {
			byVolume[volume.Row.BookID] = group
		}
	}
	return groups, byVolume, nil
}

// androidShelfSortKey mirrors domain.LibrarySort.sortKey in Liseur. It
// matters here for unnumbered companions: Android puts them after the
// numbered run and files them by title without letting a leading English
// or French article decide their order.
func androidShelfSortKey(text string) string {
	key := strings.ToLower(strings.TrimSpace(text))
	if key == "" {
		return ""
	}
	for _, prefix := range []string{"l'", "l’", "d'", "d’"} {
		if strings.HasPrefix(key, prefix) {
			if rest := strings.TrimSpace(strings.TrimPrefix(key, prefix)); rest != "" {
				return rest
			}
			return key
		}
	}
	space := strings.IndexByte(key, ' ')
	if space <= 0 {
		return key
	}
	switch key[:space] {
	case "the", "a", "an", "le", "la", "les", "un", "une", "des", "du", "de":
	default:
		return key
	}
	if rest := strings.TrimSpace(key[space+1:]); rest != "" {
		return rest
	}
	return key
}

func finalizeLibrarySeries(group *LibrarySeries, folderID string) {
	cover := -1
	unfinished := -1
	complete := true
	newest := -1
	for i := range group.Volumes {
		volume := &group.Volumes[i]
		if volume.Row.Started() {
			group.StartedCount++
			if cover < 0 {
				cover = i
			}
		}
		if volume.Row.Finished() {
			group.FinishedCount++
		} else {
			complete = false
			if unfinished < 0 {
				unfinished = i
			}
		}
		if volume.Row.lastActiveAt > group.LastActiveAt {
			group.LastActiveAt = volume.Row.lastActiveAt
		}
		added := volume.createdAt.UnixNano()
		if volume.FolderID == folderID && (newest < 0 || added > group.AddedAt ||
			(added == group.AddedAt && volume.Row.BookID > group.Volumes[newest].Row.BookID)) {
			newest = i
			group.AddedAt = added
		}
	}
	if newest < 0 {
		for i := range group.Volumes {
			added := group.Volumes[i].createdAt.UnixNano()
			if newest < 0 || added > group.AddedAt ||
				(added == group.AddedAt && group.Volumes[i].Row.BookID > group.Volumes[newest].Row.BookID) {
				newest = i
				group.AddedAt = added
			}
		}
	}
	if cover < 0 {
		cover = unfinished
	}
	if cover < 0 {
		cover = 0
	}
	group.Complete = complete
	group.CoverBookID = group.Volumes[cover].Row.BookID
	group.RepresentativeBookID = group.Volumes[newest].Row.BookID
	for _, volume := range group.Volumes {
		if volume.Row.BookID != group.CoverBookID && len(group.BackBookIDs) < 2 {
			group.BackBookIDs = append(group.BackBookIDs, volume.Row.BookID)
		}
	}
}

func commonSeriesAuthor(volumes []LibrarySeriesVolume) string {
	counts := map[string]int{}
	best, bestCount := "", 0
	for _, volume := range volumes {
		name := volume.Row.Author
		if name == "" {
			continue
		}
		counts[name]++
		if counts[name] > bestCount {
			best, bestCount = name, counts[name]
		}
	}
	return best
}

func groupMatches(group LibrarySeries, filter string) bool {
	for _, volume := range group.Volumes {
		if keepRow(volume.Row, filter) {
			return true
		}
	}
	return false
}

func entryLastActive(entry LibraryEntry) int64 {
	if entry.Series != nil {
		return entry.Series.LastActiveAt
	}
	if entry.Row != nil {
		return entry.Row.lastActiveAt
	}
	return 0
}

func entryName(entry LibraryEntry) string {
	if entry.Series != nil {
		return strings.ToLower(entry.Series.Name)
	}
	if entry.Row != nil {
		return strings.ToLower(entry.Row.Title)
	}
	return ""
}
