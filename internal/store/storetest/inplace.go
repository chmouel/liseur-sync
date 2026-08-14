package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/store"
)

// testInPlaceBooks pins ADR-0014's central claim: a book whose bytes this
// server never copied is a full catalog citizen that owns nothing, and the
// server's accounting of what it stores is unaffected by it.
//
// The two libraries here hold the *same digest* deliberately. That is the
// case the design turns on: if an in-place file had a `blobs` row, the
// uploaded copy and the file in somebody's directory would share it, and
// deleting either book would decide the other's fate.
func testInPlaceBooks(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	user := MkUser(t, s, "in-place")
	now := time.Date(2026, time.May, 4, 3, 2, 1, 0, time.UTC)
	root := "/srv/books"

	managed := store.Library{
		ID: "in-place-managed", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Source:  store.LibraryManaged,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Uploads", CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, managed); err != nil {
		t.Fatal(err)
	}
	shelf := store.Library{
		ID: "in-place-shelf", OwnerUserID: user.ID, QuotaUserID: user.ID,
		Source:  store.LibraryDirectory,
		Storage: store.LibraryStorageInPlace,
		Refresh: store.LibraryRefreshInterval, Name: "Shelf",
		RootPath: &root, CreatedAt: now,
	}
	if err := s.CreateLibrary(ctx, shelf); err != nil {
		t.Fatal(err)
	}

	blob := ingestBlob("shared-between-two-libraries", 4096)

	uploadJob := createIngestJob(
		t, s, user.ID, managed.ID, "in-place-upload", now)
	staged, err := s.CommitIngestStage(ctx, user.ID, uploadJob.ID,
		store.CommitIngestStageRequest{
			ExpectedRevision: uploadJob.Revision, Artifact: blob,
			StagingPath: contentpath.StagingPath(uploadJob.ID),
			UpdatedAt:   now.Add(time.Minute),
		})
	if err != nil {
		t.Fatal(err)
	}
	uploadJob = extractIngestJob(t, s, staged.Job, now.Add(2*time.Minute))
	if _, err := s.CommitNewBookPromotion(ctx, user.ID, uploadJob.ID,
		promotionRequest(uploadJob, blob, "uploaded-book", "uploaded-file",
			now.Add(3*time.Minute))); err != nil {
		t.Fatal(err)
	}

	scanned := inPlaceJob(t, s, user.ID, shelf.ID, "in-place-scan",
		"Author/Title.epub", now.Add(4*time.Minute))
	request := inPlaceRequest(scanned, blob, "shelf-book", "shelf-file",
		now.Add(5*time.Minute))
	committed, err := s.CommitInPlaceBook(
		ctx, user.ID, scanned.ID, request)
	if err != nil {
		t.Fatalf("commit in-place book: %v", err)
	}
	if committed.Job.State != store.IngestPromoted || committed.Replayed {
		t.Fatalf("in-place commit: %+v", committed)
	}
	if committed.File.Storage != store.LibraryStorageInPlace ||
		committed.File.BlobSHA256 != "" ||
		committed.File.ContentSHA256 != blob.SHA256 {
		t.Fatalf("in-place file: %+v", committed.File)
	}

	// One blob, because only one of these books is bytes the server keeps.
	records, err := s.ListBlobRecords(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].SHA256 != blob.SHA256 {
		t.Fatalf("blob records: %+v", records)
	}

	// A lost response replays onto the same rows rather than making a
	// second book out of one file.
	replay, err := s.CommitInPlaceBook(ctx, user.ID, scanned.ID, request)
	if err != nil {
		t.Fatalf("replay in-place commit: %v", err)
	}
	if !replay.Replayed || replay.Book.ID != committed.Book.ID ||
		replay.File.ID != committed.File.ID {
		t.Fatalf("replay: %+v", replay)
	}

	// A different claim under the same job is a conflict, never an
	// overwrite: two passes that disagree found two different files.
	conflicting := request
	conflicting.File.ContentSHA256 = ingestBlob("something-else", 4096).SHA256
	if _, err := s.CommitInPlaceBook(
		ctx, user.ID, scanned.ID, conflicting); err != store.ErrPromotionConflict {
		t.Fatalf("conflicting in-place commit: %v", err)
	}

	// Deleting the uploaded book takes its blob's last reference with it,
	// and leaves the in-place book — which never referenced it — intact
	// and readable.
	if _, err := s.TrashCatalogBook(ctx, user.ID, "uploaded-book",
		now.Add(6*time.Minute), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PurgeExpiredTrash(
		ctx, now.Add(2*time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	files, err := s.ListBookFiles(
		ctx, user.ID, "shelf-book", store.LibraryRoleRead)
	if err != nil {
		t.Fatalf("the in-place book did not survive: %v", err)
	}
	if len(files) != 1 || files[0].ContentSHA256 != blob.SHA256 {
		t.Fatalf("in-place files after the upload's purge: %+v", files)
	}
	if files[0].LibraryRoot != root {
		t.Errorf("library root %q, want %q", files[0].LibraryRoot, root)
	}
	if files[0].Availability != store.BookFileAvailable {
		t.Errorf("availability %q, want available", files[0].Availability)
	}
}

// inPlaceJob creates the `received` job an in-place scan commits from.
func inPlaceJob(
	t *testing.T,
	s store.Store,
	userID, libraryID, jobID, relativePath string,
	at time.Time,
) store.IngestJob {
	t.Helper()
	job, created, err := s.CreateIngestJob(context.Background(), userID,
		store.IngestJobRequest{
			ID: jobID, LibraryID: libraryID, Source: store.IngestScanned,
			SourceRelativePath: &relativePath,
			RequestFingerprint: "request-" + jobID, CreatedAt: at,
		})
	if err != nil || !created {
		t.Fatalf("create in-place job %s: %+v %v %v", jobID, job, created, err)
	}
	if job.Storage != store.LibraryStorageInPlace {
		t.Fatalf("job storage %q, want in_place", job.Storage)
	}
	return job
}

func inPlaceRequest(
	job store.IngestJob,
	blob store.BlobInfo,
	bookID, fileID string,
	at time.Time,
) store.CommitInPlaceBookRequest {
	modified := at.Add(-time.Hour)
	return store.CommitInPlaceBookRequest{
		ExpectedRevision:              job.Revision,
		ExtractedEmbeddedMetadataJSON: []byte(`{"title":"In place"}`),
		Book: store.CatalogBook{
			ID: bookID, LibraryID: job.LibraryID, Status: store.BookActive,
			Title: "In place " + bookID, TitleSource: store.MetadataEmbedded,
			CreatedAt: at, UpdatedAt: at,
		},
		File: store.BookFile{
			ID: fileID, LibraryID: job.LibraryID, BookID: bookID,
			Storage:            store.LibraryStorageInPlace,
			ContentSHA256:      blob.SHA256,
			ContentSizeBytes:   blob.SizeBytes,
			Source:             job.Source,
			SourceRelativePath: job.SourceRelativePath,
			SourceModifiedAt:   &modified,
			OriginalFilename:   bookID + ".epub",
			MediaType:          "application/epub+zip",
			Availability:       store.BookFileAvailable,
			CreatedAt:          at, UpdatedAt: at,
		},
		UpdatedAt: at,
	}
}
