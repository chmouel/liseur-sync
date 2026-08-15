package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// maxMetadataBodyBytes bounds an edit. A metadata form is a page of text,
// and anything larger is either a mistake or an attempt to make the
// server hold something for free.
const maxMetadataBodyBytes = 256 << 10

// entityKinds maps the plural path segment a client uses to the kind the
// store knows. Reading the kind out of a fixed table is what keeps a
// caller's string away from the table names the store builds queries
// from.
var entityKinds = map[string]store.EntityKind{
	"series":       store.EntitySeries,
	"contributors": store.EntityContributor,
	"tags":         store.EntityTag,
	"genres":       store.EntityGenre,
}

// HandleBookMetadata implements GET /v1/books/{id}/metadata — everything
// an edit form needs: each field's value, which stage supplied it, and
// whether a person has locked it.
//
// It is separate from GET /v1/books/{id} because provenance is editor
// data. A catalog client wants the title; only something about to change
// it needs to know where the title came from.
func (s *Server) HandleBookMetadata(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	meta, err := s.St.CatalogBookMetadata(
		r.Context(), tok.UserID, r.PathValue("id"), store.LibraryRoleRead)
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return
	}
	writeJSON(w, http.StatusOK, bookMetadataJSON(meta))
}

// metadataEditRequest is the wire shape of an edit. Every field is a
// pointer, because a form that submits one field must not be read as an
// assertion about the others: absent is silence, not "clear this".
type metadataEditRequest struct {
	Revision int64 `json:"revision"`

	Title         *scalarEditJSON `json:"title"`
	Subtitle      *scalarEditJSON `json:"subtitle"`
	Description   *scalarEditJSON `json:"description"`
	Publisher     *scalarEditJSON `json:"publisher"`
	PublishedDate *scalarEditJSON `json:"published_date"`

	Tags         *setEditJSON `json:"tags"`
	Genres       *setEditJSON `json:"genres"`
	Languages    *setEditJSON `json:"languages"`
	Series       *setEditJSON `json:"series"`
	Contributors *setEditJSON `json:"contributors"`
}

type scalarEditJSON struct {
	Value  string `json:"value"`
	Unlock bool   `json:"unlock"`
}

type setEditJSON struct {
	Entries []entryEditJSON `json:"entries"`
	Unlock  bool            `json:"unlock"`
}

type entryEditJSON struct {
	Name     string   `json:"name"`
	Position *float64 `json:"position"`
	Role     string   `json:"role"`
}

func (e *scalarEditJSON) edit() *metadata.ScalarEdit {
	if e == nil {
		return nil
	}
	return &metadata.ScalarEdit{Value: e.Value, Unlock: e.Unlock}
}

func (e *setEditJSON) edit() *metadata.SetEdit {
	if e == nil {
		return nil
	}
	entries := make([]metadata.EntryEdit, 0, len(e.Entries))
	for _, entry := range e.Entries {
		entries = append(entries, metadata.EntryEdit{
			Name: entry.Name, Position: entry.Position, Role: entry.Role,
		})
	}
	return &metadata.SetEdit{Entries: entries, Unlock: e.Unlock}
}

// HandleUpdateBookMetadata implements PUT /v1/books/{id}/metadata.
//
// The revision the client read is required rather than optional. Two
// people editing one book is the ordinary case in a shared library, and
// last-write-wins would silently discard the first person's work; a
// conflict they can see is worth more than a merge nobody asked for.
func (s *Server) HandleUpdateBookMetadata(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req metadataEditRequest
	if err := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, maxMetadataBodyBytes),
	).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if req.Revision < 1 {
		writeError(w, http.StatusBadRequest, "revision is required")
		return
	}
	// Read under manage: a reader must not learn a book's provenance by
	// failing to edit it any more than by asking for it.
	current, err := s.St.CatalogBookMetadata(
		r.Context(), tok.UserID, r.PathValue("id"), store.LibraryRoleManage)
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return
	}
	if current.Book.Revision != req.Revision {
		writeError(w, http.StatusConflict,
			"this book changed while you were editing it")
		return
	}
	next, changed := metadata.ApplyManualEdit(current, metadata.ManualEdit{
		Title:         req.Title.edit(),
		Subtitle:      req.Subtitle.edit(),
		Description:   req.Description.edit(),
		Publisher:     req.Publisher.edit(),
		PublishedDate: req.PublishedDate.edit(),
		Tags:          req.Tags.edit(),
		Genres:        req.Genres.edit(),
		Languages:     req.Languages.edit(),
		Series:        req.Series.edit(),
		Contributors:  req.Contributors.edit(),
	}, func() string { return uuid.New().String() })
	if !changed {
		// Nothing moved, so nothing is written and the revision does not
		// advance. Bumping it would make a resubmitted form invalidate
		// the editor that sent it.
		writeJSON(w, http.StatusOK, bookMetadataJSON(current))
		return
	}
	request := store.ApplyBookMetadataRequest{
		Metadata:         next,
		ExpectedRevision: current.Book.Revision,
		UpdatedAt:        time.Now().UTC(),
	}
	if err := store.ValidateApplyBookMetadata(request); err != nil {
		writeError(w, http.StatusBadRequest, "that metadata is not valid")
		return
	}
	applied, err := s.St.ApplyCatalogBookMetadata(r.Context(), tok.UserID, request)
	if err != nil {
		if errors.Is(err, store.ErrStaleRevision) {
			writeError(w, http.StatusConflict,
				"this book changed while you were editing it")
			return
		}
		writeCatalogError(w, err, "book not found")
		return
	}
	writeJSON(w, http.StatusOK, bookMetadataJSON(applied))
}

func bookMetadataJSON(m store.BookMetadata) map[string]any {
	scalar := func(value string, source store.MetadataSource, locked bool) map[string]any {
		return map[string]any{
			"value": value, "source": string(source), "locked": locked,
		}
	}
	tags := func(rows []store.BookTaxon) []map[string]any {
		out := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			out = append(out, map[string]any{
				"id": row.ID, "name": row.Name,
				"source": string(row.Source), "locked": row.Locked,
			})
		}
		return out
	}
	series := make([]map[string]any, 0, len(m.Series))
	for _, row := range m.Series {
		entry := map[string]any{
			"id": row.SeriesID, "name": row.Name,
			"source": string(row.Source), "locked": row.Locked,
		}
		// Omitted rather than sent as null or zero: a book with no place
		// in its series has no position, and 0 would be a place.
		if row.Position != nil {
			entry["position"] = *row.Position
		}
		series = append(series, entry)
	}
	contributors := make([]map[string]any, 0, len(m.Contributors))
	for _, row := range m.Contributors {
		contributors = append(contributors, map[string]any{
			"id": row.ContributorID, "name": row.Name, "role": row.Role,
			"source": string(row.Source), "locked": row.Locked,
		})
	}
	languages := make([]map[string]any, 0, len(m.Languages))
	for _, row := range m.Languages {
		languages = append(languages, map[string]any{
			"language": row.Language,
			"source":   string(row.Source), "locked": row.Locked,
		})
	}
	identifiers := make([]map[string]any, 0, len(m.Identifiers))
	for _, row := range m.Identifiers {
		identifiers = append(identifiers, map[string]any{
			"scheme": row.Scheme, "value": row.Value,
			"source": string(row.Source), "locked": row.Locked,
		})
	}
	return map[string]any{
		"book_id":    m.Book.ID,
		"library_id": m.Book.LibraryID,
		// The revision a client must send back to write. Without it an
		// editor cannot tell a conflict from a success.
		"revision":       m.Book.Revision,
		"title":          scalar(m.Book.Title, m.Book.TitleSource, m.Book.TitleLocked),
		"subtitle":       scalar(m.Book.Subtitle, m.Book.SubtitleSource, m.Book.SubtitleLocked),
		"description":    scalar(m.Book.Description, m.Book.DescriptionSource, m.Book.DescriptionLocked),
		"publisher":      scalar(m.Book.Publisher, m.Book.PublisherSource, m.Book.PublisherLocked),
		"published_date": scalar(m.Book.PublishedDate, m.Book.PublishedDateSource, m.Book.PublishedDateLocked),
		"tags":           tags(m.Tags),
		"genres":         tags(m.Genres),
		"series":         series,
		"contributors":   contributors,
		"languages":      languages,
		// Read-only here: identifiers feed work identity, so changing one
		// moves a reader's history between books (ADR-0003).
		"identifiers": identifiers,
		"set_locks": map[string]any{
			"tags": m.Book.SetLocks.Tags, "genres": m.Book.SetLocks.Genres,
			"languages":    m.Book.SetLocks.Languages,
			"series":       m.Book.SetLocks.Series,
			"contributors": m.Book.SetLocks.Contributors,
			"identifiers":  m.Book.SetLocks.Identifiers,
		},
	}
}

// entityRequest pulls the library, kind and limit every entity route
// needs, answering the request itself when any of them is wrong.
func entityRequest(w http.ResponseWriter, r *http.Request) (string, store.EntityKind, bool) {
	libraryID := r.PathValue("library")
	if libraryID == "" || len(libraryID) > maxLibraryIDBytes {
		writeError(w, http.StatusNotFound, "library not found")
		return "", "", false
	}
	kind, ok := entityKinds[r.PathValue("kind")]
	if !ok {
		writeError(w, http.StatusNotFound, "no such kind of entity")
		return "", "", false
	}
	return libraryID, kind, true
}

// HandleLibraryEntities implements
// GET /v1/libraries/{library}/entities/{kind}.
func (s *Server) HandleLibraryEntities(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libraryID, kind, ok := entityRequest(w, r)
	if !ok {
		return
	}
	limit, err := catalogPageSize(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The cursor is the last normalized name of the previous page, which
	// the client already has, so it needs no encoding of its own.
	after := r.URL.Query().Get("after")
	if len(after) > 512 {
		writeError(w, http.StatusBadRequest, "cursor is too long")
		return
	}
	entities, err := s.St.ListCatalogEntities(
		r.Context(), tok.UserID, libraryID, kind, after, limit)
	if err != nil {
		writeCatalogError(w, err, "library not found")
		return
	}
	out := make([]map[string]any, 0, len(entities))
	for _, entity := range entities {
		out = append(out, map[string]any{
			"id": entity.ID, "name": entity.Name,
			"book_count": entity.BookCount,
		})
	}
	body := map[string]any{"entities": out}
	if len(entities) == limit {
		body["next_after"] = entities[len(entities)-1].NormalizedName
	}
	writeJSON(w, http.StatusOK, body)
}

// HandleEntityBooks implements
// GET /v1/libraries/{library}/entities/{kind}/{entity}/books.
func (s *Server) HandleEntityBooks(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libraryID, kind, ok := entityRequest(w, r)
	if !ok {
		return
	}
	limit, err := catalogPageSize(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	after, err := decodeCatalogCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entity, err := s.St.CatalogEntityByID(
		r.Context(), tok.UserID, libraryID, r.PathValue("entity"), kind)
	if err != nil {
		writeCatalogError(w, err, "entity not found")
		return
	}
	books, err := s.St.ListBooksByEntity(r.Context(), tok.UserID, libraryID,
		entity.ID, kind, after, limit)
	if err != nil {
		writeCatalogError(w, err, "entity not found")
		return
	}
	out, err := s.catalogBooksJSON(r.Context(), tok.UserID, books)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}
	body := map[string]any{
		"entity": map[string]any{
			"id": entity.ID, "name": entity.Name,
			"book_count": entity.BookCount,
		},
		"books": out,
	}
	if len(books) == limit {
		last := books[len(books)-1]
		body["next_cursor"] = encodeCatalogCursor(store.CatalogBookCursor{
			CreatedAt: last.CreatedAt, ID: last.ID,
		})
	}
	writeJSON(w, http.StatusOK, body)
}

// HandleRenameEntity implements
// PATCH /v1/libraries/{library}/entities/{kind}/{entity}.
func (s *Server) HandleRenameEntity(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libraryID, kind, ok := entityRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, 64<<10),
	).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	entity, err := s.St.RenameCatalogEntity(r.Context(), tok.UserID,
		libraryID, r.PathValue("entity"), kind, req.Name)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			// The client is told what to do about it, because the answer
			// is a different operation rather than a different name.
			writeError(w, http.StatusConflict,
				"another entity already has that name; merge them instead")
			return
		}
		if errors.Is(err, store.ErrInvalidTransition) {
			writeError(w, http.StatusBadRequest, "that name is not usable")
			return
		}
		writeCatalogError(w, err, "entity not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": entity.ID, "name": entity.Name, "book_count": entity.BookCount,
	})
}

// HandleMergeEntities implements
// POST /v1/libraries/{library}/entities/{kind}/merge.
//
// Merging is a route of its own rather than a side effect of renaming,
// because folding two entities into one is irreversible and a person
// should have had to ask for it.
func (s *Server) HandleMergeEntities(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libraryID, kind, ok := entityRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		From string `json:"from"`
		Into string `json:"into"`
	}
	if err := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, 64<<10),
	).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	moved, err := s.St.MergeCatalogEntities(r.Context(), tok.UserID,
		libraryID, req.From, req.Into, kind, time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrInvalidTransition) {
			writeError(w, http.StatusBadRequest,
				"a merge needs two different entities")
			return
		}
		writeCatalogError(w, err, "entity not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moved": moved})
}
