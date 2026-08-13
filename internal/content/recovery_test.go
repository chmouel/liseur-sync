//go:build linux

package content

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
	"github.com/chmouel/liseur-sync/internal/store"
)

type recoveryStoreFake struct {
	jobs    map[string]store.IngestJob
	expired []store.IngestJob
}

func (f *recoveryStoreFake) ListIngestRecoveryJobs(
	_ context.Context,
	before time.Time,
	after *store.IngestRecoveryCursor,
	limit int,
) ([]store.IngestJob, error) {
	var jobs []store.IngestJob
	for _, job := range f.jobs {
		if job.UpdatedAt.After(before) || job.ArtifactsExpired ||
			(job.State != store.IngestStaged &&
				job.State != store.IngestValidated &&
				job.State != store.IngestExtracted) {
			continue
		}
		if after != nil && (job.UpdatedAt.Before(after.UpdatedAt) ||
			(job.UpdatedAt.Equal(after.UpdatedAt) && job.ID <= after.ID)) {
			continue
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].UpdatedAt.Equal(jobs[j].UpdatedAt) {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].UpdatedAt.Before(jobs[j].UpdatedAt)
	})
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}

func (f *recoveryStoreFake) PurgeExpiredIngestArtifacts(
	context.Context,
	time.Time,
	int,
) ([]store.IngestJob, error) {
	return append([]store.IngestJob(nil), f.expired...), nil
}

func (f *recoveryStoreFake) CompleteIngestArtifactCleanup(
	_ context.Context,
	jobID, stagingPath string,
) error {
	for i, job := range f.expired {
		if job.ID == jobID && job.StagingPath != nil &&
			*job.StagingPath == stagingPath {
			f.expired = append(f.expired[:i], f.expired[i+1:]...)
			break
		}
	}
	job, ok := f.jobs[jobID]
	if ok {
		job.StagingPath = nil
		job.ContentSHA256 = nil
		job.ArtifactCleanupPending = false
		f.jobs[jobID] = job
	}
	return nil
}

func (f *recoveryStoreFake) TransitionIngestJob(
	_ context.Context,
	userID, jobID string,
	change store.IngestJobTransition,
) (store.IngestJob, error) {
	job, ok := f.jobs[jobID]
	if !ok || job.UserID != userID {
		return store.IngestJob{}, store.ErrNotFound
	}
	next, err := store.ApplyIngestTransition(job, change)
	if err != nil {
		return store.IngestJob{}, err
	}
	f.jobs[jobID] = next
	return next, nil
}

type inspectResult struct {
	location ArtifactLocation
	err      error
}

type recoveryArtifactsFake struct {
	results   map[string]inspectResult
	removed   []string
	removeErr error
}

func (f *recoveryArtifactsFake) InspectArtifact(
	_ context.Context,
	path, _ string,
	_ int64,
) (ArtifactLocation, error) {
	result, ok := f.results[path]
	if !ok {
		return "", ErrStageMissing
	}
	return result.location, result.err
}

func (f *recoveryArtifactsFake) RemoveStage(_ context.Context, path string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, path)
	return nil
}

func TestRecoverIngest(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	job := func(id string, state store.IngestState, updatedAt time.Time) store.IngestJob {
		sha := digestOf([]byte(id))
		path := contentpath.StagingPath(id)
		return store.IngestJob{
			ID: id, UserID: "user", State: state, Revision: 2,
			ContentSHA256: &sha, StagingPath: &path, BytesReceived: 10,
			CreatedAt: now.Add(-time.Hour), UpdatedAt: updatedAt,
		}
	}
	jobs := map[string]store.IngestJob{
		"ready-stage": job("ready-stage", store.IngestStaged, now.Add(-5*time.Minute)),
		"ready-final": job("ready-final", store.IngestExtracted, now.Add(-4*time.Minute)),
		"missing":     job("missing", store.IngestValidated, now.Add(-3*time.Minute)),
		"corrupt":     job("corrupt", store.IngestStaged, now.Add(-2*time.Minute)),
		"recent":      job("recent", store.IngestStaged, now),
	}
	expired := job("expired", store.IngestFailed, now.Add(-time.Hour))
	fakeStore := &recoveryStoreFake{
		jobs: jobs, expired: []store.IngestJob{expired},
	}
	fakeArtifacts := &recoveryArtifactsFake{results: map[string]inspectResult{
		*jobs["ready-stage"].StagingPath: {location: ArtifactStaged},
		*jobs["ready-final"].StagingPath: {location: ArtifactPromoted},
		*jobs["missing"].StagingPath:     {err: ErrStageMissing},
		*jobs["corrupt"].StagingPath:     {err: ErrDigestMismatch},
	}}
	report, err := RecoverIngest(context.Background(), fakeStore, fakeArtifacts,
		now, now.Add(-time.Minute), 24*time.Hour, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Ready) != 2 || report.Failed != 1 ||
		report.Quarantined != 1 || report.Cleaned != 1 || report.Skipped != 0 {
		t.Fatalf("recovery report: %+v", report)
	}
	locations := map[string]ArtifactLocation{}
	for _, recovered := range report.Ready {
		locations[recovered.Job.ID] = recovered.Location
	}
	if locations["ready-stage"] != ArtifactStaged ||
		locations["ready-final"] != ArtifactPromoted {
		t.Fatalf("recovered locations: %+v", locations)
	}
	if got := fakeStore.jobs["missing"]; got.State != store.IngestFailed ||
		got.ErrorCode == nil || *got.ErrorCode != "artifact_missing" {
		t.Fatalf("missing recovery: %+v", got)
	}
	if got := fakeStore.jobs["corrupt"]; got.State != store.IngestQuarantined ||
		got.ErrorCode == nil || *got.ErrorCode != "artifact_corrupt" {
		t.Fatalf("corrupt recovery: %+v", got)
	}
	if got := fakeStore.jobs["recent"]; got.State != store.IngestStaged {
		t.Fatalf("recent job changed: %+v", got)
	}
	if len(fakeArtifacts.removed) != 1 ||
		fakeArtifacts.removed[0] != *expired.StagingPath {
		t.Fatalf("expired cleanup: %+v", fakeArtifacts.removed)
	}
}

func TestRecoverIngestReturnsFilesystemErrors(t *testing.T) {
	now := time.Now().UTC()
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	path := contentpath.StagingPath("io")
	job := store.IngestJob{
		ID: "io", UserID: "user", State: store.IngestStaged, Revision: 2,
		ContentSHA256: &sha, StagingPath: &path, UpdatedAt: now.Add(-time.Hour),
	}

	fakeStore := &recoveryStoreFake{jobs: map[string]store.IngestJob{"io": job}}
	ioErr := errors.New("read failed")
	fakeArtifacts := &recoveryArtifactsFake{results: map[string]inspectResult{
		*job.StagingPath: {err: ioErr},
	}}
	if _, err := RecoverIngest(context.Background(), fakeStore, fakeArtifacts,
		now, now, time.Hour, 10); !errors.Is(err, ioErr) {
		t.Fatalf("filesystem error: %v", err)
	}
}

func TestRecoverIngestRetriesExpiredCleanup(t *testing.T) {
	now := time.Now().UTC()
	sha := digestOf([]byte("expired"))
	path := contentpath.StagingPath("expired")
	expired := store.IngestJob{
		ID: "expired", UserID: "user", State: store.IngestFailed,
		ContentSHA256: &sha, StagingPath: &path, BytesReceived: 10,
		ArtifactsExpired: true, ArtifactCleanupPending: true,
		UpdatedAt: now.Add(-time.Hour),
	}
	fakeStore := &recoveryStoreFake{expired: []store.IngestJob{expired}}
	removeErr := errors.New("remove failed")
	fakeArtifacts := &recoveryArtifactsFake{removeErr: removeErr}
	if _, err := RecoverIngest(context.Background(), fakeStore, fakeArtifacts,
		now, now, time.Hour, 10); !errors.Is(err, removeErr) {
		t.Fatalf("first cleanup error: %v", err)
	}
	if len(fakeStore.expired) != 1 {
		t.Fatal("failed cleanup acknowledgement was lost")
	}
	fakeArtifacts.removeErr = nil
	report, err := RecoverIngest(context.Background(), fakeStore, fakeArtifacts,
		now, now, time.Hour, 10)
	if err != nil || report.Cleaned != 1 || len(fakeStore.expired) != 0 {
		t.Fatalf("retried cleanup: %+v %v", report, err)
	}
}

func TestRecoverIngestRejectsMismatchedCleanupPath(t *testing.T) {
	now := time.Now().UTC()
	sha := digestOf([]byte("expired"))
	wrongPath := contentpath.StagingPath("another-job")
	expired := store.IngestJob{
		ID: "expired", UserID: "user", State: store.IngestFailed,
		ContentSHA256: &sha, StagingPath: &wrongPath, BytesReceived: 10,
		ArtifactsExpired: true, ArtifactCleanupPending: true,
		UpdatedAt: now.Add(-time.Hour),
	}
	fakeStore := &recoveryStoreFake{expired: []store.IngestJob{expired}}
	fakeArtifacts := &recoveryArtifactsFake{}
	if _, err := RecoverIngest(context.Background(), fakeStore, fakeArtifacts,
		now, now, time.Hour, 10); !errors.Is(err, store.ErrInvariantViolation) {
		t.Fatalf("mismatched cleanup path: %v", err)
	}
	if len(fakeArtifacts.removed) != 0 {
		t.Fatalf("mismatched path was removed: %+v", fakeArtifacts.removed)
	}
}
