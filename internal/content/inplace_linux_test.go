//go:build linux

package content

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// inPlaceFile writes a file under a fresh root and returns the BookFile
// that describes it, as the sweep would have recorded it.
func inPlaceFile(t *testing.T, name string, body []byte) (string, store.BookFile) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	modified := info.ModTime().UTC()
	return root, store.BookFile{
		ID:                 "file-1",
		LibraryID:          "lib-1",
		BookID:             "book-1",
		Storage:            store.LibraryStorageInPlace,
		ContentSHA256:      "0",
		ContentSizeBytes:   int64(len(body)),
		Source:             store.IngestScanned,
		SourceRelativePath: &name,
		SourceModifiedAt:   &modified,
		LibraryRoot:        root,
	}
}

// A library this server does not own still serves its bytes exactly.
func TestOpenInPlaceFileServesTheBytesOnDisk(t *testing.T) {
	body := []byte("a book that was never copied anywhere")
	_, file := inPlaceFile(t, "author/title.epub", body)

	opened, size, err := OpenInPlaceFile(context.Background(), file)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer opened.Close()
	if size != int64(len(body)) {
		t.Errorf("size %d, want %d", size, len(body))
	}
	got, err := io.ReadAll(opened)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("read %q, want %q", got, body)
	}
}

// The whole point of the size and mtime snapshot: bytes that changed
// under us are not this book's bytes, and are refused rather than served
// under its title.
func TestOpenInPlaceFileRefusesChangedContent(t *testing.T) {
	body := []byte("the original")
	root, file := inPlaceFile(t, "book.epub", body)
	path := filepath.Join(root, "book.epub")

	t.Run("size", func(t *testing.T) {
		if err := os.WriteFile(path, []byte("a longer replacement"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := OpenInPlaceFile(context.Background(), file); !errors.Is(err, ErrSourceChanged) {
			t.Fatalf("err = %v, want ErrSourceChanged", err)
		}
	})

	t.Run("modification time", func(t *testing.T) {
		// Same length, so only the clock says anything happened.
		if err := os.WriteFile(path, []byte("the orlginal"), 0o644); err != nil {
			t.Fatal(err)
		}
		later := time.Now().Add(2 * time.Hour)
		if err := os.Chtimes(path, later, later); err != nil {
			t.Fatal(err)
		}
		if _, _, err := OpenInPlaceFile(context.Background(), file); !errors.Is(err, ErrSourceChanged) {
			t.Fatalf("err = %v, want ErrSourceChanged", err)
		}
	})
}

// A file that was deleted is gone, not corrupt: the caller answers 410
// rather than 409, and nothing is flagged for review.
func TestOpenInPlaceFileReportsAMissingFile(t *testing.T) {
	root, file := inPlaceFile(t, "book.epub", []byte("gone soon"))
	if err := os.Remove(filepath.Join(root, "book.epub")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenInPlaceFile(context.Background(), file); !errors.Is(err, ErrStageMissing) {
		t.Fatalf("err = %v, want ErrStageMissing", err)
	}
}

// The root is trusted input, but not blindly: a symlink is a second path
// to a file the sweep never validated, and a path that climbs out of the
// library is not a path the sweep could have produced at all.
func TestOpenInPlaceFileRefusesSymlinksAndEscapes(t *testing.T) {
	root, file := inPlaceFile(t, "book.epub", []byte("real content"))
	link := "link.epub"
	if err := os.Symlink(filepath.Join(root, "book.epub"),
		filepath.Join(root, link)); err != nil {
		t.Fatal(err)
	}
	linked := file
	linked.SourceRelativePath = &link
	if _, _, err := OpenInPlaceFile(context.Background(), linked); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink: err = %v, want ErrUnsafePath", err)
	}

	for _, relative := range []string{
		"../outside.epub", "/etc/passwd", "a//b.epub", "./book.epub", "",
	} {
		escaping := file
		escaping.SourceRelativePath = &relative
		if _, _, err := OpenInPlaceFile(context.Background(), escaping); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%q: err = %v, want ErrUnsafePath", relative, err)
		}
	}
}

// A library whose root has been unmounted or removed fails as a library,
// not as a corrupt file.
func TestOpenInPlaceFileReportsAnUnavailableRoot(t *testing.T) {
	_, file := inPlaceFile(t, "book.epub", []byte("content"))
	file.LibraryRoot = filepath.Join(file.LibraryRoot, "nowhere")
	if _, _, err := OpenInPlaceFile(context.Background(), file); !errors.Is(err, ErrRootMissing) {
		t.Fatalf("err = %v, want ErrRootMissing", err)
	}
	file.LibraryRoot = ""
	if _, _, err := OpenInPlaceFile(context.Background(), file); !errors.Is(err, ErrRootMissing) {
		t.Fatalf("empty root: err = %v, want ErrRootMissing", err)
	}
}

// The chokepoint is what makes the rest of the server storage-agnostic:
// the same call serves a copied file and a file left where it was found.
func TestOpenBookFileRoutesOnStorage(t *testing.T) {
	cas := openTestCAS(t)
	body := []byte("a book this server keeps a copy of")
	staged, err := cas.Stage(context.Background(), "job", bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cas.Promote(context.Background(), staged.Path, staged.SHA256, staged.Size); err != nil {
		t.Fatal(err)
	}

	managed := store.BookFile{
		Storage:       store.LibraryStorageCAS,
		BlobSHA256:    staged.SHA256,
		ContentSHA256: staged.SHA256,
	}
	opened, _, err := cas.OpenBookFile(context.Background(), managed)
	if err != nil {
		t.Fatalf("cas file: %v", err)
	}
	got, _ := io.ReadAll(opened)
	opened.Close()
	if string(got) != string(body) {
		t.Errorf("cas file read %q, want %q", got, body)
	}

	inPlaceBody := []byte("a book somebody else keeps")
	_, file := inPlaceFile(t, "book.epub", inPlaceBody)
	opened, _, err = cas.OpenBookFile(context.Background(), file)
	if err != nil {
		t.Fatalf("in-place file: %v", err)
	}
	got, _ = io.ReadAll(opened)
	opened.Close()
	if string(got) != string(inPlaceBody) {
		t.Errorf("in-place read %q, want %q", got, inPlaceBody)
	}
}
