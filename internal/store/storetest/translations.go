package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// trKey builds a plausible 64-hex key from a seed: the store never
// interprets a key, but a realistic length keeps the tests honest
// about what a cursor and an ordering have to carry. The seed is hashed
// so two seeds never collide the way a repeated filler would.
func trKey(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// trPush pushes one batch, failing the test on anything but success.
func trPush(t *testing.T, s store.Store, userID, workID, deviceID string, items ...store.WorkTranslationWrite) {
	t.Helper()
	if err := s.PushWorkTranslations(
		context.Background(), userID, workID, deviceID, items, 32<<20,
	); err != nil {
		t.Fatal(err)
	}
}

// trPull pulls one page, failing the test on anything but success.
func trPull(t *testing.T, s store.Store, userID, workID, after string, limit int) store.WorkTranslationPage {
	t.Helper()
	page, err := s.PullWorkTranslations(context.Background(), userID, workID, after, limit)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

// trAll drains a work's feed in pages of limit, collecting the rows in
// the order the protocol hands them over.
func trAll(t *testing.T, s store.Store, userID, workID string, limit int) []store.WorkTranslation {
	t.Helper()
	var all []store.WorkTranslation
	after := ""
	for {
		page := trPull(t, s, userID, workID, after, limit)
		all = append(all, page.Translations...)
		if !page.HasMore {
			return all
		}
		if page.NextCursor == "" {
			t.Fatal("has_more without a cursor")
		}
		after = page.NextCursor
	}
}

// TestWorkTranslationsUpsert walks the whole ADR-0052 write contract:
// insert if absent, last write wins on a diff, and a byte-identical
// push that changes nothing at all — not the text, not the device, not
// the timestamp a delta pull would order by.
func testWorkTranslationsUpsert(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "trine")
	w := MkWork(t, s, u, "trw1", "trsha1")

	trPush(t, s, u.ID, w.ID, "phone", store.WorkTranslationWrite{
		Key: trKey("aa"), Translation: "la mer était calme",
	})
	page := trPull(t, s, u.ID, w.ID, "", 10)
	if len(page.Translations) != 1 || page.Translations[0].Translation != "la mer était calme" ||
		page.Translations[0].DeviceID != "phone" || page.Translations[0].UpdatedAt.IsZero() {
		t.Fatalf("insert: %+v", page)
	}
	if page.HasMore || page.NextCursor == "" {
		t.Fatalf("page contract: %+v", page)
	}

	// A different text is a last-write-wins update, server-stamped.
	trPush(t, s, u.ID, w.ID, "tablet", store.WorkTranslationWrite{
		Key: trKey("aa"), Translation: "la mer était plate",
	})
	page = trPull(t, s, u.ID, w.ID, "", 10)
	if len(page.Translations) != 1 || page.Translations[0].Translation != "la mer était plate" ||
		page.Translations[0].DeviceID != "tablet" {
		t.Fatalf("update: %+v", page)
	}
	stamped := page.Translations[0].UpdatedAt

	// The identical push from another device is a no-op: the text is
	// the comparison, so a retried batch changes no row and no puller
	// ever re-downloads a row it already holds.
	trPush(t, s, u.ID, w.ID, "phone", store.WorkTranslationWrite{
		Key: trKey("aa"), Translation: "la mer était plate",
	})
	page = trPull(t, s, u.ID, w.ID, "", 10)
	if len(page.Translations) != 1 || page.Translations[0].Translation != "la mer était plate" ||
		page.Translations[0].DeviceID != "tablet" || !page.Translations[0].UpdatedAt.Equal(stamped) {
		t.Fatalf("identical push changed something: %+v", page)
	}

	// A work the user does not have is the same refusal an op names:
	// decided inside the transaction, carrying the work id.
	err := s.PushWorkTranslations(ctx, u.ID, "nope", "phone", []store.WorkTranslationWrite{
		{Key: trKey("bb"), Translation: "x"},
	}, 32<<20)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown work: %v", err)
	}
	var item *store.ItemError
	if !errors.As(err, &item) || item.WorkID != "nope" {
		t.Fatalf("unknown work is not an item error: %v", err)
	}
	if _, err := s.PullWorkTranslations(ctx, u.ID, "nope", "", 10); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pull of unknown work: %v", err)
	}
}

// TestWorkTranslationsScoping proves the cache is per user and per
// work: another account pulls nothing of this one's, and a second work
// of the same account holds only its own rows even under the same key.
func testWorkTranslationsScoping(t *testing.T, open OpenFunc) {
	s := open(t)
	u1 := MkUser(t, s, "tr-one")
	u2 := MkUser(t, s, "tr-two")
	w1 := MkWork(t, s, u1, "trw2", "trsha2")
	w2 := MkWork(t, s, u1, "trw3", "trsha3")
	// The same work id under the second account: a work is per-user, so
	// the honest isolation question is not "is the work missing" but
	// "does another account's cache leak into a work of its own".
	same := MkWork(t, s, u2, "trw2", "trsha2-other")

	trPush(t, s, u1.ID, w1.ID, "phone",
		store.WorkTranslationWrite{Key: trKey("cc"), Translation: "one"},
		store.WorkTranslationWrite{Key: trKey("dd"), Translation: "two"},
	)
	// The same key under another work is another row: the key is only
	// unique within a work.
	trPush(t, s, u1.ID, w2.ID, "phone",
		store.WorkTranslationWrite{Key: trKey("cc"), Translation: "elsewhere"},
	)

	if all := trAll(t, s, u1.ID, w1.ID, 10); len(all) != 2 {
		t.Fatalf("work holds its own rows: %+v", all)
	}
	if all := trAll(t, s, u1.ID, w2.ID, 10); len(all) != 1 || all[0].Translation != "elsewhere" {
		t.Fatalf("second work: %+v", all)
	}
	if all := trAll(t, s, u2.ID, same.ID, 10); len(all) != 0 {
		t.Fatalf("another user sees the cache: %+v", all)
	}
}

// TestWorkTranslationsPaging proves the delta cursor pages without
// skipping or repeating, including around rows that share a timestamp —
// the whole reason the feed is ordered by (updated_at, key) and the
// cursor carries both.
func testWorkTranslationsPaging(t *testing.T, open OpenFunc) {
	s := open(t)
	u := MkUser(t, s, "tr-page")
	w := MkWork(t, s, u, "trw4", "trsha4")

	// One batch of twenty keys lands under one server stamp, so every
	// boundary the pages cut at falls between equal timestamps.
	items := make([]store.WorkTranslationWrite, 20)
	for i := range items {
		items[i] = store.WorkTranslationWrite{
			Key:         trKey(fmt.Sprintf("row-%02d", i)),
			Translation: "row",
		}
	}
	trPush(t, s, u.ID, w.ID, "phone", items...)

	var finalCursor string
	for _, limit := range []int{1, 3, 7} {
		seen := map[string]bool{}
		after := ""
		pages := 0
		for {
			page := trPull(t, s, u.ID, w.ID, after, limit)
			pages++
			for _, row := range page.Translations {
				if seen[row.Key] {
					t.Fatalf("limit %d: row repeated across pages: %s", limit, row.Key)
				}
				seen[row.Key] = true
			}
			if !page.HasMore {
				finalCursor = page.NextCursor
				break
			}
			if page.NextCursor == "" {
				t.Fatal("has_more without a cursor")
			}
			after = page.NextCursor
		}
		if len(seen) != 20 {
			t.Fatalf("limit %d paged %d distinct rows, want 20 (in %d pages)", limit, len(seen), pages)
		}
	}

	// A later push moves only its own row to the head of the feed: a
	// client holding the final cursor sees exactly the delta.
	trPush(t, s, u.ID, w.ID, "phone", store.WorkTranslationWrite{
		Key: items[4].Key, Translation: "edited",
	})
	page := trPull(t, s, u.ID, w.ID, finalCursor, 20)
	if len(page.Translations) != 1 || page.Translations[0].Key != items[4].Key ||
		page.Translations[0].Translation != "edited" {
		t.Fatalf("delta after the cursor: %+v", page)
	}

	// A cursor that does not parse is refused rather than read as the
	// beginning: silently re-reading is the failure the protocol
	// exists to avoid, and a client told so can drop its cursor and
	// start over on purpose.
	if _, err := s.PullWorkTranslations(context.Background(), u.ID, w.ID, "garbage", 10); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("bad cursor: %v", err)
	}
}

// TestWorkTranslationsBudget proves the per-user byte budget evicts the
// oldest-updated rows first, inside the push that went over.
func testWorkTranslationsBudget(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "tr-budget")
	w := MkWork(t, s, u, "trw5", "trsha5")

	push := func(budget int64, items ...store.WorkTranslationWrite) {
		t.Helper()
		if err := s.PushWorkTranslations(ctx, u.ID, w.ID, "phone", items, budget); err != nil {
			t.Fatal(err)
		}
	}

	// First two rows under a budget that fits both: 64 hash bytes
	// plus six text bytes each, seventy a row, one hundred and forty
	// the pair.
	push(140, store.WorkTranslationWrite{Key: trKey("e1"), Translation: "123456"},
		store.WorkTranslationWrite{Key: trKey("e2"), Translation: "123456"})
	if all := trAll(t, s, u.ID, w.ID, 10); len(all) != 2 {
		t.Fatalf("before eviction: %+v", all)
	}

	// A third row over the budget evicts the oldest-updated row — the
	// first one — and keeps the newer two.
	push(140, store.WorkTranslationWrite{Key: trKey("e3"), Translation: "123456"})
	all := trAll(t, s, u.ID, w.ID, 10)
	if len(all) != 2 {
		t.Fatalf("after eviction: %+v", all)
	}
	for _, row := range all {
		if row.Key == trKey("e1") {
			t.Fatalf("the oldest row survived the budget: %+v", all)
		}
	}
}

// TestWorkTranslationsBudgetCountsBytes proves the budget is spent in
// bytes, not characters: a translation of three two-byte characters is
// six bytes of storage, and a budget char-counting would let twice the
// promised data through.
func testWorkTranslationsBudgetCountsBytes(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "tr-bytes")
	w := MkWork(t, s, u, "trw6", "trsha6")

	// Three two-byte characters: the row measures sixty-four hash
	// bytes plus six value bytes, seventy in all, sixty-seven counted
	// in characters. A budget of sixty-eight fits the characters but
	// not the bytes, so the row does not survive its own push; one of
	// seventy holds it.
	value := "ééé"
	push := func(budget int64, items ...store.WorkTranslationWrite) {
		t.Helper()
		if err := s.PushWorkTranslations(ctx, u.ID, w.ID, "phone", items, budget); err != nil {
			t.Fatal(err)
		}
	}

	push(68, store.WorkTranslationWrite{Key: trKey("b1"), Translation: value})
	if all := trAll(t, s, u.ID, w.ID, 10); len(all) != 0 {
		t.Fatalf("a character-counted budget kept the row: %+v", all)
	}

	push(70, store.WorkTranslationWrite{Key: trKey("b2"), Translation: value})
	all := trAll(t, s, u.ID, w.ID, 10)
	if len(all) != 1 || all[0].Key != trKey("b2") {
		t.Fatalf("a byte-counted budget lost the row: %+v", all)
	}
}

// TestWorkTranslationsMerge proves a work merge carries the cache with
// it: rows follow to the survivor, a key both works hold keeps the
// survivor's text, and a pull cursor taken before the merge sees the
// moved rows as a delta rather than never at all.
func testWorkTranslationsMerge(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "tr-merge")
	into := MkWork(t, s, u, "trw7", "trsha7")
	from := MkWork(t, s, u, "trw8", "trsha8")

	shared := trKey("shared")
	trPush(t, s, u.ID, into.ID, "phone",
		store.WorkTranslationWrite{Key: shared, Translation: "kept"},
		store.WorkTranslationWrite{Key: trKey("only-here"), Translation: "survivor's own"},
	)
	trPush(t, s, u.ID, from.ID, "phone",
		store.WorkTranslationWrite{Key: shared, Translation: "dropped"},
		store.WorkTranslationWrite{Key: trKey("moving"), Translation: "follows"},
	)

	// The cursor a client of the survivor holds before the merge.
	before := trPull(t, s, u.ID, into.ID, "", 10)
	if before.NextCursor == "" {
		t.Fatal("no cursor before the merge")
	}

	if err := s.MergeWorks(ctx, u.ID, from.ID, into.ID); err != nil {
		t.Fatal(err)
	}

	all := trAll(t, s, u.ID, into.ID, 10)
	if len(all) != 3 {
		t.Fatalf("merge lost or doubled rows: %+v", all)
	}
	texts := map[string]string{}
	for _, row := range all {
		texts[row.Key] = row.Translation
	}
	if texts[shared] != "kept" {
		t.Fatalf("collision did not keep the survivor's row: %+v", texts)
	}
	if texts[trKey("moving")] != "follows" {
		t.Fatalf("row did not follow the merge: %+v", texts)
	}

	// The moved row surfaces behind the pre-merge cursor, which is how
	// a synced device learns of it without re-reading the work.
	page := trPull(t, s, u.ID, into.ID, before.NextCursor, 10)
	if len(page.Translations) != 1 || page.Translations[0].Key != trKey("moving") {
		t.Fatalf("moved row not in the delta: %+v", page)
	}
}
