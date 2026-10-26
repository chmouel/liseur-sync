//go:build linux

package content

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlaceAudiobookWritesBesideTheBookAndNeverReplaces(t *testing.T) {
	root := t.TempDir()
	book := placeBook(t, root, "sub/Dune.epub", []byte("epub"))
	folder := uploadFolder(root)
	book.FolderID = folder.ID

	relative, err := PlaceAudiobook(t.Context(), folder, book,
		AudiobookUpload{Source: strings.NewReader("m4b"), Size: 3})
	if err != nil {
		t.Fatal(err)
	}
	if relative != "sub/Dune.m4b" {
		t.Fatalf("relative = %q", relative)
	}
	if got, _ := os.ReadFile(filepath.Join(root, relative)); string(got) != "m4b" {
		t.Fatalf("bytes = %q", got)
	}

	if _, err := PlaceAudiobook(t.Context(), folder, book,
		AudiobookUpload{Source: strings.NewReader("new"), Size: 3}); !errors.Is(err, ErrAudiobookExists) {
		t.Fatalf("second place: err = %v, want ErrAudiobookExists", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, relative)); string(got) != "m4b" {
		t.Fatalf("the first audiobook was replaced: %q", got)
	}
}

func TestPlaceAudiobookRefusesAFolderThatDoesNotAcceptUploads(t *testing.T) {
	root := t.TempDir()
	book := placeBook(t, root, "Dune.epub", []byte("epub"))
	folder := uploadFolder(root)
	folder.AcceptsUploads = false
	book.FolderID = folder.ID
	if _, err := PlaceAudiobook(t.Context(), folder, book,
		AudiobookUpload{Source: bytes.NewReader(nil)}); !errors.Is(err, ErrUploadsRefused) {
		t.Fatalf("err = %v, want ErrUploadsRefused", err)
	}
}

// A symlink where the audiobook would go is not followed.
func TestPlaceAudiobookRefusesASymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "target")
	book := placeBook(t, root, "Dune.epub", []byte("epub"))
	folder := uploadFolder(root)
	book.FolderID = folder.ID
	if err := os.Symlink(outside, filepath.Join(root, "Dune.m4b")); err != nil {
		t.Fatal(err)
	}
	if _, err := PlaceAudiobook(t.Context(), folder, book,
		AudiobookUpload{Source: strings.NewReader("m4b"), Size: 3}); err == nil {
		t.Fatal("placed through a symlink")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("wrote outside the root: %v", err)
	}
}

// A transfer that fails leaves no partial audiobook for the next pass.
func TestPlaceAudiobookRemovesAPartialFile(t *testing.T) {
	root := t.TempDir()
	book := placeBook(t, root, "Dune.epub", []byte("epub"))
	folder := uploadFolder(root)
	book.FolderID = folder.ID
	if _, err := PlaceAudiobook(t.Context(), folder, book,
		AudiobookUpload{Source: failingReader{}, Size: 3}); err == nil {
		t.Fatal("broken transfer succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, "Dune.m4b")); !os.IsNotExist(err) {
		t.Fatalf("partial file survived: %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
