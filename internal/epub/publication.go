package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"net/url"
	"sort"
	"strings"

	"github.com/readium/go-toolkit/pkg/asset"
	"github.com/readium/go-toolkit/pkg/fetcher"
	"github.com/readium/go-toolkit/pkg/manifest"
	"github.com/readium/go-toolkit/pkg/mediatype"
	readium "github.com/readium/go-toolkit/pkg/parser/epub"
	"github.com/readium/go-toolkit/pkg/pub"
)

// Publication is a request-local, read-only view of an already catalogued EPUB.
// Its caller owns the underlying descriptor. Opening it never drains chapter
// entries: full validation belongs to reconciliation, not a reader request.
type Publication struct {
	*pub.Publication
	PackagePath     string
	archive         *publicationArchive
	allowed         map[string]manifest.Link
	fontObfuscation map[string]string
}

func OpenPublication(ctx context.Context, source io.ReaderAt, size int64, limits Limits) (*Publication, error) {
	if ctx == nil || source == nil || size < 0 || limits.Validate() != nil {
		return nil, ErrInvalidValidationInput
	}
	layout, err := preflightZIP(ctx, source, size, limits.MaxEntries, limits.MaxDirectoryBytes)
	if err != nil {
		return nil, err
	}
	z, err := zip.NewReader(source, size)
	if err != nil {
		return nil, validationError(CodeInvalidEPUB, err)
	}
	if len(z.File) != len(layout.entries) {
		return nil, validationError(CodeInvalidEPUB, errors.New("inconsistent ZIP directory"))
	}
	a := &publicationArchive{entries: make(map[string]*zip.File), limits: limits, metadataOnly: true}
	var total uint64
	for i, f := range z.File {
		name, err := safeArchivePath(f.Name)
		if err != nil {
			return nil, validationError(CodeUnsafeArchive, err)
		}
		if _, exists := a.entries[name]; exists || (!f.Mode().IsRegular() && !f.Mode().IsDir()) {
			return nil, validationError(CodeUnsafeArchive, errors.New("duplicate or unsafe archive entry"))
		}
		offset, err := f.DataOffset()
		if err != nil || offset != layout.entries[i].dataOffset || f.CompressedSize64 != layout.entries[i].compressedSize {
			return nil, validationError(CodeInvalidEPUB, errors.New("inconsistent ZIP entry"))
		}
		if f.UncompressedSize64 > uint64(limits.MaxEntryBytes) || f.UncompressedSize64 > uint64(limits.MaxUncompressedBytes)-total || ratioExceeds(f.UncompressedSize64, f.CompressedSize64, limits.MaxCompressionRatio) {
			return nil, validationError(CodeArchiveLimits, errors.New("archive exceeds publication limits"))
		}
		total += f.UncompressedSize64
		a.entries[name] = f
	}
	control := func(name string) ([]byte, error) {
		f := a.entries[name]
		if f == nil {
			return nil, validationError(CodeInvalidEPUB, errors.New("missing EPUB control document"))
		}
		return readMetadata(ctx, f, limits.MaxMetadataBytes)
	}
	mimetype := a.entries["mimetype"]
	if mimetype == nil {
		return nil, validationError(CodeInvalidEPUB, errors.New("missing mimetype"))
	}
	if err := validateMimetype(ctx, mimetype, limits.MaxMetadataBytes); err != nil {
		return nil, err
	}
	container, err := control("META-INF/container.xml")
	if err != nil {
		return nil, err
	}
	packagePath, err := parseContainer(container, limits.MaxXMLDepth)
	if err != nil {
		return nil, err
	}
	packageXML, err := control(packagePath)
	if err != nil {
		return nil, err
	}
	details, err := parsePackage(ctx, packageXML, packagePath, a.entries, limits)
	if err != nil {
		return nil, err
	}
	var fontObfuscation map[string]string
	if _, exists := a.entries["META-INF/encryption.xml"]; exists {
		data, err := control("META-INF/encryption.xml")
		if err != nil {
			return nil, err
		}
		fontObfuscation, err = validateEncryption(data, a.entries, details.manifest, limits.MaxXMLDepth)
		if err != nil {
			return nil, err
		}
	}
	// These identify unsupported DRM even when encryption.xml is absent.
	for _, name := range []string{"META-INF/rights.xml", "META-INF/sinf.xml"} {
		if a.entries[name] != nil {
			return nil, validationError(CodeUnsupportedDRM, errors.New("protected publication"))
		}
	}
	builder, err := readium.NewParser(readium.ArchiveEntryLength{PageLength: 1024}).Parse(ctx, publicationAsset{}, a)
	if err != nil {
		return nil, validationError(CodeInvalidEPUB, err)
	}
	if a.err != nil {
		return nil, a.err
	}
	if builder == nil || len(builder.Manifest.ReadingOrder) == 0 {
		return nil, validationError(CodeInvalidEPUB, errors.New("empty reading order"))
	}
	a.metadataOnly = false
	p := &Publication{Publication: builder.Build(), PackagePath: packagePath, archive: a, allowed: make(map[string]manifest.Link), fontObfuscation: fontObfuscation}
	for _, list := range []manifest.LinkList{p.Manifest.ReadingOrder, p.Manifest.Resources} {
		for i := range list {
			name, err := publicationPath(list[i].Href.String())
			if err != nil || a.entries[name] == nil {
				return nil, validationError(CodeUnsafeArchive, errors.New("publication references an unavailable resource"))
			}
			f := a.entries[name]
			list[i].Size = uint(f.UncompressedSize64)
			if list[i].Properties == nil {
				list[i].Properties = manifest.Properties{}
			}
			list[i].Properties["https://readium.org/webpub-manifest/properties#archive"] = map[string]interface{}{"entryLength": f.CompressedSize64}
			p.allowed[name] = list[i]
		}
	}
	// The package is needed only to resolve legacy CFI spine indirections.
	packageLink := manifest.Link{Href: manifest.MustNewHREFFromString((&url.URL{Path: packagePath}).String(), false)}
	p.allowed[packagePath] = packageLink
	return p, nil
}

// Resource returns only declared publication resources or its package document.
func (p *Publication) Resource(ctx context.Context, name string) ([]byte, error) {
	r, size, err := p.OpenResource(name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, size+1))
	if err != nil {
		return nil, validationError(CodeInvalidEPUB, err)
	}
	if int64(len(data)) != size {
		return nil, validationError(CodeInvalidEPUB, errors.New("truncated publication resource"))
	}
	return data, nil
}

// OpenResource opens one declared archive entry without buffering it. The
// caller must close the returned reader; size is the bounded ZIP directory
// length and is suitable for Content-Length.
func (p *Publication) OpenResource(name string) (io.ReadCloser, int64, error) {
	if !p.HasResource(name) {
		return nil, 0, errors.New("publication resource not found")
	}
	f := p.archive.entries[name]
	if f == nil || !f.Mode().IsRegular() {
		return nil, 0, errors.New("publication resource not found")
	}
	r, err := f.Open()
	if err != nil {
		return nil, 0, validationError(CodeInvalidEPUB, err)
	}
	if algorithm, ok := p.fontObfuscation[name]; ok {
		r = newDeobfuscatingReader(r, p.Manifest.Metadata, algorithm)
	}
	return r, int64(f.UncompressedSize64), nil
}

func (p *Publication) HasResource(name string) bool { _, ok := p.allowed[name]; return ok }

// IndexEntry records where one archive entry sits in the ZIP directory, so
// a later request for the same content digest can be verified without
// rerunning preflightZIP or the toolkit parser: a fresh zip.Reader over the
// same bytes must describe that entry at the exact same offset, sizes and
// compression method, or the archive changed under the catalog's back.
type IndexEntry struct {
	Offset           int64
	CompressedSize   uint64
	UncompressedSize uint64
	Method           uint16
}

// Index is the file-independent shape of a Publication: the manifest,
// positions and each allowed entry's ZIP directory coordinates. It never
// holds a reference to the archive it was built from, so it is safe to
// keep across requests (and across the file handle that produced it)
// for as long as the catalog's content digest names the same bytes.
type Index struct {
	PackagePath string
	Manifest    manifest.Manifest
	Positions   []manifest.Locator
	Entries     map[string]IndexEntry
	// FontObfuscation maps an archive path to the obfuscation algorithm
	// URI declared for it in META-INF/encryption.xml, for the entries
	// validateEncryption confirmed are permitted font obfuscation (the
	// only encryption this server accepts at all). OpenIndexedResource
	// reverses it before returning bytes for such a path.
	FontObfuscation map[string]string
}

func (idx *Index) HasResource(name string) bool { _, ok := idx.Entries[name]; return ok }

// Index builds the cacheable shape of this Publication. It reads no
// chapter bytes: entry coordinates come from the ZIP directory already
// read by OpenPublication, and positions come from the reading order's own
// recorded sizes. maxPositions bounds how fine-grained the position list
// is allowed to get, not whether one is produced at all: the reader
// requires a non-empty position list to open a book at all, so a
// publication whose full, byte-granular list (one per 1,024 archive bytes
// of reading-order content, the toolkit's own count) would exceed
// maxPositions instead gets a coarser, size-weighted list built from the
// same per-entry sizes rather than reading content, capped independently
// of maxPositions so a cache of these indexes never has to hold as many
// locators as the cap technically allows. A fixed-layout publication's
// real position list is one Locator per reading-order item with no
// content read at all (the toolkit never touches the fetcher for it), so
// it is always cheap and never estimated or capped. A maxPositions of 0 or
// less means unbounded (always the fine-grained list).
func (p *Publication) Index(ctx context.Context, maxPositions int) *Index {
	entries := make(map[string]IndexEntry, len(p.allowed))
	for name := range p.allowed {
		f := p.archive.entries[name]
		if f == nil {
			continue
		}
		offset, err := f.DataOffset()
		if err != nil {
			continue
		}
		entries[name] = IndexEntry{
			Offset: offset, CompressedSize: f.CompressedSize64,
			UncompressedSize: f.UncompressedSize64, Method: f.Method,
		}
	}
	fixed := p.Manifest.Metadata.Layout == manifest.LayoutFixed
	var positions []manifest.Locator
	if fixed || maxPositions <= 0 || p.estimatedPositionCount() <= maxPositions {
		positions = p.Positions(ctx)
	}
	if len(positions) == 0 {
		positions = p.boundedPositions()
	}
	return &Index{
		PackagePath: p.PackagePath, Manifest: p.Manifest, Positions: positions,
		Entries: entries, FontObfuscation: p.fontObfuscation,
	}
}

// fallbackPositionBudget bounds how many extra, size-weighted positions
// boundedPositions distributes on top of the one mandatory position every
// reading-order item gets — not the total, so a publication with many
// chapters can't crowd out that weighted pool by consuming it on mandatory
// minimums alone. It is independent of the maxPositions a caller passes to
// Index: that parameter only decides when the fine-grained list is too
// expensive to generate, but the fallback itself is cached the same way,
// and a cache of PublicationIndexCache's default 32 entries each holding
// maxPositions (200,000) fallback locators could still retain millions of
// them. The fallback is already an approximation, so a few thousand
// positions give a reader plenty of granularity for progress tracking
// without the memory cost the estimate cap was meant to avoid in the
// first place.
const fallbackPositionBudget = 4096

// boundedPositions is the fallback for a publication too large to afford
// the toolkit's byte-granular position list (never called unless the
// fine-grained list was skipped or came back empty). Every reading-order
// item first gets one mandatory position, so the reader can always place
// it; a further fallbackPositionBudget positions are then distributed
// across the reading order in proportion to each item's own archive size,
// using the largest-remainder method so the total never exceeds the
// combined budget.
//
// Each locator's TotalProgression is computed directly from cumulative
// archive bytes, not from its ordinal position among the mandatory-plus-
// weighted counts: with many chapters, the mandatory one-per-item
// minimum can dominate that count even though it holds almost none of
// the bytes, and a client that divides progress by position multiplicity
// (as reader-engine.js does for its per-chapter progress markers) would
// then under-report a large chapter's true weight. Deriving
// TotalProgression from bytes instead keeps it accurate regardless of
// how the discrete positions themselves are distributed.
func (p *Publication) boundedPositions() []manifest.Locator {
	readingOrder := p.Manifest.ReadingOrder
	if len(readingOrder) == 0 {
		return nil
	}
	sizes := make([]uint64, len(readingOrder))
	var total uint64
	for i, link := range readingOrder {
		name, err := publicationPath(link.Href.String())
		if err != nil {
			continue
		}
		f := p.archive.entries[name]
		if f == nil {
			continue
		}
		sizes[i] = f.CompressedSize64
		total += f.CompressedSize64
	}
	budget := len(readingOrder) + fallbackPositionBudget
	counts := apportion(sizes, total, budget)
	bytesBefore := make([]uint64, len(readingOrder))
	var cumulative uint64
	for i := range readingOrder {
		bytesBefore[i] = cumulative
		cumulative += sizes[i]
	}
	positions := make([]manifest.Locator, 0, budget)
	var position uint
	for i, link := range readingOrder {
		mt := link.MediaType
		if mt == nil {
			mt = &mediatype.HTML
		}
		for page := range counts[i] {
			position++
			progression := float64(page) / float64(counts[i])
			pagePosition := position
			var totalProgression float64
			if total > 0 {
				totalProgression = (float64(bytesBefore[i]) + progression*float64(sizes[i])) / float64(total)
			} else {
				// No size information for any item: fall back to
				// spacing positions evenly, since there is nothing to
				// weight by.
				totalProgression = float64(position-1) / float64(budget)
			}
			positions = append(positions, manifest.Locator{
				Href: link.URL(nil, nil), MediaType: *mt, Title: link.Title,
				Locations: manifest.Locations{
					Progression: &progression, Position: &pagePosition,
					TotalProgression: &totalProgression,
				},
			})
		}
	}
	return positions
}

// apportion splits budget positions across len(weights) items in
// proportion to each weight, guaranteeing every item at least one and the
// total never exceeding budget (the caller has already ensured
// budget >= len(weights)). It reserves one position per item first, then
// hands out the rest (budget-len(weights)) by the largest-remainder
// method: each item's ideal extra share is floored, and the leftover
// slots go to the items with the largest fractional remainder, so the sum
// lands on the extra budget exactly instead of drifting from independent
// per-item rounding.
func apportion(weights []uint64, total uint64, budget int) []int {
	counts := make([]int, len(weights))
	for i := range counts {
		counts[i] = 1
	}
	extra := budget - len(weights)
	if extra <= 0 {
		return counts
	}
	if total == 0 {
		// No size information at all: split the extra evenly.
		for i := 0; i < extra; i++ {
			counts[i%len(weights)]++
		}
		return counts
	}
	type remainder struct {
		index     int
		remainder float64
	}
	remainders := make([]remainder, len(weights))
	assigned := 0
	for i, weight := range weights {
		ideal := float64(weight) / float64(total) * float64(extra)
		floor := int(math.Floor(ideal))
		counts[i] += floor
		assigned += floor
		remainders[i] = remainder{i, ideal - float64(floor)}
	}
	sort.Slice(remainders, func(a, b int) bool { return remainders[a].remainder > remainders[b].remainder })
	for i := 0; i < extra-assigned; i++ {
		counts[remainders[i].index]++
	}
	return counts
}

// estimatedPositionCount mirrors the toolkit's own ArchiveEntryLength
// strategy (one position per 1,024 bytes of a reading-order entry's
// archive size, at least one per entry) using only the ZIP directory
// sizes already read by OpenPublication, so Index can decide whether to
// materialize the real position list before ever calling Positions.
func (p *Publication) estimatedPositionCount() int {
	var total int64
	for _, link := range p.Manifest.ReadingOrder {
		name, err := publicationPath(link.Href.String())
		if err != nil {
			continue
		}
		f := p.archive.entries[name]
		if f == nil {
			continue
		}
		count := int64(math.Ceil(float64(f.CompressedSize64) / 1024))
		if count < 1 {
			count = 1
		}
		total += count
	}
	return int(total)
}

// ErrPublicationChanged means the catalog's content digest still names
// this file, but the bytes at the requested path moved since the index
// was built (a rewritten ZIP with the same digest by coincidence, or the
// index survived a change the catalog did not notice). The caller should
// serve 409 and drop the index from any cache: it no longer describes
// this file.
var ErrPublicationChanged = errors.New("publication changed since it was indexed")

// OpenIndexedResource opens one archive entry using a cached Index instead
// of a full OpenPublication: it reopens the ZIP directory (unavoidable,
// since source may be a fresh file handle) but skips preflightZIP, control
// document validation and the toolkit parser entirely. The entry's offset,
// sizes and compression method must match the index exactly, or this
// returns ErrPublicationChanged rather than serving bytes the index did
// not describe.
func OpenIndexedResource(source io.ReaderAt, size int64, idx *Index, name string) (io.ReadCloser, int64, error) {
	entry, ok := idx.Entries[name]
	if !ok {
		return nil, 0, errors.New("publication resource not found")
	}
	z, err := zip.NewReader(source, size)
	if err != nil {
		return nil, 0, validationError(CodeInvalidEPUB, err)
	}
	for _, f := range z.File {
		archiveName, err := safeArchivePath(f.Name)
		if err != nil || archiveName != name {
			continue
		}
		offset, err := f.DataOffset()
		if err != nil || offset != entry.Offset || f.CompressedSize64 != entry.CompressedSize ||
			f.UncompressedSize64 != entry.UncompressedSize || f.Method != entry.Method {
			return nil, 0, ErrPublicationChanged
		}
		r, err := f.Open()
		if err != nil {
			return nil, 0, validationError(CodeInvalidEPUB, err)
		}
		if algorithm, ok := idx.FontObfuscation[name]; ok {
			r = newDeobfuscatingReader(r, idx.Manifest.Metadata, algorithm)
		}
		return r, int64(f.UncompressedSize64), nil
	}
	return nil, 0, ErrPublicationChanged
}

func publicationPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(u.Path, "\\") {
		return "", errors.New("invalid publication path")
	}
	return safeArchivePath(u.Path)
}

type publicationAsset struct{}

func (publicationAsset) Name() string                                  { return "book.epub" }
func (publicationAsset) MediaType(context.Context) mediatype.MediaType { return mediatype.EPUB }
func (publicationAsset) CreateFetcher(context.Context, asset.Dependencies, string) (fetcher.Fetcher, error) {
	return nil, errors.New("publication already has a bounded fetcher")
}

type publicationArchive struct {
	entries      map[string]*zip.File
	limits       Limits
	metadataOnly bool
	err          error
}

func (*publicationArchive) Close() {}
func (a *publicationArchive) Links(context.Context) (manifest.LinkList, error) {
	return nil, nil
}
func (a *publicationArchive) Get(_ context.Context, link manifest.Link) fetcher.Resource {
	name, err := publicationPath(link.Href.String())
	f := a.entries[name]
	if err != nil || f == nil || !f.Mode().IsRegular() {
		return fetcher.NewFailureResource(link, fetcher.NotFound(nil))
	}
	return &publicationResource{a: a, file: f, link: link}
}

type publicationResource struct {
	a    *publicationArchive
	file *zip.File
	link manifest.Link
}

func (*publicationResource) File() string          { return "" }
func (*publicationResource) Close()                {}
func (r *publicationResource) Link() manifest.Link { return r.link }
func (r *publicationResource) Properties() manifest.Properties {
	return manifest.Properties{"https://readium.org/webpub-manifest/properties#archive": map[string]interface{}{"entryLength": r.file.CompressedSize64}}
}
func (r *publicationResource) Length(context.Context) (int64, *fetcher.ResourceError) {
	return int64(r.file.UncompressedSize64), nil
}
func (r *publicationResource) Read(ctx context.Context, start, end int64) ([]byte, *fetcher.ResourceError) {
	limit := r.a.limits.MaxEntryBytes
	if r.a.metadataOnly {
		limit = r.a.limits.MaxMetadataBytes
	}
	data, err := readCoverEntry(ctx, r.file, limit)
	if err == nil && r.a.metadataOnly {
		decoder := xml.NewDecoder(bytes.NewReader(data))
		decoder.Entity = xml.HTMLEntity
		depth := 0
		for {
			token, e := decoder.Token()
			if errors.Is(e, io.EOF) {
				break
			}
			if e != nil {
				err = e
				break
			}
			switch t := token.(type) {
			case xml.StartElement:
				depth++
			case xml.EndElement:
				depth--
			case xml.Directive:
				err = checkDirective(t)
			}
			if depth > r.a.limits.MaxXMLDepth {
				err = errors.New("publication XML is too deep")
			}
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		r.a.err = validationError(CodeInvalidEPUB, err)
		return nil, fetcher.Other(err)
	}
	if start == 0 && end == 0 {
		return data, nil
	}
	if start < 0 || end < start || start >= int64(len(data)) {
		return nil, fetcher.RangeNotSatisfiable(nil)
	}
	return data[start:min(end+1, int64(len(data)))], nil
}
func (r *publicationResource) Stream(ctx context.Context, w io.Writer, start, end int64) (int64, *fetcher.ResourceError) {
	data, err := r.Read(ctx, start, end)
	if err != nil {
		return 0, err
	}
	n, e := w.Write(data)
	if e != nil {
		return int64(n), fetcher.Other(e)
	}
	return int64(n), nil
}
