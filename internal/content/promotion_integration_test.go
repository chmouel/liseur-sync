//go:build linux

package content

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
	"github.com/chmouel/liseur-sync/internal/store/storetest"
)

// TestPromotionPassAgainstARealStore drives the promotion pass end to end
// against a real backend and a real CAS. The unit tests use a fake store, and
// a fake can only be as strict as its author guessed; this pins the pass
// against the preconditions the backend actually enforces — the blob hold,
// the job cross-checks, and the shared request validation.
func TestPromotionPassAgainstARealStore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cas, err := Open(filepath.Join(root, "content"))
	if err != nil {
		t.Fatalf("open cas: %v", err)
	}
	defer cas.Close()
	st, err := sqlite.Open(filepath.Join(root, "liseur.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	user := storetest.MkUser(t, st, "promoter")
	library := store.Library{
		ID: "lib-1", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Kind: store.LibraryManaged, Name: "Books", CreatedAt: now,
	}
	if err := st.CreateLibrary(ctx, library); err != nil {
		t.Fatalf("create library: %v", err)
	}

	payload := []byte("a small but entirely real publication payload")
	extracted := func(id, title string) store.IngestJob {
		t.Helper()
		job, created, err := st.CreateIngestJob(ctx, user.ID, store.IngestJobRequest{
			ID: id, LibraryID: library.ID, Source: store.IngestUpload,
			RequestFingerprint: "request-" + id, CreatedAt: now,
		})
		if err != nil || !created {
			t.Fatalf("create job: %v %v", created, err)
		}
		staged, err := cas.Stage(ctx, job.ID,
			bytes.NewReader(append(payload, id...)), 1<<20)
		if err != nil {
			t.Fatalf("stage: %v", err)
		}
		stage, err := st.CommitIngestStage(ctx, job.UserID, job.ID,
			store.CommitIngestStageRequest{
				ExpectedRevision: job.Revision,
				Artifact: store.BlobInfo{
					SHA256: staged.SHA256, SizeBytes: staged.Size},
				StagingPath: contentpath.StagingPath(job.ID),
				UpdatedAt:   now.Add(time.Minute),
			})
		if err != nil {
			t.Fatalf("commit stage: %v", err)
		}
		job = stage.Job
		for _, next := range []store.IngestState{
			store.IngestValidated, store.IngestExtracted,
		} {
			change := store.IngestJobTransition{
				ExpectedState: job.State, ExpectedRevision: job.Revision,
				NextState: next, UpdatedAt: now.Add(2 * time.Minute),
			}
			if next == store.IngestExtracted {
				change.ExtractedEmbeddedMetadataJSON = []byte(
					`{"title":"` + title + `"}`)
			}
			if job, err = st.TransitionIngestJob(
				ctx, job.UserID, job.ID, change); err != nil {
				t.Fatalf("advance to %s: %v", next, err)
			}
		}
		return job
	}

	// Promote one job on its own, with no pass around it to attach entity
	// sets afterwards. That isolates the claim: the title has to arrive in
	// the promotion transaction itself, not in a later step, because promoted
	// is terminal and no query would return this book to be finished.
	alone := extracted("job-alone", "Neuromancer")
	direct, err := PromoteIngestJob(ctx, st, cas, alone, nil,
		func() time.Time { return now.Add(4 * time.Minute) }, 48*time.Hour)
	if err != nil {
		t.Fatalf("promote alone: %v", err)
	}
	if direct.Book.Title != "Neuromancer" ||
		direct.Book.TitleSource != store.MetadataEmbedded {
		t.Fatalf("promotion returned %+v", direct.Book)
	}
	stored, err := st.CatalogBookByID(ctx, user.ID, direct.Book.ID,
		store.LibraryRoleRead)
	if err != nil {
		t.Fatalf("read directly promoted book: %v", err)
	}
	if stored.Title != "Neuromancer" {
		t.Fatalf("committed book title = %q", stored.Title)
	}

	job := extracted("job-1", "Dune")
	staged := store.BlobInfo{
		SHA256: *job.ContentSHA256, SizeBytes: job.BytesReceived}

	report, err := RunIngestPromotionPass(
		ctx, st, cas, FixedPatterns(nil), func() time.Time { return now.Add(5 * time.Minute) },
		48*time.Hour, 10)
	if err != nil {
		t.Fatalf("promotion pass: %v", err)
	}
	if report.Promoted != 1 || report.Quarantined != 0 || report.Skipped != 0 {
		t.Fatalf("report = %+v", report)
	}

	promoted, err := st.IngestJobByID(ctx, user.ID, job.ID)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if promoted.State != store.IngestPromoted || promoted.BookID == nil {
		t.Fatalf("job = %+v", promoted)
	}
	book, err := st.CatalogBookByID(ctx, user.ID, *promoted.BookID,
		store.LibraryRoleRead)
	if err != nil {
		t.Fatalf("read book: %v", err)
	}
	if book.LibraryID != library.ID || book.Status != store.BookActive {
		t.Fatalf("book = %+v", book)
	}
	// The title is committed with the book, not after it, so no crash can
	// leave a title-less book that no worker is able to list again.
	if book.Title != "Dune" || book.TitleSource != store.MetadataEmbedded {
		t.Fatalf("title = %q from %q", book.Title, book.TitleSource)
	}

	// The publication is readable at its content address, and the staging
	// copy is gone. Without this the pass could commit rows naming a blob it
	// never actually published and every assertion above would still pass.
	onDisk, err := os.ReadFile(filepath.Join(cas.Root(),
		"sha256", staged.SHA256[:2], staged.SHA256[2:], "file.epub"))
	if err != nil {
		t.Fatalf("read promoted blob: %v", err)
	}
	if !bytes.Equal(onDisk, append(payload, job.ID...)) {
		t.Fatalf("promoted blob holds %q", onDisk)
	}
	if _, err := os.Stat(filepath.Join(
		root, "content", contentpath.StagingPath(job.ID))); !os.IsNotExist(err) {
		t.Fatalf("staged copy survived promotion: %v", err)
	}

	// A worker that lost the race commits the same request against a job that
	// is already promoted. This is the case the request's timestamps exist to
	// make work: it must read back the winner's rows rather than conflict.
	// Nothing else in the tree covers the replay path.
	replayed, err := st.CommitNewBookPromotion(ctx, user.ID, job.ID,
		newBookPromotion(job, Blob{
			SHA256: staged.SHA256, Size: staged.SizeBytes}, nil))
	if err != nil {
		t.Fatalf("replay promotion: %v", err)
	}
	if !replayed.Replayed {
		t.Fatal("a second commit created a new promotion")
	}
	if replayed.Book.ID != book.ID || replayed.Book.Title != book.Title {
		t.Fatalf("replay returned %+v, want the winner's book", replayed.Book)
	}

	// A second pass has nothing to list: the job left extracted, so the pass
	// cannot create a second book for the same artifact.
	again, err := RunIngestPromotionPass(
		ctx, st, cas, FixedPatterns(nil), func() time.Time { return now.Add(9 * time.Minute) },
		48*time.Hour, 10)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if again != (IngestPromotionReport{}) {
		t.Fatalf("second pass did work: %+v", again)
	}
}
