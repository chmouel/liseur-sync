package content

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Removing a book's file, ADR-0025.
//
// This is the counterpart of ingest_linux.go and carries the same
// bound: a folder the administrator did not mark as accepting uploads
// is untouchable, and this server may only unmake there what it could
// have made. Everything else in this package still opens read-only.
//
// A delete is the last place to be careless about a path. There is no
// trash behind it, so it reuses the rooted, symlink-refusing discipline
// openUnderRoot established, and it checks that the file it is about to
// unlink is still the file the catalog described.

// ErrRemoveChanged is a file whose size or modification time no longer
// matches the catalog row. It is the same change gate a plain folder
// scans with, used here to answer a different question: is this still
// the book the reader asked to delete, or a replacement somebody put at
// the same path. A mismatch is a refusal.
var ErrRemoveChanged = errors.New(
	"content: file changed since it was scanned")

// Remove deletes one catalog book's file from its folder.
//
// It refuses a folder that does not accept uploads, refuses a path that
// is not safely under the root, refuses a symlink or anything that is
// not a regular file, and refuses a file the catalog would not
// recognise. A file that is already gone is not a failure: the caller's
// goal is that it not be there.
//
// A Calibre folder goes a different way, because a Calibre book is rows
// as much as bytes; see Writer.DeleteBook.
func Remove(
	ctx context.Context, folder store.Folder, book store.CatalogBook,
) error {
	if !folder.AcceptsUploads {
		return ErrUploadsRefused
	}
	if folder.Kind == store.FolderCalibre {
		return removeCalibre(ctx, folder, book)
	}
	if err := removeUnderRoot(
		folder.RootPath, book.RelativePath, &book,
	); err != nil {
		return err
	}
	// The cover is Calibre's shape of thing and rare beside a loose
	// EPUB, but a row that names one names a file this server would
	// otherwise leave behind. It is not size-checked: it is not the
	// publication, and refusing the whole delete over a re-encoded
	// thumbnail would be worse than leaving it.
	if book.CoverRelativePath != nil && *book.CoverRelativePath != "" {
		if err := removeUnderRoot(
			folder.RootPath, *book.CoverRelativePath, nil,
		); err != nil && !errors.Is(err, ErrRemoveChanged) {
			return err
		}
	}
	return nil
}

// removeCalibre deletes the book from metadata.db and takes its
// directory with it. The path comes from the database rather than from
// the catalog row, because Calibre renames directories and ADR-0022
// makes metadata.db authoritative.
func removeCalibre(
	ctx context.Context, folder store.Folder, book store.CatalogBook,
) error {
	if book.CalibreID == nil {
		return fmt.Errorf(
			"content: book %s has no Calibre id in a Calibre folder",
			book.ID)
	}
	writer, err := calibre.OpenWriter(folder.RootPath)
	if err != nil {
		return fmt.Errorf("content: open Calibre library: %w", err)
	}
	defer func() { _ = writer.Close() }()

	switch err := writer.DeleteBook(ctx, *book.CalibreID); {
	case err == nil, errors.Is(err, calibre.ErrNoSuchBook):
		return nil
	default:
		return err
	}
}

// removeUnderRoot is the only way this package deletes a file beneath a
// watched folder, and the mirror of openUnderRoot: rooted, lstat before
// touching, symlinks and non-regular files refused.
//
// When want is non-nil the file has to still match the catalog row by
// fileMatches — the same cheap proof a download uses before serving
// bytes. Opening first and unlinking after would be stronger still,
// but Linux has no unlink-by-descriptor, so the window between the stat
// and the unlink stays — narrowed to microseconds under a root that
// cannot be escaped, which is the same trade every scan in this package
// already makes.
func removeUnderRoot(
	rootPath, relative string, want *store.CatalogBook,
) error {
	if rootPath == "" {
		return ErrRootMissing
	}
	if !SafeRelativePath(relative) {
		return ErrUnsafePath
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrRootMissing, rootPath, err)
	}
	defer root.Close()

	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrUnsafePath
	}
	if want != nil && !fileMatches(*want, info) {
		return ErrRemoveChanged
	}
	if err := root.Remove(relative); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
