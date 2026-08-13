// Package epub validates untrusted EPUB archives without extracting them.
package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"path"
	"sort"
	"strings"
)

// Code is a stable asynchronous ingestion error code.
type Code string

const (
	CodeInvalidEPUB    Code = "invalid_epub"
	CodeUnsafeArchive  Code = "unsafe_archive"
	CodeArchiveLimits  Code = "archive_limits"
	CodeUnsupportedDRM Code = "unsupported_drm"
)

// ErrInvalidValidationInput indicates an invalid caller-supplied reader,
// size, context, or limit set rather than invalid publication content.
var ErrInvalidValidationInput = errors.New("epub: invalid validation input")

// ValidationError classifies content failures without exposing parser details
// to API clients.
type ValidationError struct {
	Code Code
	Err  error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("epub validation %s: %v", e.Code, e.Err)
}

func (e *ValidationError) Unwrap() error { return e.Err }

// ErrorCode returns a stable code for a validation failure.
func ErrorCode(err error) (Code, bool) {
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		return "", false
	}
	return validationErr.Code, true
}

// Limits bounds all archive and XML work.
type Limits struct {
	MaxEntries           int
	MaxDirectoryBytes    int64
	MaxUncompressedBytes int64
	MaxEntryBytes        int64
	MaxCompressionRatio  uint64
	MaxMetadataBytes     int64
	MaxXMLDepth          int
}

// DefaultLimits returns conservative EPUB-first server limits.
func DefaultLimits() Limits {
	return Limits{
		MaxEntries:           10_000,
		MaxDirectoryBytes:    64 << 20,
		MaxUncompressedBytes: 2 << 30,
		MaxEntryBytes:        512 << 20,
		MaxCompressionRatio:  1_000,
		MaxMetadataBytes:     4 << 20,
		MaxXMLDepth:          128,
	}
}

// Result identifies the validated package document and bounded XML assets.
type Result struct {
	PackagePath    string
	NavigationPath string
	Encrypted      bool
}

// Validate verifies an EPUB ZIP and its bounded control documents.
func Validate(
	ctx context.Context,
	reader io.ReaderAt,
	size int64,
	limits Limits,
) (Result, error) {
	if ctx == nil || reader == nil || size < 0 || !limits.valid() {
		return Result{}, ErrInvalidValidationInput
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	layout, err := preflightZIP(
		ctx, reader, size, limits.MaxEntries, limits.MaxDirectoryBytes)
	if err != nil {
		return Result{}, err
	}
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		if errors.Is(err, zip.ErrInsecurePath) {
			return Result{}, validationError(CodeUnsafeArchive, err)
		}
		return Result{}, validationError(CodeInvalidEPUB, err)
	}
	if len(archive.File) == 0 || len(archive.File) > limits.MaxEntries {
		return Result{}, validationError(
			CodeArchiveLimits, errors.New("ZIP entry count exceeds limit"))
	}
	if len(archive.File) != len(layout.entries) {
		return Result{}, validationError(
			CodeInvalidEPUB, errors.New("ZIP entry layout is inconsistent"))
	}
	entries := make(map[string]*zip.File, len(archive.File))
	var total int64
	for index, file := range archive.File {
		offset, err := file.DataOffset()
		if err != nil || offset != layout.entries[index].dataOffset ||
			file.CompressedSize64 != layout.entries[index].compressedSize {
			return Result{}, validationError(
				CodeInvalidEPUB, errors.New("ZIP local entry is inconsistent"))
		}
		name, err := safeArchivePath(file.Name)
		if err != nil {
			return Result{}, validationError(CodeUnsafeArchive, err)
		}
		if _, duplicate := entries[name]; duplicate {
			return Result{}, validationError(
				CodeUnsafeArchive, fmt.Errorf("duplicate ZIP path %q", name))
		}
		entries[name] = file
		mode := file.Mode()
		if !mode.IsRegular() && !mode.IsDir() {
			return Result{}, validationError(
				CodeUnsafeArchive, fmt.Errorf("unsafe ZIP entry type %q", name))
		}
		if mode.IsDir() {
			if file.UncompressedSize64 != 0 || file.CompressedSize64 != 0 {
				return Result{}, validationError(
					CodeUnsafeArchive,
					fmt.Errorf("ZIP directory %q contains data", name))
			}
			continue
		}
		if file.UncompressedSize64 > uint64(limits.MaxEntryBytes) ||
			ratioExceeds(
				file.UncompressedSize64,
				layout.entries[index].compressedSize,
				limits.MaxCompressionRatio) {
			return Result{}, validationError(
				CodeArchiveLimits, fmt.Errorf("ZIP entry %q exceeds limits", name))
		}
		remaining := limits.MaxUncompressedBytes - total
		entryLimit := limits.MaxEntryBytes
		if remaining < entryLimit {
			entryLimit = remaining
		}
		actual, err := drainEntry(ctx, file, entryLimit)
		if err != nil {
			return Result{}, err
		}
		if ratioExceeds(
			actual,
			layout.entries[index].compressedSize,
			limits.MaxCompressionRatio) {
			return Result{}, validationError(
				CodeArchiveLimits, fmt.Errorf("ZIP entry %q exceeds limits", name))
		}
		if actual > uint64(limits.MaxUncompressedBytes-total) {
			return Result{}, validationError(
				CodeArchiveLimits, errors.New("expanded ZIP exceeds limit"))
		}
		total += int64(actual)
		if index == 0 && name == "mimetype" {
			if err := validateMimetype(
				ctx, file, limits.MaxMetadataBytes); err != nil {
				return Result{}, err
			}
		}
	}
	if archive.File[0].Name != "mimetype" {
		return Result{}, validationError(
			CodeInvalidEPUB, errors.New("mimetype must be the first ZIP entry"))
	}
	mimetype, ok := entries["mimetype"]
	if !ok {
		return Result{}, validationError(
			CodeInvalidEPUB, errors.New("mimetype entry is missing"))
	}
	if err := validateMimetype(
		ctx, mimetype, limits.MaxMetadataBytes); err != nil {
		return Result{}, err
	}

	container, ok := entries["META-INF/container.xml"]
	if !ok || !container.Mode().IsRegular() {
		return Result{}, validationError(
			CodeInvalidEPUB, errors.New("container.xml is missing"))
	}
	containerXML, err := readMetadata(
		ctx, container, limits.MaxMetadataBytes)
	if err != nil {
		return Result{}, err
	}
	packagePath, err := parseContainer(containerXML, limits.MaxXMLDepth)
	if err != nil {
		return Result{}, err
	}
	packageFile, ok := entries[packagePath]
	if !ok || !packageFile.Mode().IsRegular() {
		return Result{}, validationError(
			CodeInvalidEPUB, errors.New("package document is missing"))
	}
	packageXML, err := readMetadata(
		ctx, packageFile, limits.MaxMetadataBytes)
	if err != nil {
		return Result{}, err
	}
	packageInfo, err := parsePackage(
		ctx, packageXML, packagePath, entries, limits)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		PackagePath: packagePath, NavigationPath: packageInfo.navigationPath,
	}
	if encryption, ok := entries["META-INF/encryption.xml"]; ok {
		encryptionXML, err := readMetadata(
			ctx, encryption, limits.MaxMetadataBytes)
		if err != nil {
			return Result{}, err
		}
		encrypted, err := validateEncryption(
			encryptionXML, entries, packageInfo.manifest, limits.MaxXMLDepth)
		if err != nil {
			return Result{}, err
		}
		result.Encrypted = encrypted
	}
	return result, nil
}

func (l Limits) valid() bool {
	return l.MaxEntries > 0 &&
		l.MaxDirectoryBytes > 0 &&
		l.MaxUncompressedBytes > 0 &&
		l.MaxEntryBytes > 0 &&
		l.MaxEntryBytes < math.MaxInt64 &&
		l.MaxCompressionRatio > 0 &&
		l.MaxMetadataBytes > 0 &&
		l.MaxMetadataBytes < math.MaxInt64 &&
		l.MaxXMLDepth > 0
}

func validationError(code Code, err error) error {
	return &ValidationError{Code: code, Err: err}
}

type zipEntryLayout struct {
	localOffset    int64
	dataOffset     int64
	compressedSize uint64
	flags          uint16
	method         uint16
	name           string
}

type zipLayout struct {
	entries []zipEntryLayout
}

func preflightZIP(
	ctx context.Context,
	reader io.ReaderAt,
	size int64,
	maxEntries int,
	maxDirectoryBytes int64,
) (zipLayout, error) {
	const (
		endHeaderSize      = 22
		localHeaderSize    = 30
		maxCommentSize     = 1<<16 - 1
		endSignature       = 0x06054b50
		localSignature     = 0x04034b50
		directorySignature = 0x02014b50
	)
	var layout zipLayout
	if size < endHeaderSize {
		return layout, validationError(
			CodeInvalidEPUB, errors.New("ZIP is truncated"))
	}
	tailSize := int64(endHeaderSize + maxCommentSize)
	if tailSize > size {
		tailSize = size
	}
	tail := make([]byte, tailSize)
	if _, err := reader.ReadAt(tail, size-tailSize); err != nil {
		return layout, validationError(CodeInvalidEPUB, err)
	}
	endIndex := -1
	for index := len(tail) - endHeaderSize; index >= 0; index-- {
		if binary.LittleEndian.Uint32(tail[index:index+4]) != endSignature {
			continue
		}
		commentLength := int(binary.LittleEndian.Uint16(
			tail[index+20 : index+22]))
		if index+endHeaderSize+commentLength == len(tail) {
			endIndex = index
			break
		}
	}
	if endIndex < 0 {
		return layout, validationError(
			CodeInvalidEPUB, errors.New("ZIP end record is missing"))
	}
	end := tail[endIndex : endIndex+endHeaderSize]
	if binary.LittleEndian.Uint16(end[4:6]) != 0 ||
		binary.LittleEndian.Uint16(end[6:8]) != 0 {
		return layout, validationError(
			CodeInvalidEPUB, errors.New("multi-disk ZIP is unsupported"))
	}
	entriesOnDisk := binary.LittleEndian.Uint16(end[8:10])
	totalEntries := binary.LittleEndian.Uint16(end[10:12])
	directorySize := binary.LittleEndian.Uint32(end[12:16])
	directoryOffset := binary.LittleEndian.Uint32(end[16:20])
	if entriesOnDisk == math.MaxUint16 || totalEntries == math.MaxUint16 ||
		directorySize == math.MaxUint32 || directoryOffset == math.MaxUint32 {
		return layout, validationError(
			CodeArchiveLimits, errors.New("ZIP64 EPUBs are unsupported"))
	}
	if entriesOnDisk != totalEntries || int(totalEntries) > maxEntries {
		return layout, validationError(
			CodeArchiveLimits, errors.New("ZIP entry count exceeds limit"))
	}
	if int64(directorySize) > maxDirectoryBytes {
		return layout, validationError(
			CodeArchiveLimits,
			errors.New("ZIP central directory exceeds limit"))
	}
	directoryEnd := int64(directoryOffset) + int64(directorySize)
	endOffset := size - tailSize + int64(endIndex)
	if directoryEnd != endOffset || directoryEnd < int64(directoryOffset) {
		return layout, validationError(
			CodeInvalidEPUB, errors.New("invalid ZIP central directory bounds"))
	}
	layout.entries = make([]zipEntryLayout, 0, totalEntries)
	var header [46]byte
	offset := int64(directoryOffset)
	count := 0
	for offset < directoryEnd {
		if err := ctx.Err(); err != nil {
			return zipLayout{}, err
		}
		if directoryEnd-offset < int64(len(header)) {
			return zipLayout{}, validationError(
				CodeInvalidEPUB, errors.New("truncated ZIP directory entry"))
		}
		if _, err := reader.ReadAt(header[:], offset); err != nil {
			return zipLayout{}, validationError(CodeInvalidEPUB, err)
		}
		if binary.LittleEndian.Uint32(header[0:4]) != directorySignature {
			return zipLayout{}, validationError(
				CodeInvalidEPUB, errors.New("invalid ZIP directory entry"))
		}
		compressedSize := binary.LittleEndian.Uint32(header[20:24])
		uncompressedSize := binary.LittleEndian.Uint32(header[24:28])
		localOffset := binary.LittleEndian.Uint32(header[42:46])
		if compressedSize == math.MaxUint32 ||
			uncompressedSize == math.MaxUint32 ||
			localOffset == math.MaxUint32 ||
			binary.LittleEndian.Uint16(header[34:36]) != 0 {
			return zipLayout{}, validationError(
				CodeArchiveLimits,
				errors.New("ZIP64 or multi-disk entry is unsupported"))
		}
		nameLength := int64(binary.LittleEndian.Uint16(header[28:30]))
		extraLength := int64(binary.LittleEndian.Uint16(header[30:32]))
		commentLength := int64(binary.LittleEndian.Uint16(header[32:34]))
		flags := binary.LittleEndian.Uint16(header[8:10])
		method := binary.LittleEndian.Uint16(header[10:12])
		if flags&1 != 0 {
			return zipLayout{}, validationError(
				CodeUnsupportedDRM,
				errors.New("ZIP-level encryption is unsupported"))
		}
		name := make([]byte, nameLength)
		if _, err := reader.ReadAt(name, offset+int64(len(header))); err != nil {
			return zipLayout{}, validationError(CodeInvalidEPUB, err)
		}
		offset += int64(len(header)) + nameLength + extraLength + commentLength
		if offset > directoryEnd {
			return zipLayout{}, validationError(
				CodeInvalidEPUB, errors.New("truncated ZIP directory entry"))
		}
		count++
		if count > maxEntries {
			return zipLayout{}, validationError(
				CodeArchiveLimits, errors.New("ZIP entry count exceeds limit"))
		}
		layout.entries = append(layout.entries, zipEntryLayout{
			localOffset: int64(localOffset), compressedSize: uint64(compressedSize),
			flags: flags, method: method, name: string(name),
		})
	}
	if count != int(totalEntries) {
		return zipLayout{}, validationError(
			CodeInvalidEPUB, errors.New("ZIP entry count is inconsistent"))
	}
	var localHeader [localHeaderSize]byte
	for index := range layout.entries {
		entry := &layout.entries[index]
		if entry.localOffset < 0 ||
			entry.localOffset+localHeaderSize > int64(directoryOffset) {
			return zipLayout{}, validationError(
				CodeInvalidEPUB, errors.New("invalid ZIP local header bounds"))
		}
		if _, err := reader.ReadAt(localHeader[:], entry.localOffset); err != nil {
			return zipLayout{}, validationError(CodeInvalidEPUB, err)
		}
		if binary.LittleEndian.Uint32(localHeader[0:4]) != localSignature {
			return zipLayout{}, validationError(
				CodeInvalidEPUB, errors.New("invalid ZIP local header"))
		}
		nameLength := int64(binary.LittleEndian.Uint16(localHeader[26:28]))
		extraLength := int64(binary.LittleEndian.Uint16(localHeader[28:30]))
		if binary.LittleEndian.Uint16(localHeader[6:8]) != entry.flags ||
			binary.LittleEndian.Uint16(localHeader[8:10]) != entry.method {
			return zipLayout{}, validationError(
				CodeInvalidEPUB,
				errors.New("ZIP local and central headers disagree"))
		}
		localName := make([]byte, nameLength)
		if _, err := reader.ReadAt(
			localName, entry.localOffset+localHeaderSize); err != nil {
			return zipLayout{}, validationError(CodeInvalidEPUB, err)
		}
		if string(localName) != entry.name {
			return zipLayout{}, validationError(
				CodeInvalidEPUB,
				errors.New("ZIP local and central names disagree"))
		}
		entry.dataOffset = entry.localOffset +
			localHeaderSize + nameLength + extraLength
		dataEnd := entry.dataOffset + int64(entry.compressedSize)
		if entry.dataOffset < entry.localOffset || dataEnd < entry.dataOffset ||
			dataEnd > int64(directoryOffset) {
			return zipLayout{}, validationError(
				CodeInvalidEPUB, errors.New("invalid ZIP entry data bounds"))
		}
	}
	ordered := append([]zipEntryLayout(nil), layout.entries...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].localOffset < ordered[j].localOffset
	})
	if len(ordered) > 0 &&
		layout.entries[0].localOffset != ordered[0].localOffset {
		return zipLayout{}, validationError(
			CodeInvalidEPUB,
			errors.New("mimetype is not the first physical ZIP entry"))
	}
	for index := 1; index < len(ordered); index++ {
		previousEnd := ordered[index-1].dataOffset +
			int64(ordered[index-1].compressedSize)
		if previousEnd > ordered[index].localOffset ||
			ordered[index-1].localOffset == ordered[index].localOffset {
			return zipLayout{}, validationError(
				CodeInvalidEPUB, errors.New("overlapping ZIP entries"))
		}
	}
	return layout, nil
}

func safeArchivePath(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) ||
		strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe ZIP path %q", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") ||
		clean != strings.TrimSuffix(name, "/") {
		return "", fmt.Errorf("unsafe ZIP path %q", name)
	}
	return clean, nil
}

func ratioExceeds(uncompressed, compressed, limit uint64) bool {
	if uncompressed == 0 {
		return false
	}
	if compressed == 0 {
		return true
	}
	quotient := uncompressed / compressed
	return quotient > limit ||
		(quotient == limit && uncompressed%compressed != 0)
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.src.Read(buffer)
}

func drainEntry(
	ctx context.Context,
	file *zip.File,
	max int64,
) (uint64, error) {
	reader, err := file.Open()
	if err != nil {
		return 0, validationError(CodeInvalidEPUB, err)
	}
	defer reader.Close()
	written, err := io.Copy(
		io.Discard, io.LimitReader(contextReader{ctx: ctx, src: reader}, max+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		return 0, validationError(CodeInvalidEPUB, err)
	}
	if written > max {
		return 0, validationError(
			CodeArchiveLimits, fmt.Errorf("ZIP entry %q exceeds limit", file.Name))
	}
	return uint64(written), nil
}

func validateMimetype(ctx context.Context, file *zip.File, max int64) error {
	if file.Method != zip.Store {
		return validationError(
			CodeInvalidEPUB, errors.New("mimetype entry must be uncompressed"))
	}
	value, err := readMetadata(ctx, file, max)
	if err != nil {
		return err
	}
	if !bytes.Equal(value, []byte("application/epub+zip")) {
		return validationError(
			CodeInvalidEPUB, errors.New("invalid EPUB mimetype"))
	}
	return nil
}

func readMetadata(
	ctx context.Context,
	file *zip.File,
	max int64,
) ([]byte, error) {
	if file.UncompressedSize64 > uint64(max) {
		return nil, validationError(
			CodeArchiveLimits, fmt.Errorf("metadata %q exceeds limit", file.Name))
	}
	reader, err := file.Open()
	if err != nil {
		return nil, validationError(CodeInvalidEPUB, err)
	}
	defer reader.Close()
	value, err := io.ReadAll(
		io.LimitReader(contextReader{ctx: ctx, src: reader}, max+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, validationError(CodeInvalidEPUB, err)
	}
	if int64(len(value)) > max {
		return nil, validationError(
			CodeArchiveLimits, fmt.Errorf("metadata %q exceeds limit", file.Name))
	}
	return value, nil
}

func parseContainer(value []byte, maxDepth int) (string, error) {
	const containerNamespace = "urn:oasis:names:tc:opendocument:xmlns:container"
	decoder := xml.NewDecoder(bytes.NewReader(value))
	depth := 0
	roots := 0
	var packagePath string
	var stack []xml.Name
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if packagePath == "" {
				return "", validationError(
					CodeInvalidEPUB,
					errors.New("container has no package document"))
			}
			if roots != 1 {
				return "", validationError(
					CodeInvalidEPUB,
					errors.New("container must have one document root"))
			}
			return packagePath, nil
		}
		if err != nil {
			return "", validationError(CodeInvalidEPUB, err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || typed.Name.Local != "container" ||
					typed.Name.Space != containerNamespace {
					return "", validationError(
						CodeInvalidEPUB,
						errors.New("invalid container document root"))
				}
			}
			depth++
			parent := xml.Name{}
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			stack = append(stack, typed.Name)
			if depth > maxDepth {
				return "", validationError(
					CodeArchiveLimits, errors.New("container XML is too deep"))
			}
			if typed.Name == (xml.Name{
				Space: containerNamespace, Local: "rootfile",
			}) && parent == (xml.Name{
				Space: containerNamespace, Local: "rootfiles",
			}) {
				fullPath := unqualifiedAttribute(typed.Attr, "full-path")
				mediaType := unqualifiedAttribute(typed.Attr, "media-type")
				if mediaType == "application/oebps-package+xml" {
					clean, err := safeArchivePath(fullPath)
					if err != nil {
						return "", validationError(CodeUnsafeArchive, err)
					}
					if packagePath == "" {
						packagePath = clean
					}
				}
			}
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1] != typed.Name {
				return "", validationError(
					CodeInvalidEPUB,
					errors.New("invalid container XML structure"))
			}
			stack = stack[:len(stack)-1]
			depth--
		case xml.Directive:
			return "", validationError(
				CodeInvalidEPUB, errors.New("XML directives are not allowed"))
		}
	}
}

type packageDetails struct {
	navigationPath string
	manifest       map[string]string
}

type navigationDocument struct {
	path      string
	root      string
	namespace string
}

func parsePackage(
	ctx context.Context,
	value []byte,
	packagePath string,
	entries map[string]*zip.File,
	limits Limits,
) (packageDetails, error) {
	const packageNamespace = "http://www.idpf.org/2007/opf"
	decoder := xml.NewDecoder(bytes.NewReader(value))
	depth := 0
	roots := 0
	manifestItems := 0
	details := packageDetails{manifest: make(map[string]string)}
	navigationSeen := make(map[string]bool)
	var navigation []navigationDocument
	var stack []xml.Name
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if roots != 1 {
				return packageDetails{}, validationError(
					CodeInvalidEPUB,
					errors.New("package must have one document root"))
			}
			for _, document := range navigation {
				file, ok := entries[document.path]
				if !ok || !file.Mode().IsRegular() {
					return packageDetails{}, validationError(
						CodeInvalidEPUB,
						errors.New("navigation document is missing"))
				}
				navigationXML, err := readMetadata(
					ctx, file, limits.MaxMetadataBytes)
				if err != nil {
					return packageDetails{}, err
				}
				if err := validateXMLDocument(
					navigationXML,
					limits.MaxXMLDepth,
					"navigation",
					document.root,
					document.namespace,
				); err != nil {
					return packageDetails{}, err
				}
			}
			if len(navigation) > 0 {
				details.navigationPath = navigation[0].path
			}
			return details, nil
		}
		if err != nil {
			return packageDetails{}, validationError(CodeInvalidEPUB, err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || typed.Name.Local != "package" ||
					typed.Name.Space != packageNamespace {
					return packageDetails{}, validationError(
						CodeInvalidEPUB,
						errors.New("invalid package document root"))
				}
			}
			parent := xml.Name{}
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			depth++
			stack = append(stack, typed.Name)
			if depth > limits.MaxXMLDepth {
				return packageDetails{}, validationError(
					CodeArchiveLimits, errors.New("package XML is too deep"))
			}
			if typed.Name != (xml.Name{
				Space: packageNamespace, Local: "item",
			}) || parent != (xml.Name{
				Space: packageNamespace, Local: "manifest",
			}) {
				continue
			}
			manifestItems++
			if manifestItems > limits.MaxEntries {
				return packageDetails{}, validationError(
					CodeArchiveLimits,
					errors.New("package manifest exceeds entry limit"))
			}
			href := unqualifiedAttribute(typed.Attr, "href")
			mediaType := unqualifiedAttribute(typed.Attr, "media-type")
			properties := unqualifiedAttribute(typed.Attr, "properties")
			if href == "" || mediaType == "" {
				return packageDetails{}, validationError(
					CodeInvalidEPUB,
					errors.New("manifest item lacks href or media type"))
			}
			resolved, err := resolveArchiveReference(packagePath, href)
			if err != nil {
				return packageDetails{}, err
			}
			if existing, duplicate := details.manifest[resolved]; duplicate && existing != mediaType {
				return packageDetails{}, validationError(
					CodeInvalidEPUB,
					fmt.Errorf("conflicting manifest path %q", resolved))
			}
			details.manifest[resolved] = mediaType
			isNavigation := mediaType == "application/x-dtbncx+xml" ||
				strings.Contains(" "+properties+" ", " nav ")
			if !isNavigation || navigationSeen[resolved] {
				continue
			}
			navigationSeen[resolved] = true
			document := navigationDocument{
				path: resolved, root: "html",
				namespace: "http://www.w3.org/1999/xhtml",
			}
			if mediaType == "application/x-dtbncx+xml" {
				document.root = "ncx"
				document.namespace = "http://www.daisy.org/z3986/2005/ncx/"
			}
			navigation = append(navigation, document)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1] != typed.Name {
				return packageDetails{}, validationError(
					CodeInvalidEPUB,
					errors.New("invalid package XML structure"))
			}
			stack = stack[:len(stack)-1]
			depth--
		case xml.Directive:
			return packageDetails{}, validationError(
				CodeInvalidEPUB, errors.New("XML directives are not allowed"))
		}
	}
}

func resolveArchiveReference(basePath, reference string) (string, error) {
	parsed, err := url.Parse(reference)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", validationError(
			CodeUnsafeArchive, errors.New("unsafe publication reference"))
	}
	unescaped, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", validationError(CodeInvalidEPUB, err)
	}
	resolved := path.Join(path.Dir(basePath), unescaped)
	clean, err := safeArchivePath(resolved)
	if err != nil {
		return "", validationError(CodeUnsafeArchive, err)
	}
	return clean, nil
}

func unqualifiedAttribute(attributes []xml.Attr, name string) string {
	for _, attribute := range attributes {
		if attribute.Name.Space == "" && attribute.Name.Local == name {
			return attribute.Value
		}
	}
	return ""
}

func validateXMLDocument(
	value []byte,
	maxDepth int,
	name, root, namespace string,
) error {
	decoder := xml.NewDecoder(bytes.NewReader(value))
	depth := 0
	roots := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if roots != 1 {
				return validationError(
					CodeInvalidEPUB,
					fmt.Errorf("%s must have one document root", name))
			}
			return nil
		}
		if err != nil {
			return validationError(CodeInvalidEPUB, err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || typed.Name.Local != root ||
					typed.Name.Space != namespace {
					return validationError(
						CodeInvalidEPUB,
						fmt.Errorf("invalid %s document root", name))
				}
			}
			depth++
			if depth > maxDepth {
				return validationError(
					CodeArchiveLimits, fmt.Errorf("%s XML is too deep", name))
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return validationError(
				CodeInvalidEPUB, errors.New("XML directives are not allowed"))
		}
	}
}

func validateEncryption(
	value []byte,
	entries map[string]*zip.File,
	manifest map[string]string,
	maxDepth int,
) (bool, error) {
	const (
		idpfObfuscation  = "http://www.idpf.org/2008/embedding"
		adobeObfuscation = "http://ns.adobe.com/pdf/enc#RC"
		containerNS      = "urn:oasis:names:tc:opendocument:xmlns:container"
		xmlEncryptionNS  = "http://www.w3.org/2001/04/xmlenc#"
	)
	decoder := xml.NewDecoder(bytes.NewReader(value))
	depth := 0
	roots := 0
	encrypted := false
	var currentAlgorithm, currentReference string
	methods, cipherData, references := 0, 0, 0
	insideEncryptedData := false
	var stack []xml.Name
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if roots != 1 || insideEncryptedData {
				return false, validationError(
					CodeInvalidEPUB,
					errors.New("invalid encryption document structure"))
			}
			return encrypted, nil
		}
		if err != nil {
			return false, validationError(CodeInvalidEPUB, err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || typed.Name.Local != "encryption" ||
					typed.Name.Space != containerNS {
					return false, validationError(
						CodeInvalidEPUB,
						errors.New("invalid encryption document root"))
				}
			}
			parent := xml.Name{}
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			depth++
			stack = append(stack, typed.Name)
			if depth > maxDepth {
				return false, validationError(
					CodeArchiveLimits, errors.New("encryption XML is too deep"))
			}
			switch typed.Name {
			case xml.Name{Space: xmlEncryptionNS, Local: "EncryptedData"}:
				if insideEncryptedData || parent != (xml.Name{
					Space: containerNS, Local: "encryption",
				}) {
					return false, validationError(
						CodeInvalidEPUB,
						errors.New("invalid EncryptedData structure"))
				}
				insideEncryptedData = true
				currentAlgorithm = ""
				currentReference = ""
				methods = 0
				cipherData = 0
				references = 0
			case xml.Name{Space: xmlEncryptionNS, Local: "EncryptionMethod"}:
				if !insideEncryptedData || parent != (xml.Name{
					Space: xmlEncryptionNS, Local: "EncryptedData",
				}) {
					return false, validationError(
						CodeInvalidEPUB,
						errors.New("EncryptionMethod is outside EncryptedData"))
				}
				methods++
				if methods != 1 {
					return false, validationError(
						CodeInvalidEPUB,
						errors.New("duplicate EncryptionMethod"))
				}
				currentAlgorithm = unqualifiedAttribute(
					typed.Attr, "Algorithm")
			case xml.Name{Space: xmlEncryptionNS, Local: "CipherData"}:
				if !insideEncryptedData || parent != (xml.Name{
					Space: xmlEncryptionNS, Local: "EncryptedData",
				}) {
					return false, validationError(
						CodeInvalidEPUB,
						errors.New("CipherData is outside EncryptedData"))
				}
				cipherData++
				if cipherData != 1 {
					return false, validationError(
						CodeInvalidEPUB,
						errors.New("duplicate CipherData"))
				}
			case xml.Name{Space: xmlEncryptionNS, Local: "CipherReference"}:
				if !insideEncryptedData || cipherData != 1 ||
					parent != (xml.Name{
						Space: xmlEncryptionNS, Local: "CipherData",
					}) {
					return false, validationError(
						CodeInvalidEPUB,
						errors.New("CipherReference is outside EncryptedData"))
				}
				references++
				if references != 1 {
					return false, validationError(
						CodeInvalidEPUB,
						errors.New("duplicate CipherReference"))
				}
				currentReference = unqualifiedAttribute(typed.Attr, "URI")
			}
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1] != typed.Name {
				return false, validationError(
					CodeInvalidEPUB,
					errors.New("invalid encryption XML structure"))
			}
			stack = stack[:len(stack)-1]
			if typed.Name == (xml.Name{
				Space: xmlEncryptionNS, Local: "EncryptedData",
			}) {
				if !insideEncryptedData {
					return false, validationError(
						CodeInvalidEPUB,
						errors.New("unexpected EncryptedData close"))
				}
				if methods != 1 || cipherData != 1 || references != 1 {
					return false, validationError(
						CodeInvalidEPUB,
						errors.New(
							"EncryptedData requires one method and reference"))
				}
				if currentAlgorithm != idpfObfuscation &&
					currentAlgorithm != adobeObfuscation {
					return false, validationError(
						CodeUnsupportedDRM,
						fmt.Errorf(
							"unsupported encryption algorithm %q",
							currentAlgorithm))
				}
				reference, err := resolveEncryptedReference(currentReference)
				if err != nil {
					return false, err
				}
				file, ok := entries[reference]
				mediaType := manifest[reference]
				if !ok || !file.Mode().IsRegular() ||
					!isFontPath(reference) || !isFontMediaType(mediaType) {
					return false, validationError(
						CodeUnsupportedDRM,
						fmt.Errorf(
							"font obfuscation targets non-font %q",
							reference))
				}
				encrypted = true
				insideEncryptedData = false
			}
			depth--
		case xml.Directive:
			return false, validationError(
				CodeInvalidEPUB, errors.New("XML directives are not allowed"))
		}
	}
}

func resolveEncryptedReference(reference string) (string, error) {
	parsed, err := url.Parse(reference)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", validationError(
			CodeUnsafeArchive, errors.New("unsafe encrypted resource reference"))
	}
	unescaped, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", validationError(CodeInvalidEPUB, err)
	}
	clean, err := safeArchivePath(unescaped)
	if err != nil {
		return "", validationError(CodeUnsafeArchive, err)
	}
	return clean, nil
}

func isFontPath(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".otf", ".ttf", ".woff", ".woff2":
		return true
	default:
		return false
	}
}

func isFontMediaType(mediaType string) bool {
	switch strings.ToLower(mediaType) {
	case "application/font-sfnt",
		"application/font-woff",
		"application/vnd.ms-opentype",
		"application/x-font-opentype",
		"application/x-font-ttf",
		"font/otf",
		"font/ttf",
		"font/woff",
		"font/woff2":
		return true
	default:
		return false
	}
}
