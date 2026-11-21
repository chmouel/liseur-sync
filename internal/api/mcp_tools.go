package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/buildinfo"
	"github.com/chmouel/liseur-sync/internal/insights"
	"github.com/chmouel/liseur-sync/internal/mcp"
	"github.com/chmouel/liseur-sync/internal/oauth"
	"github.com/chmouel/liseur-sync/internal/reading"
	"github.com/chmouel/liseur-sync/internal/store"
)

// mcpService holds what the assistant tools share (ADR-0069). Every
// tool reads through the store with the token's own user ID, so folder
// grants and per-user isolation are the same checks the native API
// makes. No tool writes, and no tool output carries a file path, a
// download URL or a credential.
type mcpService struct {
	s       *Server
	texts   *BookTextCache
	budget  *textBudget
	perCall int
	now     func() time.Time
}

func (s *Server) newMCPService() *mcpService {
	texts := s.BookTexts
	if texts == nil {
		texts = NewBookTextCache(0)
	}
	return &mcpService{
		s:       s,
		texts:   texts,
		budget:  newTextBudget(s.Cfg.MCP.TextBytesPerHour),
		perCall: s.Cfg.MCP.TextBytesPerCall,
		now:     time.Now,
	}
}

// mcpHandler is POST /mcp behind the bearer check: a per-grant request
// limit, then the protocol handler.
func (s *Server) mcpHandler() http.Handler {
	svc := s.newMCPService()
	server := mcp.New(buildinfo.Get().Short(), s.Cfg.MCP.PublicURL, func(ctx context.Context) []string {
		a, _ := oauth.AccessFrom(ctx)
		return a.Scopes
	}, svc.tools())
	limiter := s.MCPLimiter
	if limiter == nil {
		limiter = auth.NewRateLimiter(120, time.Minute)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, _ := oauth.AccessFrom(r.Context())
		if !limiter.Allow("mcp:" + a.GrantID) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		server.ServeHTTP(w, r)
	})
}

func mcpUser(ctx context.Context) string {
	a, _ := oauth.AccessFrom(ctx)
	return a.UserID
}

// Schema helpers. Tool descriptions and schemas are static strings:
// nothing a book or a folder says ever reaches them.
func objectSchema(props map[string]any, required ...string) map[string]any {
	out := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func strProp(desc string, maxLen int) map[string]any {
	return map[string]any{"type": "string", "description": desc, "maxLength": maxLen}
}

func intProp(desc string, lo, hi int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "minimum": lo, "maximum": hi}
}

func numProp(desc string, lo, hi float64) map[string]any {
	return map[string]any{"type": "number", "description": desc, "minimum": lo, "maximum": hi}
}

func enumProp(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

const maxIDLen = 128

var mcpStatuses = []reading.Status{
	reading.StatusReading, reading.StatusFinished, reading.StatusPaused, reading.StatusWantToRead,
	reading.StatusDidNotFinish, reading.StatusUnread, reading.StatusReadyToRead,
}

func requireID(name, v string) error {
	if v == "" || len(v) > maxIDLen || !utf8.ValidString(v) {
		return mcp.Errorf("%s is required", name)
	}
	return nil
}

func limitOr(v, def, hi int) (int, error) {
	if v == 0 {
		return def, nil
	}
	if v < 1 || v > hi {
		return 0, mcp.Errorf("limit must be between 1 and %d", hi)
	}
	return v, nil
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func (m *mcpService) tools() []mcp.Tool {
	tools := []mcp.Tool{
		{
			Name: "search_library", Title: "Search the library", Scope: oauth.ScopeLibrary,
			Description: "Find books in the reader's library by title, author, series or subject. Returns book IDs for the other tools.",
			InputSchema: objectSchema(map[string]any{
				"query": strProp("Words to look for.", 200),
				"limit": intProp("Most books to return (default 10).", 1, 50),
			}, "query"),
			Call: m.searchLibrary,
		},
		{
			Name: "get_book", Title: "Get a book", Scope: oauth.ScopeLibrary,
			Description: "Metadata for one book: title, authors, series, subjects, description, and the reader's work ID and status for it when they have one.",
			InputSchema: objectSchema(map[string]any{"book_id": strProp("Book ID from search_library.", maxIDLen)}, "book_id"),
			Call:        m.getBook,
		},
		{
			Name: "list_series", Title: "List series", Scope: oauth.ScopeLibrary,
			Description: "Without series_id, page through the series in the library. With series_id, list the books in that series in order.",
			InputSchema: objectSchema(map[string]any{
				"series_id": strProp("A series to list the books of.", maxIDLen),
				"cursor":    strProp("next_cursor from a previous call.", 512),
				"limit":     intProp("Most entries to return (default 50).", 1, 200),
			}),
			Call: m.listSeries,
		},
		{
			Name: "get_reading_status", Title: "Get reading status", Scope: oauth.ScopeReading,
			Description: "With work_id, the reader's status, progress and recent positions for that work. Without it, every work they have reading state for, newest first, optionally filtered by status.",
			InputSchema: objectSchema(map[string]any{
				"work_id": strProp("A work ID from get_book or a previous listing.", maxIDLen),
				"status": enumProp("Only works with this status.",
					string(reading.StatusReading), string(reading.StatusFinished), string(reading.StatusPaused),
					string(reading.StatusWantToRead), string(reading.StatusDidNotFinish), string(reading.StatusUnread),
					string(reading.StatusReadyToRead)),
				"limit": intProp("Most works to return (default 50).", 1, 500),
			}),
			Call: m.getReadingStatus,
		},
		{
			Name: "list_currently_reading", Title: "List books in progress", Scope: oauth.ScopeReading,
			Description: "The works the reader is reading now, most recently read first, with progress and the book IDs to read their text.",
			InputSchema: objectSchema(map[string]any{"limit": intProp("Most works to return (default 20).", 1, 100)}),
			Call:        m.listCurrentlyReading,
		},
		{
			Name: "list_annotations", Title: "List highlights, notes and bookmarks", Scope: oauth.ScopeReading,
			Description: "The reader's highlights, notes and bookmarks in one work, in reading order. Pass an annotation's id to read_at to read the text around it.",
			InputSchema: objectSchema(map[string]any{
				"work_id": strProp("The work.", maxIDLen),
				"kind":    enumProp("Only this kind.", string(store.AnnotationHighlight), string(store.AnnotationNote), string(store.AnnotationBookmark)),
				"cursor":  strProp("next_cursor from a previous call.", 32),
				"limit":   intProp("Most annotations to return (default 100).", 1, 500),
			}, "work_id"),
			Call: m.listAnnotations,
		},
		{
			Name: "get_stats_summary", Title: "Reading statistics summary", Scope: oauth.ScopeInsights,
			Description: "Total reading time, pages, sessions, streak and speed over a range of days, in the reader's own timezone.",
			InputSchema: objectSchema(map[string]any{
				"range": strProp("Last N days as \"Nd\" (for example \"30d\"), or \"all\". Default 30d.", 8),
				"from":  strProp("First day, YYYY-MM-DD. Use with to instead of range.", 10),
				"to":    strProp("Last day, YYYY-MM-DD.", 10),
			}),
			Call: m.statsSummary,
		},
		{
			Name: "get_work_stats", Title: "Statistics for one work", Scope: oauth.ScopeInsights,
			Description: "Reading time, pages and sessions for one work. Progress and estimated time left require the reading scope. Without work_id, returns every work read in the range.",
			InputSchema: objectSchema(map[string]any{
				"work_id": strProp("The work.", maxIDLen),
				"range":   strProp("Last N days as \"Nd\", or \"all\" (default).", 8),
			}),
			Call: m.workStats,
		},
		{
			Name: "get_reading_calendar", Title: "Reading calendar", Scope: oauth.ScopeInsights,
			Description: "Minutes, pages and sessions per day between two dates (at most 400 days).",
			InputSchema: objectSchema(map[string]any{
				"from": strProp("First day, YYYY-MM-DD.", 10),
				"to":   strProp("Last day, YYYY-MM-DD.", 10),
			}, "from", "to"),
			Call: m.calendar,
		},
		{
			Name: "list_vocabulary", Title: "List vocabulary", Scope: oauth.ScopeVocabulary,
			Description: "Words the reader saved while reading, with definitions, notes and the sentences they met them in.",
			InputSchema: objectSchema(map[string]any{
				"work_id":  strProp("Only words met in this work.", maxIDLen),
				"language": strProp("Only words in this language (BCP 47 tag).", 35),
				"cursor":   strProp("next_cursor from a previous call.", 32),
				"limit":    intProp("Most words to return (default 100).", 1, 500),
			}),
			Call: m.listVocabulary,
		},
	}
	return append(tools, m.textTools()...)
}

// mcpErr turns a store miss into something the model can act on and
// lets everything else through as an internal error.
func mcpErr(err error, what string) error {
	if errors.Is(err, store.ErrNotFound) {
		return mcp.Errorf("%s not found", what)
	}
	if errors.Is(err, store.ErrInvalidInput) {
		return mcp.Errorf("invalid %s", what)
	}
	return err
}

// mcpBook is the book shape assistants see: what the book is, never
// where it lives or how to fetch it.
func mcpBook(b store.CatalogBook, rel store.CatalogBookRelations) map[string]any {
	out := map[string]any{"book_id": b.ID, "title": b.Title}
	for k, v := range map[string]string{
		"subtitle": b.Subtitle, "publisher": b.Publisher, "published_date": b.PublishedDate,
	} {
		if v != "" {
			out[k] = v
		}
	}
	if b.Description != "" {
		out["description"] = clip(b.Description, 2000)
	}
	contributors := []map[string]any{}
	for _, c := range rel.Contributors[b.ID] {
		contributors = append(contributors, map[string]any{"name": c.Name, "role": c.Role})
	}
	out["contributors"] = contributors
	series := []map[string]any{}
	for _, s := range rel.Series[b.ID] {
		entry := map[string]any{"series_id": s.SeriesID, "name": s.Name}
		if s.Position != nil {
			entry["position"] = *s.Position
		}
		series = append(series, entry)
	}
	out["series"] = series
	if tags := rel.Tags[b.ID]; len(tags) > 0 {
		out["subjects"] = tags
	}
	if langs := rel.Languages[b.ID]; len(langs) > 0 {
		out["languages"] = langs
	}
	if b.Status != store.BookActive {
		out["available"] = false
	}
	return out
}

func (m *mcpService) books(ctx context.Context, userID string, list []store.CatalogBook) ([]map[string]any, error) {
	ids := make([]string, len(list))
	for i, b := range list {
		ids[i] = b.ID
	}
	rel, err := m.s.St.CatalogBookRelationsForBooks(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(list))
	for _, b := range list {
		out = append(out, mcpBook(b, rel))
	}
	return out, nil
}

func (m *mcpService) searchLibrary(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := mcp.DecodeArgs(raw, &in); err != nil {
		return nil, err
	}
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" || len(in.Query) > maxSearchTextBytes {
		return nil, mcp.Errorf("query must be 1 to %d bytes", maxSearchTextBytes)
	}
	limit, err := limitOr(in.Limit, 10, 50)
	if err != nil {
		return nil, err
	}
	userID := mcpUser(ctx)
	folders, err := m.s.St.ListFolders(ctx, userID, "", 100)
	if err != nil {
		return nil, err
	}
	var found []store.CatalogBook
	seen := map[string]bool{}
	truncated := false
	for _, f := range folders {
		res, err := m.s.St.SearchCatalogBooks(ctx, store.SearchQuery{FolderID: f.ID, UserID: userID, Text: in.Query, Limit: limit})
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		truncated = truncated || res.Truncated
		for _, b := range res.Books {
			if !seen[b.ID] {
				seen[b.ID] = true
				found = append(found, b)
			}
		}
	}
	if len(found) > limit {
		found, truncated = found[:limit], true
	}
	books, err := m.books(ctx, userID, found)
	if err != nil {
		return nil, err
	}
	return map[string]any{"books": books, "truncated": truncated}, nil
}

func (m *mcpService) getBook(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		BookID string `json:"book_id"`
	}
	if err := mcp.DecodeArgs(raw, &in); err != nil {
		return nil, err
	}
	if err := requireID("book_id", in.BookID); err != nil {
		return nil, err
	}
	userID := mcpUser(ctx)
	b, err := m.s.St.CatalogBookByID(ctx, userID, in.BookID)
	if err != nil {
		return nil, mcpErr(err, "book")
	}
	list, err := m.books(ctx, userID, []store.CatalogBook{b})
	if err != nil {
		return nil, err
	}
	out := list[0]
	// The reader's own link is reading state; only a grant that may read
	// reading state learns it.
	if a, _ := oauth.AccessFrom(ctx); oauth.HasScope(a, oauth.ScopeReading) {
		if workID, err := m.readerWork(ctx, userID, b); err == nil {
			out["work_id"] = workID
			if readings, err := m.s.St.WorkReadings(ctx, userID, []string{workID}); err == nil {
				if r, ok := readings[workID]; ok {
					out["reading_status"] = string(r.Resolve())
					if r.HasPosition {
						out["progression"] = r.Progression
					}
				}
			}
		}
	}
	return out, nil
}

func (m *mcpService) listSeries(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		SeriesID string `json:"series_id"`
		Cursor   string `json:"cursor"`
		Limit    int    `json:"limit"`
	}
	if err := mcp.DecodeArgs(raw, &in); err != nil {
		return nil, err
	}
	limit, err := limitOr(in.Limit, 50, 200)
	if err != nil {
		return nil, err
	}
	userID := mcpUser(ctx)
	if in.SeriesID == "" {
		list, err := m.s.St.ListCatalogEntities(ctx, userID, store.EntitySeries, in.Cursor, limit)
		if err != nil {
			return nil, mcpErr(err, "cursor")
		}
		series := []map[string]any{}
		for _, e := range list {
			series = append(series, map[string]any{"series_id": e.ID, "name": e.Name, "book_count": e.BookCount})
		}
		out := map[string]any{"series": series}
		if len(list) == limit {
			out["next_cursor"] = list[len(list)-1].NormalizedName
		}
		return out, nil
	}
	if err := requireID("series_id", in.SeriesID); err != nil {
		return nil, err
	}
	entity, err := m.s.St.CatalogEntityByID(ctx, userID, in.SeriesID, store.EntitySeries)
	if err != nil {
		return nil, mcpErr(err, "series")
	}
	var after *store.CatalogBookCursor
	if in.Cursor != "" {
		if after, err = decodeCatalogCursor(in.Cursor); err != nil {
			return nil, mcp.Errorf("invalid cursor")
		}
	}
	list, next, err := m.s.St.ListBooksByEntity(ctx, userID, in.SeriesID, store.EntitySeries, after, limit)
	if err != nil {
		return nil, mcpErr(err, "series")
	}
	books, err := m.books(ctx, userID, list)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"series_id": entity.ID, "name": entity.Name, "books": books}
	if next != nil {
		out["next_cursor"] = encodeCatalogCursor(*next)
	}
	return out, nil
}

// workEntry describes a work for a reading-status listing, with the
// catalog books the reader can open its text through.
func (m *mcpService) workEntry(ctx context.Context, userID string, w store.WorkSummary, r store.WorkReading) map[string]any {
	out := map[string]any{"work_id": w.Work.ID, "title": w.Work.Title, "status": string(r.Resolve())}
	if w.Work.Author != "" {
		out["author"] = w.Work.Author
	}
	if w.Progression != nil {
		out["progression"] = *w.Progression
	}
	if w.LastActive != nil {
		out["last_read_at"] = rfc3339(*w.LastActive)
	}
	if ids, err := m.s.St.WorkBookIDs(ctx, userID, w.Work.ID); err == nil {
		visible := []string{}
		for _, id := range ids {
			if _, err := m.s.St.CatalogBookByID(ctx, userID, id); err == nil {
				visible = append(visible, id)
			}
		}
		out["book_ids"] = visible
	}
	return out
}

func (m *mcpService) works(ctx context.Context, userID string) ([]store.WorkSummary, map[string]store.WorkReading, error) {
	list, err := m.s.St.ListWorks(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, len(list))
	for i, w := range list {
		ids[i] = w.Work.ID
	}
	readings, err := m.s.St.WorkReadings(ctx, userID, ids)
	if err != nil {
		return nil, nil, err
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i].LastActive, list[j].LastActive
		switch {
		case a == nil:
			return false
		case b == nil:
			return true
		}
		return a.After(*b)
	})
	return list, readings, nil
}

func (m *mcpService) getReadingStatus(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		WorkID string `json:"work_id"`
		Status string `json:"status"`
		Limit  int    `json:"limit"`
	}
	if err := mcp.DecodeArgs(raw, &in); err != nil {
		return nil, err
	}
	limit, err := limitOr(in.Limit, 50, 500)
	if err != nil {
		return nil, err
	}
	userID := mcpUser(ctx)
	if in.WorkID != "" {
		if err := requireID("work_id", in.WorkID); err != nil {
			return nil, err
		}
		w, err := m.s.St.WorkByID(ctx, userID, in.WorkID)
		if err != nil {
			return nil, mcpErr(err, "work")
		}
		readings, err := m.s.St.WorkReadings(ctx, userID, []string{w.ID})
		if err != nil {
			return nil, err
		}
		snap, err := m.s.St.PositionSnapshot(ctx, userID, w.ID, 10)
		if err != nil {
			return nil, err
		}
		summary := store.WorkSummary{Work: w}
		if len(snap.Ops) > 0 {
			p, at := snap.Ops[0].Progression, snap.Ops[0].ClientTS
			summary.Progression, summary.LastActive = &p, &at
		}
		out := m.workEntry(ctx, userID, summary, readings[w.ID])
		furthest := 0.0
		for _, o := range snap.Furthest {
			furthest = max(furthest, o.Progression)
		}
		out["furthest_progression"] = furthest
		recent := []map[string]any{}
		for _, o := range snap.Ops {
			recent = append(recent, map[string]any{"progression": o.Progression, "at": rfc3339(o.ClientTS), "device_id": o.DeviceID})
		}
		out["recent_positions"] = recent
		return out, nil
	}
	if in.Status != "" && !slices.Contains(mcpStatuses, reading.Status(in.Status)) {
		return nil, mcp.Errorf("unknown status")
	}
	list, readings, err := m.works(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	more := false
	for _, w := range list {
		r := readings[w.Work.ID]
		if in.Status != "" && string(r.Resolve()) != in.Status {
			continue
		}
		if len(out) == limit {
			more = true
			break
		}
		out = append(out, m.workEntry(ctx, userID, w, r))
	}
	return map[string]any{"works": out, "truncated": more}, nil
}

func (m *mcpService) listCurrentlyReading(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Limit int `json:"limit"`
	}
	if err := mcp.DecodeArgs(raw, &in); err != nil {
		return nil, err
	}
	limit, err := limitOr(in.Limit, 20, 100)
	if err != nil {
		return nil, err
	}
	userID := mcpUser(ctx)
	list, readings, err := m.works(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, w := range list {
		r := readings[w.Work.ID]
		if r.Resolve() != reading.StatusReading {
			continue
		}
		out = append(out, m.workEntry(ctx, userID, w, r))
		if len(out) == limit {
			break
		}
	}
	return map[string]any{"works": out}, nil
}

// locatorHref reads the resource a stored locator names, for a model
// that wants to know which chapter an annotation is in.
func locatorHref(raw []byte) string {
	var l struct {
		Href string `json:"href"`
	}
	if json.Unmarshal(raw, &l) != nil {
		return ""
	}
	return l.Href
}

func offsetCursor(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, mcp.Errorf("invalid cursor")
	}
	return n, nil
}

func (m *mcpService) listAnnotations(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		WorkID string `json:"work_id"`
		Kind   string `json:"kind"`
		Cursor string `json:"cursor"`
		Limit  int    `json:"limit"`
	}
	if err := mcp.DecodeArgs(raw, &in); err != nil {
		return nil, err
	}
	if err := requireID("work_id", in.WorkID); err != nil {
		return nil, err
	}
	limit, err := limitOr(in.Limit, 100, 500)
	if err != nil {
		return nil, err
	}
	skip, err := offsetCursor(in.Cursor)
	if err != nil {
		return nil, err
	}
	userID := mcpUser(ctx)
	if _, err := m.s.St.WorkByID(ctx, userID, in.WorkID); err != nil {
		return nil, mcpErr(err, "work")
	}
	list, err := m.s.St.WorkAnnotations(ctx, userID, in.WorkID)
	if err != nil {
		return nil, err
	}
	list = slices.DeleteFunc(list, func(a store.Annotation) bool {
		return a.Deleted() || (in.Kind != "" && string(a.Kind) != in.Kind)
	})
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i].Progression, list[j].Progression
		switch {
		case a == nil:
			return false
		case b == nil:
			return true
		}
		return *a < *b
	})
	out := []map[string]any{}
	for i := skip; i < len(list) && len(out) < limit; i++ {
		a := list[i]
		entry := map[string]any{"annotation_id": a.ID, "kind": string(a.Kind), "updated_at": rfc3339(a.UpdatedAt)}
		if a.Excerpt != "" {
			entry["excerpt"] = clip(a.Excerpt, 2000)
		}
		if a.Body != "" {
			entry["note"] = clip(a.Body, 4000)
		}
		if a.Color != "" {
			entry["color"] = a.Color
		}
		if a.Progression != nil {
			entry["progression"] = *a.Progression
		}
		if href := locatorHref(a.LocatorJSON); href != "" {
			entry["href"] = href
		}
		out = append(out, entry)
	}
	answer := map[string]any{"work_id": in.WorkID, "annotations": out}
	if skip+len(out) < len(list) {
		answer["next_cursor"] = strconv.Itoa(skip + len(out))
	}
	return answer, nil
}

func (m *mcpService) insightsFor(ctx context.Context) (store.StatsSnapshot, *time.Location, error) {
	snap, err := m.s.St.StatisticsSnapshot(ctx, mcpUser(ctx), nil)
	if err != nil {
		return snap, nil, err
	}
	loc, err := time.LoadLocation(snap.Timezone)
	if err != nil {
		return snap, nil, err
	}
	return snap, loc, nil
}

func validRange(v string) error {
	if v == "" || v == insights.UnboundedRange {
		return nil
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(v, "d")); err != nil || !strings.HasSuffix(v, "d") || n < 1 || n > insights.MaxRangeDays {
		return mcp.Errorf("range must look like \"30d\" or be \"all\"")
	}
	return nil
}

func validDay(name, v string) error {
	if _, err := time.Parse(insights.DayFormat, v); err != nil {
		return mcp.Errorf("%s must be a date as YYYY-MM-DD", name)
	}
	return nil
}

func (m *mcpService) statsSummary(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Range string `json:"range"`
		From  string `json:"from"`
		To    string `json:"to"`
	}
	if err := mcp.DecodeArgs(raw, &in); err != nil {
		return nil, err
	}
	if err := validRange(in.Range); err != nil {
		return nil, err
	}
	if (in.From == "") != (in.To == "") {
		return nil, mcp.Errorf("from and to go together")
	}
	if in.From != "" {
		if err := errors.Join(validDay("from", in.From), validDay("to", in.To)); err != nil {
			return nil, mcp.Errorf("from and to must be dates as YYYY-MM-DD")
		}
	}
	now := m.now()
	snap, loc, err := m.insightsFor(ctx)
	if err != nil {
		return nil, err
	}
	win := insights.ParseWindow(in.From, in.To, in.Range, insights.DefaultSummaryRange, loc, now)
	res, err := insights.Build(snap, win, now)
	if err != nil {
		return nil, err
	}
	sum := res.Summary
	out := map[string]any{
		"total_active_minutes": sum.TotalActiveMinutes, "total_pages": sum.TotalPages,
		"sessions": sum.Sessions, "streak_days": sum.StreakDays,
		"speed_progression_per_hour": sum.SpeedProgPerHour, "first_activity_day": res.FirstActivityDay,
		"timezone": snap.Timezone,
	}
	describe(win, out)
	return out, nil
}

type mcpWorkStats struct {
	insights.Work
	CurrentProgression *float64        `json:"current_progression,omitempty"`
	Status             *reading.Status `json:"status,omitempty"`
	ETASeconds         **float64       `json:"eta_seconds,omitempty"`
	CoversNarrative    *bool           `json:"covers_narrative,omitempty"`
}

func scopedWorkStats(work insights.Work, readingAllowed bool) mcpWorkStats {
	out := mcpWorkStats{Work: work}
	if readingAllowed {
		out.CurrentProgression = &work.CurrentProgression
		out.Status = &work.Status
		out.ETASeconds = &work.ETASeconds
		out.CoversNarrative = &work.CoversNarrative
	}
	return out
}

func (m *mcpService) workStats(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		WorkID string `json:"work_id"`
		Range  string `json:"range"`
	}
	if err := mcp.DecodeArgs(raw, &in); err != nil {
		return nil, err
	}
	if err := validRange(in.Range); err != nil {
		return nil, err
	}
	now := m.now()
	snap, loc, err := m.insightsFor(ctx)
	if err != nil {
		return nil, err
	}
	win := insights.ParseWindow("", "", in.Range, "", loc, now)
	res, err := insights.Build(snap, win, now)
	if err != nil {
		return nil, err
	}
	access, _ := oauth.AccessFrom(ctx)
	readingAllowed := oauth.HasScope(access, oauth.ScopeReading)
	if in.WorkID == "" {
		works := make([]mcpWorkStats, 0, len(res.Works))
		for _, work := range res.Works {
			works = append(works, scopedWorkStats(work, readingAllowed))
		}
		out := map[string]any{"works": works, "timezone": snap.Timezone}
		describe(win, out)
		return out, nil
	}
	w, ok := res.ByWork[in.WorkID]
	if !ok {
		return nil, mcp.Errorf("no statistics for that work in this range")
	}
	return scopedWorkStats(w, readingAllowed), nil
}

const maxMCPCalendarDays = 400

func (m *mcpService) calendar(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := mcp.DecodeArgs(raw, &in); err != nil {
		return nil, err
	}
	from, errFrom := time.Parse(insights.DayFormat, in.From)
	to, errTo := time.Parse(insights.DayFormat, in.To)
	if errFrom != nil || errTo != nil || to.Before(from) {
		return nil, mcp.Errorf("from and to must be dates as YYYY-MM-DD, from first")
	}
	now := m.now()
	snap, loc, err := m.insightsFor(ctx)
	if err != nil {
		return nil, err
	}
	win := insights.DayWindow(from, to, loc)
	if win.Days() > maxMCPCalendarDays {
		return nil, mcp.Errorf("at most %d days at a time", maxMCPCalendarDays)
	}
	res, err := insights.Build(snap, win, now)
	if err != nil {
		return nil, err
	}
	days := []insights.Day{}
	for _, d := range res.Days {
		if d.Minutes > 0 || d.Pages > 0 || d.Sessions > 0 {
			days = append(days, d)
		}
	}
	out := map[string]any{"from": in.From, "to": in.To, "days": days}
	describeSnapshot(snap, out)
	return out, nil
}

func (m *mcpService) listVocabulary(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		WorkID   string `json:"work_id"`
		Language string `json:"language"`
		Cursor   string `json:"cursor"`
		Limit    int    `json:"limit"`
	}
	if err := mcp.DecodeArgs(raw, &in); err != nil {
		return nil, err
	}
	limit, err := limitOr(in.Limit, 100, 500)
	if err != nil {
		return nil, err
	}
	var since int64
	if in.Cursor != "" {
		if since, err = strconv.ParseInt(in.Cursor, 10, 64); err != nil || since < 0 {
			return nil, mcp.Errorf("invalid cursor")
		}
	}
	userID := mcpUser(ctx)
	out := []map[string]any{}
	for len(out) < limit {
		page, err := m.s.St.VocabularyChanges(ctx, userID, since, limit, 1<<20)
		if err != nil {
			return nil, err
		}
		for _, w := range page.Words {
			if w.Deleted() || (in.Language != "" && !strings.EqualFold(w.Language, in.Language)) {
				continue
			}
			entry := map[string]any{"word": w.Word, "language": w.Language, "added_at": rfc3339(w.AddedAt)}
			if w.Definition != nil && *w.Definition != "" {
				entry["definition"] = clip(*w.Definition, 2000)
			}
			if w.Notes != "" {
				entry["notes"] = clip(w.Notes, 2000)
			}
			occurrences := []map[string]any{}
			matched := in.WorkID == ""
			for _, o := range w.Occurrences {
				if o.WorkID != nil && *o.WorkID == in.WorkID {
					matched = true
				}
				oc := map[string]any{"encounters": o.Encounters}
				if o.BookTitle != "" {
					oc["book_title"] = o.BookTitle
				}
				if o.WorkID != nil {
					oc["work_id"] = *o.WorkID
				}
				if o.Sentence != nil {
					oc["sentence"] = clip(*o.Sentence, 1000)
				}
				occurrences = append(occurrences, oc)
			}
			if !matched {
				continue
			}
			entry["occurrences"] = occurrences
			out = append(out, entry)
		}
		since = page.NextCursor
		if !page.HasMore {
			return map[string]any{"words": out}, nil
		}
	}
	return map[string]any{"words": out, "next_cursor": strconv.FormatInt(since, 10)}, nil
}
