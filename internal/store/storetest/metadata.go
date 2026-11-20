package storetest

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func testWorkMetadata(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "metadata-reader")
	other := MkUser(t, s, "metadata-other")
	w := MkWork(t, s, u, "metadata-book", "metadata-edition")
	revisions := map[string]int64{"title-1": 1, "author-1": 2, "title-2": 3, "title-unset": 4}
	put := func(user, work, id, field, value string) error {
		t.Helper()
		return s.WriteWorkMetadata(ctx, user, work, store.WorkMetadataWrite{
			ID: id, WriterID: "installation", Revision: revisions[id],
			Field: field, Value: value,
		})
	}
	if err := put(u.ID, w.ID, "title-1", store.MetadataTitle, `"Dune"`); err != nil {
		t.Fatal(err)
	}
	if err := put(u.ID, w.ID, "author-1", store.MetadataAuthor, `"Frank Herbert"`); err != nil {
		t.Fatal(err)
	}
	metadata, err := s.ReadWorkMetadata(ctx, u.ID, w.ID)
	if err != nil || len(metadata.Fields) != 2 ||
		metadata.Fields[0].Field != store.MetadataAuthor ||
		metadata.Fields[1].Value != `"Dune"` || metadata.Cover != nil {
		t.Fatalf("read: %+v %v", metadata, err)
	}

	// An empty string is a value somebody typed, and it is not "no
	// override": only null unsets a field.
	if err := put(u.ID, w.ID, "title-2", store.MetadataTitle, `""`); err != nil {
		t.Fatal(err)
	}
	metadata, err = s.ReadWorkMetadata(ctx, u.ID, w.ID)
	if err != nil || len(metadata.Fields) != 2 || metadata.Fields[1].Value != `""` || metadata.Fields[1].ID != "title-2" {
		t.Fatalf("explicit empty title: %+v %v", metadata, err)
	}
	if err := put(u.ID, w.ID, "title-unset", store.MetadataTitle, "null"); err != nil {
		t.Fatal(err)
	}
	metadata, err = s.ReadWorkMetadata(ctx, u.ID, w.ID)
	if err != nil || len(metadata.Fields) != 1 || metadata.Fields[0].Field != store.MetadataAuthor {
		t.Fatalf("unset left a row: %+v %v", metadata, err)
	}

	// A replay is idempotent; the same id with different content is not.
	if err := put(u.ID, w.ID, "author-1", store.MetadataAuthor, `"Frank Herbert"`); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	if err := put(u.ID, w.ID, "author-1", store.MetadataAuthor, `"Someone else"`); !errors.Is(err, store.ErrIDMismatch) {
		t.Fatalf("changed retry: %v", err)
	}
	// An older revision from the same writer never rewrites the field,
	// but its receipt is kept so the retry stays idempotent.
	revisions["author-old"] = 1
	if err := put(u.ID, w.ID, "author-old", store.MetadataAuthor, `"Early name"`); err != nil {
		t.Fatal(err)
	}
	if err := put(u.ID, w.ID, "author-old", store.MetadataAuthor, `"Early name"`); err != nil {
		t.Fatalf("superseded retry: %v", err)
	}
	metadata, err = s.ReadWorkMetadata(ctx, u.ID, w.ID)
	if err != nil || len(metadata.Fields) != 1 || metadata.Fields[0].ID != "author-1" {
		t.Fatalf("superseded write won: %+v %v", metadata, err)
	}

	if _, err := s.ReadWorkMetadata(ctx, other.ID, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user read: %v", err)
	}
	if err := put(other.ID, w.ID, "title-1", store.MetadataTitle, `"Mine"`); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user write: %v", err)
	}
	if err := put(u.ID, "no-such-work", "title-1", store.MetadataTitle, `"Mine"`); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown work: %v", err)
	}
}

func testWorkCover(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "cover-reader")
	other := MkUser(t, s, "cover-other")
	w := MkWork(t, s, u, "cover-book", "cover-edition")
	image := []byte("rendered jpeg bytes")

	if _, err := s.WorkCover(ctx, u.ID, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a work never given a cover has one: %v", err)
	}
	if _, err := s.PutWorkCover(ctx, u.ID, "no-such-work", "sha", image); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown work: %v", err)
	}
	if _, err := s.DeleteWorkCover(ctx, u.ID, "no-such-work"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown work delete: %v", err)
	}

	stamped, err := s.PutWorkCover(ctx, u.ID, w.ID, "sha-one", image)
	if err != nil || stamped.IsZero() {
		t.Fatalf("put: %v %v", stamped, err)
	}
	got, err := s.WorkCover(ctx, u.ID, w.ID)
	if err != nil || got.SHA256 != "sha-one" || !bytes.Equal(got.Data, image) {
		t.Fatalf("get: %+v %v", got, err)
	}
	metadata, err := s.ReadWorkMetadata(ctx, u.ID, w.ID)
	if err != nil || metadata.Cover == nil || metadata.Cover.SHA256 != "sha-one" {
		t.Fatalf("metadata cover: %+v %v", metadata, err)
	}
	if got, err := s.WorkCover(ctx, other.ID, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user cover: %+v %v", got, err)
	}

	// A second put replaces the image in place.
	replacement := []byte("another image")
	if _, err := s.PutWorkCover(ctx, u.ID, w.ID, "sha-two", replacement); err != nil {
		t.Fatal(err)
	}
	if got, err := s.WorkCover(ctx, u.ID, w.ID); err != nil || got.SHA256 != "sha-two" || !bytes.Equal(got.Data, replacement) {
		t.Fatalf("replacement: %+v %v", got, err)
	}

	// A delete leaves a tombstone: nothing to serve, but the choice is
	// still on record, which is what tells a client not to ask for the
	// publication's cover instead.
	if _, err := s.DeleteWorkCover(ctx, u.ID, w.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.WorkCover(ctx, u.ID, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("tombstoned cover served: %+v %v", got, err)
	}
	metadata, err = s.ReadWorkMetadata(ctx, u.ID, w.ID)
	if err != nil || metadata.Cover == nil || metadata.Cover.SHA256 != "" {
		t.Fatalf("tombstone not recorded: %+v %v", metadata, err)
	}
}

func testWorkMetadataFollowsWorks(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "metadata-merge-reader")
	image := []byte("rendered jpeg bytes")

	// A split leaves the reader's overrides with the work they were
	// written against: they are about that work, and the new one has
	// told the account nothing.
	w := MkWork(t, s, u, "metadata-split", "metadata-split-sha")
	if err := s.WriteWorkMetadata(ctx, u.ID, w.ID, store.WorkMetadataWrite{
		ID: "title", WriterID: "installation", Revision: 1,
		Field: store.MetadataTitle, Value: `"Split"`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutWorkCover(ctx, u.ID, w.ID, "split-sha", image); err != nil {
		t.Fatal(err)
	}
	if err := s.SplitWork(ctx, u.ID, w.ID, "metadata-split-sha", nil,
		store.Work{ID: "metadata-split-new", UserID: u.ID, Title: "split", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	metadata, err := s.ReadWorkMetadata(ctx, u.ID, w.ID)
	if err != nil || len(metadata.Fields) != 1 || metadata.Cover == nil {
		t.Fatalf("split lost an override: %+v %v", metadata, err)
	}
	metadata, err = s.ReadWorkMetadata(ctx, u.ID, "metadata-split-new")
	if err != nil || len(metadata.Fields) != 0 || metadata.Cover != nil {
		t.Fatalf("split invented an override: %+v %v", metadata, err)
	}

	// Both works keep their own choices side by side until they meet.
	from := MkWork(t, s, u, "metadata-from", "metadata-from-sha")
	into := MkWork(t, s, u, "metadata-into", "metadata-into-sha")
	write := func(work, id, field, value string, revision int64) {
		t.Helper()
		if err := s.WriteWorkMetadata(ctx, u.ID, work, store.WorkMetadataWrite{
			ID: id, WriterID: "installation", Revision: revision,
			Field: field, Value: value,
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(from.ID, "from-title", store.MetadataTitle, `"From"`, 1)
	write(from.ID, "from-description", store.MetadataDescription, `"From blurb"`, 2)
	write(into.ID, "into-author", store.MetadataAuthor, `"Into"`, 1)
	if _, err := s.PutWorkCover(ctx, u.ID, from.ID, "from-sha", image); err != nil {
		t.Fatal(err)
	}
	if err := s.MergeWorks(ctx, u.ID, from.ID, into.ID); err != nil {
		t.Fatal(err)
	}
	metadata, err = s.ReadWorkMetadata(ctx, u.ID, into.ID)
	if err != nil || len(metadata.Fields) != 3 {
		t.Fatalf("merge lost an override: %+v %v", metadata, err)
	}
	if metadata.Cover == nil || metadata.Cover.SHA256 != "from-sha" {
		t.Fatalf("merge lost the cover: %+v", metadata)
	}
	// The receipts followed too, so a retry against the survivor is still
	// the same request.
	write(into.ID, "from-title", store.MetadataTitle, `"From"`, 1)
	if err := s.WriteWorkMetadata(ctx, u.ID, into.ID, store.WorkMetadataWrite{
		ID: "from-title", WriterID: "installation", Revision: 1,
		Field: store.MetadataTitle, Value: `"Changed"`,
	}); !errors.Is(err, store.ErrIDMismatch) {
		t.Fatalf("receipt did not follow the merge: %v", err)
	}

	// One field in both works: with one writer the higher revision wins
	// whichever work it was written against, and the loser's receipt stays
	// so its own retry is idempotent.
	first := MkWork(t, s, u, "metadata-first", "metadata-first-sha")
	second := MkWork(t, s, u, "metadata-second", "metadata-second-sha")
	write(first.ID, "older", store.MetadataTitle, `"Older"`, 2)
	write(second.ID, "newer", store.MetadataTitle, `"Newer"`, 1)
	if err := s.MergeWorks(ctx, u.ID, first.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	metadata, err = s.ReadWorkMetadata(ctx, u.ID, second.ID)
	if err != nil || len(metadata.Fields) != 1 || metadata.Fields[0].ID != "older" || metadata.Fields[0].Value != `"Older"` {
		t.Fatalf("merge kept the wrong head: %+v %v", metadata, err)
	}
	if err := s.WriteWorkMetadata(ctx, u.ID, second.ID, store.WorkMetadataWrite{
		ID: "newer", WriterID: "installation", Revision: 1,
		Field: store.MetadataTitle, Value: `"Newer"`,
	}); err != nil {
		t.Fatalf("replay after merge: %v", err)
	}
	metadata, err = s.ReadWorkMetadata(ctx, u.ID, second.ID)
	if err != nil || len(metadata.Fields) != 1 || metadata.Fields[0].ID != "older" {
		t.Fatalf("replay overwrote a newer head: %+v %v", metadata, err)
	}

	// Two writers instead: the head written last wins, because there is
	// no revision to rank them by.
	third := MkWork(t, s, u, "metadata-third", "metadata-third-sha")
	fourth := MkWork(t, s, u, "metadata-fourth", "metadata-fourth-sha")
	for _, item := range []struct{ work, id, writer, value string }{
		{third.ID, "third", "phone", `"Phone"`},
		{fourth.ID, "fourth", "tablet", `"Tablet"`},
	} {
		if err := s.WriteWorkMetadata(ctx, u.ID, item.work, store.WorkMetadataWrite{
			ID: item.id, WriterID: item.writer, Revision: 1,
			Field: store.MetadataTitle, Value: item.value,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MergeWorks(ctx, u.ID, third.ID, fourth.ID); err != nil {
		t.Fatal(err)
	}
	metadata, err = s.ReadWorkMetadata(ctx, u.ID, fourth.ID)
	if err != nil || len(metadata.Fields) != 1 || metadata.Fields[0].ID != "fourth" {
		t.Fatalf("merge kept the earlier writer: %+v %v", metadata, err)
	}

	// A cover the survivor already had is not replaced by the source's.
	fifth := MkWork(t, s, u, "metadata-fifth", "metadata-fifth-sha")
	write(fifth.ID, "fifth-title", store.MetadataTitle, `"Fifth"`, 1)
	if _, err := s.PutWorkCover(ctx, u.ID, fifth.ID, "into-sha", []byte("into image")); err != nil {
		t.Fatal(err)
	}
	survivor := MkWork(t, s, u, "metadata-survivor", "metadata-survivor-sha")
	if _, err := s.PutWorkCover(ctx, u.ID, survivor.ID, "survivor-sha", []byte("survivor image")); err != nil {
		t.Fatal(err)
	}
	if err := s.MergeWorks(ctx, u.ID, fifth.ID, survivor.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.WorkCover(ctx, u.ID, survivor.ID); err != nil || got.SHA256 != "survivor-sha" {
		t.Fatalf("merge replaced the survivor's cover: %+v %v", got, err)
	}

	// A merge whose eligible heads are all superseded keeps both works:
	// there is no answer to give, so the reader has to make one.
	cyclicFrom := MkWork(t, s, u, "metadata-cyclic-from", "metadata-cyclic-from-sha")
	cyclicInto := MkWork(t, s, u, "metadata-cyclic-into", "metadata-cyclic-into-sha")
	writeAs := func(work, id, writer, value string, revision int64) {
		t.Helper()
		if err := s.WriteWorkMetadata(ctx, u.ID, work, store.WorkMetadataWrite{
			ID: id, WriterID: writer, Revision: revision,
			Field: store.MetadataTitle, Value: value,
		}); err != nil {
			t.Fatal(err)
		}
	}
	writeAs(cyclicFrom.ID, "cyclic-a20", "writer-a", `"A20"`, 20)
	writeAs(cyclicInto.ID, "cyclic-b20", "writer-b", `"B20"`, 20)
	writeAs(cyclicInto.ID, "cyclic-a10", "writer-a", `"A10"`, 10)
	writeAs(cyclicFrom.ID, "cyclic-b10", "writer-b", `"B10"`, 10)
	if err := s.MergeWorks(ctx, u.ID, cyclicFrom.ID, cyclicInto.ID); !errors.Is(err, store.ErrMetadataMergeConflict) {
		t.Fatalf("merge with no eligible head: %v", err)
	}
	for _, work := range []store.Work{cyclicFrom, cyclicInto} {
		if _, err := s.WorkByID(ctx, u.ID, work.ID); err != nil {
			t.Fatalf("conflict removed a work: %v", err)
		}
	}
}

func testWorkMetadataSurvivesCatalogRemoval(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "metadata-gc-reader")
	w := MkWork(t, s, u, "metadata-gc", "metadata-gc-edition")
	if err := s.WriteWorkMetadata(ctx, u.ID, w.ID, store.WorkMetadataWrite{
		ID: "title", WriterID: "installation", Revision: 1,
		Field: store.MetadataTitle, Value: `"Kept"`,
	}); err != nil {
		t.Fatal(err)
	}
	// A Calibre pass that no longer finds the book deletes it, and the
	// work survives only because the reader still has something to say
	// about it.
	folder := MkFolder(t, s, "metadata-gc", store.FolderCalibre)
	calibreID := int64(1)
	now := time.Now()
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &calibreID, RelativePath: "Other/book.epub", SizeBytes: 1,
			MTime: now, ContentSHA256: "other", Title: "Other"},
	}, true, now)
	if _, err := s.WorkByID(ctx, u.ID, w.ID); err != nil {
		t.Fatalf("catalog collection lost a work with an override: %v", err)
	}

	// A cover the reader deleted says nothing about the work, so a work
	// whose only trace is that tombstone is collected like any other
	// empty one.
	gone := MkWork(t, s, u, "metadata-tombstone", "metadata-tombstone-edition")
	if _, err := s.DeleteWorkCover(ctx, u.ID, gone.ID); err != nil {
		t.Fatal(err)
	}
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &calibreID, RelativePath: "Other/book.epub", SizeBytes: 1,
			MTime: now, ContentSHA256: "other", Title: "Other"},
	}, true, now)
	if _, err := s.WorkByID(ctx, u.ID, gone.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a cover tombstone kept a work alive: %v", err)
	}
}
