package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

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
		job.BytesReceived != 0 {
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
	contentSHA := "content-sha"
	stagingPath := ".incoming/job.tmp"
	job3, err := s.TransitionIngestJob(ctx, manager.ID, "job-3",
		store.IngestJobTransition{
			ExpectedState: store.IngestReceived, ExpectedRevision: 1,
			NextState: store.IngestStaged, BytesReceived: &bytesReceived,
			ContentSHA256: &contentSHA, StagingPath: &stagingPath,
			UpdatedAt: now.Add(3 * time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
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
	stagingPath = ".incoming/job-1.tmp"
	job, err = s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestReceived, ExpectedRevision: 1,
			NextState: store.IngestStaged, BytesReceived: &bytesReceived,
			ContentSHA256: &contentSHA, StagingPath: &stagingPath,
			UpdatedAt: now.Add(time.Minute),
		})
	if err != nil || job.State != store.IngestStaged || job.Revision != 2 ||
		job.ContentSHA256 == nil || *job.ContentSHA256 != contentSHA {
		t.Fatalf("staged transition: %+v %v", job, err)
	}
	if _, err := s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestReceived, ExpectedRevision: 1,
			NextState: store.IngestStaged, UpdatedAt: now.Add(2 * time.Minute),
		}); err != store.ErrStaleRevision {
		t.Fatalf("stale transition: %v", err)
	}
	job, err = s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestStaged, ExpectedRevision: 2,
			NextState: store.IngestValidated, UpdatedAt: now.Add(2 * time.Minute),
		})
	if err != nil || job.State != store.IngestValidated || job.Revision != 3 {
		t.Fatalf("validated transition: %+v %v", job, err)
	}
	job, err = s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestValidated, ExpectedRevision: 3,
			NextState: store.IngestExtracted, UpdatedAt: now.Add(3 * time.Minute),
		})
	if err != nil || job.State != store.IngestExtracted || job.Revision != 4 {
		t.Fatalf("extracted transition: %+v %v", job, err)
	}
	if _, err := s.TransitionIngestJob(ctx, manager.ID, job.ID,
		store.IngestJobTransition{
			ExpectedState: store.IngestExtracted, ExpectedRevision: 4,
			NextState: store.IngestPromoted, UpdatedAt: now.Add(4 * time.Minute),
		}); err != store.ErrInvalidTransition {
		t.Fatalf("generic promotion accepted: %v", err)
	}

	retryJob, err := s.TransitionIngestJob(ctx, manager.ID, "job-2",
		store.IngestJobTransition{
			ExpectedState: store.IngestReceived, ExpectedRevision: 1,
			NextState: store.IngestStaged, BytesReceived: &bytesReceived,
			ContentSHA256: &contentSHA, StagingPath: &stagingPath,
			UpdatedAt: now.Add(3 * time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil || job.State != store.IngestFailed {
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
