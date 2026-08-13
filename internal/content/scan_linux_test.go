//go:build linux

package content

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// mkFile writes one file below root, creating parents.
func mkFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return full
}

func scannedPaths(report ScanReport) []string {
	paths := make([]string, 0, len(report.Files))
	for _, file := range report.Files {
		paths = append(paths, file.RelativePath)
	}
	return paths
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestScanCollectsPublicationsInDeterministicOrder(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "Zadie Smith/NW.epub", "nw")
	mkFile(t, root, "Ada Lovelace/Notes.EPUB", "notes")
	mkFile(t, root, "Ada Lovelace/cover.jpg", "not a book")
	mkFile(t, root, "loose.epub", "loose")

	report, err := ScanWatchedRoot(context.Background(), root, ScanLimits{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !report.Complete {
		t.Fatalf("expected a complete sweep, got %+v", report)
	}
	want := []string{
		"Ada Lovelace/Notes.EPUB", "Zadie Smith/NW.epub", "loose.epub",
	}
	if got := scannedPaths(report); !equalStrings(got, want) {
		t.Fatalf("scanned %v, want %v", got, want)
	}
	if report.Skipped != 1 {
		t.Fatalf("expected the jpg to be skipped, got %d", report.Skipped)
	}
	for _, file := range report.Files {
		if file.SizeBytes <= 0 || file.ModifiedAt.IsZero() {
			t.Fatalf("file %q has no usable stat: %+v", file.RelativePath, file)
		}
	}
}

// A symlink is skipped by explicit policy, whether it points inside the
// tree, outside it, or at nothing. Following one that points back inside
// would be a second path to a file the sweep already has; following one
// that points outside would leave the root the administrator configured.
func TestScanSkipsSymlinks(t *testing.T) {
	outside := t.TempDir()
	mkFile(t, outside, "secret.epub", "not yours")
	root := t.TempDir()
	real := mkFile(t, root, "real.epub", "real")

	for name, target := range map[string]string{
		"inside.epub":  real,
		"outside.epub": filepath.Join(outside, "secret.epub"),
		"broken.epub":  filepath.Join(root, "nothing-here.epub"),
	} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatalf("symlink %s: %v", name, err)
		}
	}
	// A symlinked *directory* must not be descended into either.
	if err := os.Symlink(outside, filepath.Join(root, "elsewhere")); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}

	report, err := ScanWatchedRoot(context.Background(), root, ScanLimits{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !report.Complete {
		t.Fatalf("skipping links is not incompleteness: %+v", report)
	}
	if got := scannedPaths(report); !equalStrings(got, []string{"real.epub"}) {
		t.Fatalf("scanned %v, want only the real file", got)
	}
	if report.Symlinks != 4 {
		t.Fatalf("expected 4 skipped links, got %d", report.Symlinks)
	}
}

// A traversal that hits a limit must report itself incomplete, because an
// incomplete sweep is the one thing that may not conclude a book is gone.
func TestScanReportsIncompleteWhenBounded(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "a.epub", "a")
	mkFile(t, root, "b.epub", "b")
	mkFile(t, root, "c.epub", "c")

	report, err := ScanWatchedRoot(
		context.Background(), root, ScanLimits{MaxFiles: 2})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.Complete {
		t.Fatal("a sweep that stopped at its file limit claimed to be complete")
	}
	if len(report.Files) != 2 {
		t.Fatalf("expected the limit to be respected, got %d", len(report.Files))
	}

	deep := t.TempDir()
	mkFile(t, deep, "one/two/three/deep.epub", "deep")
	report, err = ScanWatchedRoot(
		context.Background(), deep, ScanLimits{MaxDepth: 1})
	if err != nil {
		t.Fatalf("scan deep: %v", err)
	}
	if report.Complete {
		t.Fatal("a sweep that stopped at its depth limit claimed to be complete")
	}
	if len(report.Files) != 0 {
		t.Fatalf("expected nothing within the depth limit, got %v",
			scannedPaths(report))
	}
}

// A root that is not there is not a sweep that found nothing. An unmounted
// volume and a deleted library look identical from here, and only one of
// them is a reason to take a catalog away.
func TestScanMissingRootIsNotAnEmptySweep(t *testing.T) {
	report, err := ScanWatchedRoot(context.Background(),
		filepath.Join(t.TempDir(), "never-existed"), ScanLimits{})
	if err == nil {
		t.Fatal("expected a missing root to be an error")
	}
	if report.Complete {
		t.Fatal("a root that could not be opened reported a complete sweep")
	}
}

// A root that exists and is empty *is* a completed sweep. This is the case
// that legitimately marks a library's books missing, and it must be
// distinguishable from the one above.
func TestScanEmptyRootIsACompleteSweep(t *testing.T) {
	report, err := ScanWatchedRoot(context.Background(), t.TempDir(), ScanLimits{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !report.Complete || len(report.Files) != 0 {
		t.Fatalf("expected a complete, empty sweep, got %+v", report)
	}
}

// A directory renamed out from under the traversal must not redirect it.
// Each level is entered through its own descriptor, so the sweep stays on
// the directory it is already inside; what it must never do is follow the
// name to whatever now answers to it.
func TestScanFollowsDescriptorsNotNamesAcrossAnAncestorSwap(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "shelf/book.epub", "book")
	decoy := filepath.Join(root, "decoy")
	mkFile(t, root, "decoy/impostor.epub", "impostor")

	shelf, err := os.OpenRoot(filepath.Join(root, "shelf"))
	if err != nil {
		t.Fatalf("open shelf: %v", err)
	}
	defer shelf.Close()

	// Swap the directory the descriptor refers to for an unrelated one.
	if err := os.Rename(filepath.Join(root, "shelf"),
		filepath.Join(root, "shelf-moved")); err != nil {
		t.Fatalf("rename shelf: %v", err)
	}
	if err := os.Rename(decoy, filepath.Join(root, "shelf")); err != nil {
		t.Fatalf("rename decoy: %v", err)
	}

	var report ScanReport
	report.Complete = true
	if err := scanDirectory(context.Background(), shelf, "shelf", 1,
		ScanLimits{}.withDefaults(), &report); err != nil {
		t.Fatalf("scan directory: %v", err)
	}
	if got := scannedPaths(report); !equalStrings(got, []string{"shelf/book.epub"}) {
		t.Fatalf("descriptor traversal followed the name: %v", got)
	}
}

// A rename inside the tree is not a rename as far as the catalog is
// concerned: it is one path that stopped existing and another that
// started. Proving they are the same file needs evidence the sweep does
// not have, so the scanner simply reports what is there now.
func TestScanReportsARenameAsTwoPaths(t *testing.T) {
	root := t.TempDir()
	before := mkFile(t, root, "old-name.epub", "same bytes")

	first, err := ScanWatchedRoot(context.Background(), root, ScanLimits{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := scannedPaths(first); !equalStrings(got, []string{"old-name.epub"}) {
		t.Fatalf("first scan: %v", got)
	}
	if err := os.Rename(before, filepath.Join(root, "new-name.epub")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	second, err := ScanWatchedRoot(context.Background(), root, ScanLimits{})
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if got := scannedPaths(second); !equalStrings(got, []string{"new-name.epub"}) {
		t.Fatalf("second scan: %v", got)
	}
	if !second.Complete {
		t.Fatal("a rename should not make a sweep incomplete")
	}
}

// An unreadable directory clears Complete rather than failing the sweep:
// the rest of the library is still worth scanning, but nothing may be
// concluded absent from a traversal that could not see all of it.
func TestScanUnreadableDirectoryClearsComplete(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := t.TempDir()
	mkFile(t, root, "readable/book.epub", "book")
	locked := filepath.Join(root, "locked")
	mkFile(t, root, "locked/hidden.epub", "hidden")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	report, err := ScanWatchedRoot(context.Background(), root, ScanLimits{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.Complete {
		t.Fatal("a sweep that could not read a directory claimed to be complete")
	}
	if report.Unreadable == 0 {
		t.Fatal("expected the unreadable directory to be counted")
	}
	if got := scannedPaths(report); !equalStrings(got, []string{"readable/book.epub"}) {
		t.Fatalf("expected the readable side to still be scanned, got %v", got)
	}
}

// The traversal must never write below the root. This walks the tree
// before and after a sweep and insists nothing changed, which is the
// acceptance criterion stated as a test rather than as a promise.
func TestScanNeverMutatesTheRoot(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "a/one.epub", "one")
	mkFile(t, root, "a/b/two.epub", "two")
	mkFile(t, root, "notes.txt", "notes")

	snapshot := func() map[string]string {
		seen := map[string]string{}
		if err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			stat := info.Sys().(*syscall.Stat_t)
			seen[rel] = info.Mode().String() +
				"|" + info.ModTime().UTC().String() +
				"|" + string(rune(stat.Ino))
			return nil
		}); err != nil {
			t.Fatalf("walk: %v", err)
		}
		return seen
	}

	before := snapshot()
	if _, err := ScanWatchedRoot(
		context.Background(), root, ScanLimits{}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	after := snapshot()
	if len(before) != len(after) {
		t.Fatalf("the sweep changed the tree: %d entries became %d",
			len(before), len(after))
	}
	for name, was := range before {
		if after[name] != was {
			t.Fatalf("the sweep modified %q: %q became %q", name, was, after[name])
		}
	}
}

func TestScanStopsOnCancellation(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "a.epub", "a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report, err := ScanWatchedRoot(ctx, root, ScanLimits{})
	if err == nil {
		t.Fatal("expected cancellation to be reported")
	}
	if report.Complete {
		t.Fatal("a cancelled sweep claimed to be complete")
	}
}
