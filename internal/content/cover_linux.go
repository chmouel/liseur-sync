//go:build linux

package content

import (
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ErrNoCover means this blob has already been examined and has no cover
// worth serving. It is a cached answer, not a failure: a book with no
// cover would otherwise be re-opened and re-parsed on every request that
// wants a thumbnail for it.
var ErrNoCover = errors.New("content: blob has no usable cover")

// coversDirectory holds rendered covers. They sit outside sha256/ because
// they are not content: every one of them can be regenerated from the blob
// it was made from, which is what lets a backup skip them and lets an
// operator delete the whole directory to reclaim space (ADR-0002).
const coversDirectory = "covers"

// coverAbsentMarker records "this blob has no cover" for every variant at
// once. The answer cannot differ between sizes — either the publication
// declares a cover this server can decode or it does not — and a marker
// per variant would make a coverless book cost one archive parse per size
// a client asks for.
const coverAbsentMarker = "absent"

// OpenCover returns a previously rendered cover. A miss is
// ErrStageMissing, which the caller answers by rendering one; a blob known
// to have no cover is ErrNoCover, which it answers without opening
// anything.
func (c *CAS) OpenCover(ctx context.Context, digest, variant string) (*os.File, int64, error) {
	if !validSHA256(digest) || !validCoverVariant(variant) {
		return nil, 0, ErrUnsafePath
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	leafFD, err := c.openCoverDirectories(digest, false)
	if err != nil {
		return nil, 0, err
	}
	defer unix.Close(leafFD)

	// The absent marker is checked first. Both files can exist only if a
	// blob's cover was rendered and the blob was later replaced, which
	// cannot happen: the digest names the bytes.
	if err := faccessatFile(leafFD, coverAbsentMarker); err == nil {
		return nil, 0, ErrNoCover
	}
	fd, err := unix.Openat(leafFD, coverFilename(variant),
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, 0, ErrStageMissing
	}
	if err != nil {
		return nil, 0, classifyPathError(err)
	}
	file := os.NewFile(uintptr(fd), coverFilename(variant))
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, 0, ErrUnsafePath
	}
	return file, info.Size(), nil
}

// StoreCover caches one rendered variant. Failing to cache is not failing
// to serve: the caller has the bytes in hand, so a full disk costs the
// next request a re-render rather than an error.
func (c *CAS) StoreCover(ctx context.Context, digest, variant string, data []byte) error {
	if !validSHA256(digest) || !validCoverVariant(variant) || len(data) == 0 {
		return ErrUnsafePath
	}
	return c.writeCoverFile(ctx, digest, coverFilename(variant), data)
}

// MarkCoverAbsent records that a blob has no cover to serve, so the next
// request for one costs a lookup instead of an archive parse.
func (c *CAS) MarkCoverAbsent(ctx context.Context, digest string) error {
	if !validSHA256(digest) {
		return ErrUnsafePath
	}
	return c.writeCoverFile(ctx, digest, coverAbsentMarker, []byte("absent\n"))
}

// RemoveCovers deletes every cached cover for a blob. It is called when
// the blob itself goes: a cover kept after its book was deleted is a
// picture of that book that nothing can ever reach or clean up.
func (c *CAS) RemoveCovers(ctx context.Context, digest string) error {
	if !validSHA256(digest) {
		return ErrUnsafePath
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	coversFD, err := openDirectoryAt(c.rootFD, coversDirectory)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return classifyPathError(err)
	}
	defer unix.Close(coversFD)
	prefixFD, err := openDirectoryAt(coversFD, digest[:2])
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return classifyPathError(err)
	}
	defer unix.Close(prefixFD)

	leafFD, err := openDirectoryAt(prefixFD, digest[2:])
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return classifyPathError(err)
	}
	names, err := readDirectoryEntries(leafFD)
	if err != nil {
		unix.Close(leafFD)
		return err
	}
	for _, name := range names {
		if err := unlinkIfExists(leafFD, name); err != nil {
			unix.Close(leafFD)
			return err
		}
	}
	unix.Close(leafFD)
	if err := unix.Unlinkat(prefixFD, digest[2:], unix.AT_REMOVEDIR); err != nil &&
		!errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.ENOTEMPTY) {
		return classifyPathError(err)
	}
	return nil
}

// writeCoverFile writes one file into a blob's cover directory, replacing
// any previous content atomically. The temporary name carries the target
// name so two variants rendered at once cannot collide.
func (c *CAS) writeCoverFile(
	ctx context.Context, digest, name string, data []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	leafFD, err := c.openCoverDirectories(digest, true)
	if err != nil {
		return err
	}
	defer unix.Close(leafFD)

	temporary := fmt.Sprintf(".%s.%d.tmp", name, os.Getpid())
	fd, err := unix.Openat(leafFD, temporary,
		unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return classifyPathError(err)
	}
	file := os.NewFile(uintptr(fd), temporary)
	if _, err := file.Write(data); err != nil {
		file.Close()
		_ = unlinkIfExists(leafFD, temporary)
		return err
	}
	if err := file.Close(); err != nil {
		_ = unlinkIfExists(leafFD, temporary)
		return err
	}
	if err := unix.Renameat(leafFD, temporary, leafFD, name); err != nil {
		_ = unlinkIfExists(leafFD, temporary)
		return classifyPathError(err)
	}
	// The directory is not fsynced. A cover that a crash loses is a cache
	// entry that gets rendered again, and paying two syncs per thumbnail
	// to protect a derived file would be the wrong trade.
	return nil
}

func (c *CAS) openCoverDirectories(digest string, create bool) (int, error) {
	open := openDirectoryAt
	if create {
		open = ensureDirectoryAt
	}
	coversFD, err := open(c.rootFD, coversDirectory)
	if err != nil {
		return -1, coverPathError(err, create)
	}
	defer unix.Close(coversFD)

	prefixFD, err := open(coversFD, digest[:2])
	if err != nil {
		return -1, coverPathError(err, create)
	}
	defer unix.Close(prefixFD)

	leafFD, err := open(prefixFD, digest[2:])
	if err != nil {
		return -1, coverPathError(err, create)
	}
	return leafFD, nil
}

// coverPathError keeps a missing directory a cache miss rather than an
// error: nothing has rendered a cover for this blob yet.
func coverPathError(err error, create bool) error {
	if !create && errors.Is(err, unix.ENOENT) {
		return ErrStageMissing
	}
	return classifyPathError(err)
}

// faccessatFile reports whether a name exists in a directory without
// following a symlink to somewhere else.
func faccessatFile(dirFD int, name string) error {
	return unix.Faccessat(dirFD, name, unix.F_OK, unix.AT_SYMLINK_NOFOLLOW)
}

func coverFilename(variant string) string { return variant + ".jpg" }

// validCoverVariant is an allowlist because the variant becomes a path
// element. The caller has already mapped a request parameter to a known
// size; this is what makes that mapping load-bearing rather than a
// convention.
func validCoverVariant(variant string) bool {
	if variant == "" || len(variant) > 16 || variant == coverAbsentMarker {
		return false
	}
	for _, r := range variant {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}
