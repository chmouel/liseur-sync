//go:build linux

package content

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCoverMissesBeforeAnythingIsRendered(t *testing.T) {
	cas := openTestCAS(t)
	digest := digestOf([]byte("book"))

	_, _, err := cas.OpenCover(context.Background(), digest, "thumbnail")
	if !errors.Is(err, ErrStageMissing) {
		t.Fatalf("cover of an unrendered blob: got %v, want ErrStageMissing", err)
	}
}

func TestStoredCoverComesBackByteForByte(t *testing.T) {
	cas := openTestCAS(t)
	ctx := context.Background()
	digest := digestOf([]byte("book"))
	rendered := []byte("\xff\xd8\xff\xe0 rendered jpeg bytes")

	if err := cas.StoreCover(ctx, digest, "thumbnail", rendered); err != nil {
		t.Fatal(err)
	}
	file, size, err := cas.OpenCover(ctx, digest, "thumbnail")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if size != int64(len(rendered)) {
		t.Errorf("size: got %d, want %d", size, len(rendered))
	}
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(rendered) {
		t.Errorf("bytes: got %q, want %q", got, rendered)
	}
}

// A thumbnail and a full-size cover of the same book are different
// pictures. Reading one back as the other would put the wrong image on
// every page that asks for the other size.
func TestVariantsOfOneBlobDoNotOverwriteEachOther(t *testing.T) {
	cas := openTestCAS(t)
	ctx := context.Background()
	digest := digestOf([]byte("book"))

	if err := cas.StoreCover(ctx, digest, "thumbnail", []byte("small")); err != nil {
		t.Fatal(err)
	}
	if err := cas.StoreCover(ctx, digest, "full", []byte("large picture")); err != nil {
		t.Fatal(err)
	}
	for variant, want := range map[string]string{"thumbnail": "small", "full": "large picture"} {
		file, _, err := cas.OpenCover(ctx, digest, variant)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s: got %q, want %q", variant, got, want)
		}
	}
}

// Two books' covers must not collide, which they would if the cache keyed
// on anything shorter than the whole digest.
func TestCoversOfDifferentBlobsAreSeparate(t *testing.T) {
	cas := openTestCAS(t)
	ctx := context.Background()
	first := digestOf([]byte("first"))
	second := digestOf([]byte("second"))

	if err := cas.StoreCover(ctx, first, "thumbnail", []byte("first cover")); err != nil {
		t.Fatal(err)
	}
	if err := cas.StoreCover(ctx, second, "thumbnail", []byte("second cover")); err != nil {
		t.Fatal(err)
	}
	file, _, err := cas.OpenCover(ctx, first, "thumbnail")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first cover" {
		t.Errorf("got %q, want the first blob's cover", got)
	}
}

// Re-rendering must replace rather than append or fail, so a cover written
// by an older version of the renderer can be corrected in place.
func TestStoringACoverTwiceReplacesIt(t *testing.T) {
	cas := openTestCAS(t)
	ctx := context.Background()
	digest := digestOf([]byte("book"))

	if err := cas.StoreCover(ctx, digest, "thumbnail", []byte("older longer bytes")); err != nil {
		t.Fatal(err)
	}
	if err := cas.StoreCover(ctx, digest, "thumbnail", []byte("newer")); err != nil {
		t.Fatal(err)
	}
	file, size, err := cas.OpenCover(ctx, digest, "thumbnail")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "newer" || size != int64(len("newer")) {
		t.Errorf("got %q (%d bytes), want the replacement", got, size)
	}
}

func TestMarkedAbsentCoverIsReportedForEveryVariant(t *testing.T) {
	cas := openTestCAS(t)
	ctx := context.Background()
	digest := digestOf([]byte("book"))

	if err := cas.MarkCoverAbsent(ctx, digest); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"thumbnail", "full"} {
		_, _, err := cas.OpenCover(ctx, digest, variant)
		if !errors.Is(err, ErrNoCover) {
			t.Errorf("%s: got %v, want ErrNoCover", variant, err)
		}
	}
}

// The marker is a fact about the publication, so it must not make some
// other book's cover unreachable.
func TestMarkingOneBlobAbsentLeavesOthersAlone(t *testing.T) {
	cas := openTestCAS(t)
	ctx := context.Background()
	absent := digestOf([]byte("no cover here"))
	present := digestOf([]byte("has a cover"))

	if err := cas.StoreCover(ctx, present, "thumbnail", []byte("cover")); err != nil {
		t.Fatal(err)
	}
	if err := cas.MarkCoverAbsent(ctx, absent); err != nil {
		t.Fatal(err)
	}
	file, _, err := cas.OpenCover(ctx, present, "thumbnail")
	if err != nil {
		t.Fatalf("cover of an unrelated blob: %v", err)
	}
	file.Close()
}

func TestRemovingCoversClearsEveryVariantAndTheMarker(t *testing.T) {
	cas := openTestCAS(t)
	ctx := context.Background()
	digest := digestOf([]byte("book"))

	if err := cas.StoreCover(ctx, digest, "thumbnail", []byte("small")); err != nil {
		t.Fatal(err)
	}
	if err := cas.StoreCover(ctx, digest, "full", []byte("large")); err != nil {
		t.Fatal(err)
	}
	if err := cas.RemoveCovers(ctx, digest); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"thumbnail", "full"} {
		if _, _, err := cas.OpenCover(ctx, digest, variant); !errors.Is(err, ErrStageMissing) {
			t.Errorf("%s after removal: got %v, want ErrStageMissing", variant, err)
		}
	}
	if err := cas.MarkCoverAbsent(ctx, digest); err != nil {
		t.Fatal(err)
	}
	if err := cas.RemoveCovers(ctx, digest); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cas.OpenCover(ctx, digest, "thumbnail"); !errors.Is(err, ErrNoCover) &&
		!errors.Is(err, ErrStageMissing) {
		t.Errorf("unexpected error after removal: %v", err)
	}
	if _, _, err := cas.OpenCover(ctx, digest, "thumbnail"); errors.Is(err, ErrNoCover) {
		t.Error("the absent marker survived RemoveCovers")
	}
}

func TestRemovingCoversOfABlobThatHasNoneSucceeds(t *testing.T) {
	cas := openTestCAS(t)
	if err := cas.RemoveCovers(context.Background(), digestOf([]byte("book"))); err != nil {
		t.Fatalf("removing nothing: %v", err)
	}
}

// The variant becomes a path element, so anything that is not one of the
// names this server chose has to be refused rather than sanitized.
func TestCoverVariantsAreAnAllowlist(t *testing.T) {
	cas := openTestCAS(t)
	ctx := context.Background()
	digest := digestOf([]byte("book"))

	for _, variant := range []string{
		"", "..", "../../etc", "thumb/nail", "thumbnail.jpg", "Thumbnail",
		"thumbnail1", "absent", strings.Repeat("a", 17),
	} {
		if err := cas.StoreCover(ctx, digest, variant, []byte("x")); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("storing %q: got %v, want ErrUnsafePath", variant, err)
		}
		if _, _, err := cas.OpenCover(ctx, digest, variant); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("opening %q: got %v, want ErrUnsafePath", variant, err)
		}
	}
}

func TestCoverDigestsAreValidated(t *testing.T) {
	cas := openTestCAS(t)
	ctx := context.Background()

	for _, digest := range []string{"", "../../etc/passwd", "ZZZZ", strings.Repeat("a", 63)} {
		if err := cas.StoreCover(ctx, digest, "thumbnail", []byte("x")); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("storing for %q: got %v, want ErrUnsafePath", digest, err)
		}
		if _, _, err := cas.OpenCover(ctx, digest, "thumbnail"); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("opening for %q: got %v, want ErrUnsafePath", digest, err)
		}
		if err := cas.MarkCoverAbsent(ctx, digest); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("marking %q: got %v, want ErrUnsafePath", digest, err)
		}
		if err := cas.RemoveCovers(ctx, digest); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("removing %q: got %v, want ErrUnsafePath", digest, err)
		}
	}
}

// Covers are a cache, not content. Keeping them under sha256/ would put
// regenerable files inside the tree backups and integrity checks treat as
// the thing being protected.
func TestCoversLiveOutsideTheBlobTree(t *testing.T) {
	cas := openTestCAS(t)
	digest := digestOf([]byte("book"))

	if err := cas.StoreCover(context.Background(), digest, "thumbnail", []byte("x")); err != nil {
		t.Fatal(err)
	}
	blobTree := filepath.Join(cas.Root(), "sha256")
	found := false
	err := filepath.WalkDir(blobTree, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.Contains(path, "thumbnail") {
			found = true
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if found {
		t.Error("a cached cover was written inside the blob tree")
	}
	if _, err := os.Stat(filepath.Join(cas.Root(), "covers")); err != nil {
		t.Errorf("covers directory: %v", err)
	}
}

func TestCoverOperationsObserveCancellation(t *testing.T) {
	cas := openTestCAS(t)
	digest := digestOf([]byte("book"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := cas.StoreCover(ctx, digest, "thumbnail", []byte("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("StoreCover: got %v, want context.Canceled", err)
	}
	if err := cas.MarkCoverAbsent(ctx, digest); !errors.Is(err, context.Canceled) {
		t.Errorf("MarkCoverAbsent: got %v, want context.Canceled", err)
	}
	if err := cas.RemoveCovers(ctx, digest); !errors.Is(err, context.Canceled) {
		t.Errorf("RemoveCovers: got %v, want context.Canceled", err)
	}
	if _, _, err := cas.OpenCover(ctx, digest, "thumbnail"); !errors.Is(err, context.Canceled) {
		t.Errorf("OpenCover: got %v, want context.Canceled", err)
	}
}

func TestStoringAnEmptyCoverIsRefused(t *testing.T) {
	cas := openTestCAS(t)
	digest := digestOf([]byte("book"))

	err := cas.StoreCover(context.Background(), digest, "thumbnail", nil)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("got %v, want ErrUnsafePath", err)
	}
	if _, _, err := cas.OpenCover(context.Background(), digest, "thumbnail"); !errors.Is(err, ErrStageMissing) {
		t.Errorf("after refusing an empty cover: got %v, want ErrStageMissing", err)
	}
}
