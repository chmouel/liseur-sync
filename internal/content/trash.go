//go:build linux

package content

import (
	"context"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

type trashPurgeStore interface {
	PurgeExpiredTrash(
		context.Context, time.Time, int) (store.TrashPurgeResult, error)
}

// maxTrashPurgePasses bounds one maintenance tick. Unlike availability,
// which loops until nothing changes, deletion stops at a ceiling on
// purpose: whatever is left is still past its retention window and will
// be taken by the next tick, and nothing is served from the trash in the
// meantime. Deleting a bounded amount per tick keeps one enormous purge
// from monopolising the database.
const maxTrashPurgePasses = 100

// TrashPurgeReport totals the deletions made by one tick.
type TrashPurgeReport struct {
	store.TrashPurgeResult
	Passes int
}

// PurgeExpiredTrash permanently deletes the books whose retention window
// closed before `at`, releasing quota and handing their blobs to the
// orphan sweep. It is the only routine in the server that destroys a
// user's content, so it does exactly what the retention window says and
// nothing more: a book still inside its window is not its business, and
// neither is a book nobody trashed.
func PurgeExpiredTrash(
	ctx context.Context,
	st trashPurgeStore,
	at time.Time,
	pageSize int,
) (TrashPurgeReport, error) {
	var report TrashPurgeReport
	if st == nil || at.IsZero() || pageSize < 1 || pageSize > 500 {
		return report, store.ErrInvalidTransition
	}
	at = at.UTC()
	for report.Passes < maxTrashPurgePasses {
		result, err := st.PurgeExpiredTrash(ctx, at, pageSize)
		if err != nil {
			return report, fmt.Errorf("purge expired trash: %w", err)
		}
		report.Passes++
		report.BookIDs = append(report.BookIDs, result.BookIDs...)
		report.FilesPurged += result.FilesPurged
		report.ReservationsReleased += result.ReservationsReleased
		report.BlobsOrphaned += result.BlobsOrphaned
		if len(result.BookIDs) < pageSize {
			break
		}
	}
	return report, nil
}
