//go:build linux

package content

import (
	"context"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

type orphanGCStore interface {
	PurgeOrphanedBlobRecords(context.Context, time.Time, int) ([]store.BlobRecord, error)
}

type orphanBlobRemover interface {
	RemoveBlob(context.Context, string, int64) (bool, error)
}

// BlobGCReport describes one complete startup grace-period sweep.
type BlobGCReport struct {
	RecordsPurged int
	FilesRemoved  int
	FilesMissing  int
}

// SweepOrphanedBlobs retires eligible database rows and removes their
// verified local CAS files. Content writers must remain paused for the whole
// call; startup recovery provides that serialization.
func SweepOrphanedBlobs(
	ctx context.Context,
	st orphanGCStore,
	blobs orphanBlobRemover,
	before time.Time,
	pageSize int,
) (BlobGCReport, error) {
	var report BlobGCReport
	if st == nil || blobs == nil || before.IsZero() ||
		pageSize < 1 || pageSize > 500 {
		return report, store.ErrInvalidTransition
	}
	for {
		records, err := st.PurgeOrphanedBlobRecords(
			ctx, before.UTC(), pageSize)
		if err != nil {
			return report, fmt.Errorf("purge orphan blob records: %w", err)
		}
		report.RecordsPurged += len(records)
		for _, record := range records {
			removed, err := blobs.RemoveBlob(
				ctx, record.SHA256, record.SizeBytes)
			if err != nil {
				return report, fmt.Errorf(
					"remove orphan blob %q: %w", record.SHA256, err)
			}
			if removed {
				report.FilesRemoved++
			} else {
				report.FilesMissing++
			}
		}
		if len(records) < pageSize {
			return report, nil
		}
	}
}
