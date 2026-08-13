package storetest

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/store"
)

func testBlobReconciliation(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	user := MkUser(t, s, "blob-reconciliation")
	now := time.Date(2026, time.April, 5, 6, 7, 8, 0, time.UTC)
	library := store.Library{
		ID: "blob-reconciliation", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Kind: store.LibraryManaged, Name: "Blobs", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	blob := ingestBlob("referenced-blob", 15)
	job := createIngestJob(t, s, user.ID, library.ID, "blob-job", now)
	staged, err := s.CommitIngestStage(ctx, user.ID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: job.Revision, Artifact: blob,
			StagingPath: contentpath.StagingPath(job.ID),
			UpdatedAt:   now.Add(time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
	job = extractIngestJob(t, s, staged.Job, now.Add(2*time.Minute))
	if _, err := s.CommitNewBookPromotion(ctx, user.ID, job.ID,
		promotionRequest(
			job, blob, "blob-book", "blob-file", now.Add(4*time.Minute))); err != nil {
		t.Fatal(err)
	}

	records, err := s.ListBlobRecords(ctx, "", 10)
	if err != nil || len(records) != 1 || records[0].SHA256 != blob.SHA256 ||
		records[0].OrphanedAt != nil || records[0].MissingAt != nil {
		t.Fatalf("initial blob records: %+v %v", records, err)
	}
	missing, err := s.ReconcileBlob(ctx, blob, false, now.Add(5*time.Minute))
	if err != nil || !missing.MissingMarked || missing.Record.MissingAt == nil ||
		missing.Record.OrphanedAt != nil {
		t.Fatalf("mark missing blob: %+v %v", missing, err)
	}
	restoreJob := createIngestJob(
		t, s, user.ID, library.ID, "blob-restore-job", now.Add(6*time.Minute))
	restaged, err := s.CommitIngestStage(ctx, user.ID, restoreJob.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: restoreJob.Revision, Artifact: blob,
			StagingPath: contentpath.StagingPath(restoreJob.ID),
			UpdatedAt:   now.Add(7 * time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
	restoreJob = extractIngestJob(t, s, restaged.Job, now.Add(8*time.Minute))
	if _, err := s.CommitNewBookPromotion(ctx, user.ID, restoreJob.ID,
		promotionRequest(
			restoreJob, blob, "restored-book", "restored-file",
			now.Add(9*time.Minute))); err != nil {
		t.Fatal(err)
	}
	records, err = s.ListBlobRecords(ctx, "", 10)
	if err != nil || len(records) != 1 || records[0].MissingAt != nil ||
		records[0].OrphanedAt != nil {
		t.Fatalf("promotion must clear blob marks: %+v %v", records, err)
	}

	orphan := ingestBlob("orphan-blob", 9)
	orphanedAt := now.Add(10*time.Minute + 120*time.Millisecond)
	inserted, err := s.ReconcileBlob(ctx, orphan, true, orphanedAt)
	if err != nil || !inserted.Inserted || !inserted.OrphanMarked ||
		inserted.Record.OrphanedAt == nil || inserted.Record.MissingAt != nil {
		t.Fatalf("insert orphan blob: %+v %v", inserted, err)
	}
	replayed, err := s.ReconcileBlob(ctx, orphan, true, now.Add(11*time.Minute))
	if err != nil || replayed.Inserted || replayed.OrphanMarked ||
		replayed.Record.OrphanedAt == nil {
		t.Fatalf("reconcile existing orphan: %+v %v", replayed, err)
	}
	orphanMissing, err := s.ReconcileBlob(
		ctx, orphan, false, now.Add(12*time.Minute))
	if err != nil || !orphanMissing.MissingMarked ||
		orphanMissing.Record.OrphanedAt == nil ||
		orphanMissing.Record.MissingAt == nil {
		t.Fatalf("mark orphan missing: %+v %v", orphanMissing, err)
	}

	want := []string{blob.SHA256, orphan.SHA256}
	sort.Strings(want)
	first, err := s.ListBlobRecords(ctx, "", 1)
	if err != nil || len(first) != 1 || first[0].SHA256 != want[0] {
		t.Fatalf("first blob page: %+v %v", first, err)
	}
	second, err := s.ListBlobRecords(ctx, first[0].SHA256, 10)
	if err != nil || len(second) != 1 || second[0].SHA256 != want[1] {
		t.Fatalf("second blob page: %+v %v", second, err)
	}
	wrongSize := blob
	wrongSize.SizeBytes++
	if _, err := s.ReconcileBlob(
		ctx, wrongSize, true, now.Add(13*time.Minute)); err != store.ErrInvariantViolation {
		t.Fatalf("blob size mismatch: %v", err)
	}
	if _, err := s.ReconcileBlob(ctx,
		ingestBlob("unknown-blob", 1), false, now.Add(13*time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown missing blob: %v", err)
	}
	held := ingestBlob("held-blob", 7)
	heldJob := createIngestJob(
		t, s, user.ID, library.ID, "held-blob-job", now.Add(13*time.Minute))
	if _, err := s.CommitIngestStage(ctx, user.ID, heldJob.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: heldJob.Revision, Artifact: held,
			StagingPath: contentpath.StagingPath(heldJob.ID),
			UpdatedAt:   now.Add(14 * time.Minute),
		}); err != nil {
		t.Fatal(err)
	}
	if result, err := s.ReconcileBlob(
		ctx, held, true, now.Add(15*time.Minute)); err != nil ||
		!result.Inserted || !result.OrphanMarked {
		t.Fatalf("held orphan reconciliation: %+v %v", result, err)
	}
	if purged, err := s.PurgeOrphanedBlobRecords(
		ctx, now.Add(10*time.Minute+100*time.Millisecond), 10); err != nil || len(purged) != 0 {
		t.Fatalf("premature orphan purge: %+v %v", purged, err)
	}
	purged, err := s.PurgeOrphanedBlobRecords(
		ctx, now.Add(20*time.Minute), 10)
	if err != nil || len(purged) != 1 ||
		purged[0].SHA256 != orphan.SHA256 ||
		purged[0].OrphanedAt == nil {
		t.Fatalf("orphan purge: %+v %v", purged, err)
	}
	remaining, err := s.ListBlobRecords(ctx, "", 10)
	if err != nil || len(remaining) != 2 {
		t.Fatalf("protected blobs survived purge: %+v %v", remaining, err)
	}
	remainingBySHA := make(map[string]store.BlobRecord, len(remaining))
	for _, record := range remaining {
		remainingBySHA[record.SHA256] = record
	}
	if _, ok := remainingBySHA[blob.SHA256]; !ok {
		t.Fatalf("referenced blob was purged: %+v", remaining)
	}
	if _, ok := remainingBySHA[held.SHA256]; !ok {
		t.Fatalf("held blob was purged: %+v", remaining)
	}
	if purged, err := s.PurgeOrphanedBlobRecords(
		ctx, now.Add(20*time.Minute), 10); err != nil || len(purged) != 0 {
		t.Fatalf("orphan purge replay: %+v %v", purged, err)
	}
	if _, err := s.PurgeOrphanedBlobRecords(
		ctx, time.Time{}, 10); err != store.ErrInvalidTransition {
		t.Fatalf("zero orphan cutoff: %v", err)
	}
	if _, err := s.PurgeOrphanedBlobRecords(
		ctx, now, 0); err != store.ErrInvalidTransition {
		t.Fatalf("invalid orphan purge limit: %v", err)
	}
}
