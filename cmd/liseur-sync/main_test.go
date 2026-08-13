package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/contentpath"
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
	if report.Ingest.Failed != 1 {
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

func TestOpenContentReconcilesBlobInventoryBeforeServe(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "content")
	dbPath := filepath.Join(dataDir, "liseur-sync.db")
	st, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.May, 6, 7, 8, 9, 0, time.UTC)
	user := store.User{
		ID: "reconcile-user", Name: "reconcile-user",
		Argon2Hash: "x", Timezone: "UTC", CreatedAt: now,
	}
	if err := st.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	library := store.Library{
		ID: "reconcile-library", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Kind: store.LibraryManaged, Name: "Reconciliation", CreatedAt: now,
	}
	if err := st.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}

	setupCAS, err := content.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	filesystemOnly := publishTestBlob(
		t, setupCAS, "filesystem-only", []byte("filesystem only"))
	missing := promoteTestCatalogBlob(
		t, st, nil, user.ID, library.ID, "missing",
		[]byte("missing"), now.Add(time.Minute))
	present := promoteTestCatalogBlob(
		t, st, setupCAS, user.ID, library.ID, "present",
		[]byte("present"), now.Add(10*time.Minute))
	if err := setupCAS.Close(); err != nil {
		t.Fatal(err)
	}

	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`UPDATE blobs SET orphaned_at = ? WHERE sha256 = ?`,
		now.Format(time.RFC3339Nano), present.SHA256); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Database.URL = dbPath
	cfg.Content.Root = root
	cas, report, err := openContentAndRecover(ctx, st, cfg, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if report.Ingest.Failed != 0 || report.Ingest.Quarantined != 0 ||
		len(report.Ingest.Ready) != 0 {
		t.Fatalf("unexpected ingest recovery: %+v", report.Ingest)
	}
	reconciled := report.Reconciliation
	if reconciled.PhysicalBlobs != 2 || reconciled.DatabaseBlobs != 3 ||
		reconciled.InsertedOrphans != 1 || reconciled.OrphansMarked != 1 ||
		reconciled.OrphansCleared != 1 || reconciled.MissingMarked != 1 ||
		reconciled.MissingCleared != 0 || reconciled.Unchanged != 0 {
		t.Fatalf("initial blob reconciliation: %+v", reconciled)
	}
	records := blobRecordsBySHA(t, st)
	if records[filesystemOnly.SHA256].OrphanedAt == nil ||
		records[filesystemOnly.SHA256].MissingAt != nil {
		t.Fatalf("filesystem-only blob state: %+v",
			records[filesystemOnly.SHA256])
	}
	if records[missing.SHA256].MissingAt == nil ||
		records[missing.SHA256].OrphanedAt != nil {
		t.Fatalf("missing referenced blob state: %+v", records[missing.SHA256])
	}
	if records[present.SHA256].MissingAt != nil ||
		records[present.SHA256].OrphanedAt != nil {
		t.Fatalf("present referenced blob state: %+v", records[present.SHA256])
	}
	book, err := st.CatalogBookByID(
		ctx, user.ID, "missing-book", store.LibraryRoleRead)
	if err != nil || book.Status != store.BookActive {
		t.Fatalf("missing blob changed catalog availability: %+v %v", book, err)
	}
	if _, err := os.Stat(filepath.Join(
		root, filepath.FromSlash(filesystemOnly.Path))); err != nil {
		t.Fatalf("orphan was deleted during mark-only reconciliation: %v", err)
	}
	if err := cas.Close(); err != nil {
		t.Fatal(err)
	}

	restoreCAS, err := content.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	restored := publishTestBlob(t, restoreCAS, "restore-missing", []byte("missing"))
	if restored.SHA256 != missing.SHA256 {
		t.Fatalf("restored digest: got %s want %s", restored.SHA256, missing.SHA256)
	}
	if err := restoreCAS.Close(); err != nil {
		t.Fatal(err)
	}

	cas, report, err = openContentAndRecover(
		ctx, st, cfg, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	reconciled = report.Reconciliation
	t.Cleanup(func() { cas.Close() })
	if reconciled.PhysicalBlobs != 3 || reconciled.DatabaseBlobs != 3 ||
		reconciled.MissingCleared != 1 || reconciled.Unchanged != 2 {
		t.Fatalf("restored blob reconciliation: %+v", reconciled)
	}
	records = blobRecordsBySHA(t, st)
	if records[missing.SHA256].MissingAt != nil {
		t.Fatalf("restored blob remains missing: %+v", records[missing.SHA256])
	}
	if _, err := os.Stat(filepath.Join(
		root, filepath.FromSlash(filesystemOnly.Path))); err != nil {
		t.Fatalf("orphan was deleted before a grace-period sweep: %v", err)
	}
}

func TestOpenContentSweepsGraceExpiredOrphan(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "content")
	dbPath := filepath.Join(dataDir, "liseur-sync.db")
	st, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	setupCAS, err := content.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	blob := publishTestBlob(
		t, setupCAS, "startup-gc", []byte("startup garbage collection"))
	if err := setupCAS.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Database.URL = dbPath
	cfg.Content.Root = root
	cfg.Content.OrphanGraceHours = 1
	now := time.Date(2026, time.June, 7, 8, 9, 10, 0, time.UTC)
	cas, report, err := openContentAndRecover(ctx, st, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Reconciliation.InsertedOrphans != 1 ||
		report.GC.RecordsPurged != 0 {
		t.Fatalf("initial orphan mark: %+v", report)
	}
	if err := cas.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(
		root, filepath.FromSlash(blob.Path))); err != nil {
		t.Fatalf("orphan removed before grace period: %v", err)
	}

	cas, report, err = openContentAndRecover(
		ctx, st, cfg, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cas.Close() })
	if report.GC.RecordsPurged != 1 || report.GC.FilesRemoved != 1 ||
		report.GC.FilesMissing != 0 {
		t.Fatalf("startup orphan sweep: %+v", report.GC)
	}
	if _, err := os.Stat(filepath.Join(
		root, filepath.FromSlash(blob.Path))); !os.IsNotExist(err) {
		t.Fatalf("grace-expired orphan remains: %v", err)
	}
	records, err := st.ListBlobRecords(ctx, "", 10)
	if err != nil || len(records) != 0 {
		t.Fatalf("purged orphan record remains: %+v %v", records, err)
	}
	inventory, err := cas.ListBlobs(ctx)
	if err != nil || len(inventory) != 0 {
		t.Fatalf("purged orphan inventory: %+v %v", inventory, err)
	}
}

func publishTestBlob(
	t *testing.T,
	cas *content.CAS,
	jobID string,
	data []byte,
) content.Blob {
	t.Helper()
	staged, err := cas.Stage(
		context.Background(), jobID, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	blob, err := cas.Promote(
		context.Background(), staged.Path, staged.SHA256, staged.Size)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func promoteTestCatalogBlob(
	t *testing.T,
	st store.Store,
	cas *content.CAS,
	userID, libraryID, id string,
	data []byte,
	at time.Time,
) store.BlobInfo {
	t.Helper()
	ctx := context.Background()
	jobID := id + "-job"
	sum := sha256.Sum256(data)
	blob := store.BlobInfo{
		SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(data)),
	}
	stagingPath := contentpath.StagingPath(jobID)
	if cas != nil {
		staged, err := cas.Stage(ctx, jobID, bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		blob = store.BlobInfo{SHA256: staged.SHA256, SizeBytes: staged.Size}
		stagingPath = staged.Path
	}
	job, created, err := st.CreateIngestJob(ctx, userID, store.IngestJobRequest{
		ID: jobID, LibraryID: libraryID, Source: store.IngestUpload,
		RequestFingerprint: id + "-request", CreatedAt: at,
	})
	if err != nil || !created {
		t.Fatalf("create ingest job: %+v %v %v", job, created, err)
	}
	staged, err := st.CommitIngestStage(ctx, userID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: job.Revision, Artifact: blob,
			StagingPath: stagingPath, UpdatedAt: at.Add(time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
	job, err = st.TransitionIngestJob(ctx, userID, job.ID,
		store.IngestJobTransition{
			ExpectedState:    staged.Job.State,
			ExpectedRevision: staged.Job.Revision,
			NextState:        store.IngestValidated,
			UpdatedAt:        at.Add(2 * time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
	job, err = st.TransitionIngestJob(ctx, userID, job.ID,
		store.IngestJobTransition{
			ExpectedState: job.State, ExpectedRevision: job.Revision,
			NextState: store.IngestExtracted,
			UpdatedAt: at.Add(3 * time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
	if cas != nil {
		if _, err := cas.Promote(
			ctx, stagingPath, blob.SHA256, blob.SizeBytes); err != nil {
			t.Fatal(err)
		}
	}
	promotedAt := at.Add(4 * time.Minute)
	if _, err := st.CommitNewBookPromotion(ctx, userID, job.ID,
		store.CommitNewBookPromotionRequest{
			ExpectedRevision: job.Revision,
			Blob:             blob,
			Book: store.CatalogBook{
				ID: id + "-book", LibraryID: libraryID,
				Status: store.BookActive, Title: id,
				TitleSource: store.MetadataEmbedded,
				CreatedAt:   promotedAt, UpdatedAt: promotedAt,
			},
			File: store.BookFile{
				ID: id + "-file", LibraryID: libraryID,
				BookID: id + "-book", BlobSHA256: blob.SHA256,
				Source: store.IngestUpload, OriginalFilename: id + ".epub",
				MediaType:    "application/epub+zip",
				Availability: store.BookFileAvailable,
				CreatedAt:    promotedAt, UpdatedAt: promotedAt,
			},
			UpdatedAt: promotedAt,
		}); err != nil {
		t.Fatal(err)
	}
	return blob
}

func blobRecordsBySHA(t *testing.T, st store.Store) map[string]store.BlobRecord {
	t.Helper()
	records, err := st.ListBlobRecords(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	bySHA := make(map[string]store.BlobRecord, len(records))
	for _, record := range records {
		bySHA[record.SHA256] = record
	}
	return bySHA
}
