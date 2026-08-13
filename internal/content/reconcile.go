//go:build linux

package content

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

type blobInventory interface {
	ListBlobs(context.Context) ([]Blob, error)
}

type blobReconciliationStore interface {
	ListBlobRecords(context.Context, string, int) ([]store.BlobRecord, error)
	ReconcileBlob(context.Context, store.BlobInfo, bool, time.Time) (store.BlobReconcileResult, error)
}

// BlobReconciliationReport describes one complete mark-only comparison
// between the verified CAS inventory and database blob records.
type BlobReconciliationReport struct {
	PhysicalBlobs   int
	DatabaseBlobs   int
	InsertedOrphans int
	OrphansMarked   int
	OrphansCleared  int
	MissingMarked   int
	MissingCleared  int
	Unchanged       int
}

// ReconcileBlobInventory marks missing and orphaned blob state without
// deleting content or changing catalog availability.
func ReconcileBlobInventory(
	ctx context.Context,
	st blobReconciliationStore,
	inventory blobInventory,
	at time.Time,
	pageSize int,
) (BlobReconciliationReport, error) {
	var report BlobReconciliationReport
	if st == nil || inventory == nil || at.IsZero() ||
		pageSize < 1 || pageSize > 500 {
		return report, store.ErrInvalidTransition
	}
	at = at.UTC()
	blobs, err := inventory.ListBlobs(ctx)
	if err != nil {
		return report, fmt.Errorf("inventory content blobs: %w", err)
	}
	sort.Slice(blobs, func(i, j int) bool {
		return blobs[i].SHA256 < blobs[j].SHA256
	})
	physical := make(map[string]store.BlobInfo, len(blobs))
	for _, blob := range blobs {
		info := store.BlobInfo{SHA256: blob.SHA256, SizeBytes: blob.Size}
		if err := store.ValidateBlobInfo(info); err != nil {
			return report, fmt.Errorf("inventory blob %q: %w", blob.SHA256, err)
		}
		if _, duplicate := physical[blob.SHA256]; duplicate {
			return report, fmt.Errorf(
				"inventory blob %q: %w", blob.SHA256, store.ErrInvariantViolation)
		}
		physical[blob.SHA256] = info
		result, err := st.ReconcileBlob(ctx, info, true, at)
		if err != nil {
			return report, fmt.Errorf(
				"reconcile present blob %q: %w", blob.SHA256, err)
		}
		report.add(result)
		report.PhysicalBlobs++
	}

	cursor := ""
	for {
		records, err := st.ListBlobRecords(ctx, cursor, pageSize)
		if err != nil {
			return report, fmt.Errorf("list blob records after %q: %w", cursor, err)
		}
		if len(records) > pageSize {
			return report, store.ErrInvariantViolation
		}
		previous := cursor
		for _, record := range records {
			if err := store.ValidateBlobInfo(record.BlobInfo); err != nil ||
				record.SHA256 <= previous {
				return report, fmt.Errorf(
					"invalid blob record after %q: %w",
					previous, store.ErrInvariantViolation)
			}
			previous = record.SHA256
			report.DatabaseBlobs++
			if _, ok := physical[record.SHA256]; ok {
				continue
			}
			result, err := st.ReconcileBlob(
				ctx, record.BlobInfo, false, at)
			if err != nil {
				return report, fmt.Errorf(
					"reconcile missing blob %q: %w", record.SHA256, err)
			}
			report.add(result)
		}
		if len(records) < pageSize {
			return report, nil
		}
		if previous == cursor {
			return report, store.ErrInvariantViolation
		}
		cursor = previous
	}
}

func (r *BlobReconciliationReport) add(result store.BlobReconcileResult) {
	changed := result.Inserted || result.OrphanMarked ||
		result.OrphanCleared || result.MissingMarked ||
		result.MissingCleared
	if result.Inserted {
		r.InsertedOrphans++
	}
	if result.OrphanMarked {
		r.OrphansMarked++
	}
	if result.OrphanCleared {
		r.OrphansCleared++
	}
	if result.MissingMarked {
		r.MissingMarked++
	}
	if result.MissingCleared {
		r.MissingCleared++
	}
	if !changed {
		r.Unchanged++
	}
}
