//go:build linux

package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"syscall"

	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/store"
)

// IsCalibreFolder reports whether a directory is a Calibre library,
// which is the only thing that decides a folder's kind. There is no
// setting for it: a metadata.db at the root means the curator has a
// catalog and this server should read it rather than second-guess it.
func IsCalibreFolder(rootPath string) bool {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return false
	}
	defer root.Close()
	info, err := root.Lstat(calibre.MetadataDB)
	return err == nil && info.Mode().IsRegular()
}

// reconcileCalibre reads a Calibre library's own catalog.
//
// Two things make this different from a plain pass, and both are
// deliberate:
//
//   - Books are keyed by Calibre's book id, never by path. Calibre
//     rewrites a book's directory name whenever its title or author
//     changes, so a path-keyed pass would read every metadata edit as
//     one book vanishing and another appearing — losing the reader's
//     position each time.
//   - Metadata is re-read on every pass, whatever the EPUB's stat says.
//     The interesting changes — series, tags, description, the chosen
//     cover.jpg — happen in metadata.db and never touch the publication
//     file, so a stat gate would make this server permanently blind to
//     them. Reading a few thousand rows out of a local SQLite file is
//     cheap.
//
// Nothing is written: metadata.db is opened read-only, and no seam is
// left here for a future write side.
func (r *Reconciler) reconcileCalibre(
	ctx context.Context, folder store.Folder,
) (store.ReconcileResult, error) {
	library, err := calibre.Open(folder.RootPath)
	if err != nil {
		// A library that cannot be opened says nothing about the books
		// it holds, so this is an error rather than an empty pass.
		return store.ReconcileResult{}, err
	}
	defer library.Close()

	books, err := library.Books(ctx)
	if err != nil {
		return store.ReconcileResult{}, err
	}

	root, err := os.OpenRoot(folder.RootPath)
	if err != nil {
		return store.ReconcileResult{}, err
	}
	defer root.Close()

	observed := make([]store.ObservedBook, 0, len(books))
	complete := true
	for _, book := range books {
		if err := ctx.Err(); err != nil {
			return store.ReconcileResult{}, err
		}
		formats := book.ReadableFormats()
		if len(formats) == 0 {
			// A book Calibre holds in some other format is a book this
			// server cannot serve. Skipping it is not incompleteness:
			// the pass saw it and knows exactly what it is.
			continue
		}
		obs, err := r.readCalibreBookFormats(ctx, root, book, formats)
		if err != nil {
			r.log.Warn("skipping unreadable calibre book",
				"folder", folder.ID, "calibre_id", book.ID, "error", err)
			complete = false
			continue
		}
		observed = append(observed, obs)
	}
	return r.catalog.ReconcileFolder(ctx, folder.ID, observed, complete, r.now())
}

// readCalibreBookFormats reads the first of a book's formats that is
// actually on the disk.
//
// Calibre's format rows outlive the files they name: deleting a book's
// EPUB after converting it to KEPUB leaves the EPUB row behind, and a
// reader that insisted on the first row would call a book unreadable
// while its file sat in the same directory. A missing file is therefore
// a reason to try the next format, not a reason to give up.
//
// The error returned when every format fails is the first one, which is
// the one about the format the library prefers.
func (r *Reconciler) readCalibreBookFormats(
	ctx context.Context, root *os.Root, book calibre.Book, formats []calibre.Format,
) (store.ObservedBook, error) {
	var first error
	for _, format := range formats {
		obs, err := r.readCalibreBook(ctx, root, book, format)
		if err == nil {
			return obs, nil
		}
		if first == nil {
			first = err
		}
		// Anything other than an absent file is a real failure to read a
		// file that is there — a permission, a device error, a symlink
		// refused — and the pass must not paper over it by silently
		// serving a different edition of the book.
		if !errors.Is(err, fs.ErrNotExist) {
			return store.ObservedBook{}, err
		}
	}
	return store.ObservedBook{}, first
}

// readCalibreBook builds one observation from Calibre's row plus the
// stat and digest of the file it names.
func (r *Reconciler) readCalibreBook(
	ctx context.Context, root *os.Root, book calibre.Book, format calibre.Format,
) (store.ObservedBook, error) {
	relative := book.RelativePath(format)
	if !SafeRelativePath(relative) {
		return store.ObservedBook{}, ErrUnsafePath
	}
	opened, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return store.ObservedBook{}, err
	}
	defer opened.Close()
	info, err := opened.Stat()
	if err != nil {
		return store.ObservedBook{}, err
	}
	if !info.Mode().IsRegular() {
		return store.ObservedBook{}, ErrUnsafePath
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, opened); err != nil {
		return store.ObservedBook{}, err
	}

	calibreID := book.ID
	obs := store.ObservedBook{
		CalibreID:        &calibreID,
		RelativePath:     relative,
		SizeBytes:        info.Size(),
		MTime:            info.ModTime().UTC(),
		ContentSHA256:    hex.EncodeToString(digest.Sum(nil)),
		OriginalFilename: path.Base(relative),
		MediaType:        "application/epub+zip",
		Title:            book.Title,
		Description:      book.Description,
		Publisher:        book.Publisher,
		Languages:        book.Languages,
		Tags:             calibre.SortedTags(book),
	}
	if book.Published != nil {
		obs.PublishedDate = book.Published.UTC().Format("2006-01-02")
	}
	if book.Series != "" {
		position := book.SeriesIndex
		obs.Series = append(obs.Series,
			store.ObservedSeries{Name: book.Series, Position: &position})
	}
	for i, author := range book.Authors {
		obs.Contributors = append(obs.Contributors, store.ObservedContributor{
			Name:     author,
			Role:     store.ContributorRoleAuthor,
			Position: i,
		})
	}
	for scheme, value := range book.Identifiers {
		obs.Identifiers = append(obs.Identifiers,
			store.BookIdentifier{Scheme: scheme, Value: value})
	}
	if cover, digest, ok := coverStamp(ctx, root, book); ok {
		obs.CoverRelativePath = &cover
		obs.CoverSHA256 = digest
	}
	return obs, nil
}

// coverStamp records the curator's chosen cover.jpg by digest, so a
// replaced cover invalidates the cache instead of being served stale
// under a key naming the old image. A missing cover is not a failure:
// the publication's own is rendered on demand.
func coverStamp(
	ctx context.Context, root *os.Root, book calibre.Book,
) (string, string, bool) {
	if !book.HasCover {
		return "", "", false
	}
	if err := ctx.Err(); err != nil {
		return "", "", false
	}
	relative := book.CoverPath()
	if !SafeRelativePath(relative) {
		return "", "", false
	}
	opened, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", "", false
	}
	defer opened.Close()
	info, err := opened.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxCoverBytes {
		return "", "", false
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, io.LimitReader(opened, MaxCoverBytes)); err != nil {
		return "", "", false
	}
	return relative, hex.EncodeToString(digest.Sum(nil)), true
}
