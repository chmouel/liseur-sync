//go:build linux

package content

import (
	"context"
	"os"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Files opens the bytes behind a catalog book.
//
// It exists so that nothing above this package has to know that a book's
// path is relative to a folder root, or where that root is. A handler
// asks for a book's file; the rooted, read-only open of open_linux.go is
// what it gets.
type Files struct {
	folders FolderSource
}

// NewFiles builds the opener over whatever can resolve a folder id.
func NewFiles(folders FolderSource) *Files { return &Files{folders: folders} }

// OpenBook opens the publication. The caller owns the file.
func (f *Files) OpenBook(
	ctx context.Context, book store.CatalogBook,
) (*os.File, int64, error) {
	root, err := f.root(ctx, book)
	if err != nil {
		return nil, 0, err
	}
	return OpenBook(ctx, root, book)
}

// OpenBookCover opens the cover the folder's curator chose, where there
// is one. The caller owns the file.
func (f *Files) OpenBookCover(
	ctx context.Context, book store.CatalogBook,
) (*os.File, int64, error) {
	root, err := f.root(ctx, book)
	if err != nil {
		return nil, 0, err
	}
	return OpenBookCover(ctx, root, book)
}

func (f *Files) root(ctx context.Context, book store.CatalogBook) (string, error) {
	folder, err := f.folders.FolderByID(ctx, book.FolderID)
	if err != nil {
		// A book whose folder has gone is a book whose bytes are not
		// this server's to serve, which is what ErrRootMissing means to
		// every caller of this package.
		return "", ErrRootMissing
	}
	return folder.RootPath, nil
}
