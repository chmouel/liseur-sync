package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chmouel/liseur-sync/internal/audiomap"
	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/m4b"
	"github.com/chmouel/liseur-sync/internal/store"
)

// AudiobookFiles opens the .m4b file behind a catalog audiobook under its
// folder's root without following symlinks. *content.Files implements it.
type AudiobookFiles interface {
	OpenAudiobook(ctx context.Context, ab store.CatalogAudiobook) (*os.File, int64, error)
}

const (
	audioTicketTTL         = 6 * time.Hour
	sttWindowSeconds       = 10.0
	minSTTWindowSeconds    = 1.5
	minSTTConfidence       = 0.60
	minSTTAmbiguityMargin  = 0.15
	defaultSTTUserCooldown = 3 * time.Second
	maxConcurrentSTTCalls  = 2
	sttRequestTimeout      = 15 * time.Second
	maxEPUBXHTMLBytes      = 8 << 20
)

type audioTicketState struct {
	mu          sync.Mutex
	key         [32]byte
	initialized bool
	sttSem      chan struct{}
	userLastSTT map[string]time.Time
	cooldown    time.Duration
}

var serverAudioState sync.Map // map[*Server]*audioTicketState

func (s *Server) audioState() *audioTicketState {
	if v, ok := serverAudioState.Load(s); ok {
		return v.(*audioTicketState)
	}
	st := &audioTicketState{
		sttSem:      make(chan struct{}, maxConcurrentSTTCalls),
		userLastSTT: make(map[string]time.Time),
		cooldown:    defaultSTTUserCooldown,
	}
	if _, err := rand.Read(st.key[:]); err == nil {
		st.initialized = true
	}
	actual, _ := serverAudioState.LoadOrStore(s, st)
	return actual.(*audioTicketState)
}

// RotateAudioTicketKey replaces this server instance's in-memory HMAC
// signing key (used by tests to simulate a server restart invalidating
// outstanding stream tickets).
func (s *Server) RotateAudioTicketKey() {
	st := s.audioState()
	st.mu.Lock()
	defer st.mu.Unlock()
	_, _ = rand.Read(st.key[:])
	st.initialized = true
}

func (s *Server) audioTicketKey() [32]byte {
	st := s.audioState()
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.initialized {
		_, _ = rand.Read(st.key[:])
		st.initialized = true
	}
	return st.key
}

type audioTicketPayload struct {
	Kind         string `json:"k"`
	AuthID       string `json:"aid"`
	UserID       string `json:"uid"`
	BookID       string `json:"bid"`
	AudiobookID  string `json:"abid"`
	AudiobookSHA string `json:"sha"`
	ExpiresUnix  int64  `json:"exp"`
}

func (s *Server) signAudioTicket(p audioTicketPayload) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	key := s.audioTicketKey()
	mac := hmac.New(sha256.New, key[:])
	mac.Write(raw)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(raw) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}

func (s *Server) verifyAudioTicket(ticket string, now time.Time) (audioTicketPayload, bool) {
	payloadB64, sigB64, ok := strings.Cut(ticket, ".")
	if !ok || payloadB64 == "" || sigB64 == "" {
		return audioTicketPayload{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return audioTicketPayload{}, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return audioTicketPayload{}, false
	}
	key := s.audioTicketKey()
	mac := hmac.New(sha256.New, key[:])
	mac.Write(raw)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return audioTicketPayload{}, false
	}
	var p audioTicketPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return audioTicketPayload{}, false
	}
	if p.UserID == "" || p.AuthID == "" || p.BookID == "" || p.AudiobookID == "" || p.AudiobookSHA == "" {
		return audioTicketPayload{}, false
	}
	if now.Unix() >= p.ExpiresUnix {
		return audioTicketPayload{}, false
	}
	return p, true
}

// RedactAudioStreamPath replaces the bearer ticket segment of an audio
// stream URL path with [REDACTED] so request logs never record live
// stream tickets.
func RedactAudioStreamPath(p string) string {
	const marker = "/audio/stream/"
	idx := strings.Index(p, marker)
	if idx < 0 {
		return p
	}
	prefix := p[:idx+len(marker)]
	rest := p[idx+len(marker):]
	if rest == "" {
		return p
	}
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		return prefix + "[REDACTED]" + rest[slash:]
	}
	return prefix + "[REDACTED]"
}

func (s *Server) loadActiveBookAudiobook(
	w http.ResponseWriter, r *http.Request, viewerID, bookID string,
) (store.CatalogBook, store.BookAudiobook, store.CatalogAudiobook, audiomap.Mapping, bool) {
	if bookID == "" || len(bookID) > maxFolderIDBytes {
		writeError(w, http.StatusNotFound, "book not found")
		return store.CatalogBook{}, store.BookAudiobook{}, store.CatalogAudiobook{}, audiomap.Mapping{}, false
	}
	book, err := s.St.CatalogBookByID(r.Context(), viewerID, bookID)
	if err != nil {
		writeCatalogError(w, err, "book not found")
		return store.CatalogBook{}, store.BookAudiobook{}, store.CatalogAudiobook{}, audiomap.Mapping{}, false
	}
	if book.Status != store.BookActive {
		writeError(w, http.StatusGone, "no active file for this book")
		return store.CatalogBook{}, store.BookAudiobook{}, store.CatalogAudiobook{}, audiomap.Mapping{}, false
	}
	link, ab, err := s.St.BookAudiobookByBookID(r.Context(), viewerID, bookID)
	if err != nil {
		writeCatalogError(w, err, "no linked audiobook for this book")
		return store.CatalogBook{}, store.BookAudiobook{}, store.CatalogAudiobook{}, audiomap.Mapping{}, false
	}
	if ab.Status != store.BookActive {
		writeError(w, http.StatusGone, "audiobook file is missing")
		return store.CatalogBook{}, store.BookAudiobook{}, store.CatalogAudiobook{}, audiomap.Mapping{}, false
	}
	if link.State != store.BookAudiobookActive ||
		link.EditionSHA != book.ContentSHA256 || link.AudiobookSHA != ab.ContentSHA256 ||
		link.MappingKey != audiomap.CatalogMappingKey(book, ab) {
		writeError(w, http.StatusNotFound, "audiobook is not aligned to this book")
		return store.CatalogBook{}, store.BookAudiobook{}, store.CatalogAudiobook{}, audiomap.Mapping{}, false
	}
	var mapping audiomap.Mapping
	if err := json.Unmarshal(link.MappingJSON, &mapping); err != nil || len(mapping.Segments) == 0 {
		writeError(w, http.StatusNotFound, "audiobook mapping is unavailable")
		return store.CatalogBook{}, store.BookAudiobook{}, store.CatalogAudiobook{}, audiomap.Mapping{}, false
	}
	return book, link, ab, mapping, true
}

func (s *Server) sttEnabled() bool {
	u := strings.TrimSpace(s.Cfg.Audiobookshelf.STTURL)
	if u == "" {
		return false
	}
	if strings.Contains(u, "api.groq.com") && strings.TrimSpace(s.Cfg.Audiobookshelf.STTAPIKey) == "" {
		return false
	}
	return true
}

// HandleBookAudioInfo implements GET /v1/books/{id}/audio/info.
func (s *Server) HandleBookAudioInfo(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	book, link, ab, mapping, ok := s.loadActiveBookAudiobook(w, r, tok.UserID, r.PathValue("id"))
	if !ok {
		return
	}

	var authors []string
	_ = json.Unmarshal(ab.AuthorsJSON, &authors)
	if authors == nil {
		authors = []string{}
	}
	var narrators []string
	_ = json.Unmarshal(ab.NarratorsJSON, &narrators)
	if narrators == nil {
		narrators = []string{}
	}
	var chapters []m4b.Chapter
	_ = json.Unmarshal(ab.ChaptersJSON, &chapters)
	if chapters == nil {
		chapters = []m4b.Chapter{}
	}

	var audioTime *float64
	var progression *float64
	if ubw, err := s.St.UserBookWork(r.Context(), tok.UserID, book.ID); err == nil && ubw.WorkID != "" {
		if snap, err := s.St.PositionSnapshot(r.Context(), tok.UserID, ubw.WorkID, 1); err == nil && len(snap.Ops) > 0 {
			latest := snap.Ops[0]
			if t, ok, _ := mapping.ToAudioLocator(latest.Progression, latest.LocatorJSON, func(href string) ([]byte, error) {
				return s.readBookResource(r.Context(), book, href)
			}); ok {
				audioTime = &t
				p := latest.Progression
				progression = &p
			}
		}
	}

	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"book_id": book.ID,
		"audiobook": map[string]any{
			"id":               ab.ID,
			"title":            ab.Title,
			"authors":          authors,
			"narrators":        narrators,
			"series_name":      ab.SeriesName,
			"duration_seconds": ab.DurationSeconds,
			"chapters":         chapters,
			"content_sha256":   ab.ContentSHA256,
		},
		"mapping": map[string]any{
			"source":         link.Source,
			"segments":       mapping.Segments,
			"resources":      mapping.Resources,
			"full_narrative": mapping.FullNarrative,
		},
		"stt_enabled": s.sttEnabled(),
		"audio_time":  audioTime,
		"progression": progression,
	})
}

// HandleCreateBookAudioTicket implements POST /v1/books/{id}/audio/ticket.
func (s *Server) HandleCreateBookAudioTicket(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	book, _, ab, _, ok := s.loadActiveBookAudiobook(w, r, tok.UserID, r.PathValue("id"))
	if !ok {
		return
	}
	now := time.Now().UTC()
	expiresAt := now.Add(audioTicketTTL)
	if tok.ExpiresAt != nil && tok.ExpiresAt.Before(expiresAt) {
		expiresAt = tok.ExpiresAt.UTC()
	}
	if !expiresAt.After(now) {
		writeError(w, http.StatusUnauthorized, "token expired")
		return
	}
	ticket, err := s.signAudioTicket(audioTicketPayload{
		Kind:         "token",
		AuthID:       tok.ID,
		UserID:       tok.UserID,
		BookID:       book.ID,
		AudiobookID:  ab.ID,
		AudiobookSHA: ab.ContentSHA256,
		ExpiresUnix:  expiresAt.Unix(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ticket creation failed")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"stream_url": "v1/books/" + url.PathEscape(book.ID) + "/audio/stream/" + ticket,
		"expires_at": expiresAt.Format(time.RFC3339Nano),
	})
}

// HandleStreamBookAudioTicket implements GET and HEAD
// /v1/books/{id}/audio/stream/{ticket}.
func (s *Server) HandleStreamBookAudioTicket(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	payload, ok := s.verifyAudioTicket(r.PathValue("ticket"), now)
	if !ok || payload.BookID != r.PathValue("id") {
		writeError(w, http.StatusUnauthorized, "invalid or expired stream ticket")
		return
	}
	if !s.isAudioTicketAuthActive(r.Context(), payload, now) {
		writeError(w, http.StatusUnauthorized, "stream ticket credential has been revoked")
		return
	}
	s.ServeBookAudio(w, r, payload.UserID, payload.BookID, payload.AudiobookID, payload.AudiobookSHA)
}

func (s *Server) isAudioTicketAuthActive(ctx context.Context, p audioTicketPayload, now time.Time) bool {
	if _, err := s.St.UserByID(ctx, p.UserID); err != nil {
		return false
	}
	switch p.Kind {
	case "token":
		toks, err := s.St.ListTokens(ctx, p.UserID)
		if err != nil {
			return false
		}
		for _, t := range toks {
			if t.ID == p.AuthID {
				if t.RevokedAt != nil {
					return false
				}
				if t.ExpiresAt != nil && !t.ExpiresAt.After(now) {
					return false
				}
				return t.Scopes.Allows(store.ScopeLibraryRead)
			}
		}
		return false
	default:
		return false
	}
}

// ServeBookAudio streams the linked .m4b file with HTTP Range and strong
// ETag support after verifying the viewer's folder grants on both the
// book's folder and the audiobook's folder.
func (s *Server) ServeBookAudio(
	w http.ResponseWriter, r *http.Request, viewerID, bookID, expectedAudiobookID, expectedSHA string,
) {
	af, ok := s.Files.(AudiobookFiles)
	if !ok || s.Files == nil {
		writeError(w, http.StatusServiceUnavailable, "content storage is unavailable")
		return
	}
	_, _, ab, _, ok := s.loadActiveBookAudiobook(w, r, viewerID, bookID)
	if !ok {
		return
	}
	if expectedAudiobookID != "" && (ab.ID != expectedAudiobookID || ab.ContentSHA256 != expectedSHA) {
		writeError(w, http.StatusUnauthorized, "stream ticket no longer matches active audiobook")
		return
	}
	file, _, err := af.OpenAudiobook(r.Context(), ab)
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "audio/mp4")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Strong ETag so RFC 9110 If-Range byte-range resumptions succeed in
	// http.ServeContent.
	w.Header().Set("ETag", `"`+ab.ContentSHA256+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(&catalogErrorWriter{ResponseWriter: w}, r, "",
		ab.UpdatedAt.UTC(), file)
}

type audioLocateRequest struct {
	AudioTime   *float64        `json:"audio_time,omitempty"`
	Progression *float64        `json:"progression,omitempty"`
	Locator     json.RawMessage `json:"locator,omitempty"`
	RefineSTT   bool            `json:"refine_stt,omitempty"`
}

// HandleBookAudioLocate implements POST /v1/books/{id}/audio/locate for
// bidirectional Page <-> Audio translation and optional stateless
// [t - 10s, t] Whisper STT refinement.
func (s *Server) HandleBookAudioLocate(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	book, _, ab, mapping, ok := s.loadActiveBookAudiobook(w, r, tok.UserID, r.PathValue("id"))
	if !ok {
		return
	}

	var req audioLocateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.AudioTime != nil {
		t := *req.AudioTime
		if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 {
			writeError(w, http.StatusBadRequest, "audio_time must be a non-negative number")
			return
		}
		if ab.DurationSeconds > 0 && t > ab.DurationSeconds {
			t = ab.DurationSeconds
		}
		bookProg, seg, matched := mapping.ToBook(t)
		if !matched {
			writeError(w, http.StatusNotFound, "no mapped book passage for this audio timestamp")
			return
		}
		locator, err := mapping.LocatorAt(bookProg, seg, false)
		if err != nil {
			writeError(w, http.StatusNotFound, "audiobook mapping has no reading order")
			return
		}
		href, _, _, _ := parseLocateInput(locator)

		sttMatched := false
		transcript := ""
		var before, highlight, after string
		if req.RefineSTT && s.sttEnabled() && href != "" {
			if _, refProg, tr, bef, hi, aft, ok := s.refineAudioTimeWithSTT(
				r.Context(), tok.UserID, book, ab, mapping, seg, href, t,
			); ok {
				bookProg = refProg
				transcript = tr
				before = bef
				highlight = hi
				after = aft
				sttMatched = true
			}
		}

		if sttMatched {
			locator, err = mapping.LocatorWithTextAt(bookProg, seg, false, before, highlight, after)
			if err != nil {
				writeError(w, http.StatusNotFound, "audiobook mapping has no reading order")
				return
			}
		}

		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, map[string]any{
			"direction":   "audio_to_book",
			"audio_time":  t,
			"progression": bookProg,
			"locator":     json.RawMessage(locator),
			"segment":     seg,
			"stt_matched": sttMatched,
			"transcript":  transcript,
		})
		return
	}

	if len(req.Locator) == 0 && req.Progression == nil {
		writeError(w, http.StatusBadRequest, "audio_time, locator, or progression is required")
		return
	}
	if len(req.Locator) > 0 {
		var loc map[string]json.RawMessage
		if err := json.Unmarshal(req.Locator, &loc); err != nil || loc == nil {
			writeError(w, http.StatusBadRequest, "locator must be an object")
			return
		}
	}

	locHref, _, locTotalProg, locChapProg := parseLocateInput(req.Locator)
	prog := -1.0
	if req.Progression != nil {
		p := *req.Progression
		if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
			writeError(w, http.StatusBadRequest, "progression must be in [0, 1]")
			return
		}
		prog = p
	} else if locTotalProg >= 0 {
		prog = locTotalProg
	} else if locHref != "" && locChapProg >= 0 {
		cleanHref := stripHrefFrag(locHref)
		for _, resource := range mapping.Resources {
			if stripHrefFrag(resource.Href) == cleanHref {
				prog = resource.Start + locChapProg*(resource.End-resource.Start)
				break
			}
		}
	}
	audioTime, matched, _ := mapping.ToAudioLocator(prog, req.Locator, func(href string) ([]byte, error) {
		return s.readBookResource(r.Context(), book, href)
	})
	if !matched {
		writeError(w, http.StatusNotFound, "no mapped audio timestamp for this book position")
		return
	}
	prog, seg, _ := mapping.ToBook(audioTime)
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"direction":   "book_to_audio",
		"audio_time":  audioTime,
		"progression": prog,
		"segment":     seg,
	})
}

func (s *Server) refineAudioTimeWithSTT(
	ctx context.Context,
	userID string,
	book store.CatalogBook,
	ab store.CatalogAudiobook,
	mapping audiomap.Mapping,
	seg audiomap.Segment,
	href string,
	audioTime float64,
) (chapFrac, refinedProg float64, transcript, before, highlight, after string, ok bool) {
	af, hasAF := s.Files.(AudiobookFiles)
	if !hasAF || s.Files == nil {
		return 0, 0, "", "", "", "", false
	}
	t1 := audioTime
	t0 := math.Max(seg.AudioStart, t1-sttWindowSeconds)
	if t1-t0 < minSTTWindowSeconds {
		return 0, 0, "", "", "", "", false
	}

	st := s.audioState()
	now := time.Now()
	st.mu.Lock()
	if last, exists := st.userLastSTT[userID]; exists && now.Sub(last) < st.cooldown {
		st.mu.Unlock()
		return 0, 0, "", "", "", "", false
	}
	st.userLastSTT[userID] = now
	st.mu.Unlock()

	select {
	case st.sttSem <- struct{}{}:
		defer func() { <-st.sttSem }()
	default:
		return 0, 0, "", "", "", "", false
	}

	sttCtx, cancel := context.WithTimeout(ctx, sttRequestTimeout)
	defer cancel()

	audioFile, audioSize, err := af.OpenAudiobook(sttCtx, ab)
	if err != nil {
		return 0, 0, "", "", "", "", false
	}
	defer audioFile.Close()

	clipBytes, clipFilename, err := m4b.SliceSnippet(
		sttCtx, m4b.SeekerRangeReader(audioFile, audioSize), audioSize, t0, t1,
	)
	if err != nil || len(clipBytes) == 0 {
		return 0, 0, "", "", "", "", false
	}

	tr, err := audiomap.TranscribeAudio(
		sttCtx,
		s.Cfg.Audiobookshelf.STTURL,
		s.Cfg.Audiobookshelf.STTAPIKey,
		s.Cfg.Audiobookshelf.STTModel,
		clipBytes,
		clipFilename,
	)
	if err != nil || strings.TrimSpace(tr) == "" {
		return 0, 0, "", "", "", "", false
	}

	xhtmlBytes, err := s.readBookResource(sttCtx, book, href)
	if err != nil || len(xhtmlBytes) == 0 {
		return 0, 0, "", "", "", "", false
	}

	frac, conf, margin, bef, hi, aft, matched := audiomap.MatchTranscriptDetailInXHTML(xhtmlBytes, tr)
	if !matched || conf < minSTTConfidence || margin < minSTTAmbiguityMargin {
		return 0, 0, tr, "", "", "", false
	}
	refProg := -1.0
	for _, resource := range mapping.Resources {
		if resource.Href == href {
			refProg = resource.Start + frac*(resource.End-resource.Start)
			break
		}
	}
	if refProg < seg.BookStart || refProg > seg.BookEnd {
		return 0, 0, tr, "", "", "", false
	}
	return frac, refProg, tr, bef, hi, aft, true
}

func (s *Server) readBookResource(ctx context.Context, book store.CatalogBook, href string) ([]byte, error) {
	if s.Files == nil {
		return nil, errors.New("content storage unavailable")
	}
	cleanHref := stripHrefFrag(href)
	if cleanHref == "" {
		return nil, errors.New("empty resource href")
	}
	file, size, err := s.Files.OpenBook(ctx, book)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	idx, err := s.publicationIndex(ctx, book.ContentSHA256, file, size)
	if err != nil {
		return nil, err
	}
	target := cleanHref
	if !idx.HasResource(target) {
		trimmed := strings.TrimPrefix(target, "/")
		if idx.HasResource(trimmed) {
			target = trimmed
		} else {
			return nil, os.ErrNotExist
		}
	}
	rc, resSize, err := epub.OpenIndexedResource(file, size, idx, target)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	if resSize > maxEPUBXHTMLBytes {
		return nil, errors.New("resource exceeds maximum XHTML size")
	}
	return io.ReadAll(io.LimitReader(rc, maxEPUBXHTMLBytes))
}

func parseLocateInput(raw json.RawMessage) (href, text string, totalProg, chapProg float64) {
	totalProg = -1
	chapProg = -1
	if len(raw) == 0 {
		return "", "", -1, -1
	}
	var loc struct {
		Href      string `json:"href"`
		Locations struct {
			Progression      *float64 `json:"progression"`
			TotalProgression *float64 `json:"totalProgression"`
		} `json:"locations"`
		Text struct {
			Before    string `json:"before"`
			Highlight string `json:"highlight"`
			After     string `json:"after"`
		} `json:"text"`
	}
	if err := json.Unmarshal(raw, &loc); err != nil {
		return "", "", -1, -1
	}
	href = strings.TrimSpace(loc.Href)
	var parts []string
	for _, s := range []string{loc.Text.Before, loc.Text.Highlight, loc.Text.After} {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	text = strings.Join(parts, " ")
	if loc.Locations.TotalProgression != nil &&
		!math.IsNaN(*loc.Locations.TotalProgression) &&
		*loc.Locations.TotalProgression >= 0 && *loc.Locations.TotalProgression <= 1 {
		totalProg = *loc.Locations.TotalProgression
	}
	if loc.Locations.Progression != nil &&
		!math.IsNaN(*loc.Locations.Progression) &&
		*loc.Locations.Progression >= 0 && *loc.Locations.Progression <= 1 {
		chapProg = *loc.Locations.Progression
	}
	return href, text, totalProg, chapProg
}

func stripHrefFrag(href string) string {
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	if i := strings.IndexByte(href, '?'); i >= 0 {
		href = href[:i]
	}
	return strings.TrimSpace(href)
}
