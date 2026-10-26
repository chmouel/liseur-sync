package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

const absPeer = "peer-abs-1"

func mkLink(workID, itemID string, state store.AudiobookLinkState) store.AudiobookLink {
	return store.AudiobookLink{
		PeerIdentity: absPeer, ItemID: itemID, WorkID: workID,
		BookID: "book-" + workID, EditionSHA: "sha-" + workID,
		Source: "auto", State: state,
	}
}

func linksByItem(t *testing.T, s store.Store, userID string) map[string]store.AudiobookLink {
	t.Helper()
	links, err := s.AudiobookLinks(context.Background(), userID, absPeer)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.AudiobookLink{}
	for _, l := range links {
		out[l.ItemID] = l
	}
	return out
}

// testAudiobookLinks covers the link table's own rules: one link per
// work and per item for a peer, whatever the state, rejections written
// by an unlink and cleared by a relink of that exact pair, and nothing
// visible to another account or another peer.
func testAudiobookLinks(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "abs-links")
	other := MkUser(t, s, "abs-links-other")
	w1 := MkWork(t, s, u, "abs-w1", "sha-abs-w1")
	w2 := MkWork(t, s, u, "abs-w2", "sha-abs-w2")
	MkWork(t, s, other, "abs-ow", "sha-abs-ow")

	paused := mkLink(w1.ID, "item-1", store.AudiobookLinkSuspended)
	paused.Mapping = []byte(`{"v":1}`)
	if err := s.CreateAudiobookLink(ctx, u.ID, paused); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAudiobookLink(ctx, u.ID, mkLink(w1.ID, "item-2", store.AudiobookLinkActive)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a suspended link must still reserve its work: %v", err)
	}
	if err := s.CreateAudiobookLink(ctx, u.ID, mkLink(w2.ID, "item-1", store.AudiobookLinkActive)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a suspended link must still reserve its item: %v", err)
	}
	if err := s.CreateAudiobookLink(ctx, u.ID, mkLink("no-such-work", "item-9", store.AudiobookLinkActive)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("link to a missing work: %v", err)
	}
	other1 := mkLink("abs-ow", "item-1", store.AudiobookLinkActive)
	if err := s.CreateAudiobookLink(ctx, other.ID, other1); err != nil {
		t.Fatalf("another account linking the same item: %v", err)
	}

	got := linksByItem(t, s, u.ID)["item-1"]
	if got.WorkID != w1.ID || got.State != store.AudiobookLinkSuspended || string(got.Mapping) != `{"v":1}` || got.CreatedAt.IsZero() {
		t.Fatalf("link read back wrong: %+v", got)
	}
	got.State, got.PushedSeq, got.PushedMark, got.ConfirmedLinear = store.AudiobookLinkActive, 7, "12:false", true
	if err := s.UpdateAudiobookLink(ctx, u.ID, got); err != nil {
		t.Fatal(err)
	}
	old := got
	got = linksByItem(t, s, u.ID)["item-1"]
	if got.Generation == old.Generation || got.Generation == "" {
		t.Fatal("mutation did not replace generation")
	}
	if err := s.UpdateAudiobookCursor(ctx, u.ID, old); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale cursor: %v", err)
	}
	got.PushedSeq, got.PushedMark = 7, "12:false"
	if err := s.UpdateAudiobookCursor(ctx, u.ID, got); err != nil {
		t.Fatal(err)
	}
	if cursor := linksByItem(t, s, u.ID)["item-1"]; cursor.PushedSeq != 7 || cursor.PushedMark != "12:false" {
		t.Fatalf("cursor not stored: %+v", cursor)
	}
	if got.State != store.AudiobookLinkActive || !got.ConfirmedLinear {
		t.Fatalf("update not kept: %+v", got)
	}
	if links, _ := s.AudiobookLinks(ctx, u.ID, "another-peer"); len(links) != 0 {
		t.Fatalf("links leaked across peers: %+v", links)
	}

	if err := s.DeleteAudiobookLink(ctx, u.ID, absPeer, "item-1", got.Generation, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAudiobookLink(ctx, u.ID, absPeer, "item-1", got.Generation, true); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("deleting a gone link: %v", err)
	}
	rej, err := s.AudiobookRejections(ctx, u.ID, absPeer)
	if err != nil {
		t.Fatal(err)
	}
	if len(rej) != 1 || rej[0] != (store.AudiobookRejection{WorkID: w1.ID, ItemID: "item-1"}) {
		t.Fatalf("unlink did not reject the pair: %+v", rej)
	}
	if rej, _ := s.AudiobookRejections(ctx, other.ID, absPeer); len(rej) != 0 {
		t.Fatalf("a rejection leaked across accounts: %+v", rej)
	}
	if err := s.CreateAudiobookLink(ctx, u.ID, mkLink(w1.ID, "item-1", store.AudiobookLinkActive)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAudiobookCursor(ctx, u.ID, got); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cursor from before unlink/relink: %v", err)
	}
	if err := s.UpdateAudiobookLink(ctx, u.ID, got); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("mutation from before unlink/relink: %v", err)
	}
	if rej, _ := s.AudiobookRejections(ctx, u.ID, absPeer); len(rej) != 0 {
		t.Fatalf("relinking a pair by hand must clear its rejection: %+v", rej)
	}
	if links := linksByItem(t, s, other.ID); len(links) != 1 {
		t.Fatalf("another account's link was touched: %+v", links)
	}
}

// testAudiobookLinksFollowWorks covers the work lifecycle: a merge keeps
// the survivor's link when both works hold one, carries a lone link
// across and collapses rejections; a split moves a link with its
// edition; deleting a work takes its link and rejections with it.
func testAudiobookLinksFollowWorks(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "abs-lifecycle")
	keep := MkWork(t, s, u, "abs-keep", "sha-abs-keep")
	gone := MkWork(t, s, u, "abs-gone", "sha-abs-gone")
	lone := MkWork(t, s, u, "abs-lone", "sha-abs-lone")

	if err := s.CreateAudiobookLink(ctx, u.ID, mkLink(keep.ID, "item-keep", store.AudiobookLinkNeedsMapping)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAudiobookLink(ctx, u.ID, mkLink(gone.ID, "item-gone", store.AudiobookLinkActive)); err != nil {
		t.Fatal(err)
	}
	// Rejections: the same pair on both sides, and one only on the
	// absorbed work. Only an unlink writes one, so each is made on a
	// throwaway work that is then merged into the work it belongs to.
	for _, r := range []struct{ work, item string }{{keep.ID, "item-x"}, {gone.ID, "item-x"}, {gone.ID, "item-y"}} {
		tmp := MkWork(t, s, u, "abs-tmp-"+r.work+r.item, "sha-abs-tmp-"+r.work+r.item)
		if err := s.CreateAudiobookLink(ctx, u.ID, mkLink(tmp.ID, r.item, store.AudiobookLinkActive)); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteAudiobookLink(ctx, u.ID, absPeer, r.item, linksByItem(t, s, u.ID)[r.item].Generation, true); err != nil {
			t.Fatal(err)
		}
		if err := s.MergeWorks(ctx, u.ID, tmp.ID, r.work); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.MergeWorks(ctx, u.ID, gone.ID, keep.ID); err != nil {
		t.Fatal(err)
	}
	links := linksByItem(t, s, u.ID)
	if _, ok := links["item-gone"]; ok || len(links) != 1 || links["item-keep"].WorkID != keep.ID {
		t.Fatalf("both works linked: the survivor's link must win, got %+v", links)
	}
	rej, err := s.AudiobookRejections(ctx, u.ID, absPeer)
	if err != nil {
		t.Fatal(err)
	}
	want := map[store.AudiobookRejection]bool{
		{WorkID: keep.ID, ItemID: "item-x"}: true,
		{WorkID: keep.ID, ItemID: "item-y"}: true,
	}
	if len(rej) != len(want) || !want[rej[0]] || !want[rej[1]] {
		t.Fatalf("rejections not carried and collapsed: %+v", rej)
	}

	// A lone link on the absorbed work comes across.
	if err := s.CreateAudiobookLink(ctx, u.ID, mkLink(lone.ID, "item-lone", store.AudiobookLinkSuspended)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAudiobookLink(ctx, u.ID, absPeer, "item-keep", linksByItem(t, s, u.ID)["item-keep"].Generation, false); err != nil {
		t.Fatal(err)
	}
	if err := s.MergeWorks(ctx, u.ID, lone.ID, keep.ID); err != nil {
		t.Fatal(err)
	}
	if got := linksByItem(t, s, u.ID)["item-lone"]; got.WorkID != keep.ID || got.State != store.AudiobookLinkSuspended {
		t.Fatalf("a lone link did not follow the merge: %+v", got)
	}

	// Split: the link follows the edition it was computed from.
	split := MkWork(t, s, u, "abs-split", "sha-abs-split")
	l := mkLink(split.ID, "item-split", store.AudiobookLinkActive)
	l.EditionSHA = "sha-abs-split"
	if err := s.CreateAudiobookLink(ctx, u.ID, l); err != nil {
		t.Fatal(err)
	}
	if err := s.SplitWork(ctx, u.ID, split.ID, "sha-abs-split",
		[]store.Identifier{{Kind: "dc", Value: "urn:isbn:9780316419568-abs-split"}},
		store.Work{ID: "abs-split-new", UserID: u.ID, Title: "split", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got := linksByItem(t, s, u.ID)["item-split"]; got.WorkID != "abs-split-new" {
		t.Fatalf("link did not follow its edition: %+v", got)
	}

	// Delete: the link and the rejections go with the work.
	if err := s.DeleteWork(ctx, u.ID, keep.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := linksByItem(t, s, u.ID)["item-lone"]; ok {
		t.Fatal("a link outlived its work")
	}
	if rej, _ := s.AudiobookRejections(ctx, u.ID, absPeer); len(rej) != 0 {
		t.Fatalf("rejections outlived their work: %+v", rej)
	}
}

// testAudiobookLinkDoesNotKeepAWork holds the decision ADR-0055 makes
// about garbage collection: a link nothing has synced through yet does
// not keep alive a work whose book a pass removed. The link goes with
// it, and matching may propose again.
func testAudiobookLinkDoesNotKeepAWork(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	folder := MkFolder(t, s, "abs-gc", store.FolderCalibre)
	now := time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)
	user := MkUser(t, s, "abs-gc-reader")
	id := int64(1)
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &id, RelativePath: "Linked (1)/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-linked", Title: "Linked"},
	}, true, now)
	bookID := knownByCalibreID(t, s, folder.ID)[id].ID
	w := store.Work{ID: "abs-gc-work", UserID: user.ID, Title: "Linked", CreatedAt: now}
	if _, err := s.ResolveCatalogBookWork(ctx, user.ID, bookID, w,
		[]store.Edition{{UserID: user.ID, SHA256: "sha-linked", WorkID: w.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "sha-linked"}}, false, now); err != nil {
		t.Fatal(err)
	}
	l := mkLink(w.ID, "item-gc", store.AudiobookLinkActive)
	l.BookID, l.EditionSHA = bookID, "sha-linked"
	if err := s.CreateAudiobookLink(ctx, user.ID, l); err != nil {
		t.Fatal(err)
	}
	other := int64(2)
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &other, RelativePath: "Other (2)/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-other", Title: "Other"},
	}, true, now.Add(time.Hour))
	if _, err := s.WorkByID(ctx, user.ID, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a link kept an otherwise empty work alive: %v", err)
	}
	if links := linksByItem(t, s, user.ID); len(links) != 0 {
		t.Fatalf("a link outlived its collected work: %+v", links)
	}
}

// testAudiobookSessionRevisions covers listening-session revisions:
// ordered by the remote server's clock rather than arrival, never
// overwritten, and attributed once per remote session so a later
// revision cannot move it to another work.
func testAudiobookSessionRevisions(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "abs-sessions")
	into := MkWork(t, s, u, "abs-into", "sha-abs-into")
	from := MkWork(t, s, u, "abs-from", "sha-abs-from")
	key := "abs:" + absPeer + ":sess-1"
	t0 := time.Date(2026, time.March, 1, 20, 0, 0, 0, time.UTC)
	mk := func(id string, minutes int) store.Session {
		return store.Session{
			SessionID: id, WorkID: from.ID, EditionSHA: Ptr("sha-abs-from"),
			DeviceID: "abs-peer:phone", StartedAt: t0, EndedAt: t0.Add(time.Duration(minutes) * time.Minute),
			StartProg: 0.1, EndProg: 0.12, ActiveMs: Ptr(int64(minutes) * 60_000),
			Origin: store.OriginAudiobookshelf, SourceKey: Ptr(key),
		}
	}
	expect := func(what, want string, got string, err error) {
		t.Helper()
		if err != nil || got != want {
			t.Fatalf("%s: want %q, got %q (%v)", what, want, got, err)
		}
	}
	st, err := s.AppendAudiobookSession(ctx, u.ID, mk("abs-r1", 10), t0.Add(10*time.Minute), []byte(`{"v":1}`))
	expect("first revision", "inserted", st, err)
	st, err = s.AppendAudiobookSession(ctx, u.ID, mk("abs-r1", 10), t0.Add(10*time.Minute), []byte(`{"v":1}`))
	expect("replay", "duplicate", st, err)
	for name, mutate := range map[string]func(*store.Session){
		"active time": func(s *store.Session) { s.ActiveMs = Ptr(int64(123)) },
		"progression": func(s *store.Session) { s.EndProg = 0.2 },
		"source key":  func(s *store.Session) { s.SourceKey = Ptr("another-source") },
	} {
		ses := mk("abs-r1", 10)
		mutate(&ses)
		st, err = s.AppendAudiobookSession(ctx, u.ID, ses, t0.Add(10*time.Minute), []byte(`{"v":1}`))
		expect("same id, different "+name, "conflict", st, err)
	}
	st, err = s.AppendAudiobookSession(ctx, u.ID, mk("abs-r1b", 11), t0.Add(10*time.Minute), []byte(`{"v":1}`))
	expect("same remote time, other content", "conflict", st, err)

	// The work is merged away before the next revision arrives; the
	// revision still names the old work and must land on the survivor.
	if err := s.MergeWorks(ctx, u.ID, from.ID, into.ID); err != nil {
		t.Fatal(err)
	}
	st, err = s.AppendAudiobookSession(ctx, u.ID, mk("abs-r3", 30), t0.Add(30*time.Minute), []byte(`{"v":1}`))
	expect("newer revision", "superseded", st, err)
	st, err = s.AppendAudiobookSession(ctx, u.ID, mk("abs-r2", 20), t0.Add(20*time.Minute), []byte(`{"v":1}`))
	expect("older revision arriving late", "stale", st, err)

	cur, err := s.CurrentSourceSession(ctx, u.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if cur.SessionID != "abs-r3" || cur.WorkID != into.ID || cur.DeviceID != "abs-peer:phone" ||
		cur.EditionSHA == nil || *cur.EditionSHA != "sha-abs-from" || cur.ReportedPages != nil ||
		cur.ActiveMs == nil || *cur.ActiveMs != 30*60_000 || cur.Origin != store.OriginAudiobookshelf {
		t.Fatalf("current revision wrong: %+v", cur)
	}
	got, err := s.CurrentSessionsForWork(ctx, u.ID, into.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionID != "abs-r3" {
		t.Fatalf("only the current revision should count: %+v", got)
	}
	eligible, err := s.SessionsEndedBefore(ctx, u.ID, t0.AddDate(1, 0, 0), 100)
	if err != nil || len(eligible) != 0 {
		t.Fatalf("listening revisions must remain raw: %+v %v", eligible, err)
	}
	if _, err := s.CurrentSourceSession(ctx, u.ID, "abs:"+absPeer+":nothing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown source key: %v", err)
	}
	if _, err := s.AppendAudiobookSession(ctx, u.ID, store.Session{SessionID: "x"}, t0, nil); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("a session without a source key: %v", err)
	}
	ledger, err := s.AudiobookSessionImport(ctx, u.ID, key)
	if err != nil || string(ledger.Mapping) != `{"v":1}` || len(ledger.MappingKey) != 64 || ledger.FirstImportedAt.IsZero() {
		t.Fatalf("import mapping: %+v %v", ledger, err)
	}
	st, err = s.AppendAudiobookSession(ctx, u.ID, mk("abs-r4", 40), t0.Add(40*time.Minute), []byte(`{"v":2}`))
	expect("remote update after remap", "superseded", st, err)
	still, err := s.AudiobookSessionImport(ctx, u.ID, key)
	if err != nil || string(still.Mapping) != string(ledger.Mapping) || still.MappingKey != ledger.MappingKey {
		t.Fatalf("remap replaced import mapping: %+v %v", still, err)
	}
	if imported, err := s.AudiobookSessionImported(ctx, u.ID, key, "abs-r1"); err != nil || !imported {
		t.Fatalf("superseded revision not recognized: %v %v", imported, err)
	}
	if _, err := s.AudiobookSessionImport(ctx, "another-user", key); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("import mapping leaked: %v", err)
	}
	if err := s.DeleteWork(ctx, u.ID, into.ID); err != nil {
		t.Fatal(err)
	}
	st, err = s.AppendAudiobookSession(ctx, u.ID, mk("abs-r5", 50), t0.Add(50*time.Minute), []byte(`{"v":2}`))
	expect("deleted history cannot return", "deleted", st, err)
	if _, err := s.AudiobookSessionImport(ctx, u.ID, key); err != nil {
		t.Fatalf("work deletion removed import ledger: %v", err)
	}
}

func testCatalogAudiobooksReconcileAndGrants(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC)

	u1 := MkUser(t, s, "ab-cat-u1")
	u2 := MkUser(t, s, "ab-cat-u2")

	plainFolder := store.Folder{
		ID: "f-ab-plain", Name: "PlainAudio", RootPath: "/srv/ab-plain",
		Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now,
	}
	calibreFolder := store.Folder{
		ID: "f-ab-calibre", Name: "CalibreBooks", RootPath: "/srv/ab-calibre",
		Kind: store.FolderCalibre, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateFolder(ctx, plainFolder); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFolder(ctx, calibreFolder); err != nil {
		t.Fatal(err)
	}
	// Grant u1 both folders, u2 only calibreFolder.
	if err := s.AssignUserFolder(ctx, u1.ID, plainFolder.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignUserFolder(ctx, u1.ID, calibreFolder.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignUserFolder(ctx, u2.ID, calibreFolder.ID); err != nil {
		t.Fatal(err)
	}

	// 1. Plain folder reconcile + idempotency + cue stamp invalidation.
	cueTime := now.Add(-time.Minute)
	obs1 := store.ObservedAudiobook{
		RelativePath:    "book1.m4b",
		SizeBytes:       1000,
		MTime:           now,
		ContentSHA256:   "ab-sha-1",
		CueMTime:        &cueTime,
		CueSizeBytes:    100,
		Title:           "Book One",
		AuthorsJSON:     []byte(`["Author One"]`),
		NarratorsJSON:   []byte(`["Narrator One"]`),
		DurationSeconds: 3600,
		ChaptersJSON:    []byte(`[{"index":0,"title":"Ch 1","start":0,"end":3600}]`),
	}
	res1, err := s.ReconcileFolderAudiobooks(ctx, plainFolder.ID, []store.ObservedAudiobook{obs1}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Added != 1 || !res1.Changed() {
		t.Fatalf("first plain reconcile: %+v", res1)
	}

	known, err := s.AudiobooksInFolder(ctx, plainFolder.ID)
	if err != nil || len(known) != 1 {
		t.Fatalf("AudiobooksInFolder: %v %+v", err, known)
	}
	abID := known[0].ID
	if known[0].CueSizeBytes != 100 || known[0].CueMTime == nil {
		t.Fatalf("expected cue size 100 and mtime, got %+v", known[0])
	}

	// Unchanged pass changes nothing.
	res2, err := s.ReconcileFolderAudiobooks(ctx, plainFolder.ID, []store.ObservedAudiobook{{
		RelativePath: "book1.m4b", SizeBytes: 1000, MTime: now, Unchanged: true,
	}}, true, now.Add(time.Minute))
	if err != nil || res2.Changed() {
		t.Fatalf("unchanged pass reported change: %+v %v", res2, err)
	}

	// Incomplete or empty pass never marks missing.
	if _, err := s.ReconcileFolderAudiobooks(ctx, plainFolder.ID, nil, true, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileFolderAudiobooks(ctx, plainFolder.ID, []store.ObservedAudiobook{}, false, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	ab, err := s.CatalogAudiobookByID(ctx, "", abID)
	if err != nil || ab.Status != store.BookActive {
		t.Fatalf("expected active audiobook after empty/incomplete passes, got %+v %v", ab, err)
	}

	// 2. Link book in calibreFolder to audiobook in plainFolder; verify dual-folder grant check.
	calID := int64(10)
	doReconcile(t, s, calibreFolder.ID, []store.ObservedBook{{
		CalibreID: &calID, RelativePath: "Author/Book One (10)/book.epub",
		SizeBytes: 500, MTime: now, ContentSHA256: "epub-sha-1", Title: "Book One",
	}}, true, now)
	bookID := knownByCalibreID(t, s, calibreFolder.ID)[calID].ID

	link := store.BookAudiobook{
		BookID:       bookID,
		AudiobookID:  abID,
		EditionSHA:   "epub-sha-1",
		AudiobookSHA: "ab-sha-1",
		Source:       "title_author",
		State:        store.BookAudiobookActive,
		MappingKey:   "mk-1",
		MappingJSON:  []byte(`{"version":1,"duration":3600,"segments":[{"chapter_index":0,"href":"ch1.xhtml","audio_start":0,"audio_end":3600,"book_start":0,"book_end":1}]}`),
		UpdatedAt:    now,
	}
	if err := s.UpsertBookAudiobook(ctx, link, now); err != nil {
		t.Fatal(err)
	}

	// u1 has grants on both folders -> succeeds.
	gotLink, gotAB, err := s.BookAudiobookByBookID(ctx, u1.ID, bookID)
	if err != nil {
		t.Fatalf("u1 BookAudiobookByBookID: %v", err)
	}
	if gotLink.AudiobookID != abID || gotAB.ID != abID {
		t.Fatalf("unexpected link/audiobook: %+v %+v", gotLink, gotAB)
	}
	// u2 only has grant on calibreFolder (book), NOT plainFolder (audiobook) -> ErrNotFound!
	if _, _, err := s.BookAudiobookByBookID(ctx, u2.ID, bookID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for u2 lacking audiobook folder grant, got %v", err)
	}

	// 3. Plain-folder byte replacement at same path cascades book_audiobooks.
	obsReplaced := obs1
	obsReplaced.SizeBytes = 2000
	obsReplaced.ContentSHA256 = "ab-sha-2"
	obsReplaced.Replaces = true
	resReplace, err := s.ReconcileFolderAudiobooks(ctx, plainFolder.ID, []store.ObservedAudiobook{obsReplaced}, true, now.Add(5*time.Minute))
	if err != nil || resReplace.Replaced != 1 {
		t.Fatalf("expected Replaced=1, got %+v %v", resReplace, err)
	}
	if _, _, err := s.BookAudiobookByBookID(ctx, "", bookID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected book_audiobooks link to cascade on replacement, got %v", err)
	}

	// 4. Calibre audiobook lifecycle: directory rename, Unservable, and ADR-0022 purge.
	calAbID1 := int64(101)
	calAbID2 := int64(102)
	_, err = s.ReconcileFolderAudiobooks(ctx, calibreFolder.ID, []store.ObservedAudiobook{
		{
			CalibreID: &calAbID1, RelativePath: "Author/Book A (101)/book.m4b",
			SizeBytes: 1000, MTime: now, ContentSHA256: "cal-ab-1", Title: "Book A",
			DurationSeconds: 1800, ChaptersJSON: []byte(`[]`),
		},
		{
			CalibreID: &calAbID2, RelativePath: "Author/Book B (102)/book.m4b",
			SizeBytes: 1000, MTime: now, ContentSHA256: "cal-ab-2", Title: "Book B",
			DurationSeconds: 1800, ChaptersJSON: []byte(`[]`),
		},
	}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	calKnown, err := s.AudiobooksInFolder(ctx, calibreFolder.ID)
	if err != nil || len(calKnown) != 2 {
		t.Fatalf("expected 2 calibre audiobooks, got %d (%v)", len(calKnown), err)
	}

	// Rename directory for calAbID1, mark calAbID2 Unservable.
	resCal2, err := s.ReconcileFolderAudiobooks(ctx, calibreFolder.ID, []store.ObservedAudiobook{
		{
			CalibreID: &calAbID1, RelativePath: "Author/Book B (102)/book.m4b",
			SizeBytes: 1000, MTime: now, ContentSHA256: "cal-ab-1", Title: "Book A",
			DurationSeconds: 1800, ChaptersJSON: []byte(`[]`),
		},
		{
			CalibreID: &calAbID2, Unservable: true,
		},
	}, true, now.Add(10*time.Minute))
	if err != nil || resCal2.Missing != 1 || resCal2.Purged != 0 {
		t.Fatalf("expected Missing=1 Purged=0 on Unservable, got %+v %v", resCal2, err)
	}

	// Complete non-empty pass omitting calAbID2 purges it (ADR-0022).
	resCal3, err := s.ReconcileFolderAudiobooks(ctx, calibreFolder.ID, []store.ObservedAudiobook{
		{
			CalibreID: &calAbID1, RelativePath: "Renamed Author/Book A (101)/book.m4b",
			SizeBytes: 1000, MTime: now, ContentSHA256: "cal-ab-1", Title: "Book A",
			DurationSeconds: 1800, ChaptersJSON: []byte(`[]`),
		},
	}, true, now.Add(20*time.Minute))
	if err != nil || resCal3.Purged != 1 {
		t.Fatalf("expected Purged=1 when omitted from Calibre pass, got %+v %v", resCal3, err)
	}
}

func testOriginAudiobookPaceExclusion(t *testing.T, open OpenFunc) {
	if store.MeasuresReadingPace(store.OriginAudiobook) {
		t.Fatal("OriginAudiobook must not measure reading pace")
	}
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "ab-pace-user")
	w := MkWork(t, s, u, "ab-pace-work", "sha-ab-pace")
	t0 := time.Date(2026, time.January, 10, 18, 0, 0, 0, time.UTC)

	sess := store.Session{
		SessionID: "sess-listen-1",
		WorkID:    w.ID,
		DeviceID:  "web-reader",
		StartedAt: t0,
		EndedAt:   t0.Add(30 * time.Minute),
		StartProg: 0.10,
		EndProg:   0.25,
		ActiveMs:  Ptr(int64(30 * 60 * 1000)),
		Origin:    store.OriginAudiobook,
	}
	if err := s.AppendSessions(ctx, u.ID, []store.Session{sess}); err != nil {
		t.Fatalf("AppendSessions: %v", err)
	}
	// Replay with same Origin is duplicate; replay with OriginNative is conflict.
	if err := s.AppendSessions(ctx, u.ID, []store.Session{sess}); err != nil {
		t.Fatalf("replay AppendSessions: %v", err)
	}
	conflictSess := sess
	conflictSess.Origin = store.OriginNative
	if err := s.AppendSessions(ctx, u.ID, []store.Session{conflictSess}); !errors.Is(err, store.ErrIDMismatch) {
		t.Fatalf("conflicting origin AppendSessions: %v", err)
	}

	snap, err := s.StatisticsSnapshot(ctx, u.ID, []string{w.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].Origin != store.OriginAudiobook {
		t.Fatalf("expected 1 OriginAudiobook session in snapshot, got %+v", snap.Sessions)
	}
}

// The badge, the Audiobooks shelf and the upload's duplicate check all
// ask "which audiobook can this reader hear", and all three must give a
// reader without a grant on the audiobook's folder the same answer as a
// reader with no audiobook at all.
func testPlayableAudiobookQueries(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)
	both := MkUser(t, s, "ab-play-both")
	booksOnly := MkUser(t, s, "ab-play-books")

	for _, f := range []store.Folder{
		{ID: "f-play-books", Name: "Books", RootPath: "/srv/play-books", Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now},
		{ID: "f-play-audio", Name: "Audio", RootPath: "/srv/play-audio", Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now},
	} {
		if err := s.CreateFolder(ctx, f); err != nil {
			t.Fatal(err)
		}
		if err := s.AssignUserFolder(ctx, both.ID, f.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AssignUserFolder(ctx, booksOnly.ID, "f-play-books"); err != nil {
		t.Fatal(err)
	}

	var observed []store.ObservedBook
	for i, name := range []string{"playable", "unmapped", "stale", "silent"} {
		observed = append(observed, store.ObservedBook{
			RelativePath: name + ".epub", SizeBytes: 10, MTime: now.Add(time.Duration(i) * time.Second),
			ContentSHA256: "epub-" + name, Title: name,
		})
	}
	doReconcile(t, s, "f-play-books", observed, true, now)
	books := knownByPath(t, s, "f-play-books")

	var audio []store.ObservedAudiobook
	for _, name := range []string{"playable", "unmapped", "stale"} {
		audio = append(audio, store.ObservedAudiobook{
			RelativePath: name + ".m4b", SizeBytes: 10, MTime: now,
			ContentSHA256: "m4b-" + name, Title: name, DurationSeconds: 60,
		})
	}
	if _, err := s.ReconcileFolderAudiobooks(ctx, "f-play-audio", audio, true, now); err != nil {
		t.Fatal(err)
	}
	audioByPath := map[string]string{}
	knownAudio, err := s.AudiobooksInFolder(ctx, "f-play-audio")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range knownAudio {
		audioByPath[k.RelativePath] = k.ID
	}
	for name, l := range map[string]struct {
		state   store.BookAudiobookState
		edition string
	}{
		"playable": {store.BookAudiobookActive, "epub-playable"},
		"unmapped": {store.BookAudiobookUnmapped, "epub-unmapped"},
		"stale":    {store.BookAudiobookActive, "an older edition"},
	} {
		if err := s.UpsertBookAudiobook(ctx, store.BookAudiobook{
			BookID: books[name+".epub"].ID, AudiobookID: audioByPath[name+".m4b"],
			EditionSHA: l.edition, AudiobookSHA: "m4b-" + name,
			Source: "filename", State: l.state, MappingKey: "k", UpdatedAt: now,
		}, now); err != nil {
			t.Fatal(err)
		}
	}

	ids := []string{}
	for _, b := range books {
		ids = append(ids, b.ID)
	}
	playable := books["playable.epub"].ID
	for _, tc := range []struct {
		viewer string
		want   []string
	}{{both.ID, []string{playable}}, {"", []string{playable}}, {booksOnly.ID, nil}} {
		got, err := s.PlayableAudiobookBookIDs(ctx, tc.viewer, ids)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(tc.want) || (len(tc.want) == 1 && !got[tc.want[0]]) {
			t.Errorf("PlayableAudiobookBookIDs(%q) = %v, want %v", tc.viewer, got, tc.want)
		}
		for _, ascending := range []bool{true, false} {
			listed, err := s.ListAudiobookCatalogBooks(ctx, tc.viewer, "f-play-books", nil, 10, ascending)
			if err != nil {
				t.Fatal(err)
			}
			if len(listed) != len(tc.want) || (len(listed) == 1 && listed[0].ID != playable) {
				t.Errorf("ListAudiobookCatalogBooks(%q, asc=%v) = %d books, want %v", tc.viewer, ascending, len(listed), tc.want)
			}
		}
	}
	// The cursor pages past the one match.
	listed, err := s.ListAudiobookCatalogBooks(ctx, both.ID, "f-play-books",
		&store.CatalogBookCursor{CreatedAt: now.Add(time.Hour), ID: "zzz"}, 10, true)
	if err != nil || len(listed) != 0 {
		t.Fatalf("listing past the cursor = %d, %v", len(listed), err)
	}

	if ab, err := s.CatalogAudiobookByDigest(ctx, both.ID, "m4b-stale"); err != nil || ab.ID != audioByPath["stale.m4b"] {
		t.Fatalf("CatalogAudiobookByDigest = %+v, %v", ab, err)
	}
	if _, err := s.CatalogAudiobookByDigest(ctx, booksOnly.ID, "m4b-stale"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an ungranted audiobook was found by digest: %v", err)
	}
	if _, err := s.CatalogAudiobookByDigest(ctx, "", "nothing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown digest: %v", err)
	}
}
