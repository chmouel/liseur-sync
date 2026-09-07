package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
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
	PackagePath string
	archive     *publicationArchive
	allowed     map[string]manifest.Link
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
	if _, exists := a.entries["META-INF/encryption.xml"]; exists {
		data, err := control("META-INF/encryption.xml")
		if err != nil {
			return nil, err
		}
		if _, err := validateEncryption(data, a.entries, details.manifest, limits.MaxXMLDepth); err != nil {
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
	p := &Publication{Publication: builder.Build(), PackagePath: packagePath, archive: a, allowed: make(map[string]manifest.Link)}
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
	return r, int64(f.UncompressedSize64), nil
}

func (p *Publication) HasResource(name string) bool { _, ok := p.allowed[name]; return ok }

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
