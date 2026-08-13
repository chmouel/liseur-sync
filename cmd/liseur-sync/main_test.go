package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

func TestOpenContentAndRecoverBeforeServe(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "content")
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "recovery.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	user := store.User{
		ID: "user", Name: "alice", Argon2Hash: "x", Timezone: "UTC",
		CreatedAt: now,
	}
	if err := st.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	library := store.Library{
		ID: "library", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Kind: store.LibraryManaged, Name: "Library", CreatedAt: now,
	}
	if err := st.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	job, _, err := st.CreateIngestJob(ctx, user.ID, store.IngestJobRequest{
		ID: "job", LibraryID: library.ID, Source: store.IngestUpload,
		RequestFingerprint: "request", CreatedAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	setupCAS, err := content.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := setupCAS.Stage(
		ctx, job.ID, bytes.NewReader([]byte("epub")), 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := setupCAS.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CommitIngestStage(ctx, user.ID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: job.Revision,
			Artifact: store.BlobInfo{
				SHA256: staged.SHA256, SizeBytes: staged.Size,
			},
			StagingPath: staged.Path,
			UpdatedAt:   now,
		}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(staged.Path))); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Content.Root = root
	cas, report, err := openContentAndRecover(ctx, st, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cas.Close() })
	if report.Failed != 1 {
		t.Fatalf("startup recovery report: %+v", report)
	}
	recovered, err := st.IngestJobByID(ctx, user.ID, job.ID)
	if err != nil || recovered.State != store.IngestFailed ||
		recovered.ErrorCode == nil || *recovered.ErrorCode != "artifact_missing" {
		t.Fatalf("startup recovered job: %+v %v", recovered, err)
	}
}

func TestRelativeContentRootFollowsAbsoluteSQLiteDatabase(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.Database.URL = filepath.Join(dataDir, "liseur-sync.db")
	cfg.Content.Root = "content"
	st, err := sqlite.Open(cfg.Database.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cas, _, err := openContentAndRecover(ctx, st, cfg, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cas.Close() })
	if cas.Root() != filepath.Join(dataDir, "content") {
		t.Fatalf("resolved content root: %q", cas.Root())
	}
}
