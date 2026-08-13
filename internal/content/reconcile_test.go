//go:build linux

package content

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

type blobInventoryFake struct {
	blobs []Blob
	err   error
}

func (f *blobInventoryFake) ListBlobs(context.Context) ([]Blob, error) {
	return append([]Blob(nil), f.blobs...), f.err
}

type blobReconciliationStoreFake struct {
	records   []store.BlobRecord
	results   map[string]store.BlobReconcileResult
	calls     []string
	listAfter []string
}

func (f *blobReconciliationStoreFake) ListBlobRecords(
	_ context.Context,
	after string,
	limit int,
) ([]store.BlobRecord, error) {
	f.listAfter = append(f.listAfter, after)
	var records []store.BlobRecord
	for _, record := range f.records {
		if record.SHA256 > after {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].SHA256 < records[j].SHA256
	})
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func (f *blobReconciliationStoreFake) ReconcileBlob(
	_ context.Context,
	blob store.BlobInfo,
	present bool,
	_ time.Time,
) (store.BlobReconcileResult, error) {
	key := fmt.Sprintf("%s:%t", blob.SHA256, present)
	f.calls = append(f.calls, key)
	return f.results[key], nil
}

func TestReconcileBlobInventoryPhysicalFirstAndPaginated(t *testing.T) {
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	c := strings.Repeat("c", 64)
	d := strings.Repeat("d", 64)
	inventory := &blobInventoryFake{blobs: []Blob{
		{SHA256: b, Size: 2},
		{SHA256: a, Size: 1},
	}}
	st := &blobReconciliationStoreFake{
		records: []store.BlobRecord{
			{BlobInfo: store.BlobInfo{SHA256: a, SizeBytes: 1}},
			{BlobInfo: store.BlobInfo{SHA256: c, SizeBytes: 3}},
			{BlobInfo: store.BlobInfo{SHA256: d, SizeBytes: 4}},
		},
		results: map[string]store.BlobReconcileResult{
			a + ":true":  {MissingCleared: true},
			b + ":true":  {Inserted: true, OrphanMarked: true},
			c + ":false": {MissingMarked: true},
			d + ":false": {},
		},
	}
	report, err := ReconcileBlobInventory(
		context.Background(), st, inventory, time.Now().UTC(), 2)
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		a + ":true", b + ":true", c + ":false", d + ":false",
	}
	if fmt.Sprint(st.calls) != fmt.Sprint(wantCalls) {
		t.Fatalf("reconciliation calls: got %v want %v", st.calls, wantCalls)
	}
	if report.PhysicalBlobs != 2 || report.DatabaseBlobs != 3 ||
		report.InsertedOrphans != 1 || report.OrphansMarked != 1 ||
		report.MissingMarked != 1 || report.MissingCleared != 1 ||
		report.Unchanged != 1 {
		t.Fatalf("reconciliation report: %+v", report)
	}
	if len(st.listAfter) != 2 || st.listAfter[0] != "" ||
		st.listAfter[1] != c {
		t.Fatalf("pagination cursors: %v", st.listAfter)
	}
}

func TestReconcileBlobInventoryRejectsInvalidInputs(t *testing.T) {
	now := time.Now().UTC()
	st := &blobReconciliationStoreFake{}
	inventory := &blobInventoryFake{}
	for name, run := range map[string]func() error{
		"nil store": func() error {
			_, err := ReconcileBlobInventory(
				context.Background(), nil, inventory, now, 10)
			return err
		},
		"nil inventory": func() error {
			_, err := ReconcileBlobInventory(
				context.Background(), st, nil, now, 10)
			return err
		},
		"zero time": func() error {
			_, err := ReconcileBlobInventory(
				context.Background(), st, inventory, time.Time{}, 10)
			return err
		},
		"bad page": func() error {
			_, err := ReconcileBlobInventory(
				context.Background(), st, inventory, now, 0)
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
