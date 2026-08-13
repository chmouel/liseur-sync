//go:build linux

package content

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

func validatedExtractionJob(now time.Time) store.IngestJob {
	job := stagedValidationJob(now)
	job.State = store.IngestValidated
	job.Revision++
	return job
}

func TestExtractIngestMetadataPersistsCanonicalJSON(t *testing.T) {
	now := time.Now().UTC()
	job := validatedExtractionJob(now)
	st := &validationStoreFake{job: job}
	position := 2.5
	metadata := epub.Metadata{
		Title:         "Canonical",
		PublishedDate: "2026",
		Identifiers: []epub.Identifier{
			{Scheme: "isbn", Value: "9780000000000"},
		},
		Series: []epub.Series{{Name: "Sequence", Position: &position}},
	}
	artifacts := &validationArtifactFake{
		publication: epub.Result{
			PackagePath: "OPS/book.opf",
			Metadata:    metadata,
		},
		location: ArtifactPromoted,
	}
	result, err := ExtractIngestMetadata(
		context.Background(), st, artifacts, job, validationClock(now),
		24*time.Hour, epub.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"title":"Canonical","published_date":"2026","identifiers":[{"scheme":"isbn","value":"9780000000000"}],"series":[{"name":"Sequence","position":2.5}]}`
	if result.Job.State != store.IngestExtracted ||
		string(result.Job.ExtractedEmbeddedMetadataJSON) != want ||
		result.Metadata.Title != metadata.Title ||
		result.Location != ArtifactPromoted || st.transitions != 1 {
		t.Fatalf("metadata extraction result: %+v", result)
	}
}

func TestExtractIngestMetadataQuarantinesContentFailure(t *testing.T) {
	now := time.Now().UTC()
	retention := 24 * time.Hour
	job := validatedExtractionJob(now)
	st := &validationStoreFake{job: job}
	artifacts := &validationArtifactFake{
		location: ArtifactStaged,
		err: &epub.ValidationError{
			Code: epub.CodeArchiveLimits, Err: errors.New("too large"),
		},
	}
	result, err := ExtractIngestMetadata(
		context.Background(), st, artifacts, job, validationClock(now),
		retention, epub.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if result.Job.State != store.IngestQuarantined ||
		result.Job.ErrorCode == nil ||
		*result.Job.ErrorCode != string(epub.CodeArchiveLimits) ||
		result.Job.ExpiresAt == nil ||
		!result.Job.ExpiresAt.Equal(now.Add(retention)) ||
		len(result.Job.ExtractedEmbeddedMetadataJSON) != 0 ||
		result.Location != ArtifactStaged || st.transitions != 1 {
		t.Fatalf("quarantined extraction result: %+v", result)
	}
}

func TestExtractIngestMetadataLeavesOperationalFailuresRetryable(t *testing.T) {
	now := time.Now().UTC()
	job := validatedExtractionJob(now)

	t.Run("artifact", func(t *testing.T) {
		st := &validationStoreFake{job: job}
		ioErr := errors.New("read failed")
		if _, err := ExtractIngestMetadata(
			context.Background(), st, &validationArtifactFake{err: ioErr},
			job, validationClock(now), time.Hour,
			epub.DefaultLimits()); !errors.Is(err, ioErr) {
			t.Fatalf("operational extraction error: %v", err)
		}
		if st.transitions != 0 || st.job.State != store.IngestValidated {
			t.Fatalf("operational failure changed job: %+v", st.job)
		}
	})

	t.Run("marshal", func(t *testing.T) {
		st := &validationStoreFake{job: job}
		notJSON := math.NaN()
		artifacts := &validationArtifactFake{
			publication: epub.Result{
				Metadata: epub.Metadata{
					Series: []epub.Series{
						{Name: "Invalid", Position: &notJSON},
					},
				},
			},
		}
		if _, err := ExtractIngestMetadata(
			context.Background(), st, artifacts, job, validationClock(now),
			time.Hour, epub.DefaultLimits()); err == nil {
			t.Fatal("non-JSON metadata was accepted")
		}
		if st.transitions != 0 || st.job.State != store.IngestValidated {
			t.Fatalf("marshal failure changed job: %+v", st.job)
		}
	})

	t.Run("store", func(t *testing.T) {
		storeErr := errors.New("database unavailable")
		st := &validationStoreFake{job: job, transitionErr: storeErr}
		artifacts := &validationArtifactFake{
			publication: epub.Result{
				Metadata: epub.Metadata{Title: "Retry"},
			},
		}
		if _, err := ExtractIngestMetadata(
			context.Background(), st, artifacts, job, validationClock(now),
			time.Hour, epub.DefaultLimits()); !errors.Is(err, storeErr) {
			t.Fatalf("store extraction error: %v", err)
		}
		if st.transitions != 0 || st.job.State != store.IngestValidated {
			t.Fatalf("store failure changed job: %+v", st.job)
		}
	})
}

func TestExtractIngestMetadataTimestampsAfterExtraction(t *testing.T) {
	startedAt := time.Now().UTC()
	finishedAt := startedAt.Add(2 * time.Hour)
	current := startedAt
	job := validatedExtractionJob(startedAt)
	st := &validationStoreFake{job: job}
	artifacts := &validationArtifactFake{
		publication: epub.Result{Metadata: epub.Metadata{Title: "Finished"}},
		onValidate:  func() { current = finishedAt },
	}
	result, err := ExtractIngestMetadata(
		context.Background(), st, artifacts, job,
		func() time.Time { return current }, time.Hour, epub.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Job.UpdatedAt.Equal(finishedAt) {
		t.Fatalf("post-extraction timestamp: %+v", result.Job)
	}
}

func TestRunIngestMetadataExtractionPassProcessesBoundedBatches(t *testing.T) {
	now := time.Now().UTC()
	jobs := make(map[string]store.IngestJob)
	for index, id := range []string{"job-a", "job-b", "job-c"} {
		job := validatedExtractionJob(now)
		path := contentpath.StagingPath(id)
		job.ID = id
		job.StagingPath = &path
		job.UpdatedAt = now.Add(time.Duration(index-3) * time.Minute)
		jobs[id] = job
	}
	queue := &validationQueueFake{jobs: jobs}
	artifacts := &validationArtifactFake{
		publication: epub.Result{
			Metadata: epub.Metadata{Title: "Bounded"},
		},
		location: ArtifactStaged,
	}
	report, err := RunIngestMetadataExtractionPass(
		context.Background(), queue, artifacts, validationClock(now),
		time.Hour, epub.DefaultLimits(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Extracted != 2 || report.Quarantined != 0 ||
		report.Skipped != 0 ||
		queue.jobs["job-a"].State != store.IngestExtracted ||
		queue.jobs["job-b"].State != store.IngestExtracted ||
		queue.jobs["job-c"].State != store.IngestValidated {
		t.Fatalf("first extraction pass: %+v %+v", report, queue.jobs)
	}
	report, err = RunIngestMetadataExtractionPass(
		context.Background(), queue, artifacts, validationClock(now),
		time.Hour, epub.DefaultLimits(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Extracted != 1 ||
		queue.jobs["job-c"].State != store.IngestExtracted {
		t.Fatalf("second extraction pass: %+v %+v", report, queue.jobs)
	}
}

func TestRunIngestMetadataExtractionPassSkipsStaleRevision(t *testing.T) {
	now := time.Now().UTC()
	stale := validatedExtractionJob(now)
	stale.ID = "job-a"
	stalePath := contentpath.StagingPath(stale.ID)
	stale.StagingPath = &stalePath
	valid := validatedExtractionJob(now)
	valid.ID = "job-b"
	validPath := contentpath.StagingPath(valid.ID)
	valid.StagingPath = &validPath
	queue := &validationQueueFake{
		jobs: map[string]store.IngestJob{
			stale.ID: stale,
			valid.ID: valid,
		},
		transitionErrors: map[string]error{
			stale.ID: store.ErrStaleRevision,
		},
	}
	report, err := RunIngestMetadataExtractionPass(
		context.Background(), queue, &validationArtifactFake{},
		validationClock(now), time.Hour, epub.DefaultLimits(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Extracted != 1 || report.Skipped != 1 ||
		queue.jobs[stale.ID].State != store.IngestValidated ||
		queue.jobs[valid.ID].State != store.IngestExtracted {
		t.Fatalf("stale extraction pass: %+v %+v", report, queue.jobs)
	}
}
