package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

func TestAdminUsageNoSubcommandPlainStderr(t *testing.T) {
	var stderr bytes.Buffer
	code := runMain([]string{"admin", "-config", "unused.toml"}, &stderr)
	if code == 0 {
		t.Fatal("runMain exited 0, want non-zero")
	}
	got := stderr.String()
	if !strings.Contains(got, "usage: liseur-sync admin [-config <file>] <subcommand>\n\n") {
		t.Fatalf("stderr did not contain plain multi-line admin usage:\n%s", got)
	}
	if strings.Contains(got, `\n`) {
		t.Fatalf("stderr contains escaped newline sequence: %q", got)
	}
	if strings.Contains(got, "ERROR") || strings.Contains(got, "err=") {
		t.Fatalf("stderr contains slog output: %q", got)
	}
}

func TestAdminUsageHelpFlagExitsZero(t *testing.T) {
	var stderr bytes.Buffer
	code := runMain([]string{"admin", "-h"}, &stderr)
	if code != 0 {
		t.Fatalf("runMain exited %d, want 0", code)
	}
	if !strings.Contains(stderr.String(),
		"usage: liseur-sync admin [-config <file>] <subcommand>\n\n") {
		t.Fatalf("stderr did not contain admin usage:\n%s", stderr.String())
	}
}

func TestDefaultConfigPathFallsBackWithoutEnv(t *testing.T) {
	t.Setenv("LISEUR_CONFIG", "")
	if got := defaultConfigPath(); got != "liseur-sync.toml" {
		t.Fatalf("defaultConfigPath() = %q, want %q", got, "liseur-sync.toml")
	}
}

func TestDefaultConfigPathUsesEnvOverride(t *testing.T) {
	t.Setenv("LISEUR_CONFIG", "/etc/liseur-sync/prod.toml")
	if got := defaultConfigPath(); got != "/etc/liseur-sync/prod.toml" {
		t.Fatalf("defaultConfigPath() = %q, want %q", got, "/etc/liseur-sync/prod.toml")
	}
}

func TestConfigFlagOverridesLISEUR_CONFIGEnvVar(t *testing.T) {
	t.Setenv("LISEUR_CONFIG", "/etc/liseur-sync/prod.toml")
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to TOML config file")
	if err := fs.Parse([]string{"-config", "explicit.toml"}); err != nil {
		t.Fatal(err)
	}
	if *cfgPath != "explicit.toml" {
		t.Fatalf("cfgPath = %q, want %q (explicit -config must win over LISEUR_CONFIG)", *cfgPath, "explicit.toml")
	}
}

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
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Library", CreatedAt: now,
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
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Reconciliation", CreatedAt: now,
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
	// Startup propagates the blob it could not find into the catalog, so
	// the server never comes up offering a download it cannot serve.
	if report.Availability.FilesMarkedMissing != 1 ||
		report.Availability.BooksMarkedMissing != 1 ||
		report.Availability.FilesMarkedAvailable != 0 ||
		report.Availability.BooksMarkedActive != 0 {
		t.Fatalf("startup availability: %+v", report.Availability)
	}
	assertBookStatus(t, st, user.ID, "missing-book", store.BookMissing)
	assertBookStatus(t, st, user.ID, "present-book", store.BookActive)
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
	// The bytes came back, so the book must become downloadable again.
	if report.Availability.FilesMarkedAvailable != 1 ||
		report.Availability.BooksMarkedActive != 1 ||
		report.Availability.FilesMarkedMissing != 0 ||
		report.Availability.BooksMarkedMissing != 0 {
		t.Fatalf("availability after restore: %+v", report.Availability)
	}
	assertBookStatus(t, st, user.ID, "missing-book", store.BookActive)
	assertBookStatus(t, st, user.ID, "present-book", store.BookActive)
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
			NextState:                     store.IngestExtracted,
			ExtractedEmbeddedMetadataJSON: []byte(`{}`),
			UpdatedAt:                     at.Add(3 * time.Minute),
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

func assertBookStatus(
	t *testing.T,
	st store.Store,
	userID, bookID string,
	want store.BookStatus,
) {
	t.Helper()
	book, err := st.CatalogBookByID(
		context.Background(), userID, bookID, store.LibraryRoleRead)
	if err != nil {
		t.Fatalf("%s: %v", bookID, err)
	}
	if book.Status != want {
		t.Fatalf("%s status: got %q want %q", bookID, book.Status, want)
	}
}

// TestTrashPurgeWorkerDeletesOnlyWhatItsWindowAllows runs the worker the
// way serve does. It is the only thing in the server that destroys a
// user's content, so what matters is not that it deletes but that it
// stops: a book still inside its retention window must survive the tick
// that takes the one beside it.
func TestTrashPurgeWorkerDeletesOnlyWhatItsWindowAllows(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "purge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.CreateUser(ctx, store.User{
		ID: "user", Name: "alice", Argon2Hash: "x", Timezone: "UTC",
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateLibrary(ctx, store.Library{
		ID: "library", OwnerUserID: "user", QuotaUserID: "user",
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Library", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"expired", "still-recoverable"} {
		if err := st.CreateCatalogBook(ctx, "user", store.CatalogBook{
			ID: id, LibraryID: "library", Status: store.BookActive,
			Title: id, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.TrashCatalogBook(
		ctx, "user", "expired", now.Add(-48*time.Hour), now.Add(-time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TrashCatalogBook(
		ctx, "user", "still-recoverable", now, now.Add(720*time.Hour),
	); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{}
	cfg.Content.TrashRetentionHours = 720
	cfg.Content.RecoveryBatchSize = 50
	// The worker purges once on entry and then sleeps for an hour, so a
	// context cancelled during that sleep exercises exactly one tick and
	// the shutdown path with it.
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := runTrashPurgeWorker(runCtx, st, cfg); err != nil {
		t.Fatalf("worker returned %v, want a clean shutdown", err)
	}

	left, err := st.ListTrashedBooks(ctx, "user", "library", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].ID != "still-recoverable" {
		t.Fatalf("trash after a purge tick = %+v", left)
	}
}
