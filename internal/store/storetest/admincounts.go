package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// testAdminCounts covers the one aggregate read the admin panel makes.
// Two properties matter and neither is obvious from the SQL: an empty
// instance answers with zeros and non-nil maps rather than an error or
// a nil map, and every number is an integer with nothing that could
// identify a user, a book or a path (ADR-0013).
func testAdminCounts(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()

	empty, err := s.AdminCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Users != 0 || empty.Folders != 0 {
		t.Fatalf("empty instance: %+v", empty)
	}
	if empty.FoldersByKind == nil || empty.BooksByStatus == nil {
		t.Fatalf("empty instance returned nil maps: %+v", empty)
	}

	now := time.Date(2026, time.April, 5, 6, 7, 8, 0, time.UTC)
	alice := MkUser(t, s, "counts-alice")
	MkUser(t, s, "counts-bob")
	if err := s.SetUserAdmin(ctx, alice.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserDisabled(ctx, "u-counts-bob", true, now); err != nil {
		t.Fatal(err)
	}

	plain := MkFolder(t, s, "counts-plain", store.FolderPlain)
	MkFolder(t, s, "counts-calibre", store.FolderCalibre)

	doReconcile(t, s, plain.ID, []store.ObservedBook{
		{RelativePath: "active.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-active", Title: "Active"},
		{RelativePath: "away.epub", SizeBytes: 1, MTime: now, ContentSHA256: "sha-away", Title: "Away"},
	}, true, now)
	doReconcile(t, s, plain.ID, []store.ObservedBook{
		{RelativePath: "active.epub", SizeBytes: 1, MTime: now, Unchanged: true},
	}, true, now.Add(time.Hour))

	got, err := s.AdminCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Users != 2 || got.AdminUsers != 1 || got.DisabledUsers != 1 {
		t.Fatalf("users: %+v", got)
	}
	if got.Folders != 2 || got.FoldersByKind["plain"] != 1 || got.FoldersByKind["calibre"] != 1 {
		t.Fatalf("folders: %+v", got)
	}
	if got.BooksByStatus["active"] != 1 || got.BooksByStatus["missing"] != 1 {
		t.Fatalf("books by status: %+v", got.BooksByStatus)
	}
}
