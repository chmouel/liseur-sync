//go:build linux

package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// OpenBookFile opens whichever copy of a file's bytes this server can
// reach: the content-addressed one it owns, or the file where its owner
// put it. It is the single read chokepoint (ADR-0014), so that a caller
// serving a download or rendering a cover never has to know which kind
// of library the book came from.
//
// The caller owns the returned file and must close it.
func (c *CAS) OpenBookFile(
	ctx context.Context, file store.BookFile,
) (*os.File, int64, error) {
	file = file.Normalized()
	if file.Storage != store.LibraryStorageInPlace {
		return c.OpenBlob(ctx, file.BlobSHA256)
	}
	return OpenInPlaceFile(ctx, file)
}

// OpenBookFileCover opens the cover a library's curator chose for this
// file — Calibre's cover.jpg — which lives beside the publication under
// the library root rather than inside it.
//
// The bytes are bounded by MaxCoverBytes and then proved by digest
// rather than by stat. A cover has no
// snapshot of its own to compare against, and it is small enough to hash
// on the way out; a cover.jpg somebody replaced therefore fails here and
// falls back to the publication's own until the next refresh records the
// new one, instead of being served under a cache key that names the old
// image.
func (c *CAS) OpenBookFileCover(
	ctx context.Context, file store.BookFile,
) (*os.File, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if file.CoverSHA256 == "" || file.CoverRelativePath == nil {
		return nil, 0, ErrStageMissing
	}
	if file.LibraryRoot == "" {
		return nil, 0, ErrRootMissing
	}
	relative := *file.CoverRelativePath
	if !safeRelativePath(relative) {
		return nil, 0, ErrUnsafePath
	}
	root, err := os.OpenRoot(file.LibraryRoot)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %s: %v",
			ErrRootMissing, file.LibraryRoot, err)
	}
	defer root.Close()

	opened, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrStageMissing
	}
	if err != nil {
		return nil, 0, err
	}
	info, err := opened.Stat()
	if err != nil {
		opened.Close()
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		opened.Close()
		return nil, 0, ErrUnsafePath
	}
	if info.Size() > MaxCoverBytes {
		// The refresh only ever records a cover within this bound, so
		// anything larger is a file somebody swapped after it was
		// recorded. Refusing it before the hash is what keeps a request
		// from being made to read an arbitrary amount of their disk.
		opened.Close()
		return nil, 0, ErrSourceChanged
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, io.LimitReader(opened, MaxCoverBytes)); err != nil {
		opened.Close()
		return nil, 0, err
	}
	if hex.EncodeToString(digest.Sum(nil)) != file.CoverSHA256 {
		opened.Close()
		return nil, 0, ErrSourceChanged
	}
	if _, err := opened.Seek(0, io.SeekStart); err != nil {
		opened.Close()
		return nil, 0, err
	}
	return opened, info.Size(), nil
}

// OpenInPlaceFile opens a file under a library root that this server does
// not own.
//
// Everything here treats the root as somebody else's directory. The open
// goes through os.Root, so a path that climbs out of the library — or a
// symlink that points out of it — fails rather than reads; and the size
// and modification time are compared with the snapshot taken when the
// file was catalogued, because the alternative is serving whatever bytes
// happen to be at that path now under the title of the book that used to
// be there. A mismatch is not a corruption to repair: it is a file whose
// owner changed it, and the sweep is what re-reads it.
func OpenInPlaceFile(
	ctx context.Context, file store.BookFile,
) (*os.File, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if file.LibraryRoot == "" {
		return nil, 0, ErrRootMissing
	}
	if file.SourceRelativePath == nil || *file.SourceRelativePath == "" {
		return nil, 0, ErrUnsafePath
	}
	relative := *file.SourceRelativePath
	if !safeRelativePath(relative) {
		return nil, 0, ErrUnsafePath
	}
	root, err := os.OpenRoot(file.LibraryRoot)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %s: %v",
			ErrRootMissing, file.LibraryRoot, err)
	}
	defer root.Close()

	// Lstat first: os.Root would follow a symlink that stays inside the
	// library, and the sweep skips symlinks by policy, so one appearing
	// here is a path the catalogue never validated.
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrStageMissing
	}
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, ErrUnsafePath
	}
	opened, err := root.Open(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrStageMissing
	}
	if err != nil {
		return nil, 0, err
	}
	// Stat the descriptor rather than trust the lstat: between the two
	// calls the path could have been replaced, and it is the file that
	// will actually be read that has to match.
	opened2, err := opened.Stat()
	if err != nil {
		opened.Close()
		return nil, 0, err
	}
	if !opened2.Mode().IsRegular() {
		opened.Close()
		return nil, 0, ErrUnsafePath
	}
	if !sourceMatches(file, opened2) {
		opened.Close()
		return nil, 0, ErrSourceChanged
	}
	return opened, opened2.Size(), nil
}

// sourceMatches is the cheap proof that a file is still the one that was
// read. It is not a digest: re-hashing every download would read the file
// twice and turn a range request into a full read, and re-hashing is what
// the sweep is for.
func sourceMatches(file store.BookFile, info os.FileInfo) bool {
	if file.ContentSizeBytes > 0 && info.Size() != file.ContentSizeBytes {
		return false
	}
	if file.SourceModifiedAt == nil {
		return true
	}
	// Filesystems and databases disagree about sub-second precision, so
	// the comparison is to the second.
	return info.ModTime().UTC().Truncate(time.Second).
		Equal(file.SourceModifiedAt.UTC().Truncate(time.Second))
}

// safeRelativePath refuses anything the sweep would never have recorded:
// an absolute path, a parent reference, or an empty segment. os.Root
// would refuse most of them too, but a path that cannot be right should
// not reach the filesystem at all.
func safeRelativePath(relative string) bool {
	if relative == "" || strings.HasPrefix(relative, "/") {
		return false
	}
	if relative != path.Clean(relative) {
		return false
	}
	for segment := range strings.SplitSeq(relative, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
