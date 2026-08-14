package webui

// Books handlers: the browser surface of the content server. All of the
// storage work is delegated — this file decides what a page shows, not
// how bytes are stored or served.

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/google/uuid"
)

// Uploader stages a web-form upload through the same path the API uses.
// The UI must not grow its own ingest: the ordering rules around staged
// bytes and their cleanup are subtle, and two implementations would
// diverge.
type Uploader interface {
	StageUpload(ctx context.Context, userID, libraryID, key string, body io.Reader) (store.IngestJob, bool, error)
}

// Downloader serves a book's bytes for a caller identified some other
// way than by a token — here, by a session cookie. Reusing it keeps the
// media-type allowlist and filename sanitizing in one place.
type Downloader interface {
	ServeBookDownload(w http.ResponseWriter, r *http.Request, userID, bookID string)
}

// booksPageSize keeps the page short enough to read. The UI paginates
// with the same opaque cursor the API hands out.
const booksPageSize = 25

func (s *Server) handleBooks(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	libs, err := s.St.ListLibraries(r.Context(), u.ID, store.LibraryRoleRead)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	v := BooksView{
		Notice:  r.URL.Query().Get("notice"),
		Problem: r.URL.Query().Get("problem"),
		View:    readPrefs(r).View,
		Back:    "books",
	}
	selected := r.URL.Query().Get("library")
	if selected == "" && len(libs) > 0 {
		selected = libs[0].Library.ID
	}
	for _, l := range libs {
		v.Libraries = append(v.Libraries, LibraryOption{
			ID:       l.Library.ID,
			Name:     l.Library.Name,
			CanWrite: l.Role == store.LibraryRoleManage,
			Selected: l.Library.ID == selected,
		})
		if l.Library.ID == selected {
			v.Selected = selected
			v.CanWrite = l.Role == store.LibraryRoleManage
		}
	}
	// A library id from the query string that the user cannot read is
	// treated as no selection at all, rather than as an error: it is
	// most often a stale bookmark.
	if v.Selected == "" {
		booksPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
			Render(r.Context(), w)
		return
	}

	loc := userLoc(u)
	books, next, err := s.listBooksPage(r, u.ID, v.Selected)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	bookIDs := make([]string, 0, len(books))
	for _, b := range books {
		bookIDs = append(bookIDs, b.ID)
	}
	authors, _ := s.St.CatalogAuthorsForBooks(r.Context(), u.ID, bookIDs)

	for _, b := range books {
		row := BookRow{
			ID:     b.ID,
			Title:  b.Title,
			Author: authors[b.ID],
			Added:  b.CreatedAt.In(loc).Format("Jan 2, 2006"),
		}
		files, err := s.St.ListBookFiles(r.Context(), u.ID, b.ID, store.LibraryRoleRead)
		if err == nil {
			for _, f := range files {
				if f.Availability == store.BookFileAvailable {
					row.CanGet = true
					row.CanRead = row.CanRead || isEPUB(f.MediaType)
				}
			}
		}
		v.Books = append(v.Books, row)
	}
	if next != "" {
		v.NextURL = "books?library=" + url.QueryEscape(v.Selected) +
			"&cursor=" + url.QueryEscape(next)
	}
	// The view toggle comes back to this library rather than to the
	// first one, which is what "keep looking at what I was looking at"
	// means.
	v.Back = "books?library=" + url.QueryEscape(v.Selected)

	// An htmx continuation asks for more of one list, not for the page
	// around it: answering with the whole document would append a second
	// copy of the shell to the grid, and would make the librarian's
	// review queries run again for markup nobody is going to look at.
	if isHTMXRequest(r) {
		booksFragment(relPrefix(r.URL.Path), csrfFor(a), v).Render(r.Context(), w)
		return
	}
	// Only a librarian uploads, so only a librarian is shown what became
	// of an upload. Without this the page is silent about a file that was
	// accepted and then rejected, which looks exactly like losing it.
	if v.CanWrite {
		v.Uploads = s.uploadActivity(r, u.ID, v.Selected, loc)
		v.Trash = s.trashActivity(r, u.ID, v.Selected, loc)
		// Shown to librarians only, for the same reason as the trash: it
		// is a list of things to do something about, and only a librarian
		// can do anything about them.
		v.Duplicates = s.duplicateGroups(r, u.ID, v.Selected)
		v.Similar = s.similarGroups(r, u.ID, v.Selected, loc)
		// Same audience, same reason: a watched file that changed is a
		// decision waiting for somebody who can make it.
		v.Review = s.reviewRows(r, u.ID, v.Selected)
	}
	booksPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
		Render(r.Context(), w)
}

// isHTMXRequest reports whether this is htmx asking for a fragment.
// Nothing about access depends on it — the header is a hint about what
// to render, never about what may be read — so an attacker setting it
// gains a page of markup they were already entitled to.
func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// uploadActivityLimit keeps the section a status list rather than a log.
const uploadActivityLimit = 10

// uploadActivity reports uploads that have not reached the catalog. A
// failure here is not the page's failure: the books still render.
func (s *Server) uploadActivity(
	r *http.Request, userID, libraryID string, loc *time.Location,
) []UploadRow {
	jobs, err := s.St.ListIngestActivity(
		r.Context(), userID, libraryID, uploadActivityLimit)
	if err != nil {
		// Swallowing this silently is what made the original problem so
		// hard to see, so it is at least recorded.
		slog.Error("upload activity unavailable",
			"library", libraryID, "err", err)
		return nil
	}
	rows := make([]UploadRow, 0, len(jobs))
	for _, job := range jobs {
		row := UploadRow{
			When:  job.CreatedAt.In(loc).Format("Jan 2, 2006 15:04"),
			State: "still being read",
		}
		switch job.State {
		case store.IngestQuarantined, store.IngestFailed:
			row.Reason = uploadFailureReason(job)
		default:
			row.Pending = true
		}
		rows = append(rows, row)
	}
	return rows
}

// uploadFailureReason turns an ingest error code into an explanation with
// something to do about it. The codes come from EPUB validation, so they
// describe the file, not the server.
func uploadFailureReason(job store.IngestJob) string {
	code := ""
	if job.ErrorCode != nil {
		code = *job.ErrorCode
	}
	switch code {
	case "invalid_epub":
		return "Not a readable EPUB. Re-export it, or convert it first."
	case "unsupported_drm":
		return "This EPUB is DRM-protected and cannot be stored."
	case "unsafe_archive":
		return "The archive is malformed and was refused."
	case "archive_limits":
		return "The EPUB is too large or too complex for this server."
	case "":
		return "The upload could not be processed."
	}
	return "The upload could not be processed (" + code + ")."
}

// listBooksPage returns one page and the cursor for the next, or "" when
// this is the last one.
func (s *Server) listBooksPage(
	r *http.Request, userID, libraryID string,
) ([]store.CatalogBook, string, error) {
	after, err := decodeBooksCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		// A mangled cursor restarts at the beginning. There is nothing
		// for a reader to do about it, and an error page would be worse
		// than the first page.
		after = nil
	}
	books, err := s.St.ListCatalogBooks(r.Context(), userID, libraryID, after, booksPageSize)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if len(books) < booksPageSize {
		return books, "", nil
	}
	last := books[len(books)-1]
	return books, encodeBooksCursor(store.CatalogBookCursor{
		CreatedAt: last.CreatedAt, ID: last.ID,
	}), nil
}

func (s *Server) handleBook(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	v, ok := s.bookView(r, u, r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	v.Notice = r.URL.Query().Get("notice")
	v.Problem = r.URL.Query().Get("problem")
	bookPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
		Render(r.Context(), w)
}

// bookView assembles the book page. It is separate from the handler
// because the lookup panel renders the same page with an extra section
// on it, and a second assembly would drift from this one.
func (s *Server) bookView(r *http.Request, u *store.User, bookID string) (BookView, bool) {
	book, err := s.St.CatalogBookByID(r.Context(), u.ID, bookID, store.LibraryRoleRead)
	if err != nil {
		return BookView{}, false
	}
	v := BookView{
		ID: book.ID, Title: book.Title, Subtitle: book.Subtitle,
		Description: book.Description, Publisher: book.Publisher,
		Published: book.PublishedDate, LibraryID: book.LibraryID,
		Added: book.CreatedAt.In(userLoc(u)).Format("Jan 2, 2006"),
	}
	// Chips are a reader's fact about the book, so they are read with
	// read access. The same rows serve the edit form below, but only for
	// somebody who could submit it.
	if meta, err := s.St.CatalogBookMetadata(
		r.Context(), u.ID, bookID, store.LibraryRoleRead,
	); err == nil {
		v.Authors, v.Byline = contributorChips(book.LibraryID, meta.Contributors)
		for _, ser := range meta.Series {
			v.Series = append(v.Series, ChipLink{
				Name: ser.Name,
				URL:  entityURL(book.LibraryID, "series", ser.SeriesID),
			})
		}
		for _, t := range meta.Tags {
			v.Tags = append(v.Tags, ChipLink{
				Name: t.Name, URL: entityURL(book.LibraryID, "tags", t.ID),
			})
		}
		for _, g := range meta.Genres {
			v.Genres = append(v.Genres, ChipLink{
				Name: g.Name, URL: entityURL(book.LibraryID, "genres", g.ID),
			})
		}
	}
	// The edit form is only built for somebody who could submit it. A
	// reader asking for this page must not be told a value's provenance,
	// which is librarian's information rather than a fact about the book.
	if full, err := s.St.CatalogBookMetadata(
		r.Context(), u.ID, bookID, store.LibraryRoleManage,
	); err == nil {
		v.CanWrite = true
		v.Edit = metadataEditView(full)
		v.Lookup = LookupView{
			Offered:  s.Lookup != nil,
			Revision: strconv.FormatInt(full.Book.Revision, 10),
		}
	}
	files, err := s.St.ListBookFiles(r.Context(), u.ID, bookID, store.LibraryRoleRead)
	if err == nil {
		for _, f := range files {
			if f.Availability != store.BookFileAvailable {
				continue
			}
			v.Files = append(v.Files, BookFileRow{
				Name: f.OriginalFilename, MediaType: f.MediaType, SHA256: f.BlobSHA256,
			})
			v.CanRead = v.CanRead || isEPUB(f.MediaType)
		}
	}
	return v, true
}

// entityURL is the page listing everything else that claims an entity.
func entityURL(libraryID, kind, entityID string) string {
	return "libraries/" + url.PathEscape(libraryID) + "/" + kind + "/" +
		url.PathEscape(entityID)
}

// contributorChips returns the links and the byline. The byline names
// authors only when the file said who they are, and falls back to every
// contributor when it did not: a book credited solely to a translator
// should still say so rather than say nothing.
func contributorChips(libraryID string, rows []store.BookContributor) ([]ChipLink, string) {
	chips := make([]ChipLink, 0, len(rows))
	var authors []string
	var everyone []string
	for _, c := range rows {
		chips = append(chips, ChipLink{
			Name: c.Name,
			URL:  entityURL(libraryID, "contributors", c.ContributorID),
		})
		everyone = append(everyone, c.Name)
		if c.Role == "" || strings.EqualFold(c.Role, "author") ||
			strings.EqualFold(c.Role, "aut") {
			authors = append(authors, c.Name)
		}
	}
	if len(authors) == 0 {
		authors = everyone
	}
	return chips, strings.Join(authors, ", ")
}

// handleBookDownload hands off to the API's download, which owns the
// rules about what a stored file may claim to be.
func (s *Server) handleBookDownload(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if s.Downloads == nil {
		http.Error(w, "content storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	s.Downloads.ServeBookDownload(w, r, u.ID, r.PathValue("id"))
}

// handleUploadBook takes the file from the form and stages it. It is a
// mutation, so it checks CSRF, and it answers with a redirect rather
// than a page so that a reload does not re-upload.
func (s *Server) handleUploadBook(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if s.Uploads == nil {
		s.uploadResult(w, r, "", "", "content storage is unavailable")
		return
	}
	reader, err := r.MultipartReader()
	if err != nil {
		s.uploadResult(w, r, "", "", "that was not a file upload")
		return
	}
	// The CSRF token travels in the multipart body, so it is only
	// readable after the parts before the file have been consumed. The
	// form puts it first for exactly that reason: the file must not be
	// streamed anywhere until the request is known to be genuine.
	fields := map[string]string{}
	var part *multipartPart
	for {
		p, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.uploadResult(w, r, fields["library"], "", "the upload was malformed")
			return
		}
		if p.FileName() != "" && p.FormName() == "file" {
			part = &multipartPart{p}
			break
		}
		value, err := io.ReadAll(io.LimitReader(p, 4<<10))
		p.Close()
		if err != nil {
			s.uploadResult(w, r, fields["library"], "", "the upload was malformed")
			return
		}
		fields[p.FormName()] = string(value)
	}
	if subtle.ConstantTimeCompare([]byte(fields["csrf"]), []byte(csrfFor(a))) != 1 {
		http.Error(w, "bad csrf", http.StatusForbidden)
		return
	}
	library := strings.TrimSpace(fields["library"])
	if library == "" {
		s.uploadResult(w, r, "", "", "choose a library first")
		return
	}
	if part == nil {
		s.uploadResult(w, r, library, "", "choose a file first")
		return
	}

	// The key makes a double submit idempotent. A browser has none to
	// offer, so one is generated per request: a user who clicks twice
	// gets two jobs, but the content-addressed store gives them one
	// blob, and the second job is a no-op once ingest deduplicates it.
	key := "web-" + uuid.New().String()
	_, _, err = s.Uploads.StageUpload(r.Context(), u.ID, library, key, part)
	if err != nil {
		part.Close()
		s.uploadResult(w, r, library, "", uploadProblem(err))
		return
	}
	part.Close()
	s.uploadResult(w, r, library, "Upload stored. The book appears once it has been read.", "")
}

// multipartPart exists so the reader handed to StageUpload cannot be
// closed by it: closing a multipart part drains the rest of it, which
// after a refused upload is the data we declined to read.
type multipartPart struct{ io.ReadCloser }

func (s *Server) uploadResult(w http.ResponseWriter, r *http.Request, library, notice, problem string) {
	q := url.Values{}
	if library != "" {
		q.Set("library", library)
	}
	if notice != "" {
		q.Set("notice", notice)
	}
	if problem != "" {
		q.Set("problem", problem)
	}
	target := "books"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	redirectRel(w, relPrefix(r.URL.Path)+target, http.StatusSeeOther)
}

// uploadProblem turns a staging failure into something a person can act
// on. The API's status codes are the authority on what went wrong; this
// only translates them.
func uploadProblem(err error) string {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "that library does not exist, or you cannot write to it"
	case errors.Is(err, store.ErrConflict),
		errors.Is(err, store.ErrIdempotencyConflict):
		return "that upload is already in progress"
	}
	var quota *store.QuotaExceededError
	if errors.As(err, &quota) {
		return "the file does not fit in your storage quota"
	}
	return "the upload failed; the file may be too large"
}

func encodeBooksCursor(c store.CatalogBookCursor) string {
	return c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID
}

func decodeBooksCursor(raw string) (*store.CatalogBookCursor, error) {
	if raw == "" {
		return nil, nil
	}
	at, id, ok := strings.Cut(raw, "|")
	if !ok || id == "" {
		return nil, errors.New("malformed cursor")
	}
	parsed, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, errors.New("malformed cursor")
	}
	return &store.CatalogBookCursor{CreatedAt: parsed, ID: id}, nil
}

// handleDeleteBook moves a book to the trash. It is deliberately not a
// permanent deletion: the bytes stay until retention runs out, so a
// misclick is recoverable from the same page it happened on.
func (s *Server) handleDeleteBook(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	library := strings.TrimSpace(r.FormValue("library"))
	now := time.Now().UTC()
	retention := time.Duration(s.Cfg.Content.TrashRetentionHours) * time.Hour
	book, err := s.St.TrashCatalogBook(
		r.Context(), u.ID, r.PathValue("id"), now, now.Add(retention))
	if err != nil {
		s.uploadResult(w, r, library, "", trashProblem(err))
		return
	}
	if library == "" {
		library = book.LibraryID
	}
	s.uploadResult(w, r, library,
		"Deleted. You can put it back until it is purged.", "")
}

// handleRestoreBook is the undo for handleDeleteBook.
func (s *Server) handleRestoreBook(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	library := strings.TrimSpace(r.FormValue("library"))
	book, err := s.St.RestoreCatalogBook(
		r.Context(), u.ID, r.PathValue("id"), time.Now().UTC())
	if err != nil {
		s.uploadResult(w, r, library, "", trashProblem(err))
		return
	}
	if library == "" {
		library = book.LibraryID
	}
	notice := "Restored."
	if book.Status == store.BookMissing {
		// Saying "restored" alone would be a lie: the catalog entry is
		// back but there is nothing to download.
		notice = "Restored, but its file is gone — upload it again to read it."
	}
	s.uploadResult(w, r, library, notice, "")
}

// trashProblem translates a refused deletion into a sentence. Readers and
// strangers get the same answer, so neither learns the book exists.
func trashProblem(err error) string {
	switch {
	case errors.Is(err, store.ErrInvalidTransition):
		return "that book is not in a state where this makes sense"
	case errors.Is(err, store.ErrNotFound):
		return "that book does not exist, or you cannot manage it"
	default:
		return "that did not work; try again"
	}
}

// duplicateLimit bounds the survey. It counts books rather than groups,
// so a library that is duplicated wholesale reports the first of them and
// no more: the point is to tell the user it is happening, and the tenth
// example does not make the point better.
const duplicateLimit = 50

// duplicateGroups collects books sharing bytes into one entry per file.
// The store returns them ordered by digest, so a group is a run.
func (s *Server) duplicateGroups(
	r *http.Request, userID, libraryID string,
) []DuplicateGroup {
	books, err := s.St.ListDuplicateContentBooks(
		r.Context(), userID, libraryID, duplicateLimit)
	if err != nil {
		slog.Error("duplicate listing unavailable",
			"library", libraryID, "err", err)
		return nil
	}
	return groupDuplicates(books)
}

// similarGroups reports books that look like one book without being one
// file. Like the digest report it is offered only to a librarian, and
// like the digest report it changes nothing: this one is a guess, and a
// guess acted on automatically is how a library loses an edition
// somebody chose deliberately.
func (s *Server) similarGroups(
	r *http.Request, userID, libraryID string, loc *time.Location,
) []SimilarGroup {
	groups, err := s.St.ListSimilarBooks(
		r.Context(), userID, libraryID, duplicateLimit)
	if err != nil {
		slog.Error("similarity listing unavailable",
			"library", libraryID, "err", err)
		return nil
	}
	out := make([]SimilarGroup, 0, len(groups))
	for _, group := range groups {
		row := SimilarGroup{}
		for _, b := range group.Books {
			row.Books = append(row.Books, BookRow{
				ID: b.ID, Title: b.Title,
				Added: b.CreatedAt.In(loc).Format("Jan 2, 2006"),
			})
		}
		out = append(out, row)
	}
	return out
}

// groupDuplicates turns the store's digest-ordered list into one entry
// per file. A group is a run of books carrying the same digest, so this
// depends on that ordering and on nothing else, which is why it is a
// function of its argument and testable as one.
func groupDuplicates(books []store.DuplicateContentBook) []DuplicateGroup {
	var groups []DuplicateGroup
	digest := ""
	for i, duplicate := range books {
		if i == 0 || duplicate.SHA256 != digest {
			digest = duplicate.SHA256
			groups = append(groups, DuplicateGroup{})
		}
		last := len(groups) - 1
		groups[last].Titles = append(groups[last].Titles, duplicate.Book.Title)
	}
	// The limit can cut a group in half, leaving a lone title that reads
	// as "this book duplicates itself". Dropping it is better than
	// explaining it.
	if len(groups) > 0 && len(groups[len(groups)-1].Titles) < 2 {
		groups = groups[:len(groups)-1]
	}
	return groups
}

// reviewLimit keeps the section a list of decisions rather than a
// second catalog. A queue longer than this means something happened to
// the root, not to the books, and the page cannot help with that.
const reviewLimit = 25

// reviewRows lists the watched books whose source changed under them.
// Like the other sections, a failure here must not take the page down.
func (s *Server) reviewRows(
	r *http.Request, userID, libraryID string,
) []ReviewRow {
	books, err := s.St.ListBooksInReview(r.Context(), userID, libraryID, reviewLimit)
	if err != nil {
		slog.Error("review listing unavailable", "library", libraryID, "err", err)
		return nil
	}
	rows := make([]ReviewRow, 0, len(books))
	for _, b := range books {
		rows = append(rows, ReviewRow{
			ID: b.ID, Title: b.Title, Reason: b.ReviewReason,
		})
	}
	return rows
}

// handleAcceptBook records that a librarian looked at a changed watched
// file and is content with the copy being served.
//
// It clears the flag and stops. The book returns to `missing`, and the
// availability pass — the only thing allowed to decide a book is
// servable — puts it back in the catalog on its next run if it still has
// a file. Reingesting the new bytes here would be the server answering
// the question it raised.
func (s *Server) handleAcceptBook(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	library := strings.TrimSpace(r.FormValue("library"))
	// The manage role is checked by reading the book under it, so a
	// reader cannot clear a librarian's queue.
	book, err := s.St.CatalogBookByID(
		r.Context(), u.ID, r.PathValue("id"), store.LibraryRoleManage)
	if err != nil {
		s.uploadResult(w, r, library, "", trashProblem(err))
		return
	}
	if library == "" {
		library = book.LibraryID
	}
	if _, err := s.St.SetCatalogBookReview(
		r.Context(), book.LibraryID, book.ID, "", time.Now().UTC()); err != nil {
		s.uploadResult(w, r, library, "", "that did not work; try again")
		return
	}
	s.uploadResult(w, r, library,
		"Accepted. It returns to the catalog shortly.", "")
}

// trashLimit keeps the trash section a short list of recent regrets
// rather than a second catalog.
const trashLimit = 10

// trashActivity lists what can still be restored. Like uploadActivity, a
// failure here must not take the page down with it.
func (s *Server) trashActivity(
	r *http.Request, userID, libraryID string, loc *time.Location,
) []TrashRow {
	books, err := s.St.ListTrashedBooks(r.Context(), userID, libraryID, trashLimit)
	if err != nil {
		slog.Error("trash listing unavailable", "library", libraryID, "err", err)
		return nil
	}
	rows := make([]TrashRow, 0, len(books))
	for _, b := range books {
		row := TrashRow{ID: b.ID, Title: b.Title}
		if b.TrashExpiresAt != nil {
			row.Until = b.TrashExpiresAt.In(loc).Format("Jan 2, 2006 15:04")
		}
		rows = append(rows, row)
	}
	return rows
}

// isEPUB reports whether a stored file is something the browser reader
// can open. Everything else in a library stays downloadable; only EPUB
// is offered for reading, because that is the only format the reader
// knows how to unpack.
func isEPUB(mediaType string) bool {
	return strings.HasPrefix(mediaType, "application/epub")
}
