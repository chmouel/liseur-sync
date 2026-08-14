package webui

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/google/uuid"
)

// The metadata form submits every field on every save, so the handler
// cannot tell an edit from an untouched field by looking at the values
// alone. Each input therefore carries a hidden copy of what was
// rendered, and only a field whose text actually moved becomes a manual
// edit. Without that, opening a book and pressing Save would lock every
// field on it — turning a glance into an assertion.
const wasSuffix = "_was"

// editedScalar returns the edit a form field describes, or nil when the
// user left it alone. An explicit unlock wins over the value, because a
// user who asks for the extractors back is not also asserting a value.
func editedScalar(r *http.Request, field string) *metadata.ScalarEdit {
	if r.FormValue(field+"_unlock") != "" {
		return &metadata.ScalarEdit{Unlock: true}
	}
	value := strings.TrimSpace(r.FormValue(field))
	if value == strings.TrimSpace(r.FormValue(field+wasSuffix)) {
		return nil
	}
	return &metadata.ScalarEdit{Value: value}
}

// editedSet is the same idea for a set, with the parse deferred until
// something is known to have changed: a set the user did not touch must
// not be rewritten just because the text form round-trips imperfectly.
func editedSet(
	r *http.Request, field string, parse func(string) []metadata.EntryEdit,
) *metadata.SetEdit {
	if r.FormValue(field+"_unlock") != "" {
		return &metadata.SetEdit{Unlock: true}
	}
	value := strings.TrimSpace(r.FormValue(field))
	if value == strings.TrimSpace(r.FormValue(field+wasSuffix)) {
		return nil
	}
	return &metadata.SetEdit{Entries: parse(value)}
}

// handleEditBookMetadata records a librarian's corrections. It is a
// mutation, so it checks CSRF and answers with a redirect: a reload must
// not resubmit an edit whose revision has since moved on.
func (s *Server) handleEditBookMetadata(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	bookID := r.PathValue("id")
	if !s.checkCSRF(r, a) {
		s.bookResult(w, r, bookID, "", "that form had expired; try again")
		return
	}
	// Read under manage. A reader must not learn a book's provenance by
	// failing to edit it any more than by asking for it.
	current, err := s.St.CatalogBookMetadata(
		r.Context(), u.ID, bookID, store.LibraryRoleManage)
	if err != nil {
		s.bookResult(w, r, bookID, "",
			"that book does not exist, or you cannot edit it")
		return
	}
	edit := metadata.ManualEdit{
		Title:         editedScalar(r, "title"),
		Subtitle:      editedScalar(r, "subtitle"),
		Description:   editedScalar(r, "description"),
		Publisher:     editedScalar(r, "publisher"),
		PublishedDate: editedScalar(r, "published"),
		Tags:          editedSet(r, "tags", metadata.ParseNameList),
		Genres:        editedSet(r, "genres", metadata.ParseNameList),
		Languages:     editedSet(r, "languages", metadata.ParseNameList),
		Series:        editedSet(r, "series", metadata.ParseSeriesList),
		Contributors:  editedSet(r, "contributors", metadata.ParseContributorList),
	}
	next, changed := metadata.ApplyManualEdit(
		current, edit, func() string { return uuid.New().String() })
	if !changed {
		s.bookResult(w, r, bookID, "nothing to save", "")
		return
	}
	request := store.ApplyBookMetadataRequest{
		Metadata:         next,
		ExpectedRevision: current.Book.Revision,
		UpdatedAt:        time.Now().UTC(),
	}
	if err := store.ValidateApplyBookMetadata(request); err != nil {
		s.bookResult(w, r, bookID, "", "that metadata is not valid")
		return
	}
	if _, err := s.St.ApplyCatalogBookMetadata(r.Context(), u.ID, request); err != nil {
		if errors.Is(err, store.ErrStaleRevision) {
			s.bookResult(w, r, bookID, "",
				"somebody else changed this book while you were editing it; "+
					"reload to see their version")
			return
		}
		s.bookResult(w, r, bookID, "", "that edit could not be saved")
		return
	}
	s.bookResult(w, r, bookID, "saved", "")
}

// bookResult sends the librarian back to the book they were editing,
// with a sentence about what happened.
func (s *Server) bookResult(
	w http.ResponseWriter, r *http.Request, bookID, notice, problem string,
) {
	q := url.Values{}
	if notice != "" {
		q.Set("notice", notice)
	}
	if problem != "" {
		q.Set("problem", problem)
	}
	target := relPrefix(r.URL.Path) + "books/" + url.PathEscape(bookID)
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	redirectRel(w, target, http.StatusSeeOther)
}

// metadataEditView builds the form's state from stored metadata. Every
// field carries both its text and its lock, because a lock is the thing
// the user most needs to see: it explains why a rescan stopped changing
// a value they no longer agree with.
func metadataEditView(m store.BookMetadata) MetadataEditView {
	scalar := func(value string, source store.MetadataSource, locked bool) MetadataFieldView {
		return MetadataFieldView{
			Value: value, Source: sourceLabel(source), Locked: locked,
		}
	}
	b := m.Book
	v := MetadataEditView{
		Title:     scalar(b.Title, b.TitleSource, b.TitleLocked),
		Subtitle:  scalar(b.Subtitle, b.SubtitleSource, b.SubtitleLocked),
		Publisher: scalar(b.Publisher, b.PublisherSource, b.PublisherLocked),
		Published: scalar(b.PublishedDate, b.PublishedDateSource, b.PublishedDateLocked),
		Description: scalar(
			b.Description, b.DescriptionSource, b.DescriptionLocked),
	}
	tagNames := make([]string, 0, len(m.Tags))
	for _, row := range m.Tags {
		tagNames = append(tagNames, row.Name)
	}
	genreNames := make([]string, 0, len(m.Genres))
	for _, row := range m.Genres {
		genreNames = append(genreNames, row.Name)
	}
	languages := make([]string, 0, len(m.Languages))
	for _, row := range m.Languages {
		languages = append(languages, row.Language)
	}
	series := make([]metadata.EntryEdit, 0, len(m.Series))
	for _, row := range m.Series {
		series = append(series, metadata.EntryEdit{
			Name: row.Name, Position: row.Position,
		})
	}
	contributors := make([]metadata.EntryEdit, 0, len(m.Contributors))
	for _, row := range m.Contributors {
		contributors = append(contributors, metadata.EntryEdit{
			Name: row.Name, Role: row.Role,
		})
	}
	v.Tags = MetadataFieldView{
		Value: metadata.FormatNameList(tagNames), Locked: b.SetLocks.Tags,
	}
	v.Genres = MetadataFieldView{
		Value: metadata.FormatNameList(genreNames), Locked: b.SetLocks.Genres,
	}
	v.Languages = MetadataFieldView{
		Value: metadata.FormatNameList(languages), Locked: b.SetLocks.Languages,
	}
	v.Series = MetadataFieldView{
		Value: metadata.FormatSeriesList(series), Locked: b.SetLocks.Series,
	}
	v.Contributors = MetadataFieldView{
		Value:  metadata.FormatContributorList(contributors),
		Locked: b.SetLocks.Contributors,
	}
	return v
}

// sourceLabel says where a value came from in words a librarian can act
// on. "manual" is spelled as the person it was, because the useful fact
// is that somebody decided this and not that a program did.
func sourceLabel(source store.MetadataSource) string {
	switch source {
	case store.MetadataEmbedded:
		return "from the file"
	case store.MetadataFilename:
		return "from the filename"
	case store.MetadataExternal:
		return "from an external source"
	case store.MetadataCalibre:
		return "from Calibre"
	case store.MetadataManual:
		return "edited by hand"
	default:
		return ""
	}
}
