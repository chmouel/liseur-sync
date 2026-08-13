package epub

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
)

// ErrNoCover means the publication declares no cover image this server can
// serve. It is not a validation failure: plenty of legitimate EPUBs have
// no cover, and one that names a cover it does not contain is still a
// readable book.
var ErrNoCover = errors.New("epub: no cover image")

// CoverImage is a cover exactly as the publication stores it, before any
// transcoding. MediaType is the manifest's claim about the bytes and is
// not trusted: the decoder decides what they actually are.
type CoverImage struct {
	Path      string
	MediaType string
	Data      []byte
}

// ReadCover finds and reads the cover image declared by an EPUB.
//
// It deliberately does not re-run Validate. The only caller is serving a
// cover for a blob that was validated before it was ever promoted, and
// Validate reads every entry in the archive — paying that on a cache miss
// would turn a thumbnail into a full decompression pass over the book.
// What it does keep are the bounds that make reading one entry of an
// untrusted ZIP safe: the archive layout, the path checks, the XML limits,
// and a cap on the cover entry itself.
func ReadCover(
	ctx context.Context,
	reader io.ReaderAt,
	size int64,
	limits Limits,
	maxCoverBytes int64,
) (CoverImage, error) {
	if ctx == nil || reader == nil || size < 0 || maxCoverBytes <= 0 ||
		limits.Validate() != nil {
		return CoverImage{}, ErrInvalidValidationInput
	}
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		if errors.Is(err, zip.ErrInsecurePath) {
			return CoverImage{}, validationError(CodeUnsafeArchive, err)
		}
		return CoverImage{}, validationError(CodeInvalidEPUB, err)
	}
	if len(archive.File) == 0 || len(archive.File) > limits.MaxEntries {
		return CoverImage{}, validationError(
			CodeArchiveLimits, errors.New("ZIP entry count exceeds limit"))
	}
	entries := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		name, err := safeArchivePath(file.Name)
		if err != nil {
			return CoverImage{}, validationError(CodeUnsafeArchive, err)
		}
		// A duplicate path is what a two-faced archive uses to have one
		// reader see one file and another reader see a different one.
		// Validate refuses those outright; here the first entry wins and
		// only for reading, which keeps this in step with it.
		if _, seen := entries[name]; !seen {
			entries[name] = file
		}
	}

	container, ok := entries["META-INF/container.xml"]
	if !ok || !container.Mode().IsRegular() {
		return CoverImage{}, validationError(
			CodeInvalidEPUB, errors.New("container.xml is missing"))
	}
	containerXML, err := readMetadata(ctx, container, limits.MaxMetadataBytes)
	if err != nil {
		return CoverImage{}, err
	}
	packagePath, err := parseContainer(containerXML, limits.MaxXMLDepth)
	if err != nil {
		return CoverImage{}, err
	}
	packageFile, ok := entries[packagePath]
	if !ok || !packageFile.Mode().IsRegular() {
		return CoverImage{}, validationError(
			CodeInvalidEPUB, errors.New("package document is missing"))
	}
	packageXML, err := readMetadata(ctx, packageFile, limits.MaxMetadataBytes)
	if err != nil {
		return CoverImage{}, err
	}
	details, err := parsePackage(ctx, packageXML, packagePath, entries, limits)
	if err != nil {
		return CoverImage{}, err
	}
	metadata, err := extractPackageMetadata(
		ctx, packageXML, entries, details, limits)
	if err != nil {
		return CoverImage{}, err
	}
	// A publication that declares no cover and one that declares a cover
	// it does not contain are the same answer, so they are one branch: an
	// empty path is a path no entry has.
	file, ok := entries[metadata.CoverPath]
	if !ok || !file.Mode().IsRegular() {
		return CoverImage{}, ErrNoCover
	}
	data, err := readCoverEntry(ctx, file, maxCoverBytes)
	if err != nil {
		return CoverImage{}, err
	}
	return CoverImage{
		Path:      metadata.CoverPath,
		MediaType: metadata.CoverMediaType,
		Data:      data,
	}, nil
}

// readCoverEntry reads one entry under a hard cap. The cap is applied to
// the bytes actually produced rather than to the size the archive's
// directory claims, because that claim is written by whoever built the
// archive and a lying header is the whole problem.
func readCoverEntry(
	ctx context.Context, file *zip.File, max int64,
) ([]byte, error) {
	handle, err := file.Open()
	if err != nil {
		return nil, validationError(CodeInvalidEPUB, err)
	}
	defer handle.Close()
	data, err := io.ReadAll(io.LimitReader(
		contextReader{ctx: ctx, src: handle}, max+1))
	if err != nil {
		if errors.Is(err, ctx.Err()) && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, validationError(CodeInvalidEPUB, err)
	}
	if int64(len(data)) > max {
		return nil, validationError(CodeArchiveLimits,
			fmt.Errorf("cover %q exceeds limit", file.Name))
	}
	return data, nil
}
