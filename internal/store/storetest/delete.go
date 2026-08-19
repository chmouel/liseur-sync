package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testDeleteWork is the reader's own delete: a work nothing on this
// server backs goes, and takes its reading with it.
func testDeleteWork(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "delete-work")
	w := MkWork(t, s, u, "w-delete", "sha-delete")

	if _, err := s.AppendOps(ctx, u.ID, "d-delete", []store.Op{{
		OpID: "018e6f1a-0000-7000-8000-0000000000d1", WorkID: w.ID,
		EditionSHA: Ptr("sha-delete"), ClientTS: time.Now(), Progression: 0.42,
		Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendSessions(ctx, u.ID, []store.Session{{
		SessionID: "s-delete", WorkID: w.ID, EditionSHA: Ptr("sha-delete"),
		DeviceID: "d-delete", StartedAt: time.Now().Add(-time.Hour),
		EndedAt: time.Now(), StartProg: 0.4, EndProg: 0.42,
		Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteWork(ctx, u.ID, w.ID); err != nil {
		t.Fatalf("DeleteWork: %v", err)
	}
	if _, err := s.WorkByID(ctx, u.ID, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the work survived its own deletion: %v", err)
	}
	sessions, err := s.SessionsForWork(ctx, u.ID, w.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions outlived their work: %+v", sessions)
	}
	page, err := s.Changes(ctx, u.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range page.Ops {
		if op.WorkID == w.ID {
			t.Fatalf("an op outlived its work: %+v", op)
		}
	}
	// The alias went with the work, so the same book read again is a
	// new work rather than a resurrection of the old one.
	aliases, err := s.ResolveAliases(ctx, u.ID, []store.Identifier{
		{Kind: "sha256", Value: "sha-delete"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 0 {
		t.Fatalf("an alias outlived its work: %v", aliases)
	}

	if err := s.DeleteWork(ctx, u.ID, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting a work twice: want ErrNotFound, got %v", err)
	}
	if err := s.DeleteWork(ctx, u.ID, "w-never-existed"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting an unknown work: want ErrNotFound, got %v", err)
	}
}

// testDeleteWorkRefusesAMappedWork is the guard that keeps an unplugged
// disk from costing somebody their reading history: a work a catalog
// book still maps to is not the reader's to delete, missing file or
// not.
func testDeleteWorkRefusesAMappedWork(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	u := MkUser(t, s, "delete-mapped")
	folder := MkFolder(t, s, "mapped", store.FolderPlain)
	// The folder keeps a second book throughout, because a pass that
	// observes nothing marks nothing missing.
	keep := store.ObservedBook{
		RelativePath: "keep.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-keep", Title: "Keep",
	}
	doReconcile(t, s, folder.ID, []store.ObservedBook{keep, {
		RelativePath: "book.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-mapped", Title: "Mapped",
	}}, true, now)
	bookID := knownByPath(t, s, folder.ID)["book.epub"].ID

	w := store.Work{ID: "w-mapped", UserID: u.ID, Title: "Mapped", CreatedAt: now}
	if _, err := s.ResolveCatalogBookWork(ctx, u.ID, bookID, w,
		[]store.Edition{{UserID: u.ID, SHA256: "sha-mapped", WorkID: w.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "sha-mapped"}}, false, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AppendOps(ctx, u.ID, "d-mapped", []store.Op{{
		OpID: "018e6f1a-0000-7000-8000-0000000000d3", WorkID: w.ID,
		EditionSHA: Ptr("sha-mapped"), ClientTS: now, Progression: 0.2,
		Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteWork(ctx, u.ID, w.ID); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("deleting a work a book maps: want ErrInvalidInput, got %v", err)
	}

	// Still refused when the file is only missing: absence is evidence
	// about a disk, not a decision about a book.
	doReconcile(t, s, folder.ID, []store.ObservedBook{keep}, true, now.Add(time.Hour))
	if err := s.DeleteWork(ctx, u.ID, w.ID); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("deleting a work whose book is missing: want ErrInvalidInput, got %v", err)
	}
	if _, err := s.WorkByID(ctx, u.ID, w.ID); err != nil {
		t.Fatalf("a refused delete took the work anyway: %v", err)
	}

	// Once the book is gone from the catalog the same work is the
	// reader's to remove.
	if err := s.DeleteMissingBook(ctx, bookID); err != nil {
		t.Fatalf("DeleteMissingBook: %v", err)
	}
	if err := s.DeleteWork(ctx, u.ID, w.ID); err != nil {
		t.Fatalf("DeleteWork after the book went: %v", err)
	}
}

// testDeleteWorkIsPerUser keeps the per-user boundary where every other
// reading-state method keeps it.
func testDeleteWorkIsPerUser(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	mine := MkUser(t, s, "delete-mine")
	theirs := MkUser(t, s, "delete-theirs")
	w := MkWork(t, s, theirs, "w-theirs", "sha-theirs")

	if err := s.DeleteWork(ctx, mine.ID, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("one reader deleted another's work: %v", err)
	}
	if _, err := s.WorkByID(ctx, theirs.ID, w.ID); err != nil {
		t.Fatalf("the other reader's work is gone: %v", err)
	}
}

// testDeleteMissingBook is the administrator's half: a file that is not
// coming back leaves the catalog, with the same collection a pass that
// dropped it would run, and readers keep what they read.
func testDeleteMissingBook(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	u := MkUser(t, s, "delete-book")
	folder := MkFolder(t, s, "gone", store.FolderPlain)
	keep := store.ObservedBook{
		RelativePath: "keep.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-keep", Title: "Keep",
	}
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		keep,
		{
			RelativePath: "read.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "sha-read", Title: "Read",
			Series: []store.ObservedSeries{{Name: "Teixcalaan", Position: Ptr(1.0)}},
		},
		{
			RelativePath: "unread.epub", SizeBytes: 1, MTime: now,
			ContentSHA256: "sha-unread", Title: "Unread",
		},
	}, true, now)
	known := knownByPath(t, s, folder.ID)
	readID, unreadID := known["read.epub"].ID, known["unread.epub"].ID

	readWork := store.Work{ID: "w-read", UserID: u.ID, Title: "Read", CreatedAt: now}
	if _, err := s.ResolveCatalogBookWork(ctx, u.ID, readID, readWork,
		[]store.Edition{{UserID: u.ID, SHA256: "sha-read", WorkID: readWork.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "sha-read"}}, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendOps(ctx, u.ID, "d-book", []store.Op{{
		OpID: "018e6f1a-0000-7000-8000-0000000000d2", WorkID: readWork.ID,
		EditionSHA: Ptr("sha-read"), ClientTS: now, Progression: 0.3,
		Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}
	emptyWork := store.Work{ID: "w-unread", UserID: u.ID, Title: "Unread", CreatedAt: now}
	if _, err := s.ResolveCatalogBookWork(ctx, u.ID, unreadID, emptyWork,
		[]store.Edition{{UserID: u.ID, SHA256: "sha-unread", WorkID: emptyWork.ID}},
		[]store.Identifier{{Kind: "sha256", Value: "sha-unread"}}, false, now); err != nil {
		t.Fatal(err)
	}

	// An active book is not deletable: the next pass would re-add it.
	if err := s.DeleteMissingBook(ctx, readID); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("deleting an active book: want ErrInvalidInput, got %v", err)
	}
	if err := s.DeleteMissingBook(ctx, "b-never-existed"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting an unknown book: want ErrNotFound, got %v", err)
	}

	doReconcile(t, s, folder.ID, []store.ObservedBook{keep}, true, now.Add(time.Hour))

	if err := s.DeleteMissingBook(ctx, readID); err != nil {
		t.Fatalf("DeleteMissingBook: %v", err)
	}
	if err := s.DeleteMissingBook(ctx, unreadID); err != nil {
		t.Fatalf("DeleteMissingBook: %v", err)
	}
	if _, err := s.CatalogBookByID(ctx, readID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a deleted book is still in the catalog: %v", err)
	}

	// The reader keeps what they read, as a work no book backs; the
	// work behind nothing at all is collected with the book.
	surviving, err := s.WorkByID(ctx, u.ID, readWork.ID)
	if err != nil {
		t.Fatalf("a work with reading behind it went with its book: %v", err)
	}
	ids, err := s.WorkBookIDs(ctx, u.ID, surviving.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("a deleted book left its mapping behind: %v", ids)
	}
	if _, err := s.WorkByID(ctx, u.ID, emptyWork.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a work with no book and no reading survived: %v", err)
	}

	// The series only that book named belongs to nobody now (ADR-0019).
	series, err := s.ListCatalogEntities(ctx, u.ID, store.EntitySeries, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 0 {
		t.Fatalf("an entity no book names survived the delete: %+v", series)
	}

	// And the reader can now remove the work the book left behind.
	if err := s.DeleteWork(ctx, u.ID, surviving.ID); err != nil {
		t.Fatalf("DeleteWork on the orphan the delete created: %v", err)
	}
}

// mkUploadFolder is a folder an administrator marked writable, which is
// the only kind a book can be deleted out of (ADR-0025).
func mkUploadFolder(t *testing.T, s store.Store, name string) store.Folder {
	t.Helper()
	f := MkFolder(t, s, name, store.FolderPlain)
	if err := s.SetFolderUploads(
		context.Background(), f.ID, true, time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	f.AcceptsUploads = true
	return f
}

// testDeleteCatalogBook is the delete an upload earns: a book in a
// folder the server may write into leaves the catalog, and one in a
// folder it may not is refused however it is asked.
func testDeleteCatalogBook(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Now().UTC()

	writable := mkUploadFolder(t, s, "deletable")
	doReconcile(t, s, writable.ID, []store.ObservedBook{{
		RelativePath: "sent.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-sent", Title: "Sent",
	}}, true, now)
	sentID := knownByPath(t, s, writable.ID)["sent.epub"].ID

	// A folder nobody marked is what every folder used to be: observed,
	// never written, and not this server's to delete out of.
	readOnly := MkFolder(t, s, "curated", store.FolderPlain)
	doReconcile(t, s, readOnly.ID, []store.ObservedBook{{
		RelativePath: "theirs.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-theirs-file", Title: "Theirs",
	}}, true, now)
	theirsID := knownByPath(t, s, readOnly.ID)["theirs.epub"].ID

	if _, err := s.DeleteCatalogBook(
		ctx, theirsID, store.DeleteBookOptions{},
	); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("deleting out of a folder that takes no uploads: "+
			"want ErrInvalidInput, got %v", err)
	}
	if _, err := s.CatalogBookByID(ctx, theirsID); err != nil {
		t.Fatalf("a refused delete took the book anyway: %v", err)
	}

	if _, err := s.DeleteCatalogBook(
		ctx, sentID, store.DeleteBookOptions{},
	); err != nil {
		t.Fatalf("DeleteCatalogBook: %v", err)
	}
	if _, err := s.CatalogBookByID(ctx, sentID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the book survived its own deletion: %v", err)
	}
	if _, err := s.DeleteCatalogBook(
		ctx, sentID, store.DeleteBookOptions{},
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting a book twice: want ErrNotFound, got %v", err)
	}
	if _, err := s.DeleteCatalogBook(
		ctx, "b-never-existed", store.DeleteBookOptions{},
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting an unknown book: want ErrNotFound, got %v", err)
	}
}

// testDeleteCatalogBookForgetsOneReader is the second question the
// delete asks. The file is shared, so it goes for everybody; the
// reading is per-user, so only the reader who asked loses theirs.
func testDeleteCatalogBookForgetsOneReader(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mine := MkUser(t, s, "forget-mine")
	theirs := MkUser(t, s, "forget-theirs")

	folder := mkUploadFolder(t, s, "shared")
	doReconcile(t, s, folder.ID, []store.ObservedBook{{
		RelativePath: "shared.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-shared", Title: "Shared",
	}}, true, now)
	bookID := knownByPath(t, s, folder.ID)["shared.epub"].ID

	readIt := func(u store.User, workID string) {
		t.Helper()
		w := store.Work{ID: workID, UserID: u.ID, Title: "Shared", CreatedAt: now}
		if _, err := s.ResolveCatalogBookWork(ctx, u.ID, bookID, w,
			[]store.Edition{{UserID: u.ID, SHA256: "sha-shared", WorkID: workID}},
			[]store.Identifier{{Kind: "sha256", Value: "sha-shared"}},
			false, now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendOps(ctx, u.ID, "d-"+workID, []store.Op{{
			OpID:   "018e6f1a-0000-7000-8000-00000000" + workID[len(workID)-4:],
			WorkID: workID, EditionSHA: Ptr("sha-shared"), ClientTS: now,
			Progression: 0.3, Origin: store.OriginNative,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	readIt(mine, "w-fa01")
	readIt(theirs, "w-fa02")

	out, err := s.DeleteCatalogBook(ctx, bookID, store.DeleteBookOptions{
		ForgetReadingFor: mine.ID,
	})
	if err != nil {
		t.Fatalf("DeleteCatalogBook: %v", err)
	}
	if !out.ReadingForgotten || out.ReadingKept {
		t.Fatalf("result = %+v, want the caller's reading forgotten", out)
	}
	if _, err := s.WorkByID(ctx, mine.ID, "w-fa01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the caller asked to forget and did not: %v", err)
	}
	// The other reader never asked, so their reading survives as the
	// bookless work only they can remove (ADR-0024).
	if _, err := s.WorkByID(ctx, theirs.ID, "w-fa02"); err != nil {
		t.Fatalf("one reader's delete took another's reading: %v", err)
	}
	if err := s.DeleteWork(ctx, theirs.ID, "w-fa02"); err != nil {
		t.Fatalf("the surviving work is not its own reader's to remove: %v", err)
	}
}

// testDeleteCatalogBookKeepsReadingASecondCopyHolds is the case a
// per-user, per-book mapping makes possible: the same book twice, one
// work behind both. Deleting one copy must not take reading the other
// still holds, and the reader is told so rather than told it failed.
func testDeleteCatalogBookKeepsReadingASecondCopyHolds(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	u := MkUser(t, s, "two-copies")

	folder := mkUploadFolder(t, s, "twice")
	doReconcile(t, s, folder.ID, []store.ObservedBook{{
		RelativePath: "first.epub", SizeBytes: 1, MTime: now,
		ContentSHA256: "sha-first", Title: "Twice",
	}, {
		RelativePath: "second.epub", SizeBytes: 2, MTime: now,
		ContentSHA256: "sha-second", Title: "Twice",
	}}, true, now)
	byPath := knownByPath(t, s, folder.ID)

	// Two files, one book: they differ byte for byte, so the catalog
	// keeps them apart, but they carry the same partial-md5 and so
	// resolve to one work — which is the reader's position in a book
	// they happen to hold twice.
	var workID string
	for i, b := range []struct{ id, sha string }{
		{byPath["first.epub"].ID, "sha-first"},
		{byPath["second.epub"].ID, "sha-second"},
	} {
		proposed := store.Work{
			ID:     fmt.Sprintf("w-twice-%d", i),
			UserID: u.ID, Title: "Twice", CreatedAt: now,
		}
		res, err := s.ResolveCatalogBookWork(ctx, u.ID, b.id, proposed,
			[]store.Edition{{UserID: u.ID, SHA256: b.sha, WorkID: proposed.ID}},
			[]store.Identifier{
				{Kind: "sha256", Value: b.sha},
				{Kind: "partial-md5", Value: "md5-twice"},
			},
			false, now)
		if err != nil {
			t.Fatal(err)
		}
		if workID == "" {
			workID = res.WorkID
		} else if res.WorkID != workID {
			t.Fatalf("the second copy resolved to work %q, want %q: "+
				"this test needs one work behind both books",
				res.WorkID, workID)
		}
	}
	w := store.Work{ID: workID}

	out, err := s.DeleteCatalogBook(ctx, byPath["first.epub"].ID,
		store.DeleteBookOptions{ForgetReadingFor: u.ID})
	if err != nil {
		t.Fatalf("DeleteCatalogBook: %v", err)
	}
	if out.ReadingForgotten || !out.ReadingKept {
		t.Fatalf("result = %+v, want the reading kept by the second copy", out)
	}
	if _, err := s.WorkByID(ctx, u.ID, w.ID); err != nil {
		t.Fatalf("reading a second copy still holds was deleted: %v", err)
	}

	// With the last copy gone there is nothing left to hold it, so the
	// same request now forgets.
	out, err = s.DeleteCatalogBook(ctx, byPath["second.epub"].ID,
		store.DeleteBookOptions{ForgetReadingFor: u.ID})
	if err != nil {
		t.Fatalf("DeleteCatalogBook: %v", err)
	}
	if !out.ReadingForgotten || out.ReadingKept {
		t.Fatalf("result = %+v, want the reading forgotten", out)
	}
	if _, err := s.WorkByID(ctx, u.ID, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the last copy went and the reading stayed: %v", err)
	}
}
