package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// vocabularyWordJSON is the wire shape of a vocabulary word. A tombstone
// carries identity, rev, seq, generation and when, and nothing else.
type vocabularyWordJSON struct {
	Language            string                     `json:"language"`
	Normalized          string                     `json:"normalized"`
	Word                string                     `json:"word,omitempty"`
	Definition          *string                    `json:"definition,omitempty"`
	TranslationLanguage *string                    `json:"translation_language,omitempty"`
	Notes               string                     `json:"notes,omitempty"`
	DefinitionEdited    bool                       `json:"definition_edited,omitempty"`
	DefinitionUpdatedAt string                     `json:"definition_updated_at,omitempty"`
	NotesUpdatedAt      string                     `json:"notes_updated_at,omitempty"`
	AddedAt             string                     `json:"added_at,omitempty"`
	ImportedBaseline    *vocabularyBaselineJSON    `json:"imported_baseline,omitempty"`
	Occurrences         []vocabularyOccurrenceJSON `json:"occurrences"`
	Rev                 int64                      `json:"rev,omitempty"`
	Generation          int64                      `json:"generation,omitempty"`
	Seq                 int64                      `json:"seq,omitempty"`
	DeviceID            string                     `json:"device_id,omitempty"`
	UpdatedAt           string                     `json:"updated_at,omitempty"`
	Deleted             bool                       `json:"deleted,omitempty"`
	DeletedAt           string                     `json:"deleted_at,omitempty"`
}

type vocabularyBaselineJSON struct {
	DueAt           string `json:"due_at"`
	ReviewedAt      string `json:"reviewed_at,omitempty"`
	Streak          int    `json:"streak,omitempty"`
	IntervalMinutes int    `json:"interval_minutes,omitempty"`
	Repetitions     int    `json:"repetitions,omitempty"`
}

type vocabularyOccurrenceJSON struct {
	OccurrenceKey     string          `json:"occurrence_key"`
	WorkID            *string         `json:"work_id,omitempty"`
	BookTitle         string          `json:"book_title,omitempty"`
	LocationKey       string          `json:"location_key,omitempty"`
	Locator           json.RawMessage `json:"locator,omitempty"`
	Sentence          *string         `json:"sentence,omitempty"`
	SentenceEdited    bool            `json:"sentence_edited,omitempty"`
	SentenceUpdatedAt string          `json:"sentence_updated_at,omitempty"`
	LastSeenAt        string          `json:"last_seen_at,omitempty"`
	Encounters        int             `json:"encounters,omitempty"`
}

type vocabularyReviewJSON struct {
	ReviewID        string `json:"review_id"`
	Language        string `json:"language"`
	Normalized      string `json:"normalized"`
	WordGeneration  int64  `json:"word_generation"`
	Seq             int64  `json:"seq,omitempty"`
	Rating          string `json:"rating,omitempty"`
	ReviewedAt      string `json:"reviewed_at,omitempty"`
	IntervalMinutes int    `json:"interval_minutes,omitempty"`
	DueAt           string `json:"due_at,omitempty"`
	DeviceID        string `json:"device_id,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	Deleted         bool   `json:"deleted,omitempty"`
	DeletedAt       string `json:"deleted_at,omitempty"`
}

func vocabularyWordToJSON(w store.VocabularyWord) vocabularyWordJSON {
	out := vocabularyWordJSON{
		Language:   w.Language,
		Normalized: w.Normalized,
		Rev:        w.Rev,
		Generation: w.Generation,
		Seq:        w.Seq,
		UpdatedAt:  w.UpdatedAt.UTC().Format(wireTimeFormat),
	}
	if w.Deleted() {
		out.Deleted = true
		out.DeletedAt = w.DeletedAt.UTC().Format(wireTimeFormat)
		return out
	}
	out.Word = w.Word
	out.Definition = w.Definition
	out.TranslationLanguage = w.TranslationLanguage
	out.Notes = w.Notes
	out.DefinitionEdited = w.DefinitionEdited
	if w.DefinitionUpdatedAt != nil {
		out.DefinitionUpdatedAt = w.DefinitionUpdatedAt.UTC().Format(wireTimeFormat)
	}
	if w.NotesUpdatedAt != nil {
		out.NotesUpdatedAt = w.NotesUpdatedAt.UTC().Format(wireTimeFormat)
	}
	out.AddedAt = w.AddedAt.UTC().Format(wireTimeFormat)
	if w.Baseline != nil {
		out.ImportedBaseline = &vocabularyBaselineJSON{
			DueAt:           w.Baseline.DueAt.UTC().Format(wireTimeFormat),
			Streak:          w.Baseline.Streak,
			IntervalMinutes: w.Baseline.IntervalMinutes,
			Repetitions:     w.Baseline.Repetitions,
		}
		if w.Baseline.ReviewedAt != nil {
			out.ImportedBaseline.ReviewedAt = w.Baseline.ReviewedAt.UTC().Format(wireTimeFormat)
		}
	}
	out.Occurrences = make([]vocabularyOccurrenceJSON, 0, len(w.Occurrences))
	for _, o := range w.Occurrences {
		oj := vocabularyOccurrenceJSON{
			OccurrenceKey:  o.OccurrenceKey,
			WorkID:         o.WorkID,
			BookTitle:      o.BookTitle,
			LocationKey:    o.LocationKey,
			Sentence:       o.Sentence,
			SentenceEdited: o.SentenceEdited,
			Encounters:     o.Encounters,
		}
		if len(o.LocatorJSON) > 0 {
			oj.Locator = o.LocatorJSON
		}
		if o.SentenceUpdatedAt != nil {
			oj.SentenceUpdatedAt = o.SentenceUpdatedAt.UTC().Format(wireTimeFormat)
		}
		if !o.LastSeenAt.IsZero() {
			oj.LastSeenAt = o.LastSeenAt.UTC().Format(wireTimeFormat)
		}
		out.Occurrences = append(out.Occurrences, oj)
	}
	out.DeviceID = w.DeviceID
	return out
}

func vocabularyReviewToJSON(r store.VocabularyReview) vocabularyReviewJSON {
	out := vocabularyReviewJSON{
		ReviewID:       r.ReviewID,
		Language:       r.Language,
		Normalized:     r.Normalized,
		WordGeneration: r.WordGeneration,
		Seq:            r.Seq,
		UpdatedAt:      r.UpdatedAt.UTC().Format(wireTimeFormat),
	}
	if r.Deleted() {
		out.Deleted = true
		out.DeletedAt = r.DeletedAt.UTC().Format(wireTimeFormat)
		return out
	}
	out.Rating = r.Rating
	out.ReviewedAt = r.ReviewedAt.UTC().Format(wireTimeFormat)
	out.IntervalMinutes = r.IntervalMinutes
	out.DueAt = r.DueAt.UTC().Format(wireTimeFormat)
	out.DeviceID = r.DeviceID
	return out
}

// vocabularyWordWriteJSON is one item of a batched word push.
type vocabularyWordWriteJSON struct {
	Language            string                          `json:"language"`
	Normalized          string                          `json:"normalized"`
	BaseRev             int64                           `json:"base_rev"`
	Deleted             bool                            `json:"deleted"`
	Word                string                          `json:"word"`
	Definition          *string                         `json:"definition"`
	TranslationLanguage *string                         `json:"translation_language"`
	Notes               string                          `json:"notes"`
	DefinitionEdited    bool                            `json:"definition_edited"`
	DefinitionUpdatedAt *string                         `json:"definition_updated_at"`
	NotesUpdatedAt      *string                         `json:"notes_updated_at"`
	AddedAt             string                          `json:"added_at"`
	ImportedBaseline    *vocabularyBaselineJSON         `json:"imported_baseline"`
	Occurrences         []vocabularyOccurrenceWriteJSON `json:"occurrences"`
}

type vocabularyOccurrenceWriteJSON struct {
	OccurrenceKey     string          `json:"occurrence_key"`
	WorkID            *string         `json:"work_id"`
	BookTitle         string          `json:"book_title"`
	LocationKey       string          `json:"location_key"`
	Locator           json.RawMessage `json:"locator"`
	Sentence          *string         `json:"sentence"`
	SentenceEdited    bool            `json:"sentence_edited"`
	SentenceUpdatedAt *string         `json:"sentence_updated_at"`
	LastSeenAt        string          `json:"last_seen_at"`
	Encounters        int             `json:"encounters"`
}

// vocabularyReviewWriteJSON is one item of a batched review push.
type vocabularyReviewWriteJSON struct {
	ReviewID        string `json:"review_id"`
	Language        string `json:"language"`
	Normalized      string `json:"normalized"`
	WordGeneration  int64  `json:"word_generation"`
	Deleted         bool   `json:"deleted"`
	Rating          string `json:"rating"`
	ReviewedAt      string `json:"reviewed_at"`
	IntervalMinutes int    `json:"interval_minutes"`
	DueAt           string `json:"due_at"`
}

const (
	vocabularyMaxPerItemBytes = 64 << 10
	vocabularyMaxLocatorBytes = 16 << 10
	vocabularyMaxRefBytes     = 128
)

func vocabTextReason(field, s string, minLen, maxLen int) string {
	if len(s) < minLen {
		return field + " required"
	}
	if len(s) > maxLen {
		return field + " too large"
	}
	if !utf8.ValidString(s) {
		return field + " is not valid UTF-8"
	}
	if strings.IndexByte(s, 0) >= 0 {
		return field + " contains NUL"
	}
	return ""
}

func vocabOptionalTextReason(field string, p *string, maxLen int) string {
	if p == nil {
		return ""
	}
	if len(*p) > maxLen {
		return field + " too large"
	}
	if !utf8.ValidString(*p) {
		return field + " is not valid UTF-8"
	}
	if strings.IndexByte(*p, 0) >= 0 {
		return field + " contains NUL"
	}
	return ""
}

func parseVocabTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

// validateVocabularyWordWrite bounds one word push item at the edge.
func (s *Server) validateVocabularyWordWrite(in vocabularyWordWriteJSON) (store.VocabularyWordWrite, string) {
	var out store.VocabularyWordWrite
	if reason := vocabTextReason("language", in.Language, 1, 80); reason != "" {
		return out, reason
	}
	if reason := vocabTextReason("normalized", in.Normalized, 1, 512); reason != "" {
		return out, reason
	}
	if in.BaseRev < 0 {
		return out, "base_rev must be >= 0"
	}
	out.Language, out.Normalized, out.BaseRev, out.Deleted = in.Language, in.Normalized, in.BaseRev, in.Deleted
	if in.Deleted {
		return out, ""
	}

	if reason := vocabTextReason("word", in.Word, 1, 512); reason != "" {
		return out, reason
	}
	if reason := vocabOptionalTextReason("definition", in.Definition, 24576); reason != "" {
		return out, reason
	}
	if reason := vocabOptionalTextReason("translation_language", in.TranslationLanguage, 80); reason != "" {
		return out, reason
	}
	if len(in.Notes) > 12288 {
		return out, "notes too large"
	}
	if !utf8.ValidString(in.Notes) || strings.IndexByte(in.Notes, 0) >= 0 {
		return out, "notes is not valid text"
	}
	now := time.Now()
	futureCap := now.Add(24 * time.Hour)
	addedAt, ok := parseVocabTime(in.AddedAt)
	if !ok || len(in.AddedAt) > 64 {
		return out, "bad added_at"
	}
	if addedAt.After(futureCap) {
		return out, "added_at in the future"
	}
	out.Word = in.Word
	out.Definition = in.Definition
	out.TranslationLanguage = in.TranslationLanguage
	out.Notes = in.Notes
	out.DefinitionEdited = in.DefinitionEdited
	out.AddedAt = addedAt

	if du, reason := vocabOptionalTime(in.DefinitionUpdatedAt, futureCap); reason != "" {
		return out, "bad definition_updated_at"
	} else {
		out.DefinitionUpdatedAt = du
	}
	if nu, reason := vocabOptionalTime(in.NotesUpdatedAt, futureCap); reason != "" {
		return out, "bad notes_updated_at"
	} else {
		out.NotesUpdatedAt = nu
	}

	if in.ImportedBaseline != nil {
		b := in.ImportedBaseline
		due, ok := parseVocabTime(b.DueAt)
		if !ok || len(b.DueAt) > 64 {
			return out, "bad imported_baseline due_at"
		}
		if b.Streak < 0 || b.Streak > 14 {
			return out, "imported_baseline streak out of range"
		}
		if b.IntervalMinutes < 0 || b.IntervalMinutes > 10000000 {
			return out, "imported_baseline interval_minutes out of range"
		}
		if b.Repetitions < 1 || b.Repetitions > 1000000 {
			return out, "imported_baseline repetitions out of range"
		}
		baseline := &store.VocabularyBaseline{
			DueAt: due, Streak: b.Streak,
			IntervalMinutes: b.IntervalMinutes, Repetitions: b.Repetitions,
		}
		if b.ReviewedAt != "" {
			rv, ok := parseVocabTime(b.ReviewedAt)
			if !ok || len(b.ReviewedAt) > 64 {
				return out, "bad imported_baseline reviewed_at"
			}
			if rv.After(futureCap) {
				return out, "imported_baseline reviewed_at in the future"
			}
			baseline.ReviewedAt = &rv
		}
		out.Baseline = baseline
	}

	if len(in.Occurrences) > 50 {
		return out, "too many occurrences"
	}
	occ := make([]store.VocabularyOccurrence, 0, len(in.Occurrences))
	seen := make(map[string]bool, len(in.Occurrences))
	for _, o := range in.Occurrences {
		if reason := vocabTextReason("occurrence_key", o.OccurrenceKey, 1, vocabularyMaxRefBytes); reason != "" {
			return out, reason
		}
		if seen[o.OccurrenceKey] {
			return out, "duplicate occurrence_key"
		}
		seen[o.OccurrenceKey] = true
		if o.WorkID != nil {
			if reason := vocabOptionalTextReason("work_id", o.WorkID, vocabularyMaxRefBytes); reason != "" {
				return out, reason
			}
		}
		if reason := vocabOptionalTextReason("book_title", strPtr(o.BookTitle), 1024); reason != "" {
			return out, reason
		}
		if reason := vocabOptionalTextReason("location_key", strPtr(o.LocationKey), vocabularyMaxRefBytes); reason != "" {
			return out, reason
		}
		if len(o.Locator) > vocabularyMaxLocatorBytes {
			return out, "occurrence locator too large"
		}
		if len(o.Locator) > 0 && !json.Valid(o.Locator) {
			return out, "occurrence locator is not valid JSON"
		}
		if reason := vocabOptionalTextReason("sentence", o.Sentence, 4096); reason != "" {
			return out, reason
		}
		lastSeen, ok := parseVocabTime(o.LastSeenAt)
		if !ok || len(o.LastSeenAt) > 64 {
			return out, "bad occurrence last_seen_at"
		}
		if lastSeen.After(futureCap) {
			return out, "occurrence last_seen_at in the future"
		}
		if o.Encounters < 1 || o.Encounters > 1000000 {
			return out, "occurrence encounters out of range"
		}
		so := store.VocabularyOccurrence{
			OccurrenceKey:  o.OccurrenceKey,
			WorkID:         o.WorkID,
			BookTitle:      o.BookTitle,
			LocationKey:    o.LocationKey,
			Sentence:       o.Sentence,
			SentenceEdited: o.SentenceEdited,
			LastSeenAt:     lastSeen,
			Encounters:     o.Encounters,
		}
		if len(o.Locator) > 0 && string(o.Locator) != "null" {
			so.LocatorJSON = o.Locator
		}
		if o.SentenceUpdatedAt != nil {
			su, reason := vocabOptionalTime(o.SentenceUpdatedAt, futureCap)
			if reason != "" {
				return out, "bad occurrence sentence_updated_at"
			}
			so.SentenceUpdatedAt = su
		}
		occ = append(occ, so)
	}
	occJSON, err := json.Marshal(occ)
	if err != nil {
		return out, "bad occurrences"
	}
	out.OccurrencesJSON = occJSON
	return out, ""
}

func vocabOptionalTime(s *string, limit time.Time) (*time.Time, string) {
	if s == nil {
		return nil, ""
	}
	t, ok := parseVocabTime(*s)
	if !ok || len(*s) > 64 {
		return nil, "bad timestamp"
	}
	if t.After(limit) {
		return nil, "timestamp in the future"
	}
	return &t, ""
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// validateVocabularyReviewWrite bounds one review push item at the edge.
func (s *Server) validateVocabularyReviewWrite(in vocabularyReviewWriteJSON) (store.VocabularyReviewWrite, string) {
	var out store.VocabularyReviewWrite
	if reason := vocabTextReason("review_id", in.ReviewID, 1, 64); reason != "" {
		return out, reason
	}
	if reason := vocabTextReason("language", in.Language, 1, 80); reason != "" {
		return out, reason
	}
	if reason := vocabTextReason("normalized", in.Normalized, 1, 512); reason != "" {
		return out, reason
	}
	if in.WordGeneration < 1 {
		return out, "word_generation must be >= 1"
	}
	out.ReviewID, out.Language, out.Normalized = in.ReviewID, in.Language, in.Normalized
	out.WordGeneration, out.Deleted = in.WordGeneration, in.Deleted
	if in.Deleted {
		return out, ""
	}
	switch in.Rating {
	case "AGAIN", "HARD", "GOOD", "EASY":
	default:
		return out, "rating must be AGAIN, HARD, GOOD or EASY"
	}
	now := time.Now()
	futureCap := now.Add(24 * time.Hour)
	reviewedAt, ok := parseVocabTime(in.ReviewedAt)
	if !ok || len(in.ReviewedAt) > 64 {
		return out, "bad reviewed_at"
	}
	if reviewedAt.After(futureCap) {
		return out, "reviewed_at in the future"
	}
	if in.IntervalMinutes < 0 || in.IntervalMinutes > 10000000 {
		return out, "interval_minutes out of range"
	}
	dueAt, ok := parseVocabTime(in.DueAt)
	if !ok || len(in.DueAt) > 64 {
		return out, "bad due_at"
	}
	out.Rating = in.Rating
	out.ReviewedAt = reviewedAt
	out.IntervalMinutes = in.IntervalMinutes
	out.DueAt = dueAt
	return out, ""
}

// HandlePushVocabularyWords implements POST /v1/vocabulary/words —
// batched, compare-and-set on rev, never atomic: 200 with one result
// per item, and one bad item fails alone.
func (s *Server) HandlePushVocabularyWords(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var body struct {
		Words []json.RawMessage `json:"words"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes)).Decode(&body); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Words) == 0 {
		writeError(w, http.StatusBadRequest, "words required")
		return
	}
	if len(body.Words) > s.Cfg.Ops.VocabularyMaxBatch {
		writeError(w, http.StatusBadRequest, "batch too large")
		return
	}

	type item struct {
		Language   string              `json:"language"`
		Normalized string              `json:"normalized"`
		Status     string              `json:"status"`
		Rev        int64               `json:"rev,omitempty"`
		Generation int64               `json:"generation,omitempty"`
		Seq        int64               `json:"seq,omitempty"`
		Reason     string              `json:"reason,omitempty"`
		Server     *vocabularyWordJSON `json:"server,omitempty"`
	}
	out := struct {
		Results []item `json:"results"`
	}{Results: make([]item, len(body.Words))}

	items := make([]store.VocabularyWordWrite, 0, len(body.Words))
	itemPos := make([]int, 0, len(body.Words))
	for i, raw := range body.Words {
		if len(raw) > vocabularyMaxPerItemBytes {
			out.Results[i] = item{Status: "invalid", Reason: "item too large"}
			continue
		}
		var in vocabularyWordWriteJSON
		if err := json.Unmarshal(raw, &in); err != nil {
			out.Results[i] = item{Status: "invalid", Reason: "malformed vocabulary word"}
			continue
		}
		parsed, reason := s.validateVocabularyWordWrite(in)
		if reason != "" {
			out.Results[i] = item{Language: in.Language, Normalized: in.Normalized, Status: "invalid", Reason: reason}
			continue
		}
		items = append(items, parsed)
		itemPos = append(itemPos, i)
	}

	if len(items) > 0 {
		results, err := s.St.PushVocabularyWords(r.Context(), tok.UserID, tok.DeviceID, items, s.Cfg.Ops.VocabularyMaxWords)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "push failed")
			return
		}
		for j, res := range results {
			entry := item{Language: res.Language, Normalized: res.Normalized, Status: res.Status,
				Rev: res.Rev, Generation: res.Generation, Seq: res.Seq, Reason: res.Reason}
			if res.Server != nil {
				server := vocabularyWordToJSON(*res.Server)
				entry.Server = &server
			}
			out.Results[itemPos[j]] = entry
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// HandlePushVocabularyReviews implements POST /v1/vocabulary/reviews —
// batched, per-item results, never atomic.
func (s *Server) HandlePushVocabularyReviews(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	var body struct {
		Reviews []json.RawMessage `json:"reviews"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.Cfg.Ops.MaxBodyBytes)).Decode(&body); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Reviews) == 0 {
		writeError(w, http.StatusBadRequest, "reviews required")
		return
	}
	if len(body.Reviews) > s.Cfg.Ops.VocabularyReviewMaxBatch {
		writeError(w, http.StatusBadRequest, "batch too large")
		return
	}

	type item struct {
		ReviewID string `json:"review_id"`
		Status   string `json:"status"`
		Seq      int64  `json:"seq,omitempty"`
		Reason   string `json:"reason,omitempty"`
	}
	out := struct {
		Results []item `json:"results"`
	}{Results: make([]item, len(body.Reviews))}

	items := make([]store.VocabularyReviewWrite, 0, len(body.Reviews))
	itemPos := make([]int, 0, len(body.Reviews))
	for i, raw := range body.Reviews {
		if len(raw) > vocabularyMaxPerItemBytes {
			out.Results[i] = item{Status: "invalid", Reason: "item too large"}
			continue
		}
		var in vocabularyReviewWriteJSON
		if err := json.Unmarshal(raw, &in); err != nil {
			out.Results[i] = item{Status: "invalid", Reason: "malformed vocabulary review"}
			continue
		}
		parsed, reason := s.validateVocabularyReviewWrite(in)
		if reason != "" {
			out.Results[i] = item{ReviewID: in.ReviewID, Status: "invalid", Reason: reason}
			continue
		}
		items = append(items, parsed)
		itemPos = append(itemPos, i)
	}

	if len(items) > 0 {
		results, err := s.St.PushVocabularyReviews(r.Context(), tok.UserID, tok.DeviceID, items)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "push failed")
			return
		}
		for j, res := range results {
			out.Results[itemPos[j]] = item{ReviewID: res.ReviewID, Status: res.Status, Seq: res.Seq, Reason: res.Reason}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// HandleVocabularyChanges implements
// GET /v1/vocabulary/changes?since=<seq>&limit=<n> — the interleaved
// word and review delta feed, tombstones included.
func (s *Server) HandleVocabularyChanges(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if err != nil || since < 0 {
		since = 0
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	page, err := s.St.VocabularyChanges(r.Context(), tok.UserID, since, limit, 1<<20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "changes failed")
		return
	}
	out := struct {
		Words      []vocabularyWordJSON   `json:"words"`
		Reviews    []vocabularyReviewJSON `json:"reviews"`
		HighWater  int64                  `json:"high_water"`
		NextCursor int64                  `json:"next_cursor"`
		HasMore    bool                   `json:"has_more"`
	}{HighWater: page.HighWater, NextCursor: page.NextCursor, HasMore: page.HasMore,
		Words: []vocabularyWordJSON{}, Reviews: []vocabularyReviewJSON{}}
	for _, word := range page.Words {
		out.Words = append(out.Words, vocabularyWordToJSON(word))
	}
	for _, review := range page.Reviews {
		out.Reviews = append(out.Reviews, vocabularyReviewToJSON(review))
	}
	writeJSON(w, http.StatusOK, out)
}
