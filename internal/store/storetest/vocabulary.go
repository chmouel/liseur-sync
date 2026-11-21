package storetest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func vocabWordWrite(language, normalized string, baseRev int64) store.VocabularyWordWrite {
	return store.VocabularyWordWrite{
		Language:        language,
		Normalized:      normalized,
		BaseRev:         baseRev,
		Word:            normalized,
		AddedAt:         time.Now().UTC().Truncate(time.Microsecond),
		OccurrencesJSON: []byte("[]"),
	}
}

func pushVocabWord(t *testing.T, s store.Store, userID, deviceID string, w store.VocabularyWordWrite) store.VocabularyWordResult {
	t.Helper()
	res, err := s.PushVocabularyWords(context.Background(), userID, deviceID, []store.VocabularyWordWrite{w}, 50000)
	if err != nil {
		t.Fatal(err)
	}
	return res[0]
}

func vocabReviewWrite(word store.VocabularyWord, reviewID, rating string) store.VocabularyReviewWrite {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return store.VocabularyReviewWrite{
		ReviewID:        reviewID,
		Language:        word.Language,
		Normalized:      word.Normalized,
		WordGeneration:  word.Generation,
		Rating:          rating,
		ReviewedAt:      now,
		IntervalMinutes: 10,
		DueAt:           now.Add(10 * 24 * time.Hour),
	}
}

func pushVocabReview(t *testing.T, s store.Store, userID, deviceID string, r store.VocabularyReviewWrite) store.VocabularyReviewResult {
	t.Helper()
	res, err := s.PushVocabularyReviews(context.Background(), userID, deviceID, []store.VocabularyReviewWrite{r})
	if err != nil {
		t.Fatal(err)
	}
	return res[0]
}

func testVocabulary(t *testing.T, open OpenFunc) {
	t.Run("WordLifecycle", func(t *testing.T) { testVocabularyWordLifecycle(t, open) })
	t.Run("DeleteCascadesReviewTombstones", func(t *testing.T) { testVocabularyDeleteCascades(t, open) })
	t.Run("ResurrectionBumpsGeneration", func(t *testing.T) { testVocabularyResurrection(t, open) })
	t.Run("ReviewUndoAndStaleGeneration", func(t *testing.T) { testVocabularyReviewUndo(t, open) })
	t.Run("WordLimit", func(t *testing.T) { testVocabularyWordLimit(t, open) })
	t.Run("ChangesPagination", func(t *testing.T) { testVocabularyChangesPagination(t, open) })
	t.Run("ChangesByteBudget", func(t *testing.T) { testVocabularyChangesByteBudget(t, open) })
	t.Run("OccurrencesRoundTrip", func(t *testing.T) { testVocabularyOccurrencesRoundTrip(t, open) })
	t.Run("OccurrencesRetry", func(t *testing.T) { testVocabularyOccurrencesRetry(t, open) })
	t.Run("AbsentBaseRev", func(t *testing.T) { testVocabularyAbsentBaseRev(t, open) })
	t.Run("EscapeHeavyByteBudget", func(t *testing.T) { testVocabularyEscapeByteBudget(t, open) })
	t.Run("HTMLByteBudget", func(t *testing.T) { testVocabularyHTMLByteBudget(t, open) })
	t.Run("ConcurrentUpdates", func(t *testing.T) { testVocabularyConcurrentUpdates(t, open) })
	t.Run("UserIsolation", func(t *testing.T) { testVocabularyUserIsolation(t, open) })
}

func testVocabularyWordLifecycle(t *testing.T, open OpenFunc) {
	s := open(t)
	u := MkUser(t, s, "vocab")

	create := vocabWordWrite("en", "ephemeral", 0)
	r := pushVocabWord(t, s, u.ID, "d1", create)
	if r.Status != "applied" || r.Rev != 1 || r.Generation != 1 || r.Seq == 0 {
		t.Fatalf("create: %+v", r)
	}

	// A byte-identical retry of the create is acknowledged.
	if r := pushVocabWord(t, s, u.ID, "d1", create); r.Status != "duplicate" || r.Rev != 1 || r.Generation != 1 {
		t.Fatalf("create retry: %+v", r)
	}

	// An edit at the rev the client read applies.
	edit := create
	edit.BaseRev = 1
	edit.Definition = Ptr("lasting for a very short time")
	r = pushVocabWord(t, s, u.ID, "d1", edit)
	if r.Status != "applied" || r.Rev != 2 || r.Generation != 1 {
		t.Fatalf("edit: %+v", r)
	}

	// The same edit pushed again is a duplicate, not a second write.
	if r := pushVocabWord(t, s, u.ID, "d1", edit); r.Status != "duplicate" || r.Rev != 2 {
		t.Fatalf("edit retry: %+v", r)
	}

	// A stale base rev is a conflict carrying the server copy.
	stale := create
	stale.BaseRev = 1
	stale.Word = "ephemerally"
	r = pushVocabWord(t, s, u.ID, "d1", stale)
	if r.Status != "conflict" || r.Server == nil || r.Server.Rev != 2 ||
		r.Server.Definition == nil || *r.Server.Definition != "lasting for a very short time" {
		t.Fatalf("stale edit: %+v", r)
	}
}

func testVocabularyDeleteCascades(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "deleter")

	create := vocabWordWrite("en", "serendipity", 0)
	if r := pushVocabWord(t, s, u.ID, "d1", create); r.Status != "applied" {
		t.Fatalf("create: %+v", r)
	}
	word := store.VocabularyWord{Language: "en", Normalized: "serendipity", Generation: 1}
	for _, id := range []string{"r1", "r2"} {
		if r := pushVocabReview(t, s, u.ID, "d1", vocabReviewWrite(word, id, "GOOD")); r.Status != "applied" {
			t.Fatalf("review %s: %+v", id, r)
		}
	}

	del := create
	del.BaseRev, del.Deleted = 1, true
	r := pushVocabWord(t, s, u.ID, "d1", del)
	if r.Status != "applied" || r.Rev != 2 {
		t.Fatalf("delete: %+v", r)
	}
	// A retried delete (same base rev) and a delete quoting the
	// tombstone's own rev are both already accepted.
	if r := pushVocabWord(t, s, u.ID, "d1", del); r.Status != "duplicate" || r.Rev != 2 {
		t.Fatalf("delete retry: %+v", r)
	}
	del.BaseRev = 2
	if r := pushVocabWord(t, s, u.ID, "d1", del); r.Status != "duplicate" {
		t.Fatalf("delete of tombstone: %+v", r)
	}

	// The feed carries the word tombstone and both review tombstones,
	// each with a fresh seq and in seq order.
	page, err := s.VocabularyChanges(ctx, u.ID, 0, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Words) != 1 || !page.Words[0].Deleted() {
		t.Fatalf("word tombstone missing: %+v", page.Words)
	}
	if len(page.Reviews) != 2 {
		t.Fatalf("review tombstones missing: %+v", page.Reviews)
	}
	if !page.Reviews[0].Deleted() || !page.Reviews[1].Deleted() {
		t.Fatalf("reviews not tombstoned: %+v", page.Reviews)
	}
	if page.Reviews[0].Seq <= page.Words[0].Seq || page.Reviews[1].Seq <= page.Reviews[0].Seq {
		t.Fatalf("review tombstones did not get fresh seqs: word %d reviews %d,%d",
			page.Words[0].Seq, page.Reviews[0].Seq, page.Reviews[1].Seq)
	}

	// A review against the deleted word is refused.
	if r := pushVocabReview(t, s, u.ID, "d1", vocabReviewWrite(word, "r3", "EASY")); r.Status != "invalid" {
		t.Fatalf("review against deleted word: %+v", r)
	}
}

func testVocabularyResurrection(t *testing.T, open OpenFunc) {
	s := open(t)
	u := MkUser(t, s, "phoenix")

	create := vocabWordWrite("en", "recalcitrant", 0)
	if r := pushVocabWord(t, s, u.ID, "d1", create); r.Status != "applied" {
		t.Fatalf("create: %+v", r)
	}
	del := create
	del.BaseRev, del.Deleted = 1, true
	if r := pushVocabWord(t, s, u.ID, "d1", del); r.Status != "applied" {
		t.Fatalf("delete: %+v", r)
	}

	// A create onto the tombstone (base rev matching it) resurrects the
	// word and bumps the generation.
	resurrect := vocabWordWrite("en", "recalcitrant", 2)
	r := pushVocabWord(t, s, u.ID, "d1", resurrect)
	if r.Status != "applied" || r.Rev != 3 || r.Generation != 2 {
		t.Fatalf("resurrect: %+v", r)
	}

	// A review written against the dead generation is stale; one at the
	// new generation lands.
	stale := vocabReviewWrite(store.VocabularyWord{Language: "en", Normalized: "recalcitrant", Generation: 1}, "old", "GOOD")
	if r := pushVocabReview(t, s, u.ID, "d1", stale); r.Status != "invalid" {
		t.Fatalf("stale generation review: %+v", r)
	}
	fresh := vocabReviewWrite(store.VocabularyWord{Language: "en", Normalized: "recalcitrant", Generation: 2}, "new", "GOOD")
	if r := pushVocabReview(t, s, u.ID, "d1", fresh); r.Status != "applied" {
		t.Fatalf("fresh generation review: %+v", r)
	}
}

func testVocabularyReviewUndo(t *testing.T, open OpenFunc) {
	s := open(t)
	u := MkUser(t, s, "undoer")

	create := vocabWordWrite("en", "laconic", 0)
	if r := pushVocabWord(t, s, u.ID, "d1", create); r.Status != "applied" {
		t.Fatalf("create: %+v", r)
	}
	word := store.VocabularyWord{Language: "en", Normalized: "laconic", Generation: 1}
	review := vocabReviewWrite(word, "r1", "HARD")
	if r := pushVocabReview(t, s, u.ID, "d1", review); r.Status != "applied" {
		t.Fatalf("review: %+v", r)
	}
	// The identical review is a duplicate.
	if r := pushVocabReview(t, s, u.ID, "d1", review); r.Status != "duplicate" {
		t.Fatalf("review retry: %+v", r)
	}

	// Undo the review: a tombstone.
	undo := review
	undo.Deleted = true
	if r := pushVocabReview(t, s, u.ID, "d1", undo); r.Status != "applied" {
		t.Fatalf("undo: %+v", r)
	}
	// Undoing again is already accepted.
	if r := pushVocabReview(t, s, u.ID, "d1", undo); r.Status != "duplicate" {
		t.Fatalf("undo retry: %+v", r)
	}
	// Replaying the original (undeleted) payload matches the stored one
	// ignoring deleted_at, so it is a duplicate, not a resurrection.
	if r := pushVocabReview(t, s, u.ID, "d1", review); r.Status != "duplicate" {
		t.Fatalf("undeleted replay: %+v", r)
	}
	// A conflicting payload on the same id is a conflict.
	conflicting := review
	conflicting.Rating = "EASY"
	if r := pushVocabReview(t, s, u.ID, "d1", conflicting); r.Status != "conflict" {
		t.Fatalf("conflicting review: %+v", r)
	}

	// A delete of an unknown review is invalid.
	unknown := review
	unknown.ReviewID, unknown.Deleted = "ghost", true
	if r := pushVocabReview(t, s, u.ID, "d1", unknown); r.Status != "invalid" {
		t.Fatalf("unknown review delete: %+v", r)
	}
}

func testVocabularyWordLimit(t *testing.T, open OpenFunc) {
	s := open(t)
	u := MkUser(t, s, "capped")
	ctx := context.Background()

	const capN = 3
	for i := 0; i < capN; i++ {
		res, err := s.PushVocabularyWords(ctx, u.ID, "d1",
			[]store.VocabularyWordWrite{vocabWordWrite("en", fmt.Sprintf("w-%d", i), 0)}, capN)
		if err != nil || res[0].Status != "applied" {
			t.Fatalf("under cap: %+v %v", res, err)
		}
	}
	res, err := s.PushVocabularyWords(ctx, u.ID, "d1",
		[]store.VocabularyWordWrite{vocabWordWrite("en", "over", 0)}, capN)
	if err != nil || res[0].Status != "invalid" || res[0].Reason != "word limit reached" {
		t.Fatalf("over cap: %+v %v", res, err)
	}
}

func testVocabularyChangesPagination(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "pager")

	const n = 7
	for i := 0; i < n; i++ {
		if r := pushVocabWord(t, s, u.ID, "d1", vocabWordWrite("en", fmt.Sprintf("w-%d", i), 0)); r.Status != "applied" {
			t.Fatalf("push %d: %+v", i, r)
		}
	}

	seen := 0
	since := int64(0)
	for {
		page, err := s.VocabularyChanges(ctx, u.ID, since, 3, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range page.Words {
			if w.Seq <= since {
				t.Fatalf("out of order: seq %d after cursor %d", w.Seq, since)
			}
			since = w.Seq
			seen++
		}
		if !page.HasMore {
			if page.NextCursor != page.HighWater {
				t.Fatalf("next cursor %d, high water %d", page.NextCursor, page.HighWater)
			}
			break
		}
		if page.NextCursor != since {
			t.Fatalf("next cursor %d, last emitted %d", page.NextCursor, since)
		}
	}
	if seen != n {
		t.Fatalf("feed lost records: %d of %d", seen, n)
	}
}

func testVocabularyChangesByteBudget(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "budget")

	// A handful of words, each carrying a chunky occurrence so the byte
	// budget bites before the item limit does.
	occ := make([]byte, 0, 900)
	occ = append(occ, '"')
	for i := 0; i < 900; i++ {
		occ = append(occ, 'x')
	}
	occ = append(occ, '"')
	for i := 0; i < 5; i++ {
		w := vocabWordWrite("en", fmt.Sprintf("w-%d", i), 0)
		w.Notes = string(occ[1 : len(occ)-1])
		if r := pushVocabWord(t, s, u.ID, "d1", w); r.Status != "applied" {
			t.Fatalf("push %d: %+v", i, r)
		}
	}

	// A tiny budget still emits at least one item and reports more.
	page, err := s.VocabularyChanges(ctx, u.ID, 0, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Words) != 1 || !page.HasMore {
		t.Fatalf("byte budget: %d items, has_more=%v", len(page.Words), page.HasMore)
	}
	if page.NextCursor != page.Words[0].Seq {
		t.Fatalf("byte budget cursor %d, last emitted %d", page.NextCursor, page.Words[0].Seq)
	}

	// With no budget all five arrive.
	all, err := s.VocabularyChanges(ctx, u.ID, 0, 100, 0)
	if err != nil || len(all.Words) != 5 || all.HasMore {
		t.Fatalf("unbounded page: %d items, %v", len(all.Words), err)
	}
}

func testVocabularyConcurrentUpdates(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "racer")

	create := vocabWordWrite("en", "concurrent", 0)
	if r := pushVocabWord(t, s, u.ID, "d1", create); r.Status != "applied" {
		t.Fatalf("create: %+v", r)
	}

	const writers = 4
	var wg sync.WaitGroup
	statuses := make(chan string, writers)
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := create
			w.BaseRev = 1
			w.Notes = fmt.Sprintf("writer %d", i)
			res, err := s.PushVocabularyWords(ctx, u.ID, fmt.Sprintf("dev-%d", i),
				[]store.VocabularyWordWrite{w}, 50000)
			if err != nil {
				errs <- err
				return
			}
			statuses <- res[0].Status
		}(i)
	}
	wg.Wait()
	close(errs)
	close(statuses)
	for err := range errs {
		t.Fatal(err)
	}
	applied := 0
	for st := range statuses {
		switch st {
		case "applied":
			applied++
		case "conflict":
		default:
			t.Fatalf("unexpected status %q", st)
		}
	}
	if applied != 1 {
		t.Fatalf("exactly one concurrent update must win: %d applied", applied)
	}
	page, err := s.VocabularyChanges(ctx, u.ID, 0, 100, 0)
	if err != nil || len(page.Words) != 1 || page.Words[0].Rev != 2 {
		t.Fatalf("final word: %+v %v", page.Words, err)
	}
}

func testVocabularyUserIsolation(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	alice := MkUser(t, s, "vocab-alice")
	bob := MkUser(t, s, "vocab-bob")

	if r := pushVocabWord(t, s, alice.ID, "da", vocabWordWrite("en", "mine", 0)); r.Status != "applied" {
		t.Fatalf("alice create: %+v", r)
	}

	bobPage, err := s.VocabularyChanges(ctx, bob.ID, 0, 100, 0)
	if err != nil || len(bobPage.Words) != 0 || len(bobPage.Reviews) != 0 {
		t.Fatalf("bob sees alice's words: %+v %v", bobPage, err)
	}
	// The same key under another account is that account's own row.
	if r := pushVocabWord(t, s, bob.ID, "db", vocabWordWrite("en", "mine", 0)); r.Status != "applied" {
		t.Fatalf("bob create: %+v", r)
	}
	alicePage, err := s.VocabularyChanges(ctx, alice.ID, 0, 100, 0)
	if err != nil || len(alicePage.Words) != 1 || alicePage.Words[0].UserID != alice.ID {
		t.Fatalf("alice page: %+v %v", alicePage, err)
	}
}

// vocabOccurrencesJSON carries a sentence, a locator and every field a
// decoded occurrence should round-trip.
func vocabOccurrencesJSON() []byte {
	return []byte(`[{"occurrence_key":"occ-1","work_id":"work-1",` +
		`"book_title":"A Memory Called Empire","location_key":"chapter-3",` +
		`"locator":{"href":"ch3.xhtml","n":42,"nested":{"a":1,"b":[1,2,3]}},` +
		`"sentence":"the sea was calm","last_seen_at":"2025-06-01T10:00:00Z",` +
		`"encounters":2}]`)
}

func testVocabularyOccurrencesRoundTrip(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "vocab-occ")

	create := vocabWordWrite("en", "occurrence", 0)
	create.OccurrencesJSON = vocabOccurrencesJSON()
	if r := pushVocabWord(t, s, u.ID, "d1", create); r.Status != "applied" {
		t.Fatalf("create: %+v", r)
	}

	// The delta feed decodes the occurrences packet, sentence and
	// locator included, not just stores its bytes.
	page, err := s.VocabularyChanges(ctx, u.ID, 0, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Words) != 1 || len(page.Words[0].Occurrences) != 1 {
		t.Fatalf("occurrences not decoded: %+v", page.Words)
	}
	occ := page.Words[0].Occurrences[0]
	if occ.Sentence == nil || *occ.Sentence != "the sea was calm" ||
		len(occ.LocatorJSON) == 0 || occ.Encounters != 2 {
		t.Fatalf("occurrence payload: %+v", occ)
	}

	// A conflict's server copy carries the decoded occurrences too.
	stale := create
	stale.BaseRev = 0
	stale.Word = "occurrences"
	r := pushVocabWord(t, s, u.ID, "d1", stale)
	if r.Status != "conflict" || r.Server == nil || len(r.Server.Occurrences) != 1 ||
		r.Server.Occurrences[0].Sentence == nil || *r.Server.Occurrences[0].Sentence != "the sea was calm" {
		t.Fatalf("conflict server occurrences: %+v", r)
	}
}

func testVocabularyOccurrencesRetry(t *testing.T, open OpenFunc) {
	s := open(t)
	u := MkUser(t, s, "vocab-occ-retry")

	create := vocabWordWrite("en", "retry", 0)
	create.OccurrencesJSON = vocabOccurrencesJSON()
	if r := pushVocabWord(t, s, u.ID, "d1", create); r.Status != "applied" {
		t.Fatalf("create: %+v", r)
	}
	if r := pushVocabWord(t, s, u.ID, "d1", create); r.Status != "duplicate" {
		t.Fatalf("identical retry: %+v", r)
	}

	// A retry whose occurrences differ only in whitespace and object-key
	// order still matches: jsonb re-serializes both, and a byte compare
	// would wrongly call this a duplicate miss.
	retry := create
	retry.OccurrencesJSON = []byte(`[ { "encounters" : 2, "sentence":"the sea was calm",` +
		` "locator":{"nested":{"b":[1,2,3],"a":1},"n":42,"href":"ch3.xhtml"},` +
		` "location_key":"chapter-3","book_title":"A Memory Called Empire","work_id":"work-1",` +
		` "last_seen_at":"2025-06-01T10:00:00Z","occurrence_key":"occ-1"} ]`)
	if r := pushVocabWord(t, s, u.ID, "d1", retry); r.Status != "duplicate" {
		t.Fatalf("canonical retry: %+v", r)
	}

	// A number is compared by value: jsonb rewrites 1e2 as 100, and a
	// retry spelling it the other way is still the same payload.
	expCreate := vocabWordWrite("en", "exponent", 0)
	expCreate.OccurrencesJSON = []byte(`[{"occurrence_key":"occ-1",` +
		`"locator":{"n":1e2},"last_seen_at":"2025-06-01T10:00:00Z","encounters":1}]`)
	if r := pushVocabWord(t, s, u.ID, "d1", expCreate); r.Status != "applied" {
		t.Fatalf("exponent create: %+v", r)
	}
	expRetry := expCreate
	expRetry.OccurrencesJSON = []byte(`[{"occurrence_key":"occ-1",` +
		`"locator":{"n":100},"last_seen_at":"2025-06-01T10:00:00Z","encounters":1}]`)
	if r := pushVocabWord(t, s, u.ID, "d1", expRetry); r.Status != "duplicate" {
		t.Fatalf("exponent retry: %+v", r)
	}

	// Two integers a float64 cannot tell apart are two payloads, so the
	// second is a conflict, not a duplicate.
	bigCreate := vocabWordWrite("en", "bigint", 0)
	bigCreate.OccurrencesJSON = []byte(`[{"occurrence_key":"occ-1",` +
		`"locator":{"n":9007199254740992},"last_seen_at":"2025-06-01T10:00:00Z","encounters":1}]`)
	if r := pushVocabWord(t, s, u.ID, "d1", bigCreate); r.Status != "applied" {
		t.Fatalf("big int create: %+v", r)
	}
	bigRetry := bigCreate
	bigRetry.OccurrencesJSON = []byte(`[{"occurrence_key":"occ-1",` +
		`"locator":{"n":9007199254740993},"last_seen_at":"2025-06-01T10:00:00Z","encounters":1}]`)
	if r := pushVocabWord(t, s, u.ID, "d1", bigRetry); r.Status != "conflict" {
		t.Fatalf("distinct large integers matched: %+v", r)
	}

	// Valid JSON numbers outside PostgreSQL's JSONB numeric range still
	// round-trip and retry cleanly because occurrences are stored as raw
	// JSON bytes (matching ops.locator_json and annotations.locator_json).
	hugeExp := vocabWordWrite("en", "huge-exp", 0)
	hugeExp.OccurrencesJSON = []byte(`[{"occurrence_key":"occ-1",` +
		`"locator":{"n":1e200000},"last_seen_at":"2025-06-01T10:00:00Z","encounters":1}]`)
	if r := pushVocabWord(t, s, u.ID, "d1", hugeExp); r.Status != "applied" {
		t.Fatalf("huge exponent create: %+v", r)
	}
	if r := pushVocabWord(t, s, u.ID, "d1", hugeExp); r.Status != "duplicate" {
		t.Fatalf("huge exponent retry: %+v", r)
	}
}

func testVocabularyAbsentBaseRev(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "vocab-ghost")

	// A live write with a nonzero base_rev names a word the server does
	// not have: a conflict, not a silent create at that rev.
	w := vocabWordWrite("en", "ghost", 7)
	r := pushVocabWord(t, s, u.ID, "d1", w)
	if r.Status != "conflict" || r.Server != nil {
		t.Fatalf("absent word with base_rev: %+v", r)
	}
	page, err := s.VocabularyChanges(ctx, u.ID, 0, 100, 0)
	if err != nil || len(page.Words) != 0 {
		t.Fatalf("absent word was inserted: %+v %v", page.Words, err)
	}
}

func testVocabularyEscapeByteBudget(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "vocab-escape")

	// Each word carries 400 control bytes: 400 raw bytes but 2400
	// encoded, so a raw-length budget would pack four onto a page.
	const n = 6
	for i := 0; i < n; i++ {
		w := vocabWordWrite("en", fmt.Sprintf("w-%d", i), 0)
		w.Notes = strings.Repeat("\x01", 400)
		if r := pushVocabWord(t, s, u.ID, "d1", w); r.Status != "applied" {
			t.Fatalf("push %d: %+v", i, r)
		}
	}

	seen, pages := 0, 0
	since := int64(0)
	for {
		page, err := s.VocabularyChanges(ctx, u.ID, since, 100, 3000)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page.Words) > 1 {
			t.Fatalf("budget admitted %d escape-heavy words", len(page.Words))
		}
		for _, w := range page.Words {
			if w.Seq <= since {
				t.Fatalf("out of order: seq %d after cursor %d", w.Seq, since)
			}
			since = w.Seq
			seen++
		}
		if !page.HasMore {
			if page.NextCursor != page.HighWater {
				t.Fatalf("next cursor %d, high water %d", page.NextCursor, page.HighWater)
			}
			break
		}
		if page.NextCursor != since {
			t.Fatalf("next cursor %d, last emitted %d", page.NextCursor, since)
		}
	}
	if seen != n {
		t.Fatalf("feed lost records: %d of %d", seen, n)
	}
	if pages < 2 {
		t.Fatalf("escape-heavy budget did not split pages: %d", pages)
	}
}

func testVocabularyHTMLByteBudget(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "vocab-html")

	// Each word carries 300 bytes of <, > and &, which encoding/json
	// writes as six-byte \uXXXX escapes: 1800 encoded for 300 raw, so a
	// raw-length budget would pack two onto a page.
	const n = 5
	for i := 0; i < n; i++ {
		w := vocabWordWrite("en", fmt.Sprintf("w-%d", i), 0)
		w.Notes = strings.Repeat("<>&", 100)
		if r := pushVocabWord(t, s, u.ID, "d1", w); r.Status != "applied" {
			t.Fatalf("push %d: %+v", i, r)
		}
	}

	seen, pages := 0, 0
	since := int64(0)
	for {
		page, err := s.VocabularyChanges(ctx, u.ID, since, 100, 3000)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page.Words) != 1 {
			t.Fatalf("budget admitted %d HTML-escaped words", len(page.Words))
		}
		used := 0
		for _, w := range page.Words {
			if w.Seq <= since {
				t.Fatalf("out of order: seq %d after cursor %d", w.Seq, since)
			}
			used += store.VocabularyWordEncodedSize(w)
			since = w.Seq
			seen++
		}
		if used > 3000 {
			t.Fatalf("page exceeded the budget: %d bytes", used)
		}
		if !page.HasMore {
			if page.NextCursor != page.HighWater {
				t.Fatalf("next cursor %d, high water %d", page.NextCursor, page.HighWater)
			}
			break
		}
		if page.NextCursor != since {
			t.Fatalf("next cursor %d, last emitted %d", page.NextCursor, since)
		}
	}
	if seen != n {
		t.Fatalf("feed lost records: %d of %d", seen, n)
	}
	if pages < 2 {
		t.Fatalf("HTML-escaped budget did not split pages: %d", pages)
	}
}
