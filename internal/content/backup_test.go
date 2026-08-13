//go:build linux

package content

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

type referencedBlobStoreFake struct {
	blobs     []store.BlobInfo
	err       error
	listAfter []string
}

func (f *referencedBlobStoreFake) ListReferencedBlobs(
	_ context.Context,
	after string,
	limit int,
) ([]store.BlobInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.listAfter = append(f.listAfter, after)
	var out []store.BlobInfo
	for _, blob := range f.blobs {
		if blob.SHA256 > after {
			out = append(out, blob)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SHA256 < out[j].SHA256 })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func digest(c string) string { return strings.Repeat(c, 64) }

// TestVerifyBackupAcceptsACompleteBackup: a backup holding every
// referenced blob is restorable, and content the database does not
// reference does not make it otherwise. That last part is the point of
// the rule: after a restore those blobs are exactly what ordinary
// grace-period reconciliation deals with.
func TestVerifyBackupAcceptsACompleteBackup(t *testing.T) {
	st := &referencedBlobStoreFake{blobs: []store.BlobInfo{
		{SHA256: digest("a"), SizeBytes: 10},
		{SHA256: digest("b"), SizeBytes: 20},
	}}
	inventory := &blobInventoryFake{blobs: []Blob{
		{SHA256: digest("a"), Size: 10},
		{SHA256: digest("b"), Size: 20},
		{SHA256: digest("c"), Size: 30},
	}}
	report, err := VerifyBackup(context.Background(), st, inventory, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid() {
		t.Fatalf("complete backup reported invalid: %+v", report)
	}
	if report.ReferencedBlobs != 2 || report.PresentBlobs != 2 ||
		report.ExtraBlobs != 1 || len(report.Problems) != 0 {
		t.Fatalf("report = %+v", report)
	}
}

// TestVerifyBackupFindsEveryMissingReferencedBlob is the acceptance
// criterion from the ADR. A backup that is missing one blob out of
// thousands is not "mostly fine": the book it belongs to cannot be read
// after a restore, and the operator has to be told which one.
func TestVerifyBackupFindsEveryMissingReferencedBlob(t *testing.T) {
	st := &referencedBlobStoreFake{blobs: []store.BlobInfo{
		{SHA256: digest("a"), SizeBytes: 10},
		{SHA256: digest("b"), SizeBytes: 20},
		{SHA256: digest("c"), SizeBytes: 30},
	}}
	inventory := &blobInventoryFake{blobs: []Blob{
		{SHA256: digest("b"), Size: 20},
	}}
	report, err := VerifyBackup(context.Background(), st, inventory, 50)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid() {
		t.Fatal("backup missing two blobs reported valid")
	}
	if report.MissingBlobs != 2 || report.PresentBlobs != 1 ||
		report.ReferencedBlobs != 3 {
		t.Fatalf("report = %+v", report)
	}
	found := map[string]bool{}
	for _, p := range report.Problems {
		found[p.SHA256] = true
	}
	if !found[digest("a")] || !found[digest("c")] {
		t.Fatalf("problems did not name both missing blobs: %+v", report.Problems)
	}
}

// TestVerifyBackupRejectsABlobOfTheWrongSize covers the copy that looks
// complete because every filename is there. A blob whose size disagrees
// with the database is not the file the database recorded, and restoring
// it would serve bytes nobody uploaded.
func TestVerifyBackupRejectsABlobOfTheWrongSize(t *testing.T) {
	st := &referencedBlobStoreFake{blobs: []store.BlobInfo{
		{SHA256: digest("a"), SizeBytes: 10},
	}}
	inventory := &blobInventoryFake{blobs: []Blob{
		{SHA256: digest("a"), Size: 9},
	}}
	report, err := VerifyBackup(context.Background(), st, inventory, 50)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid() {
		t.Fatal("truncated blob reported valid")
	}
	if report.MismatchedBlobs != 1 || report.PresentBlobs != 0 {
		t.Fatalf("report = %+v", report)
	}
	// It is referenced, so it must not also be reported as content the
	// database does not know about.
	if report.ExtraBlobs != 0 {
		t.Fatalf("mismatched blob counted as extra: %+v", report)
	}
	if len(report.Problems) != 1 ||
		!strings.Contains(report.Problems[0].Detail, "9 bytes") {
		t.Fatalf("problem does not say what is wrong: %+v", report.Problems)
	}
}

// TestVerifyBackupPagesThroughEveryBlob: the whole point is completeness,
// so a database with more referenced blobs than fit in one page must not
// be verified only as far as the first page.
func TestVerifyBackupPagesThroughEveryBlob(t *testing.T) {
	var refs []store.BlobInfo
	var have []Blob
	for _, c := range "abcdef012" {
		refs = append(refs, store.BlobInfo{
			SHA256: digest(string(c)), SizeBytes: 10,
		})
		if c != '1' {
			have = append(have, Blob{SHA256: digest(string(c)), Size: 10})
		}
	}
	st := &referencedBlobStoreFake{blobs: refs}
	report, err := VerifyBackup(
		context.Background(), st, &blobInventoryFake{blobs: have}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReferencedBlobs != 9 || report.MissingBlobs != 1 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Problems) != 1 || report.Problems[0].SHA256 != digest("1") {
		t.Fatalf("problems = %+v", report.Problems)
	}
	if len(st.listAfter) < 4 {
		t.Fatalf("did not page: %v", st.listAfter)
	}
}

// TestVerifyBackupCapsTheProblemListButNotTheCount: a wholly wrong
// backup must still report an exact count, because "50 problems" and
// "50000 problems" call for different responses.
func TestVerifyBackupCapsTheProblemListButNotTheCount(t *testing.T) {
	// Digests must be distinct and ordered, so they are built from the
	// index rather than repeated characters.
	var refs []store.BlobInfo
	for i := range maxBackupProblemsReported + 10 {
		refs = append(refs, store.BlobInfo{
			SHA256: strings.Repeat("0", 60) + hex4(i), SizeBytes: 10,
		})
	}
	st := &referencedBlobStoreFake{blobs: refs}
	report, err := VerifyBackup(
		context.Background(), st, &blobInventoryFake{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if report.MissingBlobs != len(refs) {
		t.Fatalf("count = %d, want %d", report.MissingBlobs, len(refs))
	}
	if len(report.Problems) != maxBackupProblemsReported {
		t.Fatalf("problem list = %d", len(report.Problems))
	}
}

func hex4(i int) string {
	const digits = "0123456789abcdef"
	return string([]byte{
		digits[(i>>12)&0xf], digits[(i>>8)&0xf],
		digits[(i>>4)&0xf], digits[i&0xf],
	})
}

func TestVerifyBackupRejectsBadArguments(t *testing.T) {
	st := &referencedBlobStoreFake{}
	inventory := &blobInventoryFake{}
	for name, call := range map[string]func() error{
		"no store": func() error {
			_, err := VerifyBackup(context.Background(), nil, inventory, 50)
			return err
		},
		"no inventory": func() error {
			_, err := VerifyBackup(context.Background(), st, nil, 50)
			return err
		},
		"zero page": func() error {
			_, err := VerifyBackup(context.Background(), st, inventory, 0)
			return err
		},
		"huge page": func() error {
			_, err := VerifyBackup(context.Background(), st, inventory, 501)
			return err
		},
	} {
		if err := call(); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// TestVerifyBackupRefusesADuplicateInventory: two files claiming the same
// digest mean the inventory cannot be trusted to say what is present, and
// guessing which one is real is worse than refusing.
func TestVerifyBackupRefusesADuplicateInventory(t *testing.T) {
	st := &referencedBlobStoreFake{}
	inventory := &blobInventoryFake{blobs: []Blob{
		{SHA256: digest("a"), Size: 10},
		{SHA256: digest("a"), Size: 11},
	}}
	if _, err := VerifyBackup(
		context.Background(), st, inventory, 50,
	); !errors.Is(err, store.ErrInvariantViolation) {
		t.Fatalf("duplicate inventory accepted: %v", err)
	}
}

func TestVerifyBackupPropagatesStoreErrors(t *testing.T) {
	sentinel := errors.New("database is gone")
	if _, err := VerifyBackup(context.Background(),
		&referencedBlobStoreFake{err: sentinel}, &blobInventoryFake{}, 50,
	); !errors.Is(err, sentinel) {
		t.Fatalf("store error swallowed: %v", err)
	}
	if _, err := VerifyBackup(context.Background(),
		&referencedBlobStoreFake{}, &blobInventoryFake{err: sentinel}, 50,
	); !errors.Is(err, sentinel) {
		t.Fatalf("inventory error swallowed: %v", err)
	}
}
