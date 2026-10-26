package webui

// A book's audiobook on its details page (ADR-0057, ADR-0058): what is
// attached, a player when it lines up with the text, and the form that
// attaches one where the book's folder accepts uploads.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/chmouel/liseur-sync/internal/audiomap"
	"github.com/chmouel/liseur-sync/internal/m4b"
	"github.com/chmouel/liseur-sync/internal/store"
)

// AudiobookUploader receives one .m4b onto a book. The rules live in the
// API's ReceiveAudiobook; Uploads implements this when it can, and a
// value that does not hides the form.
type AudiobookUploader interface {
	ReceiveAudiobookTo(w http.ResponseWriter, r *http.Request, userID, bookID string) (state string, err error)
}

// BookAudioView is the audiobook paired with a book, as its page shows it.
type BookAudioView struct {
	Title     string
	Narrators string
	Duration  string
	Seconds   float64
	Chapters  []BookAudioChapter
	// Playable is a pairing the API would stream positions across: the
	// same gate as audio/info, so the player never offers what the
	// endpoints behind it would refuse.
	Playable bool
}

type BookAudioChapter struct {
	Title string
	// Value is the start in seconds, as the chapter picker's option.
	Value string
	Clock string
}

func (s *Server) audiobookUploader() AudiobookUploader {
	up, _ := s.Uploads.(AudiobookUploader)
	return up
}

// bookAudio reads the pairing for a book the viewer can already see.
// Nil means there is nothing to say: no audiobook, or one whose folder
// the viewer holds no grant on.
func (s *Server) bookAudio(ctx context.Context, viewerID string, book store.CatalogBook) *BookAudioView {
	link, ab, err := s.St.BookAudiobookByBookID(ctx, viewerID, book.ID)
	if err != nil || ab.Status != store.BookActive {
		return nil
	}
	v := &BookAudioView{
		Title:    ab.Title,
		Duration: audioDuration(ab.DurationSeconds),
		Seconds:  ab.DurationSeconds,
	}
	var narrators []string
	_ = json.Unmarshal(ab.NarratorsJSON, &narrators)
	v.Narrators = strings.Join(narrators, ", ")
	var mapping audiomap.Mapping
	if book.Status == store.BookActive &&
		link.State == store.BookAudiobookActive &&
		link.EditionSHA == book.ContentSHA256 &&
		link.AudiobookSHA == ab.ContentSHA256 &&
		link.MappingKey == audiomap.CatalogMappingKey(book, ab) {
		v.Playable = json.Unmarshal(link.MappingJSON, &mapping) == nil && len(mapping.Segments) > 0
	}
	var chapters []m4b.Chapter
	_ = json.Unmarshal(ab.ChaptersJSON, &chapters)
	for _, c := range chapters {
		// "Track 07" says less than the book does about the same chapter.
		title := c.Title
		if audiomap.GenericTitle(title, ab.Title) {
			if named := mapping.BookTitleAt(c.Start); named != "" {
				title = named
			}
		}
		v.Chapters = append(v.Chapters, BookAudioChapter{
			Title: title, Value: strconv.FormatFloat(c.Start, 'f', -1, 64), Clock: audioClock(c.Start),
		})
	}
	return v
}

// canAddAudiobook mirrors the API's checks that need no body: an active
// book, in a folder that accepts uploads, with no audiobook linked to it
// in any folder.
func (s *Server) canAddAudiobook(ctx context.Context, book store.CatalogBook, folder store.Folder) bool {
	if s.audiobookUploader() == nil || book.Status != store.BookActive || !folder.AcceptsUploads {
		return false
	}
	_, _, err := s.St.BookAudiobookByBookID(ctx, "", book.ID)
	return errors.Is(err, store.ErrNotFound)
}

// handleBookAudiobookUpload implements POST /ui/books/{id}/audiobook.
func (s *Server) handleBookAudiobookUpload(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	bookID := r.PathValue("id")
	// A relative Location resolves against /ui/books/{id}/audiobook, so
	// the details page is one level up; relative keeps a proxy prefix.
	back := "../" + url.PathEscape(bookID)
	// The token rides in the query string, as on the library upload, so
	// checking it does not consume the multipart body.
	if !checkCSRFValue(r.URL.Query().Get("csrf"), a) {
		s.uploadDone(w, r, back, "", "that form had expired; try again")
		return
	}
	up := s.audiobookUploader()
	if up == nil {
		s.uploadDone(w, r, back, "", "this server cannot receive uploads")
		return
	}
	state, err := up.ReceiveAudiobookTo(w, r, u.ID, bookID)
	if err != nil {
		s.uploadDone(w, r, back, "", err.Error())
		return
	}
	switch store.BookAudiobookState(state) {
	case store.BookAudiobookActive:
		s.uploadDone(w, r, back+"#listen", "audiobook added", "")
	case store.BookAudiobookUnmapped:
		s.uploadDone(w, r, back, "audiobook added, but its chapters could not be lined up with this book", "")
	default:
		s.uploadDone(w, r, back, "audiobook saved; it appears here once the folder has been scanned", "")
	}
}
