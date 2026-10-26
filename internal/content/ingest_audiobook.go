//go:build unix

package content

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Uploading an audiobook onto a book that is already in the catalog
// (ADR-0058). It is ADR-0023's write, aimed at a book rather than a
// folder: one file that was not there, placed where the next pass pairs
// it with that book and with nothing else.

// ErrAudiobookExists is a book that already has an audiobook file where
// this one would go. Adding never replaces.
var ErrAudiobookExists = errors.New("content: the book already has an audiobook")

// AudiobookUpload is one .m4b on its way to a book, already validated
// and spooled outside every folder root.
type AudiobookUpload struct {
	Source io.Reader
	Size   int64
}

// PlaceAudiobook writes an audiobook beside a book and answers with its
// path relative to the folder root.
//
// In a plain folder the file takes the EPUB's own name with an .m4b
// extension, which is what the same-name pairing rule matches. In a
// Calibre folder it becomes the book's M4B format, which pairs by
// calibre_id.
func PlaceAudiobook(
	ctx context.Context, folder store.Folder, book store.CatalogBook, up AudiobookUpload,
) (string, error) {
	if !folder.AcceptsUploads {
		return "", ErrUploadsRefused
	}
	if book.FolderID != folder.ID {
		return "", fmt.Errorf("content: book %s is not in folder %s", book.ID, folder.ID)
	}
	if folder.Kind == store.FolderCalibre {
		return placeCalibreAudiobook(ctx, folder, book, up)
	}
	relative := AudiobookPathFor(book.RelativePath)
	if !SafeRelativePath(relative) {
		return "", ErrUnsafePath
	}
	root, err := os.OpenRoot(folder.RootPath)
	if err != nil {
		return "", fmt.Errorf("content: open root: %w", err)
	}
	defer root.Close()

	file, err := root.OpenFile(relative,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if errors.Is(err, os.ErrExist) {
		return "", ErrAudiobookExists
	}
	if err != nil {
		return "", fmt.Errorf("content: create %q: %w", relative, err)
	}
	if err := copyInto(ctx, file, up.Source); err != nil {
		file.Close()
		_ = root.Remove(relative)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = root.Remove(relative)
		return "", fmt.Errorf("content: close %q: %w", relative, err)
	}
	return relative, nil
}

// AudiobookPathFor is where a plain folder keeps the audiobook of the
// publication at relative: the same directory and the same name.
func AudiobookPathFor(relative string) string {
	return strings.TrimSuffix(relative, path.Ext(relative)) + audiobookExtension
}

func placeCalibreAudiobook(
	ctx context.Context, folder store.Folder, book store.CatalogBook, up AudiobookUpload,
) (string, error) {
	if book.CalibreID == nil {
		return "", fmt.Errorf(
			"content: book %s has no Calibre id in a Calibre folder", book.ID)
	}
	writer, err := calibre.OpenWriter(folder.RootPath)
	if err != nil {
		return "", fmt.Errorf("content: open Calibre library: %w", err)
	}
	defer func() { _ = writer.Close() }()
	relative, err := writer.AddFormat(ctx, *book.CalibreID, "M4B", up.Source, up.Size)
	if errors.Is(err, calibre.ErrFormatExists) {
		return "", ErrAudiobookExists
	}
	return relative, err
}

// IngestAudiobook places an audiobook beside a book and reconciles the
// folder, which indexes the file and pairs it. As with Ingest, a pass
// that concludes nothing is not a failed upload: the file is on disk.
func (i *Ingester) IngestAudiobook(
	ctx context.Context, folder store.Folder, book store.CatalogBook, up AudiobookUpload,
) (string, error) {
	relative, err := PlaceAudiobook(ctx, folder, book, up)
	if err != nil {
		return "", err
	}
	_, _ = i.rec.Reconcile(ctx, folder)
	return relative, nil
}
