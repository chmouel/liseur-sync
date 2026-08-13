//go:build linux

package content

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/epub"
	"golang.org/x/sys/unix"
)

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func openTestCAS(t *testing.T) *CAS {
	t.Helper()
	cas, err := Open(filepath.Join(t.TempDir(), "content"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cas.Close(); err != nil {
			t.Error(err)
		}
	})
	return cas
}

func minimalEPUB(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	add := func(name, body string, method uint16) {
		t.Helper()
		header := &zip.FileHeader{Name: name, Method: method}
		target, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", "application/epub+zip", zip.Store)
	add("META-INF/container.xml",
		`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">`+
			`<rootfiles><rootfile full-path="OPS/book.opf"`+
			` media-type="application/oebps-package+xml"/>`+
			`</rootfiles></container>`,
		zip.Deflate)
	add("OPS/book.opf",
		`<package xmlns="http://www.idpf.org/2007/opf">`+
			`<metadata/><manifest/></package>`,
		zip.Deflate)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestStageAndPromote(t *testing.T) {
	cas := openTestCAS(t)
	data := []byte("hello epub")
	staged, err := cas.Stage(context.Background(), "job-1", bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}

	if staged.SHA256 != digestOf(data) || staged.Size != int64(len(data)) ||
		filepath.Dir(filepath.FromSlash(staged.Path)) != ".incoming" {
		t.Fatalf("staged blob: %+v", staged)
	}
	stagePath := filepath.Join(cas.Root(), filepath.FromSlash(staged.Path))
	if info, err := os.Lstat(stagePath); err != nil || !info.Mode().IsRegular() ||
		info.Mode().Perm() != 0o400 {
		t.Fatalf("staged file: %+v %v", info, err)
	}
	if location, err := cas.InspectArtifact(context.Background(),
		staged.Path, staged.SHA256, staged.Size); err != nil ||
		location != ArtifactStaged {
		t.Fatalf("inspect staged artifact: %q %v", location, err)
	}

	blob, err := cas.Promote(context.Background(), staged.Path, staged.SHA256, staged.Size)
	if err != nil {
		t.Fatal(err)
	}
	expectedPath := filepath.ToSlash(filepath.Join(
		"sha256", staged.SHA256[:2], staged.SHA256[2:], "file.epub"))
	if blob.Path != expectedPath || blob.AlreadyPresent {
		t.Fatalf("promoted blob: %+v", blob)
	}
	if _, err := os.Lstat(stagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stage still exists after promotion: %v", err)
	}
	finalPath := filepath.Join(cas.Root(), filepath.FromSlash(blob.Path))
	got, err := os.ReadFile(finalPath)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("final content: %q %v", got, err)
	}
	if info, err := os.Lstat(finalPath); err != nil || info.Mode().Perm() != 0o400 {
		t.Fatalf("final mode: %+v %v", info, err)
	}
	if location, err := cas.InspectArtifact(context.Background(),
		staged.Path, staged.SHA256, staged.Size); err != nil ||
		location != ArtifactPromoted {
		t.Fatalf("inspect promoted artifact: %q %v", location, err)
	}

	replayed, err := cas.Promote(context.Background(), staged.Path, staged.SHA256, staged.Size)
	if err != nil || !replayed.AlreadyPresent || replayed.Path != blob.Path {
		t.Fatalf("lost-result replay: %+v %v", replayed, err)
	}
}

func TestValidateEPUBArtifactFromStageAndFinal(t *testing.T) {
	cas := openTestCAS(t)
	data := minimalEPUB(t)
	staged, err := cas.Stage(
		context.Background(), "validate-epub",
		bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	result, location, err := cas.ValidateEPUBArtifact(
		context.Background(), staged.Path, staged.SHA256, staged.Size,
		epub.DefaultLimits())
	if err != nil || location != ArtifactStaged ||
		result.PackagePath != "OPS/book.opf" {
		t.Fatalf("validate staged EPUB: %+v %q %v", result, location, err)
	}
	if _, err := cas.Promote(
		context.Background(), staged.Path, staged.SHA256, staged.Size); err != nil {
		t.Fatal(err)
	}
	result, location, err = cas.ValidateEPUBArtifact(
		context.Background(), staged.Path, staged.SHA256, staged.Size,
		epub.DefaultLimits())
	if err != nil || location != ArtifactPromoted ||
		result.PackagePath != "OPS/book.opf" {
		t.Fatalf("validate promoted EPUB: %+v %q %v", result, location, err)
	}

	invalid, err := cas.Stage(
		context.Background(), "validate-invalid",
		bytes.NewReader([]byte("not an EPUB")), 11)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cas.ValidateEPUBArtifact(
		context.Background(), invalid.Path, invalid.SHA256, invalid.Size,
		epub.DefaultLimits()); err == nil {
		t.Fatal("invalid EPUB passed CAS validation")
	} else if code, ok := epub.ErrorCode(err); !ok || code != epub.CodeInvalidEPUB {
		t.Fatalf("invalid EPUB error: %v", err)
	}
}

func TestStageBoundsAndCleanup(t *testing.T) {
	cas := openTestCAS(t)
	data := []byte("hello")
	if _, err := cas.Stage(context.Background(), "exact", bytes.NewReader(data), 5); err != nil {
		t.Fatalf("exact limit rejected: %v", err)
	}
	if _, err := cas.Stage(context.Background(), "empty", bytes.NewReader(nil), 0); err != nil {
		t.Fatalf("empty zero-limit input rejected: %v", err)
	}
	if _, err := cas.Stage(context.Background(), "zero", bytes.NewReader(data), 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("nonempty zero-limit input: %v", err)
	}
	if _, err := cas.Stage(context.Background(), "large", bytes.NewReader(data), 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized input: %v", err)
	}
	largePrefix := stagePrefix("large")
	for _, suffix := range []string{".partial", ".stage"} {
		if _, err := os.Lstat(filepath.Join(cas.Root(), ".incoming", largePrefix+suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("oversized stage residue %s: %v", suffix, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cas.Stage(ctx, "canceled", bytes.NewReader(data), 5); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled stage: %v", err)
	}
	canceledPrefix := stagePrefix("canceled")
	for _, suffix := range []string{".partial", ".stage"} {
		if _, err := os.Lstat(filepath.Join(cas.Root(), ".incoming", canceledPrefix+suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("canceled stage residue %s: %v", suffix, err)
		}
	}
	if _, err := cas.Stage(context.Background(), "negative", bytes.NewReader(data), -1); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("negative limit: %v", err)
	}
}

func TestStageCollisionAndPartialRecovery(t *testing.T) {
	cas := openTestCAS(t)
	data := []byte("same job")
	staged, err := cas.Stage(context.Background(), "job", bytes.NewReader(data), 100)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := cas.Stage(context.Background(), "job", bytes.NewReader([]byte("ignored")), 100)
	if err != nil || replayed != staged {
		t.Fatalf("completed stage replay: %+v %v", replayed, err)
	}
	if _, err := cas.Stage(context.Background(), "job", bytes.NewReader(nil), staged.Size-1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("replayed stage ignored tighter bound: %v", err)
	}
	if err := cas.RemoveStage(context.Background(), staged.Path); err != nil {
		t.Fatal(err)
	}
	if err := cas.RemoveStage(context.Background(), staged.Path); err != nil {
		t.Fatalf("idempotent remove: %v", err)
	}

	prefix := stagePrefix("partial")
	partialPath := filepath.Join(cas.Root(), ".incoming", prefix+".partial")
	if err := os.WriteFile(partialPath, []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := cas.Stage(context.Background(), "partial", bytes.NewReader(data), 100)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.SHA256 != digestOf(data) {
		t.Fatalf("partial recovery digest: %+v", recovered)
	}
}

func TestRestartPromotion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "content")
	first, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("restart")
	staged, err := first.Stage(context.Background(), "restart-job", bytes.NewReader(data), 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	blob, err := second.Promote(context.Background(), staged.Path, staged.SHA256, staged.Size)
	if err != nil || blob.AlreadyPresent {
		t.Fatalf("restart promotion: %+v %v", blob, err)
	}
}

func TestConcurrentPromotionDeduplicates(t *testing.T) {
	cas := openTestCAS(t)
	data := bytes.Repeat([]byte("dedup"), 4096)
	first, err := cas.Stage(context.Background(), "dedup-1", bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := cas.Stage(context.Background(), "dedup-2", bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	stages := []StagedBlob{first, second}
	results := make(chan Blob, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, staged := range stages {
		wg.Add(1)
		go func(staged StagedBlob) {
			defer wg.Done()
			blob, err := cas.Promote(context.Background(), staged.Path, staged.SHA256, staged.Size)
			if err != nil {
				errs <- err
				return
			}
			results <- blob
		}(staged)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	published, deduplicated := 0, 0
	var path string
	for result := range results {
		if path == "" {
			path = result.Path
		} else if result.Path != path {
			t.Fatalf("dedup paths differ: %q != %q", result.Path, path)
		}
		if result.AlreadyPresent {
			deduplicated++
		} else {
			published++
		}
	}
	if published != 1 || deduplicated != 1 {
		t.Fatalf("promotion outcomes: %d published, %d deduplicated", published, deduplicated)
	}
	for _, staged := range stages {
		if _, err := os.Lstat(filepath.Join(cas.Root(), filepath.FromSlash(staged.Path))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deduplicated stage remains: %v", err)
		}
	}
}

func TestListBlobsInventoriesOnlyVerifiedFinals(t *testing.T) {
	cas := openTestCAS(t)
	var want []Blob
	for _, item := range []struct {
		job  string
		data string
	}{
		{job: "inventory-b", data: "second"},
		{job: "inventory-a", data: "first"},
	} {
		staged, err := cas.Stage(context.Background(), item.job,
			bytes.NewReader([]byte(item.data)), int64(len(item.data)))
		if err != nil {
			t.Fatal(err)
		}
		blob, err := cas.Promote(
			context.Background(), staged.Path, staged.SHA256, staged.Size)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, Blob{
			Path: blob.Path, SHA256: blob.SHA256, Size: blob.Size,
		})
	}
	if _, err := cas.Stage(context.Background(), "inventory-staged",
		bytes.NewReader([]byte("not final")), 9); err != nil {
		t.Fatal(err)
	}
	sort.Slice(want, func(i, j int) bool { return want[i].SHA256 < want[j].SHA256 })
	got, err := cas.ListBlobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blob inventory: got %+v want %+v", got, want)
	}
}

func TestRemoveBlobVerifiesAndDeletesIdempotently(t *testing.T) {
	cas := openTestCAS(t)
	data := []byte("garbage collected")
	staged, err := cas.Stage(context.Background(), "remove-blob",
		bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	blob, err := cas.Promote(
		context.Background(), staged.Path, staged.SHA256, staged.Size)
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := cas.RemoveBlob(
		context.Background(), blob.SHA256, blob.Size+1); !errors.Is(err, ErrCorruptBlob) || removed {
		t.Fatalf("wrong-size removal: %v %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(
		cas.Root(), filepath.FromSlash(blob.Path))); err != nil {
		t.Fatalf("failed verification removed blob: %v", err)
	}
	if removed, err := cas.RemoveBlob(
		context.Background(), blob.SHA256, blob.Size); err != nil || !removed {
		t.Fatalf("remove blob: %v %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(
		cas.Root(), filepath.FromSlash(blob.Path))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed blob remains: %v", err)
	}
	if removed, err := cas.RemoveBlob(
		context.Background(), blob.SHA256, blob.Size); err != nil || removed {
		t.Fatalf("remove blob replay: %v %v", removed, err)
	}
	inventory, err := cas.ListBlobs(context.Background())
	if err != nil || len(inventory) != 0 {
		t.Fatalf("inventory after removal: %+v %v", inventory, err)
	}
}

func TestListBlobsRejectsCorruptionAndUnexpectedEntries(t *testing.T) {
	t.Run("corrupt blob", func(t *testing.T) {
		cas := openTestCAS(t)
		data := []byte("original")
		staged, err := cas.Stage(
			context.Background(), "inventory-corrupt", bytes.NewReader(data), 100)
		if err != nil {
			t.Fatal(err)
		}
		blob, err := cas.Promote(
			context.Background(), staged.Path, staged.SHA256, staged.Size)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(cas.Root(), filepath.FromSlash(blob.Path))
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("tampered"), 0o400); err != nil {
			t.Fatal(err)
		}
		if _, err := cas.ListBlobs(context.Background()); !errors.Is(err, ErrCorruptBlob) {
			t.Fatalf("corrupt inventory: %v", err)
		}
	})
	t.Run("unexpected entry", func(t *testing.T) {
		cas := openTestCAS(t)
		if err := os.WriteFile(
			filepath.Join(cas.Root(), "sha256", "unexpected"),
			[]byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := cas.ListBlobs(context.Background()); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("unsafe inventory: %v", err)
		}
	})
	t.Run("fifo blob", func(t *testing.T) {
		cas := openTestCAS(t)
		digest := digestOf([]byte("fifo"))
		leaf := filepath.Join(
			cas.Root(), "sha256", digest[:2], digest[2:])
		if err := os.MkdirAll(leaf, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mkfifo(filepath.Join(leaf, "file.epub"), 0o400); err != nil {
			t.Fatal(err)
		}
		if _, err := cas.ListBlobs(context.Background()); !errors.Is(err, ErrCorruptBlob) {
			t.Fatalf("FIFO inventory: %v", err)
		}
	})
}

func TestPromotionRejectsMutatedStage(t *testing.T) {
	cas := openTestCAS(t)
	data := []byte("original")
	staged, err := cas.Stage(context.Background(), "mutated", bytes.NewReader(data), 100)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cas.Root(), filepath.FromSlash(staged.Path))
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := cas.Promote(context.Background(), staged.Path, staged.SHA256, staged.Size); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("mutated stage: %v", err)
	}
	if _, err := cas.InspectArtifact(context.Background(),
		staged.Path, staged.SHA256, staged.Size); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("inspected mutated stage: %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("mutated stage removed: %v", err)
	}
}

func TestPromotionRejectsUnsafeAndCorruptFiles(t *testing.T) {
	cas := openTestCAS(t)
	outside := filepath.Join(t.TempDir(), "outside.epub")
	data := []byte("outside")
	if err := os.WriteFile(outside, data, 0o600); err != nil {
		t.Fatal(err)
	}
	prefix := stagePrefix("symlink")
	stagePath := filepath.Join(cas.Root(), ".incoming", prefix+".stage")
	if err := os.Symlink(outside, stagePath); err != nil {
		t.Fatal(err)
	}
	if _, err := cas.Promote(context.Background(),
		filepath.ToSlash(filepath.Join(".incoming", prefix+".stage")),
		digestOf(data), int64(len(data))); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink stage: %v", err)
	}

	good := []byte("good blob")
	staged, err := cas.Stage(context.Background(), "corrupt-final", bytes.NewReader(good), 100)
	if err != nil {
		t.Fatal(err)
	}
	finalPath := filepath.Join(cas.Root(), filepath.FromSlash(finalRelativePath(staged.SHA256)))
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(finalPath, []byte("bad blob!"), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := cas.Promote(context.Background(), staged.Path, staged.SHA256, staged.Size); !errors.Is(err, ErrCorruptBlob) {
		t.Fatalf("corrupt final: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(cas.Root(), filepath.FromSlash(staged.Path))); err != nil {
		t.Fatalf("stage removed after corrupt final: %v", err)
	}
}

func TestOpenRejectsUnsafeRoot(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink root: %v", err)
	}
	if _, err := Open(filepath.Join(link, "child")); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink ancestor: %v", err)
	}
	unsafe := filepath.Join(parent, "unsafe")
	if err := os.Mkdir(unsafe, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(unsafe); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("writable root: %v", err)
	}
	public := filepath.Join(parent, "public")
	if err := os.Mkdir(public, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(public); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("public root: %v", err)
	}
}

func TestStageLockHonorsCancellation(t *testing.T) {
	cas := openTestCAS(t)
	prefix := stagePrefix("blocked")
	unlock, err := cas.lockStage(context.Background(), prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := cas.Stage(ctx, "blocked", bytes.NewReader([]byte("data")), 4); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked stage cancellation: %v", err)
	}
}

func TestCanceledOperationsDoNotMutate(t *testing.T) {
	cas := openTestCAS(t)
	data := []byte("cancel safety")
	staged, err := cas.Stage(context.Background(), "cancel-safety", bytes.NewReader(data), 100)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cas.Promote(ctx, staged.Path, staged.SHA256, staged.Size); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled promotion: %v", err)
	}
	stagePath := filepath.Join(cas.Root(), filepath.FromSlash(staged.Path))
	if _, err := os.Lstat(stagePath); err != nil {
		t.Fatalf("canceled promotion moved stage: %v", err)
	}
	if err := cas.RemoveStage(ctx, staged.Path); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled removal: %v", err)
	}
	if _, err := os.Lstat(stagePath); err != nil {
		t.Fatalf("canceled removal deleted stage: %v", err)
	}
}
