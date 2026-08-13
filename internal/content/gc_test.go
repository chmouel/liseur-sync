//go:build linux

package content

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

type orphanGCStoreFake struct {
	pages [][]store.BlobRecord
	calls int
}

func (f *orphanGCStoreFake) PurgeOrphanedBlobRecords(
	context.Context,
	time.Time,
	int,
) ([]store.BlobRecord, error) {
	if f.calls >= len(f.pages) {
		return nil, nil
	}
	page := f.pages[f.calls]
	f.calls++
	return append([]store.BlobRecord(nil), page...), nil
}

type orphanBlobRemoverFake struct {
	missing map[string]bool
	calls   []string
	err     error
}

func (f *orphanBlobRemoverFake) RemoveBlob(
	_ context.Context,
	sha string,
	_ int64,
) (bool, error) {
	f.calls = append(f.calls, sha)
	if f.err != nil {
		return false, f.err
	}
	return !f.missing[sha], nil
}

func TestSweepOrphanedBlobsPaginates(t *testing.T) {
	a := store.BlobRecord{BlobInfo: store.BlobInfo{
		SHA256: digestOf([]byte("gc-a")), SizeBytes: 1,
	}}
	b := store.BlobRecord{BlobInfo: store.BlobInfo{
		SHA256: digestOf([]byte("gc-b")), SizeBytes: 2,
	}}
	st := &orphanGCStoreFake{pages: [][]store.BlobRecord{{a}, {b}, {}}}
	blobs := &orphanBlobRemoverFake{missing: map[string]bool{b.SHA256: true}}
	report, err := SweepOrphanedBlobs(
		context.Background(), st, blobs, time.Now().UTC(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if report.RecordsPurged != 2 || report.FilesRemoved != 1 ||
		report.FilesMissing != 1 || st.calls != 3 {
		t.Fatalf("GC report: %+v, store calls: %d", report, st.calls)
	}
	wantCalls := fmt.Sprint([]string{a.SHA256, b.SHA256})
	if fmt.Sprint(blobs.calls) != wantCalls {
		t.Fatalf("blob removal calls: got %v want %s", blobs.calls, wantCalls)
	}
}

func TestSweepOrphanedBlobsReturnsRemovalError(t *testing.T) {
	record := store.BlobRecord{BlobInfo: store.BlobInfo{
		SHA256: digestOf([]byte("gc-error")), SizeBytes: 1,
	}}
	st := &orphanGCStoreFake{pages: [][]store.BlobRecord{{record}}}
	removeErr := errors.New("remove failed")
	blobs := &orphanBlobRemoverFake{err: removeErr}
	report, err := SweepOrphanedBlobs(
		context.Background(), st, blobs, time.Now().UTC(), 10)
	if !errors.Is(err, removeErr) || report.RecordsPurged != 1 {
		t.Fatalf("removal error: %+v %v", report, err)
	}
}

func TestSweepOrphanedBlobsRejectsInvalidInputs(t *testing.T) {
	now := time.Now().UTC()
	st := &orphanGCStoreFake{}
	blobs := &orphanBlobRemoverFake{}
	for name, run := range map[string]func() error{
		"nil store": func() error {
			_, err := SweepOrphanedBlobs(
				context.Background(), nil, blobs, now, 10)
			return err
		},
		"nil blobs": func() error {
			_, err := SweepOrphanedBlobs(
				context.Background(), st, nil, now, 10)
			return err
		},
		"zero cutoff": func() error {
			_, err := SweepOrphanedBlobs(
				context.Background(), st, blobs, time.Time{}, 10)
			return err
		},
		"bad page": func() error {
			_, err := SweepOrphanedBlobs(
				context.Background(), st, blobs, now, 0)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, store.ErrInvalidTransition) {
				t.Fatalf("invalid input error: %v", err)
			}
		})
	}
}
