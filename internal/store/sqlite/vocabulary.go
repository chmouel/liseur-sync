package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

const vocabularyWordCols = `user_id, language, normalized, seq, rev, generation, word,
    definition, translation_language, notes, definition_edited,
    definition_updated_at, notes_updated_at, added_at, baseline_due_at,
    baseline_reviewed_at, baseline_streak, baseline_interval_minutes,
    baseline_repetitions, occurrences_json, device_id, updated_at, deleted_at`

const vocabularyReviewCols = `user_id, review_id, language, normalized, word_generation,
    seq, rating, reviewed_at, interval_minutes, due_at, device_id, updated_at, deleted_at`

func scanVocabularyWord(row interface{ Scan(...any) error }) (store.VocabularyWord, error) {
	var w store.VocabularyWord
	var definition, translationLang, defUpdatedAt, notesUpdatedAt sql.NullString
	var addedAt, updatedAt string
	var deletedAt, baselineDueAt, baselineReviewedAt sql.NullString
	var defEdited int
	var streak, intervalMinutes, repetitions int
	err := row.Scan(&w.UserID, &w.Language, &w.Normalized, &w.Seq, &w.Rev, &w.Generation,
		&w.Word, &definition, &translationLang, &w.Notes, &defEdited,
		&defUpdatedAt, &notesUpdatedAt, &addedAt, &baselineDueAt,
		&baselineReviewedAt, &streak, &intervalMinutes, &repetitions,
		&w.OccurrencesJSON, &w.DeviceID, &updatedAt, &deletedAt)
	if err != nil {
		return w, err
	}
	if definition.Valid {
		w.Definition = &definition.String
	}
	if translationLang.Valid {
		w.TranslationLanguage = &translationLang.String
	}
	w.DefinitionEdited = defEdited != 0
	if w.DefinitionUpdatedAt, err = parseTimePtr(defUpdatedAt); err != nil {
		return w, err
	}
	if w.NotesUpdatedAt, err = parseTimePtr(notesUpdatedAt); err != nil {
		return w, err
	}
	if w.AddedAt, err = parseTime(addedAt); err != nil {
		return w, err
	}
	if w.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return w, err
	}
	if w.DeletedAt, err = parseTimePtr(deletedAt); err != nil {
		return w, err
	}
	if baselineDueAt.Valid {
		due, err := parseTime(baselineDueAt.String)
		if err != nil {
			return w, err
		}
		b := &store.VocabularyBaseline{
			DueAt: due, Streak: streak,
			IntervalMinutes: intervalMinutes, Repetitions: repetitions,
		}
		if b.ReviewedAt, err = parseTimePtr(baselineReviewedAt); err != nil {
			return w, err
		}
		w.Baseline = b
	}
	if len(w.OccurrencesJSON) > 0 {
		if err := json.Unmarshal(w.OccurrencesJSON, &w.Occurrences); err != nil {
			return w, err
		}
	}
	return w, nil
}

func scanVocabularyReview(row interface{ Scan(...any) error }) (store.VocabularyReview, error) {
	var r store.VocabularyReview
	var reviewedAt, dueAt, updatedAt string
	var deletedAt sql.NullString
	err := row.Scan(&r.UserID, &r.ReviewID, &r.Language, &r.Normalized,
		&r.WordGeneration, &r.Seq, &r.Rating, &reviewedAt, &r.IntervalMinutes,
		&dueAt, &r.DeviceID, &updatedAt, &deletedAt)
	if err != nil {
		return r, err
	}
	if r.ReviewedAt, err = parseTime(reviewedAt); err != nil {
		return r, err
	}
	if r.DueAt, err = parseTime(dueAt); err != nil {
		return r, err
	}
	if r.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return r, err
	}
	if r.DeletedAt, err = parseTimePtr(deletedAt); err != nil {
		return r, err
	}
	return r, nil
}

// nextVocabularySeqTx advances the per-user vocabulary counter — a
// third counter beside the op and annotation ones, never shared with
// them. Advanced inside the write's transaction, holding the counter
// row, so seq order is commit order per user.
func nextVocabularySeqTx(ctx context.Context, tx *sql.Tx, userID string) (int64, error) {
	var seq int64
	err := tx.QueryRowContext(ctx,
		`INSERT INTO seq_counters (user_id, next_vocabulary_seq) VALUES (?, 2)
		 ON CONFLICT(user_id) DO UPDATE SET next_vocabulary_seq = next_vocabulary_seq + 1
		 RETURNING next_vocabulary_seq - 1`, userID).Scan(&seq)
	return seq, err
}

func vocabularyWordTx(ctx context.Context, tx *sql.Tx, userID, language, normalized string) (store.VocabularyWord, bool, error) {
	w, err := scanVocabularyWord(tx.QueryRowContext(ctx,
		`SELECT `+vocabularyWordCols+` FROM vocabulary_words
		 WHERE user_id = ? AND language = ? AND normalized = ?`,
		userID, language, normalized))
	if errors.Is(err, sql.ErrNoRows) {
		return w, false, nil
	}
	return w, err == nil, err
}

func vocabularyReviewTx(ctx context.Context, tx *sql.Tx, userID, reviewID string) (store.VocabularyReview, bool, error) {
	r, err := scanVocabularyReview(tx.QueryRowContext(ctx,
		`SELECT `+vocabularyReviewCols+` FROM vocabulary_reviews
		 WHERE user_id = ? AND review_id = ?`, userID, reviewID))
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

func liveVocabularyWordCountTx(ctx context.Context, tx *sql.Tx, userID string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM vocabulary_words
		 WHERE user_id = ? AND deleted_at IS NULL`, userID).Scan(&n)
	return n, err
}

// PushVocabularyWords applies a batch of word writes, never atomically:
// one result per item, one bad item fails alone. Compare-and-set on
// rev; a retry of an accepted write (same key, same base rev,
// byte-identical payload) is acknowledged, not conflicted. A delete
// tombstones the word and every live review for it in the same
// transaction, so a synced device observes both on the feed.
func (s *Store) PushVocabularyWords(ctx context.Context, userID, deviceID string, items []store.VocabularyWordWrite, maxLiveWords int) ([]store.VocabularyWordResult, error) {
	results := make([]store.VocabularyWordResult, len(items))
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := lockWorkGraph(ctx, tx, userID); err != nil {
			return err
		}
		now := time.Now()
		for i, item := range items {
			r, err := pushVocabularyWordTx(ctx, tx, userID, deviceID, item, maxLiveWords, now)
			if err != nil {
				return err
			}
			results[i] = r
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if store.AnyVocabularyWordApplied(results) {
		s.Notify(userID, store.TopicVocabulary)
	}
	return results, nil
}

func pushVocabularyWordTx(ctx context.Context, tx *sql.Tx, userID, deviceID string, item store.VocabularyWordWrite, maxLiveWords int, now time.Time) (store.VocabularyWordResult, error) {
	r := store.VocabularyWordResult{Language: item.Language, Normalized: item.Normalized, Status: "applied"}
	stored, found, err := vocabularyWordTx(ctx, tx, userID, item.Language, item.Normalized)
	if err != nil {
		return r, err
	}

	if item.Deleted {
		if !found {
			r.Status = "duplicate"
			return r, nil
		}
		if stored.Deleted() && (stored.Rev == item.BaseRev || stored.Rev == item.BaseRev+1) {
			r.Status, r.Rev, r.Generation, r.Seq = "duplicate", stored.Rev, stored.Generation, stored.Seq
			return r, nil
		}
		if stored.Rev != item.BaseRev {
			r.Status, r.Server = "conflict", &stored
			return r, nil
		}
		return tombstoneVocabularyWordTx(ctx, tx, userID, stored, now)
	}

	if found {
		if stored.Rev == item.BaseRev+1 && store.SameVocabularyWordPayload(stored, item, deviceID) {
			r.Status, r.Rev, r.Generation, r.Seq = "duplicate", stored.Rev, stored.Generation, stored.Seq
			return r, nil
		}
		if stored.Rev != item.BaseRev {
			r.Status, r.Server = "conflict", &stored
			return r, nil
		}
	}
	if !found || stored.Deleted() {
		if maxLiveWords > 0 {
			n, err := liveVocabularyWordCountTx(ctx, tx, userID)
			if err != nil {
				return r, err
			}
			if n >= maxLiveWords {
				r.Status, r.Reason = "invalid", "word limit reached"
				return r, nil
			}
		}
	}
	seq, err := nextVocabularySeqTx(ctx, tx, userID)
	if err != nil {
		return r, err
	}
	generation := int64(1)
	rev := int64(1)
	if found {
		rev = stored.Rev + 1
		if stored.Deleted() {
			generation = stored.Generation + 1
		} else {
			generation = stored.Generation
		}
	}
	occ := item.OccurrencesJSON
	if len(occ) == 0 {
		occ = []byte("[]")
	}
	defEdited := 0
	if item.DefinitionEdited {
		defEdited = 1
	}
	if !found {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO vocabulary_words (`+vocabularyWordCols+`)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
			userID, item.Language, item.Normalized, seq, rev, generation, item.Word,
			nullStr(item.Definition), nullStr(item.TranslationLanguage), item.Notes,
			defEdited, formatTimePtr(item.DefinitionUpdatedAt), formatTimePtr(item.NotesUpdatedAt),
			formatTime(item.AddedAt), baselineDueAt(item.Baseline), baselineReviewedAt(item.Baseline),
			baselineStreak(item.Baseline), baselineIntervalMinutes(item.Baseline),
			baselineRepetitions(item.Baseline), occ, deviceID, formatTime(now))
	} else {
		_, err = tx.ExecContext(ctx,
			`UPDATE vocabulary_words SET seq = ?, rev = ?, generation = ?, word = ?,
			        definition = ?, translation_language = ?, notes = ?, definition_edited = ?,
			        definition_updated_at = ?, notes_updated_at = ?, added_at = ?,
			        baseline_due_at = ?, baseline_reviewed_at = ?, baseline_streak = ?,
			        baseline_interval_minutes = ?, baseline_repetitions = ?, occurrences_json = ?,
			        device_id = ?, updated_at = ?, deleted_at = NULL
			 WHERE user_id = ? AND language = ? AND normalized = ?`,
			seq, rev, generation, item.Word, nullStr(item.Definition),
			nullStr(item.TranslationLanguage), item.Notes, defEdited,
			formatTimePtr(item.DefinitionUpdatedAt), formatTimePtr(item.NotesUpdatedAt),
			formatTime(item.AddedAt), baselineDueAt(item.Baseline), baselineReviewedAt(item.Baseline),
			baselineStreak(item.Baseline), baselineIntervalMinutes(item.Baseline),
			baselineRepetitions(item.Baseline), occ, deviceID, formatTime(now),
			userID, item.Language, item.Normalized)
	}
	if err != nil {
		return r, err
	}
	r.Rev, r.Generation, r.Seq = rev, generation, seq
	return r, nil
}

// tombstoneVocabularyWordTx writes the word's tombstone and, in the
// same transaction, tombstones every live review for it with a fresh
// seq each, so a synced device observes the reviews disappear on the
// feed rather than keeping them against a word it no longer has.
func tombstoneVocabularyWordTx(ctx context.Context, tx *sql.Tx, userID string, stored store.VocabularyWord, now time.Time) (store.VocabularyWordResult, error) {
	r := store.VocabularyWordResult{Language: stored.Language, Normalized: stored.Normalized, Status: "applied"}
	seq, err := nextVocabularySeqTx(ctx, tx, userID)
	if err != nil {
		return r, err
	}
	nowStr := formatTime(now)
	if _, err := tx.ExecContext(ctx,
		`UPDATE vocabulary_words SET seq = ?, rev = rev + 1, word = '',
		        definition = NULL, translation_language = NULL, notes = '',
		        definition_edited = 0, definition_updated_at = NULL, notes_updated_at = NULL,
		        baseline_due_at = NULL, baseline_reviewed_at = NULL, baseline_streak = 0,
		        baseline_interval_minutes = 0, baseline_repetitions = 0,
		        occurrences_json = '[]', device_id = '', updated_at = ?, deleted_at = ?
		 WHERE user_id = ? AND language = ? AND normalized = ?`,
		seq, nowStr, nowStr, userID, stored.Language, stored.Normalized); err != nil {
		return r, err
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT review_id FROM vocabulary_reviews
		 WHERE user_id = ? AND language = ? AND normalized = ? AND deleted_at IS NULL
		 ORDER BY seq`, userID, stored.Language, stored.Normalized)
	if err != nil {
		return r, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return r, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return r, err
	}
	for _, id := range ids {
		rSeq, err := nextVocabularySeqTx(ctx, tx, userID)
		if err != nil {
			return r, err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE vocabulary_reviews SET seq = ?, updated_at = ?, deleted_at = ?
			 WHERE user_id = ? AND review_id = ?`,
			rSeq, nowStr, nowStr, userID, id); err != nil {
			return r, err
		}
	}
	r.Rev, r.Generation, r.Seq = stored.Rev+1, stored.Generation, seq
	return r, nil
}

// PushVocabularyReviews applies a batch of review writes. The review id
// is checked before the word: a review that already exists is answered
// from the review alone, and a new review must name a live word at the
// generation it was written against.
func (s *Store) PushVocabularyReviews(ctx context.Context, userID, deviceID string, items []store.VocabularyReviewWrite) ([]store.VocabularyReviewResult, error) {
	results := make([]store.VocabularyReviewResult, len(items))
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := lockWorkGraph(ctx, tx, userID); err != nil {
			return err
		}
		now := time.Now()
		for i, item := range items {
			r, err := pushVocabularyReviewTx(ctx, tx, userID, deviceID, item, now)
			if err != nil {
				return err
			}
			results[i] = r
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if store.AnyVocabularyReviewApplied(results) {
		s.Notify(userID, store.TopicVocabulary)
	}
	return results, nil
}

func pushVocabularyReviewTx(ctx context.Context, tx *sql.Tx, userID, deviceID string, item store.VocabularyReviewWrite, now time.Time) (store.VocabularyReviewResult, error) {
	r := store.VocabularyReviewResult{ReviewID: item.ReviewID, Status: "applied"}
	stored, found, err := vocabularyReviewTx(ctx, tx, userID, item.ReviewID)
	if err != nil {
		return r, err
	}
	if found {
		if item.Deleted {
			if stored.Language != item.Language || stored.Normalized != item.Normalized ||
				stored.WordGeneration != item.WordGeneration {
				r.Status = "conflict"
				return r, nil
			}
			if stored.Deleted() {
				r.Status, r.Seq = "duplicate", stored.Seq
				return r, nil
			}
			seq, err := nextVocabularySeqTx(ctx, tx, userID)
			if err != nil {
				return r, err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE vocabulary_reviews SET seq = ?, updated_at = ?, deleted_at = ?
				 WHERE user_id = ? AND review_id = ?`,
				seq, formatTime(now), formatTime(now), userID, item.ReviewID); err != nil {
				return r, err
			}
			r.Seq = seq
			return r, nil
		}
		if store.SameVocabularyReviewPayload(stored, item) {
			r.Status, r.Seq = "duplicate", stored.Seq
			return r, nil
		}
		r.Status = "conflict"
		return r, nil
	}

	if item.Deleted {
		r.Status, r.Reason = "invalid", "unknown review"
		return r, nil
	}
	word, found, err := vocabularyWordTx(ctx, tx, userID, item.Language, item.Normalized)
	if err != nil {
		return r, err
	}
	if !found || word.Deleted() || word.Generation != item.WordGeneration {
		r.Status, r.Reason = "invalid", "unknown word or stale generation"
		return r, nil
	}
	seq, err := nextVocabularySeqTx(ctx, tx, userID)
	if err != nil {
		return r, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO vocabulary_reviews (`+vocabularyReviewCols+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		userID, item.ReviewID, item.Language, item.Normalized, item.WordGeneration,
		seq, item.Rating, formatTime(item.ReviewedAt), item.IntervalMinutes,
		formatTime(item.DueAt), deviceID, formatTime(now)); err != nil {
		return r, err
	}
	r.Seq = seq
	return r, nil
}

// VocabularyChanges returns words and reviews with seq > since, in seq
// order, tombstones included. It reads one consistent snapshot so the
// high water and the page agree.
func (s *Store) VocabularyChanges(ctx context.Context, userID string, since int64, limit int, maxBytes int) (store.VocabularyChangesPage, error) {
	var page store.VocabularyChangesPage
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE((SELECT next_vocabulary_seq - 1 FROM seq_counters WHERE user_id = ?), 0)`,
		userID).Scan(&page.HighWater); err != nil {
		return page, err
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT kind, seq, k1, k2 FROM (
		     SELECT 'word' AS kind, seq, language AS k1, normalized AS k2
		       FROM vocabulary_words WHERE user_id = ? AND seq > ? AND seq <= ?
		     UNION ALL
		     SELECT 'review' AS kind, seq, review_id AS k1, '' AS k2
		       FROM vocabulary_reviews WHERE user_id = ? AND seq > ? AND seq <= ?
		 ) ORDER BY seq ASC LIMIT ?`,
		userID, since, page.HighWater, userID, since, page.HighWater, limit+1)
	if err != nil {
		return page, err
	}
	type key struct {
		kind   string
		seq    int64
		k1, k2 string
	}
	var keys []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.kind, &k.seq, &k.k1, &k.k2); err != nil {
			rows.Close()
			return page, err
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return page, err
	}

	var emitted, used int
	for _, k := range keys {
		if emitted >= limit {
			page.HasMore = true
			break
		}
		var size int
		if k.kind == "word" {
			w, err := scanVocabularyWord(tx.QueryRowContext(ctx,
				`SELECT `+vocabularyWordCols+` FROM vocabulary_words
				 WHERE user_id = ? AND language = ? AND normalized = ?`,
				userID, k.k1, k.k2))
			if err != nil {
				return page, err
			}
			size = vocabularyWordSize(w)
			if maxBytes > 0 && emitted > 0 && used+size > maxBytes {
				page.HasMore = true
				break
			}
			page.Words = append(page.Words, w)
		} else {
			r, err := scanVocabularyReview(tx.QueryRowContext(ctx,
				`SELECT `+vocabularyReviewCols+` FROM vocabulary_reviews
				 WHERE user_id = ? AND review_id = ?`, userID, k.k1))
			if err != nil {
				return page, err
			}
			size = 256
			if maxBytes > 0 && emitted > 0 && used+size > maxBytes {
				page.HasMore = true
				break
			}
			page.Reviews = append(page.Reviews, r)
		}
		used += size
		emitted++
		page.NextCursor = k.seq
	}
	if !page.HasMore {
		if page.HighWater > since {
			page.NextCursor = page.HighWater
		} else {
			page.NextCursor = since
		}
	}
	return page, nil
}

func vocabularyWordSize(w store.VocabularyWord) int {
	size := 256 + len(w.Word) + len(w.Notes) + len(w.OccurrencesJSON)
	if w.Definition != nil {
		size += len(*w.Definition)
	}
	if w.TranslationLanguage != nil {
		size += len(*w.TranslationLanguage)
	}
	return size
}

func baselineDueAt(b *store.VocabularyBaseline) sql.NullString {
	if b == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(b.DueAt), Valid: true}
}

func baselineReviewedAt(b *store.VocabularyBaseline) sql.NullString {
	if b == nil {
		return formatTimePtr(nil)
	}
	return formatTimePtr(b.ReviewedAt)
}

func baselineStreak(b *store.VocabularyBaseline) int {
	if b == nil {
		return 0
	}
	return b.Streak
}

func baselineIntervalMinutes(b *store.VocabularyBaseline) int {
	if b == nil {
		return 0
	}
	return b.IntervalMinutes
}

func baselineRepetitions(b *store.VocabularyBaseline) int {
	if b == nil {
		return 0
	}
	return b.Repetitions
}
