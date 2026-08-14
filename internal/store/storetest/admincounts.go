package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/store"
)

// testAdminCounts covers the one aggregate read the admin panel makes.
// Two properties matter and neither is obvious from the SQL: an empty
// instance answers with zeros rather than an error or a nil map, and the
// oldest-job timestamp is reported only for the states a job can still
// be stuck in (ADR-0013).
func testAdminCounts(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()

	empty, err := s.AdminCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Users != 0 || empty.Libraries != 0 || empty.Blobs != 0 ||
		empty.BlobBytes != 0 || empty.TrashNextExpiry != nil {
		t.Fatalf("empty instance: %+v", empty)
	}
	if empty.BooksByStatus == nil || empty.JobsByState == nil ||
		empty.OldestJobByState == nil {
		t.Fatalf("empty instance returned nil maps: %+v", empty)
	}

	now := time.Date(2026, time.April, 5, 6, 7, 8, 0, time.UTC)
	alice := MkUser(t, s, "counts-alice")
	bob := MkUser(t, s, "counts-bob")
	if err := s.SetUserAdmin(ctx, alice.ID, true); err != nil {
		t.Fatal(err)
	}

	managed := store.Library{
		ID: "counts-managed", OwnerUserID: alice.ID, QuotaUserID: alice.ID,
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Managed", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, managed); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := s.CreateLibrary(ctx, store.Library{
		ID: "counts-watched", OwnerUserID: bob.ID, QuotaUserID: bob.ID,
		Source:  store.LibraryDirectory,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshInterval, Name: "Watched", RootPath: &root,
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// One job promoted into a book with a blob, and one left mid-flight.
	blob := ingestBlob("counts-blob", 4096)
	job := createIngestJob(t, s, alice.ID, managed.ID, "counts-job", now)
	staged, err := s.CommitIngestStage(ctx, alice.ID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: job.Revision, Artifact: blob,
			StagingPath: contentpath.StagingPath(job.ID),
			UpdatedAt:   now.Add(time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
	job = extractIngestJob(t, s, staged.Job, now.Add(2*time.Minute))
	if _, err := s.CommitNewBookPromotion(ctx, alice.ID, job.ID,
		promotionRequest(job, blob, "counts-book", "counts-file",
			now.Add(4*time.Minute))); err != nil {
		t.Fatal(err)
	}
	stuck := createIngestJob(t, s, alice.ID, managed.ID, "counts-stuck", now)

	got, err := s.AdminCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Users != 2 || got.AdminUsers != 1 || got.DisabledUsers != 0 {
		t.Fatalf("users: %+v", got)
	}
	if got.Libraries != 2 || got.ManagedLibraries != 1 || got.WatchedLibraries != 1 {
		t.Fatalf("libraries: %+v", got)
	}
	if got.BooksByStatus["active"] != 1 {
		t.Fatalf("books by status: %+v", got.BooksByStatus)
	}
	if got.Blobs != 1 || got.BlobBytes != 4096 || got.OrphanBlobs != 0 {
		t.Fatalf("blobs: %+v", got)
	}
	if got.JobsByState["promoted"] != 1 || got.JobsByState[string(stuck.State)] != 1 {
		t.Fatalf("jobs by state: %+v", got.JobsByState)
	}
	if _, ok := got.OldestJobByState["promoted"]; ok {
		t.Fatal("a promoted job is history, not a stuck worker")
	}
	oldest, ok := got.OldestJobByState[string(stuck.State)]
	if !ok {
		t.Fatalf("no oldest timestamp for %q: %+v", stuck.State, got.OldestJobByState)
	}
	if !oldest.UTC().Equal(now) {
		t.Fatalf("oldest job: got %v, want %v", oldest.UTC(), now)
	}
}
