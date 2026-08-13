package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/store"
)

func ingestBlob(label string, size int64) store.BlobInfo {
	sum := sha256.Sum256([]byte(label))
	return store.BlobInfo{SHA256: hex.EncodeToString(sum[:]), SizeBytes: size}
}

func createIngestJob(
	t *testing.T,
	s store.Store,
	userID, libraryID, jobID string,
	at time.Time,
) store.IngestJob {
	t.Helper()
	job, created, err := s.CreateIngestJob(context.Background(), userID,
		store.IngestJobRequest{
			ID: jobID, LibraryID: libraryID, Source: store.IngestUpload,
			RequestFingerprint: "request-" + jobID, CreatedAt: at,
		})
	if err != nil || !created {
		t.Fatalf("create job %s: %+v %v %v", jobID, job, created, err)
	}
	return job
}

func extractIngestJob(
	t *testing.T,
	s store.Store,
	job store.IngestJob,
	at time.Time,
) store.IngestJob {
	t.Helper()
	ctx := context.Background()
	var err error
	job, err = s.TransitionIngestJob(ctx, job.UserID, job.ID,
		store.IngestJobTransition{
			ExpectedState: job.State, ExpectedRevision: job.Revision,
			NextState: store.IngestValidated, UpdatedAt: at,
		})
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.TransitionIngestJob(ctx, job.UserID, job.ID,
		store.IngestJobTransition{
			ExpectedState: job.State, ExpectedRevision: job.Revision,
			NextState:                     store.IngestExtracted,
			ExtractedEmbeddedMetadataJSON: []byte(`{"title":"Extracted"}`),
			UpdatedAt:                     at.Add(time.Second),
		})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func promotionRequest(
	job store.IngestJob,
	blob store.BlobInfo,
	bookID, fileID string,
	at time.Time,
) store.CommitNewBookPromotionRequest {
	return store.CommitNewBookPromotionRequest{
		ExpectedRevision: job.Revision,
		Blob:             blob,
		Book: store.CatalogBook{
			ID: bookID, LibraryID: job.LibraryID, Status: store.BookActive,
			Title: "Promoted " + bookID, TitleSource: store.MetadataEmbedded,
			CreatedAt: at, UpdatedAt: at,
		},
		File: store.BookFile{
			ID: fileID, LibraryID: job.LibraryID, BookID: bookID,
			BlobSHA256: blob.SHA256, Source: job.Source,
			OriginalFilename: bookID + ".epub",
			MediaType:        "application/epub+zip", Availability: store.BookFileAvailable,
			CreatedAt: at, UpdatedAt: at,
		},
		UpdatedAt: at,
	}
}

func testIngestJobs(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	owner := MkUser(t, s, "ingest-owner")
	reader := MkUser(t, s, "ingest-reader")
	manager := MkUser(t, s, "ingest-manager")
	outsider := MkUser(t, s, "ingest-outsider")
	now := time.Date(2026, time.January, 2, 3, 4, 5, 100_000_000, time.UTC)
	managed := store.Library{
		ID: "ingest-managed", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Managed", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, managed); err != nil {
		t.Fatal(err)
	}
	root := "/srv/ingest"
	watched := store.Library{
		ID: "ingest-watched", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryWatched, Name: "Watched", RootPath: &root, CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, watched); err != nil {
		t.Fatal(err)
	}
	for _, libraryID := range []string{managed.ID, watched.ID} {
		if err := s.GrantLibraryAccess(ctx, owner.ID, libraryID,
			manager.ID, store.LibraryRoleManage, now); err != nil {
			t.Fatal(err)
		}
		if err := s.GrantLibraryAccess(ctx, owner.ID, libraryID,
			reader.ID, store.LibraryRoleRead, now); err != nil {
			t.Fatal(err)
		}
	}

	clientKey := "upload-1"
	request := store.IngestJobRequest{
		ID: "job-1", LibraryID: managed.ID, Source: store.IngestUpload,
		ClientKey: &clientKey, RequestFingerprint: "request-v1", CreatedAt: now,
	}
	job, created, err := s.CreateIngestJob(ctx, manager.ID, request)
	if err != nil || !created {
		t.Fatalf("create ingest job: %+v %v %v", job, created, err)
	}
	if job.UserID != manager.ID || job.QuotaUserID != owner.ID ||
		job.State != store.IngestReceived || job.Revision != 1 ||
		job.BytesReceived != 0 ||
		job.ExtractedEmbeddedMetadataJSON != nil {
		t.Fatalf("created ingest job: %+v", job)
	}
	if _, _, err := s.CreateIngestJob(ctx, reader.ID, store.IngestJobRequest{
		ID: "reader-job", LibraryID: managed.ID, Source: store.IngestUpload,
		RequestFingerprint: "reader-request", CreatedAt: now,
	}); err != store.ErrNotFound {
		t.Fatalf("reader created ingest job: %v", err)
	}
	if _, _, err := s.CreateIngestJob(ctx, outsider.ID, store.IngestJobRequest{
		ID: "outsider-job", LibraryID: managed.ID, Source: store.IngestUpload,
		RequestFingerprint: "outsider-request", CreatedAt: now,
	}); err != store.ErrNotFound {
		t.Fatalf("outsider created ingest job: %v", err)
	}
	if _, _, err := s.CreateIngestJob(ctx, manager.ID, store.IngestJobRequest{
		ID: "wrong-kind", LibraryID: watched.ID, Source: store.IngestUpload,
		RequestFingerprint: "wrong-kind", CreatedAt: now,
	}); err != store.ErrNotFound {
		t.Fatalf("upload job accepted by watched library: %v", err)
	}
	if _, _, err := s.CreateIngestJob(ctx, manager.ID, store.IngestJobRequest{
		ID: "missing-path", LibraryID: watched.ID, Source: store.IngestWatched,
		RequestFingerprint: "missing-path", CreatedAt: now,
	}); err == nil {
		t.Fatal("watched job without a relative path was accepted")
	}

	replay := request
	replay.ID = "job-replay"
	replayed, created, err := s.CreateIngestJob(ctx, manager.ID, replay)
	if err != nil || created || replayed.ID != job.ID {
		t.Fatalf("idempotent replay: %+v %v %v", replayed, created, err)
	}
	mismatchedID := request
	mismatchedID.RequestFingerprint = "different-id-payload"
	if _, _, err := s.CreateIngestJob(ctx, manager.ID, mismatchedID); err != store.ErrIDMismatch {
		t.Fatalf("job id mismatch: %v", err)
	}
	mismatchedKey := replay
	mismatchedKey.ID = "job-key-conflict"
	mismatchedKey.RequestFingerprint = "different-key-payload"
	if _, _, err := s.CreateIngestJob(ctx, manager.ID, mismatchedKey); err != store.ErrIdempotencyConflict {
		t.Fatalf("client key mismatch: %v", err)
	}

	if got, err := s.IngestJobByID(ctx, owner.ID, job.ID); err != nil || got.ID != job.ID {
		t.Fatalf("owner ingest read: %+v %v", got, err)
	}
	if got, err := s.IngestJobByID(ctx, manager.ID, job.ID); err != nil || got.ID != job.ID {
		t.Fatalf("manager ingest read: %+v %v", got, err)
	}
	if _, err := s.IngestJobByID(ctx, reader.ID, job.ID); err != store.ErrNotFound {
		t.Fatalf("reader saw ingest job: %v", err)
	}
	if _, err := s.IngestJobByID(ctx, outsider.ID, job.ID); err != store.ErrNotFound {
		t.Fatalf("outsider saw ingest job: %v", err)
	}

	for i := 2; i <= 3; i++ {
		if _, created, err := s.CreateIngestJob(ctx, manager.ID, store.IngestJobRequest{
			ID: fmt.Sprintf("job-%d", i), LibraryID: managed.ID,
			Source: store.IngestUpload, RequestFingerprint: fmt.Sprintf("request-%d", i),
			CreatedAt: now.Add(time.Duration(i-1) * 10 * time.Millisecond),
		}); err != nil || !created {
			t.Fatalf("create pagination job %d: %v %v", i, created, err)
		}
	}
	firstPage, err := s.ListIngestJobs(ctx, manager.ID, managed.ID, nil, 2)
	if err != nil || len(firstPage) != 2 ||
		firstPage[0].ID != "job-1" || firstPage[1].ID != "job-2" {
		t.Fatalf("first ingest page: %+v %v", firstPage, err)
	}
	secondPage, err := s.ListIngestJobs(ctx, manager.ID, managed.ID,
		&store.IngestJobCursor{
			CreatedAt: firstPage[1].CreatedAt,
			ID:        firstPage[1].ID,
		}, 2)
	if err != nil || len(secondPage) != 1 || secondPage[0].ID != "job-3" {
		t.Fatalf("second ingest page: %+v %v", secondPage, err)
	}
	if _, err := s.ListIngestJobs(ctx, reader.ID, managed.ID, nil, 10); err != store.ErrNotFound {
		t.Fatalf("reader listed ingest jobs: %v", err)
	}

	bytesReceived := int64(123)
	stagingPath := contentpath.StagingPath("job-3")
	artifact := ingestBlob("content", bytesReceived)
	stagedJob3, err := s.CommitIngestStage(ctx, manager.ID, "job-3",
		store.CommitIngestStageRequest{
			ExpectedRevision: 1, Artifact: artifact, StagingPath: stagingPath,
			UpdatedAt: now.Add(3 * time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
	job3 := stagedJob3.Job
	start := make(chan struct{})
	transitionErrors := make(chan error, 2)
	var transitionWG sync.WaitGroup
	for range 2 {
		transitionWG.Add(1)
		go func() {
			defer transitionWG.Done()
			<-start
			_, err := s.TransitionIngestJob(ctx, manager.ID, job3.ID,
				store.IngestJobTransition{
					ExpectedState:    store.IngestStaged,
					ExpectedRevision: job3.Revision,
					NextState:        store.IngestValidated,
					UpdatedAt:        now.Add(4 * time.Minute),
				})
			transitionErrors <- err
		}()
	}
	close(start)
	transitionWG.Wait()
	close(transitionErrors)
	var succeeded, stale int
	for err := range transitionErrors {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, store.ErrStaleRevision):
			stale++
		default:
			t.Fatalf("competing transition: %v", err)
		}
	}
	if succeeded != 1 || stale != 1 {
		t.Fatalf("competing transitions: %d succeeded, %d stale", succeeded, stale)
	}

	prestage, created, err := s.CreateIngestJob(ctx, manager.ID, store.IngestJobRequest{
		ID: "job-prestage-failure", LibraryID: managed.ID,
		Source: store.IngestUpload, RequestFingerprint: "prestage-failure",
		CreatedAt: now.Add(30 * time.Millisecond),
	})
	if err != nil || !created {
		t.Fatalf("create pre-staging failure job: %+v %v %v", prestage, created, err)
	}
	expiry := now.Add(24 * time.Hour)
	prestage, err = s.TransitionIngestJob(ctx, manager.ID, prestage.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestReceived, ExpectedRevision: 1,
			NextState: store.IngestFailed, ErrorCode: "upload_interrupted",
			ExpiresAt: &expiry, UpdatedAt: now.Add(time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
	prestage, err = s.TransitionIngestJob(ctx, manager.ID, prestage.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestFailed, ExpectedRevision: prestage.Revision,
			NextState: store.IngestReceived, IncrementRetry: true,
			UpdatedAt: now.Add(2 * time.Minute),
		})
	if err != nil || prestage.State != store.IngestReceived ||
		prestage.RetryCount != 1 || prestage.ContentSHA256 != nil ||
		prestage.StagingPath != nil || prestage.ErrorCode != nil {
		t.Fatalf("pre-staging retry: %+v %v", prestage, err)
	}

	if _, err := s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestReceived, ExpectedRevision: 1,
			NextState: store.IngestValidated, UpdatedAt: now.Add(time.Minute),
		}); err != store.ErrInvalidTransition {
		t.Fatalf("invalid transition accepted: %v", err)
	}
	if _, err := s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestReceived, ExpectedRevision: 1,
			NextState: store.IngestStaged, UpdatedAt: now.Add(time.Minute),
		}); err != store.ErrInvalidTransition {
		t.Fatalf("generic staging accepted: %v", err)
	}
	stagingPath = contentpath.StagingPath(job.ID)
	if _, err := s.CommitIngestStage(ctx, manager.ID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: 1, Artifact: artifact,
			StagingPath: contentpath.StagingPath("different-job"),
			UpdatedAt:   now.Add(time.Minute),
		}); err != store.ErrInvalidTransition {
		t.Fatalf("mismatched staging path: %v", err)
	}
	if _, err := s.CommitIngestStage(ctx, manager.ID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: 1, Artifact: artifact, StagingPath: stagingPath,
			UpdatedAt: now.Add(-time.Minute),
		}); err != store.ErrInvalidTransition {
		t.Fatalf("backward staging timestamp: %v", err)
	}
	stagedJob, err := s.CommitIngestStage(ctx, manager.ID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: 1, Artifact: artifact, StagingPath: stagingPath,
			UpdatedAt: now.Add(time.Minute),
		})
	job = stagedJob.Job
	if err != nil || job.State != store.IngestStaged || job.Revision != 2 ||
		job.ContentSHA256 == nil || *job.ContentSHA256 != artifact.SHA256 {
		t.Fatalf("staged transition: %+v %v", job, err)
	}

	func() {
		ctx := context.Background()
		now := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)

		boundaryUser := MkUser(t, s, "quota-boundary")
		boundaryLibrary := store.Library{
			ID: "quota-boundary", OwnerUserID: boundaryUser.ID,
			QuotaUserID: boundaryUser.ID, Kind: store.LibraryManaged,
			Name: "Boundary", CreatedAt: now,
		}
		if err := s.CreateLibrary(ctx, boundaryLibrary); err != nil {
			t.Fatal(err)
		}
		first := createIngestJob(t, s, boundaryUser.ID, boundaryLibrary.ID, "boundary-1", now)
		second := createIngestJob(t, s, boundaryUser.ID, boundaryLibrary.ID, "boundary-2", now)
		limit := int64(10)
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for index, job := range []store.IngestJob{first, second} {
			wg.Add(1)
			go func(index int, job store.IngestJob) {
				defer wg.Done()
				<-start
				_, err := s.CommitIngestStage(ctx, job.UserID, job.ID,
					store.CommitIngestStageRequest{
						ExpectedRevision: job.Revision,
						Artifact:         ingestBlob(fmt.Sprintf("boundary-%d", index), 6),
						StagingPath:      contentpath.StagingPath(job.ID),
						QuotaLimitBytes:  &limit, UpdatedAt: now.Add(time.Minute),
					})
				results <- err
			}(index, job)
		}
		close(start)
		wg.Wait()
		close(results)
		var staged, rejected int
		for err := range results {
			switch {
			case err == nil:
				staged++
			case errors.Is(err, store.ErrQuotaExceeded):
				rejected++
			default:
				t.Fatalf("boundary staging: %v", err)
			}
		}
		if staged != 1 || rejected != 1 {
			t.Fatalf("quota boundary: %d staged, %d rejected", staged, rejected)
		}

		dedupUser := MkUser(t, s, "quota-dedup")
		dedupLibrary := store.Library{
			ID: "quota-dedup", OwnerUserID: dedupUser.ID,
			QuotaUserID: dedupUser.ID, Kind: store.LibraryManaged,
			Name: "Dedup", CreatedAt: now,
		}
		if err := s.CreateLibrary(ctx, dedupLibrary); err != nil {
			t.Fatal(err)
		}
		blob := ingestBlob("deduplicated", 6)
		var stagedJobs []store.IngestJob
		for index := 1; index <= 2; index++ {
			job := createIngestJob(t, s, dedupUser.ID, dedupLibrary.ID,
				fmt.Sprintf("dedup-%d", index), now)
			result, err := s.CommitIngestStage(ctx, job.UserID, job.ID,
				store.CommitIngestStageRequest{
					ExpectedRevision: job.Revision, Artifact: blob,
					StagingPath:     contentpath.StagingPath(job.ID),
					QuotaLimitBytes: &blob.SizeBytes, UpdatedAt: now.Add(time.Minute),
				})
			if err != nil {
				t.Fatal(err)
			}
			if index == 1 && result.Quota.AdditionalBytes != blob.SizeBytes {
				t.Fatalf("first dedup charge: %+v", result.Quota)
			}
			if index == 2 && result.Quota.AdditionalBytes != 0 {
				t.Fatalf("second dedup charge: %+v", result.Quota)
			}
			stagedJobs = append(stagedJobs, result.Job)
		}
		expiry := now.Add(24 * time.Hour)
		failed, err := s.TransitionIngestJob(ctx, dedupUser.ID, stagedJobs[1].ID,
			store.IngestJobTransition{
				ExpectedState:    stagedJobs[1].State,
				ExpectedRevision: stagedJobs[1].Revision,
				NextState:        store.IngestFailed, ErrorCode: "validation_failed",
				ExpiresAt: &expiry, UpdatedAt: now.Add(2 * time.Minute),
			})
		if err != nil || failed.State != store.IngestFailed {
			t.Fatalf("held failure: %+v %v", failed, err)
		}
		third := createIngestJob(t, s, dedupUser.ID, dedupLibrary.ID, "dedup-3", now)
		thirdStage, err := s.CommitIngestStage(ctx, third.UserID, third.ID,
			store.CommitIngestStageRequest{
				ExpectedRevision: third.Revision, Artifact: blob,
				StagingPath:     contentpath.StagingPath(third.ID),
				QuotaLimitBytes: &blob.SizeBytes, UpdatedAt: now.Add(2 * time.Minute),
			})
		if err != nil || thirdStage.Quota.AdditionalBytes != 0 {
			t.Fatalf("failed hold not counted as dedup: %+v %v", thirdStage, err)
		}

		cleanupUser := MkUser(t, s, "quota-cleanup")
		cleanupLibrary := store.Library{
			ID: "quota-cleanup", OwnerUserID: cleanupUser.ID,
			QuotaUserID: cleanupUser.ID, Kind: store.LibraryManaged,
			Name: "Cleanup", CreatedAt: now,
		}
		if err := s.CreateLibrary(ctx, cleanupLibrary); err != nil {
			t.Fatal(err)
		}
		cleanupBlob := ingestBlob("cleanup-held", 6)
		cleanupJob := createIngestJob(t, s, cleanupUser.ID,
			cleanupLibrary.ID, "cleanup-held", now)
		cleanupStage, err := s.CommitIngestStage(ctx, cleanupJob.UserID,
			cleanupJob.ID, store.CommitIngestStageRequest{
				ExpectedRevision: cleanupJob.Revision, Artifact: cleanupBlob,
				StagingPath:     contentpath.StagingPath(cleanupJob.ID),
				QuotaLimitBytes: &cleanupBlob.SizeBytes,
				UpdatedAt:       now.Add(time.Minute),
			})
		if err != nil {
			t.Fatal(err)
		}
		cleanupExpiry := now.Add(time.Hour)
		if _, err := s.TransitionIngestJob(ctx, cleanupJob.UserID, cleanupJob.ID,
			store.IngestJobTransition{
				ExpectedState:    cleanupStage.Job.State,
				ExpectedRevision: cleanupStage.Job.Revision,
				NextState:        store.IngestFailed, ErrorCode: "retained_failure",
				ExpiresAt: &cleanupExpiry, UpdatedAt: now.Add(2 * time.Minute),
			}); err != nil {
			t.Fatal(err)
		}
		blockedJob := createIngestJob(t, s, cleanupUser.ID,
			cleanupLibrary.ID, "cleanup-blocked", now)
		blockedRequest := store.CommitIngestStageRequest{
			ExpectedRevision: blockedJob.Revision,
			Artifact:         ingestBlob("cleanup-blocked", 1),
			StagingPath:      contentpath.StagingPath(blockedJob.ID),
			QuotaLimitBytes:  &cleanupBlob.SizeBytes,
			UpdatedAt:        now.Add(3 * time.Minute),
		}
		if _, err := s.CommitIngestStage(ctx, blockedJob.UserID,
			blockedJob.ID, blockedRequest); !errors.Is(err, store.ErrQuotaExceeded) {
			t.Fatalf("retained failure did not consume quota: %v", err)
		}
		if purged, err := s.PurgeExpiredIngestArtifacts(ctx, now.Add(30*time.Minute), 10); err != nil || len(purged) != 0 {
			t.Fatalf("premature ingest purge: %+v %v", purged, err)
		}
		purged, err := s.PurgeExpiredIngestArtifacts(ctx, now.Add(2*time.Hour), 10)
		if err != nil || len(purged) != 1 ||
			purged[0].ID != cleanupJob.ID ||
			purged[0].StagingPath == nil ||
			*purged[0].StagingPath != contentpath.StagingPath(cleanupJob.ID) {
			t.Fatalf("expired ingest purge: %+v %v", purged, err)
		}
		blockedStage, err := s.CommitIngestStage(ctx, blockedJob.UserID,
			blockedJob.ID, blockedRequest)
		if err != nil || blockedStage.Job.State != store.IngestStaged {
			t.Fatalf("purge did not release quota: %+v %v", blockedStage, err)
		}
		pending, err := s.IngestJobByID(ctx, cleanupJob.UserID, cleanupJob.ID)
		if err != nil || pending.State != store.IngestFailed ||
			!pending.ArtifactsExpired || !pending.ArtifactCleanupPending ||
			pending.StagingPath == nil || pending.ContentSHA256 == nil ||
			pending.BytesReceived != cleanupBlob.SizeBytes {
			t.Fatalf("pending ingest cleanup: %+v %v", pending, err)
		}
		if retry, err := s.PurgeExpiredIngestArtifacts(
			ctx, now.Add(2*time.Hour), 10); err != nil || len(retry) != 1 ||
			retry[0].ID != cleanupJob.ID {
			t.Fatalf("pending cleanup was not retryable: %+v %v", retry, err)
		}
		if err := s.CompleteIngestArtifactCleanup(
			ctx, cleanupJob.ID, contentpath.StagingPath("wrong-job")); err != store.ErrStaleRevision {
			t.Fatalf("cleanup accepted wrong path: %v", err)
		}
		if err := s.CompleteIngestArtifactCleanup(
			ctx, cleanupJob.ID, contentpath.StagingPath(cleanupJob.ID)); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteIngestArtifactCleanup(
			ctx, cleanupJob.ID, contentpath.StagingPath(cleanupJob.ID)); err != nil {
			t.Fatalf("cleanup acknowledgement was not idempotent: %v", err)
		}
		tombstone, err := s.IngestJobByID(ctx, cleanupJob.UserID, cleanupJob.ID)
		if err != nil || tombstone.State != store.IngestFailed ||
			!tombstone.ArtifactsExpired || tombstone.ArtifactCleanupPending ||
			tombstone.StagingPath != nil || tombstone.ContentSHA256 != nil ||
			tombstone.BytesReceived != 0 {
			t.Fatalf("expired ingest tombstone: %+v %v", tombstone, err)
		}
		replayedTombstone, created, err := s.CreateIngestJob(ctx,
			cleanupJob.UserID, store.IngestJobRequest{
				ID: cleanupJob.ID, LibraryID: cleanupLibrary.ID,
				Source:             store.IngestUpload,
				RequestFingerprint: "request-" + cleanupJob.ID,
				CreatedAt:          now,
			})
		if err != nil || created || replayedTombstone.State != store.IngestFailed {
			t.Fatalf("expired job ID was reusable: %+v %v %v",
				replayedTombstone, created, err)
		}
		if _, err := s.TransitionIngestJob(ctx, tombstone.UserID, tombstone.ID,
			store.IngestJobTransition{
				ExpectedState: tombstone.State, ExpectedRevision: tombstone.Revision,
				NextState: store.IngestReceived, IncrementRetry: true,
				UpdatedAt: now.Add(3 * time.Hour),
			}); err != store.ErrInvalidTransition {
			t.Fatalf("expired tombstone was retryable: %v", err)
		}
		if purged, err := s.PurgeExpiredIngestArtifacts(
			ctx, now.Add(2*time.Hour), 10); err != nil || len(purged) != 0 {
			t.Fatalf("completed artifact cleanup repeated: %+v %v", purged, err)
		}

		otherUser := MkUser(t, s, "quota-other")
		otherLibrary := store.Library{
			ID: "quota-other", OwnerUserID: otherUser.ID,
			QuotaUserID: otherUser.ID, Kind: store.LibraryManaged,
			Name: "Other", CreatedAt: now,
		}
		if err := s.CreateLibrary(ctx, otherLibrary); err != nil {
			t.Fatal(err)
		}
		otherJob := createIngestJob(t, s, otherUser.ID, otherLibrary.ID, "other-1", now)
		otherStage, err := s.CommitIngestStage(ctx, otherJob.UserID, otherJob.ID,
			store.CommitIngestStageRequest{
				ExpectedRevision: otherJob.Revision, Artifact: blob,
				StagingPath:     contentpath.StagingPath(otherJob.ID),
				QuotaLimitBytes: &blob.SizeBytes, UpdatedAt: now.Add(time.Minute),
			})
		if err != nil || otherStage.Quota.AdditionalBytes != blob.SizeBytes {
			t.Fatalf("other principal charge: %+v %v", otherStage, err)
		}

		promoteJobs := []store.IngestJob{
			extractIngestJob(t, s, stagedJobs[0], now.Add(3*time.Minute)),
			extractIngestJob(t, s, thirdStage.Job, now.Add(3*time.Minute)),
		}
		promotionResults := make(chan store.IngestPromotionResult, 2)
		promotionErrors := make(chan error, 2)
		start = make(chan struct{})
		wg = sync.WaitGroup{}
		requests := make([]store.CommitNewBookPromotionRequest, 2)
		for index, job := range promoteJobs {
			requests[index] = promotionRequest(job, blob,
				fmt.Sprintf("promoted-book-%d", index),
				fmt.Sprintf("promoted-file-%d", index),
				now.Add(5*time.Minute))
			wg.Add(1)
			go func(job store.IngestJob, request store.CommitNewBookPromotionRequest) {
				defer wg.Done()
				<-start
				result, err := s.CommitNewBookPromotion(ctx, job.UserID, job.ID, request)
				if err != nil {
					promotionErrors <- err
					return
				}
				promotionResults <- result
			}(job, requests[index])
		}
		close(start)
		wg.Wait()
		close(promotionResults)
		close(promotionErrors)
		for err := range promotionErrors {
			t.Fatalf("concurrent promotion: %v", err)
		}
		promoted := 0
		for result := range promotionResults {
			if result.Job.State != store.IngestPromoted || result.Replayed ||
				string(result.Job.ExtractedEmbeddedMetadataJSON) !=
					`{"title":"Extracted"}` {
				t.Fatalf("promotion result: %+v", result)
			}
			promoted++
		}
		if promoted != 2 {
			t.Fatalf("want two promoted jobs, got %d", promoted)
		}
		replayed, err := s.CommitNewBookPromotion(ctx, promoteJobs[0].UserID,
			promoteJobs[0].ID, requests[0])
		if err != nil || !replayed.Replayed {
			t.Fatalf("promotion replay: %+v %v", replayed, err)
		}
		conflictingReplay := requests[0]
		conflictingReplay.File.ID = "different-file"
		if _, err := s.CommitNewBookPromotion(ctx, promoteJobs[0].UserID,
			promoteJobs[0].ID, conflictingReplay); err != store.ErrPromotionConflict {
			t.Fatalf("promotion replay conflict: %v", err)
		}
		conflictingReplay = requests[0]
		conflictingReplay.Book.Title = "Different title"
		if _, err := s.CommitNewBookPromotion(ctx, promoteJobs[0].UserID,
			promoteJobs[0].ID, conflictingReplay); err != store.ErrPromotionConflict {
			t.Fatalf("promotion metadata replay conflict: %v", err)
		}

		mismatchJob := createIngestJob(t, s, dedupUser.ID, dedupLibrary.ID,
			"dedup-size-mismatch", now)
		mismatchBlob := blob
		mismatchBlob.SizeBytes++
		if _, err := s.CommitIngestStage(ctx, mismatchJob.UserID, mismatchJob.ID,
			store.CommitIngestStageRequest{
				ExpectedRevision: mismatchJob.Revision, Artifact: mismatchBlob,
				StagingPath: contentpath.StagingPath(mismatchJob.ID),
				UpdatedAt:   now.Add(6 * time.Minute),
			}); err != store.ErrInvariantViolation {
			t.Fatalf("blob size mismatch: %v", err)
		}

		existingBook := store.CatalogBook{
			ID: "promotion-collision", LibraryID: dedupLibrary.ID,
			Status: store.BookActive, Title: "Existing",
			TitleSource: store.MetadataManual, CreatedAt: now,
		}
		if err := s.CreateCatalogBook(ctx, dedupUser.ID, existingBook); err != nil {
			t.Fatal(err)
		}
		collisionJob := createIngestJob(t, s, dedupUser.ID, dedupLibrary.ID,
			"promotion-collision-job", now)
		collisionBlob := ingestBlob("collision", 7)
		collisionStage, err := s.CommitIngestStage(ctx, collisionJob.UserID,
			collisionJob.ID, store.CommitIngestStageRequest{
				ExpectedRevision: collisionJob.Revision, Artifact: collisionBlob,
				StagingPath: contentpath.StagingPath(collisionJob.ID),
				UpdatedAt:   now.Add(7 * time.Minute),
			})
		if err != nil {
			t.Fatal(err)
		}
		collisionJob = extractIngestJob(t, s, collisionStage.Job, now.Add(8*time.Minute))
		collisionRequest := promotionRequest(collisionJob, collisionBlob,
			existingBook.ID, "collision-file", now.Add(10*time.Minute))
		if _, err := s.CommitNewBookPromotion(ctx, collisionJob.UserID,
			collisionJob.ID, collisionRequest); err != store.ErrConflict {
			t.Fatalf("promotion collision: %v", err)
		}
		collisionRequest.Book.ID = "promotion-retry"
		collisionRequest.File.BookID = collisionRequest.Book.ID
		collisionRequest.File.ID = "promotion-retry-file"
		result, err := s.CommitNewBookPromotion(ctx, collisionJob.UserID,
			collisionJob.ID, collisionRequest)
		if err != nil || result.Job.State != store.IngestPromoted {
			t.Fatalf("promotion rollback lost hold/job: %+v %v", result, err)
		}
	}()
	if _, err := s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestReceived, ExpectedRevision: 1,
			NextState: store.IngestStaged, UpdatedAt: now.Add(2 * time.Minute),
		}); err != store.ErrStaleRevision {
		t.Fatalf("stale transition: %v", err)
	}
	if _, err := s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestStaged, ExpectedRevision: 2,
			ExtractedEmbeddedMetadataJSON: []byte(`{"title":"too early"}`),
			NextState:                     store.IngestValidated,
			UpdatedAt:                     now.Add(2 * time.Minute),
		}); err != store.ErrInvalidTransition {
		t.Fatalf("pre-extraction metadata accepted: %v", err)
	}
	job, err = s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestStaged, ExpectedRevision: 2,
			NextState: store.IngestValidated, UpdatedAt: now.Add(2 * time.Minute),
		})
	if err != nil || job.State != store.IngestValidated || job.Revision != 3 {
		t.Fatalf("validated transition: %+v %v", job, err)
	}
	if _, err := s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestValidated, ExpectedRevision: 3,
			NextState: store.IngestExtracted, UpdatedAt: now.Add(3 * time.Minute),
		}); err != store.ErrInvalidTransition {
		t.Fatalf("metadata-free extraction accepted: %v", err)
	}
	if _, err := s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestValidated, ExpectedRevision: 3,
			NextState:                     store.IngestExtracted,
			ExtractedEmbeddedMetadataJSON: []byte(`{"title":`),
			UpdatedAt:                     now.Add(3 * time.Minute),
		}); err != store.ErrInvalidTransition {
		t.Fatalf("invalid extraction metadata accepted: %v", err)
	}
	if _, err := s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestValidated, ExpectedRevision: 3,
			NextState:                     store.IngestExtracted,
			ExtractedEmbeddedMetadataJSON: []byte(`null`),
			UpdatedAt:                     now.Add(3 * time.Minute),
		}); err != store.ErrInvalidTransition {
		t.Fatalf("non-object extraction metadata accepted: %v", err)
	}
	const extractedMetadata = `{"title":"Embedded","languages":["en","fr"]}`
	job, err = s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestValidated, ExpectedRevision: 3,
			NextState:                     store.IngestExtracted,
			ExtractedEmbeddedMetadataJSON: []byte(extractedMetadata),
			UpdatedAt:                     now.Add(3 * time.Minute),
		})
	if err != nil || job.State != store.IngestExtracted || job.Revision != 4 ||
		string(job.ExtractedEmbeddedMetadataJSON) != extractedMetadata {
		t.Fatalf("extracted transition: %+v %v", job, err)
	}
	roundTripped, err := s.IngestJobByID(ctx, manager.ID, job.ID)
	if err != nil ||
		string(roundTripped.ExtractedEmbeddedMetadataJSON) != extractedMetadata {
		t.Fatalf("extracted metadata round trip: %+v %v", roundTripped, err)
	}
	if _, err := s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestExtracted, ExpectedRevision: 4,
			NextState: store.IngestPromoted, UpdatedAt: now.Add(4 * time.Minute),
		}); err != store.ErrInvalidTransition {
		t.Fatalf("generic promotion accepted: %v", err)
	}

	stagedRetry, err := s.CommitIngestStage(ctx, manager.ID, "job-2",
		store.CommitIngestStageRequest{
			ExpectedRevision: 1, Artifact: artifact,
			StagingPath: contentpath.StagingPath("job-2"),
			UpdatedAt:   now.Add(3 * time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
	retryJob := stagedRetry.Job
	retryJob, err = s.TransitionIngestJob(ctx, manager.ID, retryJob.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestStaged, ExpectedRevision: retryJob.Revision,
			NextState: store.IngestFailed, ErrorCode: "invalid_epub",
			ErrorDetail: "missing container", ExpiresAt: &expiry,
			UpdatedAt: now.Add(4 * time.Minute),
		})
	if err != nil || retryJob.ErrorCode == nil || retryJob.ExpiresAt == nil {
		t.Fatalf("failed transition: %+v %v", retryJob, err)
	}
	retryJob, err = s.TransitionIngestJob(ctx, manager.ID, retryJob.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestFailed, ExpectedRevision: retryJob.Revision,
			NextState: store.IngestStaged, IncrementRetry: true,
			UpdatedAt: now.Add(5 * time.Minute),
		})
	if err != nil || retryJob.State != store.IngestStaged ||
		retryJob.RetryCount != 1 || retryJob.ErrorCode != nil ||
		retryJob.ExpiresAt != nil {
		t.Fatalf("retry transition: %+v %v", retryJob, err)
	}
	if _, err := s.TransitionIngestJob(ctx, owner.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestExtracted, ExpectedRevision: job.Revision,
			NextState: store.IngestFailed, ErrorCode: "wrong-user",
			ExpiresAt: &expiry, UpdatedAt: now.Add(5 * time.Minute),
		}); err != store.ErrNotFound {
		t.Fatalf("cross-user transition: %v", err)
	}

	if err := s.RevokeLibraryAccess(ctx, owner.ID, managed.ID, manager.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestJobByID(ctx, manager.ID, job.ID); err != store.ErrNotFound {
		t.Fatalf("revoked manager read ingest job: %v", err)
	}
	job, err = s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestExtracted, ExpectedRevision: job.Revision,
			NextState: store.IngestFailed, ErrorCode: "worker_failure",
			ExpiresAt: &expiry, UpdatedAt: now.Add(6 * time.Minute),
		})
	if err != nil || job.State != store.IngestFailed ||
		string(job.ExtractedEmbeddedMetadataJSON) != extractedMetadata {
		t.Fatalf("ACL revocation stranded ingest job: %+v %v", job, err)
	}
}

func testConcurrentIngestJobCreate(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	user := MkUser(t, s, "ingest-concurrent")
	now := time.Now().UTC()
	library := store.Library{
		ID: "ingest-concurrent", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Kind: store.LibraryManaged, Name: "Concurrent", CreatedAt: now,
	}

	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	clientKey := "same-upload"
	const workers = 12
	start := make(chan struct{})
	jobs := make(chan store.IngestJob, workers)
	created := make(chan bool, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			job, wasCreated, err := s.CreateIngestJob(ctx, user.ID,
				store.IngestJobRequest{
					ID: fmt.Sprintf("candidate-%02d", i), LibraryID: library.ID,
					Source: store.IngestUpload, ClientKey: &clientKey,
					RequestFingerprint: "same-request", CreatedAt: now,
				})
			if err != nil {
				errs <- err
				return
			}
			jobs <- job
			created <- wasCreated
		}(i)
	}
	close(start)
	wg.Wait()
	close(jobs)
	close(created)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ingest creation: %v", err)
	}
	var jobID string
	for job := range jobs {
		if jobID == "" {
			jobID = job.ID
		} else if job.ID != jobID {
			t.Fatalf("idempotency split jobs: %q != %q", job.ID, jobID)
		}
	}
	createdCount := 0
	for value := range created {
		if value {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("want one created ingest job, got %d", createdCount)
	}
	listed, err := s.ListIngestJobs(ctx, user.ID, library.ID, nil, 20)
	if err != nil || len(listed) != 1 || listed[0].ID != jobID {
		t.Fatalf("concurrent ingest jobs: %+v %v", listed, err)
	}
}

func testIngestRecoveryList(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	user := MkUser(t, s, "ingest-recovery")
	now := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	library := store.Library{
		ID: "ingest-recovery", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Kind: store.LibraryManaged, Name: "Recovery", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	stage := func(id string, at time.Time) store.IngestJob {
		job := createIngestJob(t, s, user.ID, library.ID, id, now)
		blob := ingestBlob(id, 10)
		result, err := s.CommitIngestStage(ctx, user.ID, job.ID,
			store.CommitIngestStageRequest{
				ExpectedRevision: job.Revision, Artifact: blob,
				StagingPath: contentpath.StagingPath(job.ID),
				UpdatedAt:   at,
			})
		if err != nil {
			t.Fatal(err)
		}
		return result.Job
	}
	first := stage("recovery-a", now.Add(time.Minute))
	second := stage("recovery-b", now.Add(2*time.Minute))
	recent := stage("recovery-recent", now.Add(10*time.Minute))
	failed := stage("recovery-failed", now.Add(3*time.Minute))
	expiry := now.Add(time.Hour)
	if _, err := s.TransitionIngestJob(ctx, user.ID, failed.ID,
		store.IngestJobTransition{
			ExpectedState: failed.State, ExpectedRevision: failed.Revision,
			NextState: store.IngestFailed, ErrorCode: "failed",
			ExpiresAt: &expiry, UpdatedAt: now.Add(4 * time.Minute),
		}); err != nil {
		t.Fatal(err)
	}

	page, err := s.ListIngestRecoveryJobs(ctx, now.Add(5*time.Minute), nil, 1)
	if err != nil || len(page) != 1 || page[0].ID != first.ID {
		t.Fatalf("first recovery page: %+v %v", page, err)
	}
	cursor := &store.IngestRecoveryCursor{
		UpdatedAt: page[0].UpdatedAt, ID: page[0].ID,
	}
	page, err = s.ListIngestRecoveryJobs(ctx, now.Add(5*time.Minute), cursor, 10)
	if err != nil || len(page) != 1 || page[0].ID != second.ID {
		t.Fatalf("second recovery page: %+v %v", page, err)
	}
	if page[0].ID == recent.ID || page[0].ID == failed.ID {
		t.Fatalf("recovery list included ineligible job: %+v", page)
	}
	if _, err := s.ListIngestRecoveryJobs(ctx, time.Time{}, nil, 10); err != store.ErrInvalidTransition {
		t.Fatalf("zero recovery cutoff: %v", err)
	}

	workerPage, err := s.ListIngestWorkerJobs(
		ctx, store.IngestStaged, 2)
	if err != nil || len(workerPage) != 2 ||
		workerPage[0].ID != first.ID || workerPage[1].ID != second.ID {
		t.Fatalf("staged worker page: %+v %v", workerPage, err)
	}
	validated, err := s.TransitionIngestJob(ctx, user.ID, second.ID,
		store.IngestJobTransition{
			ExpectedState: second.State, ExpectedRevision: second.Revision,
			NextState: store.IngestValidated,
			UpdatedAt: now.Add(11 * time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
	workerPage, err = s.ListIngestWorkerJobs(
		ctx, store.IngestValidated, 10)
	if err != nil || len(workerPage) != 1 ||
		workerPage[0].ID != validated.ID {
		t.Fatalf("validated worker page: %+v %v", workerPage, err)
	}
	if _, err := s.ListIngestWorkerJobs(
		ctx, store.IngestPromoted, 10); err != store.ErrInvalidTransition {
		t.Fatalf("invalid worker state: %v", err)
	}
}

// testIngestActivityShowsWhatNeverBecameABook covers the question a user
// asks after an upload vanishes: the job that failed must be findable,
// newest first, without paging past every upload that worked.
func testIngestActivityShowsWhatNeverBecameABook(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	owner := MkUser(t, s, "activity-owner")
	reader := MkUser(t, s, "activity-reader")
	outsider := MkUser(t, s, "activity-outsider")
	now := time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)
	if err := s.CreateLibrary(ctx, store.Library{
		ID: "lib-activity", OwnerUserID: owner.ID, QuotaUserID: owner.ID,
		Kind: store.LibraryManaged, Name: "Activity", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantLibraryAccess(
		ctx, owner.ID, "lib-activity", reader.ID, store.LibraryRoleRead, now,
	); err != nil {
		t.Fatal(err)
	}

	// One job that goes bad, and a newer one still working. Ordering is
	// newest first, so the newer job leads.
	bad := createIngestJob(t, s, owner.ID, "lib-activity", "job-bad", now)
	stagedBad, err := s.CommitIngestStage(ctx, owner.ID, bad.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: bad.Revision,
			Artifact:         ingestBlob("activity-bad", 43),
			StagingPath:      contentpath.StagingPath(bad.ID),
			UpdatedAt:        now.Add(30 * time.Second),
		})
	if err != nil {
		t.Fatal(err)
	}
	bad = stagedBad.Job
	code, detail := "invalid_epub", "not an epub"
	quarantineExpiry := now.Add(72 * time.Hour)
	if _, err := s.TransitionIngestJob(ctx, owner.ID, bad.ID,
		store.IngestJobTransition{
			ExpectedState: bad.State, ExpectedRevision: bad.Revision,
			NextState: store.IngestQuarantined, ErrorCode: code,
			ErrorDetail: detail, ExpiresAt: &quarantineExpiry,
			UpdatedAt: now.Add(time.Minute),
		}); err != nil {
		t.Fatal(err)
	}
	working := createIngestJob(
		t, s, owner.ID, "lib-activity", "job-working", now.Add(time.Hour))

	activity, err := s.ListIngestActivity(ctx, owner.ID, "lib-activity", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(activity) != 2 ||
		activity[0].ID != working.ID || activity[1].ID != bad.ID {
		t.Fatalf("activity order: %+v", activity)
	}
	if activity[1].ErrorCode == nil || *activity[1].ErrorCode != code {
		t.Fatalf("failure reason lost: %+v", activity[1])
	}

	// A promoted job is a book; the catalog is where it belongs.
	promoteForActivity(t, s, working, now.Add(2*time.Hour))
	activity, err = s.ListIngestActivity(ctx, owner.ID, "lib-activity", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(activity) != 1 || activity[0].ID != bad.ID {
		t.Fatalf("promoted job still listed as activity: %+v", activity)
	}

	// Uploading is a manage capability, so seeing why an upload failed is
	// too: a reader is not a librarian.
	for _, id := range []string{reader.ID, outsider.ID} {
		if _, err := s.ListIngestActivity(
			ctx, id, "lib-activity", 50); err != store.ErrNotFound {
			t.Fatalf("non-manager read ingest activity: %v", err)
		}
	}
	for _, limit := range []int{0, 501} {
		if _, err := s.ListIngestActivity(
			ctx, owner.ID, "lib-activity", limit); err == nil {
			t.Fatalf("activity limit %d accepted", limit)
		}
	}
}

func promoteForActivity(
	t *testing.T, s store.Store, job store.IngestJob, at time.Time,
) {
	t.Helper()
	ctx := context.Background()
	blob := ingestBlob("activity-"+job.ID, 41)
	staged, err := s.CommitIngestStage(ctx, job.UserID, job.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: job.Revision, Artifact: blob,
			StagingPath: contentpath.StagingPath(job.ID), UpdatedAt: at,
		})
	if err != nil {
		t.Fatal(err)
	}
	promoted := extractIngestJob(t, s, staged.Job, at.Add(time.Minute))
	if _, err := s.CommitNewBookPromotion(ctx, promoted.UserID, promoted.ID,
		promotionRequest(promoted, blob, "book-"+promoted.ID,
			"file-"+promoted.ID, at.Add(2*time.Minute))); err != nil {
		t.Fatal(err)
	}
}

// testAbandonedIngestList: an upload writes its bytes before the database
// points at them, so `received` is where an interrupted one is left. The
// query has to find exactly those and nothing that is merely in progress
// somewhere further along.
func testAbandonedIngestList(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	user := MkUser(t, s, "ingest-abandoned")
	other := MkUser(t, s, "ingest-abandoned-other")
	now := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	library := store.Library{
		ID: "ingest-abandoned", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Kind: store.LibraryManaged, Name: "Abandoned", CreatedAt: now,
	}
	otherLibrary := store.Library{
		ID: "ingest-abandoned-2", OwnerUserID: other.ID, QuotaUserID: other.ID,
		Kind: store.LibraryManaged, Name: "Abandoned", CreatedAt: now,
	}
	for _, l := range []store.Library{library, otherLibrary} {
		if err := s.CreateLibrary(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	createIngestJob(t, s, user.ID, library.ID, "abandoned-a", now)
	createIngestJob(t, s, user.ID, library.ID, "abandoned-b", now)
	// A job that got its bytes committed is not abandoned, whatever else
	// happens to it afterwards.
	staged := createIngestJob(t, s, user.ID, library.ID, "abandoned-staged", now)
	if _, err := s.CommitIngestStage(ctx, user.ID, staged.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: staged.Revision,
			Artifact:         ingestBlob("abandoned-staged", 10),
			StagingPath:      contentpath.StagingPath(staged.ID),
			UpdatedAt:        now.Add(time.Minute),
		}); err != nil {
		t.Fatal(err)
	}
	// This sweep is global housekeeping, so another user's interrupted
	// upload has to be visible too — it occupies the same disk.
	createIngestJob(t, s, other.ID, otherLibrary.ID, "abandoned-z", now)

	page, err := s.ListAbandonedIngestJobs(ctx, "", 2)
	if err != nil || len(page) != 2 ||
		page[0].ID != "abandoned-a" || page[1].ID != "abandoned-b" {
		t.Fatalf("first abandoned page: %+v %v", page, err)
	}
	page, err = s.ListAbandonedIngestJobs(ctx, page[1].ID, 10)
	if err != nil || len(page) != 1 || page[0].ID != "abandoned-z" ||
		page[0].UserID != other.ID {
		t.Fatalf("second abandoned page: %+v %v", page, err)
	}
	if _, err := s.ListAbandonedIngestJobs(ctx, "", 0); err != store.ErrInvalidTransition {
		t.Fatalf("zero limit: %v", err)
	}
	if _, err := s.ListAbandonedIngestJobs(ctx, "", 501); err != store.ErrInvalidTransition {
		t.Fatalf("oversized limit: %v", err)
	}
}
