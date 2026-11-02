package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// A KOReader fingerprint resolves to a book only through a grant, and
// only when every granted book behind it is the same file (ADR-0059).
func testCatalogBookByPartialMD5(t *testing.T, open OpenFunc) {
	t.Helper()
	s := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	reader := store.User{ID: "pmd5-reader", Name: "pmd5-reader", Argon2Hash: "x", Timezone: "UTC", CreatedAt: now}
	if err := s.CreateUser(ctx, reader); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pmd5-a", "pmd5-b", "pmd5-c"} {
		if err := s.CreateFolder(ctx, store.Folder{ID: id, Name: id, RootPath: "/" + id, Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	book := func(path, sha, md5 string) store.ObservedBook {
		return store.ObservedBook{
			RelativePath: path, SizeBytes: 1, MTime: now, ContentSHA256: sha, PartialMD5: md5,
			OriginalFilename: path, MediaType: "application/epub+zip", Title: path,
		}
	}
	// "same" is one file held in two folders; "clash" is two different
	// files that share a fingerprint, one of them in an ungranted folder.
	for folder, books := range map[string][]store.ObservedBook{
		"pmd5-a": {book("same.epub", "sha-same", "md5-same"), book("clash1.epub", "sha-clash-1", "md5-clash")},
		"pmd5-b": {book("same.epub", "sha-same", "md5-same")},
		"pmd5-c": {book("clash2.epub", "sha-clash-2", "md5-clash"), book("hidden.epub", "sha-hidden", "md5-hidden")},
	} {
		if _, err := s.ReconcileFolder(ctx, folder, books, true, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, folder := range []string{"pmd5-a", "pmd5-b"} {
		if err := s.AssignUserFolder(ctx, reader.ID, folder); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.CatalogBookByPartialMD5(ctx, reader.ID, "md5-same")
	if err != nil || got.ContentSHA256 != "sha-same" {
		t.Fatalf("byte-identical copies = %+v, %v", got, err)
	}
	// The other file sharing md5-clash is in a folder the reader cannot
	// see, so for them the fingerprint names one book.
	got, err = s.CatalogBookByPartialMD5(ctx, reader.ID, "md5-clash")
	if err != nil || got.ContentSHA256 != "sha-clash-1" {
		t.Fatalf("collision outside the reader's grants = %+v, %v", got, err)
	}
	for _, md5 := range []string{"md5-hidden", "md5-unknown", ""} {
		if _, err := s.CatalogBookByPartialMD5(ctx, reader.ID, md5); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%q = %v, want ErrNotFound", md5, err)
		}
	}

	if err := s.AssignUserFolder(ctx, reader.ID, "pmd5-c"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CatalogBookByPartialMD5(ctx, reader.ID, "md5-clash"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("two files under one fingerprint = %v, want ErrNotFound", err)
	}
	if err := s.UnassignUserFolder(ctx, reader.ID, "pmd5-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.UnassignUserFolder(ctx, reader.ID, "pmd5-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CatalogBookByPartialMD5(ctx, reader.ID, "md5-same"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("revoked grant = %v, want ErrNotFound", err)
	}
}
