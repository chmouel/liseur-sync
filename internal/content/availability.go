//go:build linux

package content

import (
	"context"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

type catalogAvailabilityStore interface {
	ReconcileCatalogAvailability(
		context.Context, time.Time, int) (store.CatalogAvailabilityResult, error)
}

// maxAvailabilityPasses bounds the loop so that a store that reports work
// it did not do cannot spin the maintenance goroutine forever. Each pass
// changes at least one row, so the ceiling is a row count, not a guess.
const maxAvailabilityPasses = 10_000

// CatalogAvailabilityReport totals one complete reconciliation, run to
// convergence.
type CatalogAvailabilityReport struct {
	store.CatalogAvailabilityResult
	Passes int
}

// ReconcileCatalogAvailability propagates the blob presence recorded by
// ReconcileBlobInventory into the catalog, so that a book whose bytes are
// gone stops being offered for download. It must run after the inventory
// pass, which is what establishes presence in the first place.
func ReconcileCatalogAvailability(
	ctx context.Context,
	st catalogAvailabilityStore,
	at time.Time,
	pageSize int,
) (CatalogAvailabilityReport, error) {
	var report CatalogAvailabilityReport
	if st == nil || at.IsZero() || pageSize < 1 || pageSize > 500 {
		return report, store.ErrInvalidTransition
	}
	at = at.UTC()
	for {
		result, err := st.ReconcileCatalogAvailability(ctx, at, pageSize)
		if err != nil {
			return report, fmt.Errorf("reconcile catalog availability: %w", err)
		}
		report.Passes++
		report.FilesMarkedMissing += result.FilesMarkedMissing
		report.FilesMarkedAvailable += result.FilesMarkedAvailable
		report.BooksMarkedMissing += result.BooksMarkedMissing
		report.BooksMarkedActive += result.BooksMarkedActive
		if !result.Changed() {
			return report, nil
		}
		if report.Passes >= maxAvailabilityPasses {
			return report, fmt.Errorf(
				"catalog availability did not converge in %d passes: %w",
				maxAvailabilityPasses, store.ErrInvariantViolation)
		}
	}
}
