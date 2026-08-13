//go:build linux

package content_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/content"
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
	cas, err := content.Open(filepath.Join(root, "content"))
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

	job, created, err := st.CreateIngestJob(ctx, user.ID, store.IngestJobRequest{
		ID: "job-1", LibraryID: library.ID, Source: store.IngestUpload,
		RequestFingerprint: "request-job-1", CreatedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("create job: %v %v", created, err)
	}

	payload := []byte("a small but entirely real publication payload")
	staged, err := cas.Stage(ctx, job.ID, bytes.NewReader(payload), 1<<20)
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
			change.ExtractedEmbeddedMetadataJSON = []byte(`{"title":"Dune"}`)
		}
		if job, err = st.TransitionIngestJob(
			ctx, job.UserID, job.ID, change); err != nil {
			t.Fatalf("advance to %s: %v", next, err)
		}
	}

	report, err := content.RunIngestPromotionPass(
		ctx, st, cas, func() time.Time { return now.Add(5 * time.Minute) },
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

	// A second pass has nothing to list: the job left extracted, so the pass
	// cannot create a second book for the same artifact.
	again, err := content.RunIngestPromotionPass(
		ctx, st, cas, func() time.Time { return now.Add(9 * time.Minute) },
		48*time.Hour, 10)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if again != (content.IngestPromotionReport{}) {
		t.Fatalf("second pass did work: %+v", again)
	}
}
