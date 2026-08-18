package epub

import (
	"archive/zip"
	"bytes"
	"testing"
)

func epubWithPackage(t *testing.T, packageXML string, extra ...zipEntry) []byte {
	t.Helper()
	entries := validEntries()
	entries[2].body = packageXML
	entries = append(entries, extra...)
	return makeEPUB(t, entries...)
}

func TestValidateExtractsEPUB3Metadata(t *testing.T) {
	data := epubWithPackage(t, `
<package xmlns="http://www.idpf.org/2007/opf"
 xmlns:dc="http://purl.org/dc/elements/1.1/">
 <metadata>
  <dc:title id="title-main"> The Main Title </dc:title>
  <meta refines="#title-main" property="title-type">main</meta>
  <dc:title id="title-sub">A Subtitle</dc:title>
  <meta refines="#title-sub" property="title-type">subtitle</meta>
  <dc:description> A long
    description. </dc:description>
  <dc:publisher>Example Press</dc:publisher>
  <dc:date id="created">2025-01-02</dc:date>
  <meta refines="#created" property="event">creation</meta>
  <dc:date id="published">2026-02-03</dc:date>
  <meta refines="#published" property="event">publication</meta>
  <dc:identifier id="book-id">9780000000001</dc:identifier>
  <meta refines="#book-id" property="identifier-type"
    scheme="onix:codelist5">15</meta>
  <dc:language>EN-US</dc:language>
  <dc:subject>Science Fiction</dc:subject>
  <dc:creator id="creator">Ada Author</dc:creator>
  <meta refines="#creator" property="role"
    scheme="marc:relators">aut</meta>
  <dc:contributor id="translator">Tara Translator</dc:contributor>
  <meta refines="#translator" property="role"
    scheme="marc:relators">trl</meta>
  <meta id="series" property="belongs-to-collection">Space Cycle</meta>
  <meta refines="#series" property="collection-type">series</meta>
  <meta refines="#series" property="group-position">2.5</meta>
 </metadata>
 <manifest>
  <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"
    properties="nav"/>
  <item id="font" href="font.otf" media-type="font/otf"/>
  <item id="cover" href="cover.jpg" media-type="image/jpeg"
    properties="cover-image"/>
 </manifest>
</package>`, zipEntry{
		name: "OPS/cover.jpg", body: "jpeg", method: zip.Store,
	})
	result, err := validateBytes(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	metadata := result.Metadata
	if metadata.Title != "The Main Title" ||
		metadata.Subtitle != "A Subtitle" ||
		metadata.Description != "A long description." ||
		metadata.Publisher != "Example Press" ||
		metadata.PublishedDate != "2026-02-03" ||
		metadata.CoverPath != "OPS/cover.jpg" ||
		metadata.CoverMediaType != "image/jpeg" {
		t.Fatalf("scalar metadata: %+v", metadata)
	}
	if len(metadata.Identifiers) != 1 ||
		metadata.Identifiers[0] != (Identifier{
			Scheme: "isbn", Value: "9780000000001",
		}) ||
		len(metadata.Languages) != 1 || metadata.Languages[0] != "en-us" ||
		len(metadata.Subjects) != 1 ||
		metadata.Subjects[0] != "Science Fiction" {
		t.Fatalf("list metadata: %+v", metadata)
	}

	if len(metadata.Contributors) != 2 ||
		metadata.Contributors[0] != (Contributor{
			Name: "Ada Author", Role: "author",
		}) ||
		metadata.Contributors[1] != (Contributor{
			Name: "Tara Translator", Role: "translator",
		}) {
		t.Fatalf("contributors: %+v", metadata.Contributors)
	}
	if len(metadata.Series) != 1 ||
		metadata.Series[0].Name != "Space Cycle" ||
		metadata.Series[0].Position == nil ||
		*metadata.Series[0].Position != 2.5 {
		t.Fatalf("series: %+v", metadata.Series)
	}
}

func TestValidateExtractsLegacyPublicationDateEvent(t *testing.T) {
	data := epubWithPackage(t, `
<package xmlns="http://www.idpf.org/2007/opf"
 xmlns:dc="http://purl.org/dc/elements/1.1/"
 xmlns:opf="http://www.idpf.org/2007/opf">
 <metadata>
  <dc:date opf:event="creation">2020-01-01</dc:date>
  <dc:date opf:event="publication">2021-02-02</dc:date>
 </metadata>
 <manifest>
  <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"
    properties="nav"/>
  <item id="font" href="font.otf" media-type="font/otf"/>
 </manifest>
</package>`)
	result, err := validateBytes(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata.PublishedDate != "2021-02-02" {
		t.Fatalf("legacy publication date: %+v", result.Metadata)
	}
}

func TestValidateIgnoresNonPublicationDates(t *testing.T) {
	data := epubWithPackage(t, `
<package xmlns="http://www.idpf.org/2007/opf"
 xmlns:dc="http://purl.org/dc/elements/1.1/"
 xmlns:opf="http://www.idpf.org/2007/opf">
 <metadata>
  <dc:date opf:event="creation">2020-01-01</dc:date>
  <dc:date>2021-02-02</dc:date>
 </metadata>
 <manifest>
  <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"
    properties="nav"/>
  <item id="font" href="font.otf" media-type="font/otf"/>
 </manifest>
</package>`)
	result, err := validateBytes(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata.PublishedDate != "2021-02-02" {
		t.Fatalf("publication date: %+v", result.Metadata)
	}
}

func TestIdentifierTypeSchemeUsesCorrectONIXMappings(t *testing.T) {
	for value, want := range map[string]string{
		"05": "ismn",
		"06": "doi",
		"15": "isbn",
		"04": "upc",
		"17": "legal-deposit",
		"23": "oclc",
		"25": "ismn",
	} {
		if got := identifierTypeScheme(value, "onix:codelist5"); got != want {
			t.Fatalf("ONIX %s: got %q want %q", value, got, want)
		}
	}
}

func TestValidateExtractsEPUB2Metadata(t *testing.T) {
	data := epubWithPackage(t, `
<package xmlns="http://www.idpf.org/2007/opf"
 xmlns:dc="http://purl.org/dc/elements/1.1/"
 xmlns:opf="http://www.idpf.org/2007/opf">
 <metadata>
  <dc:title>Legacy Book</dc:title>
  <dc:creator opf:role="aut">Legacy Author</dc:creator>
  <dc:contributor opf:role="ill">Legacy Illustrator</dc:contributor>
  <dc:identifier opf:scheme="UUID">urn:uuid:book</dc:identifier>
  <meta name="calibre:series" content="Legacy Series"/>
  <meta name="calibre:series_index" content="3"/>
  <meta name="cover" content="cover-image"/>
 </metadata>
 <manifest>
  <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"
    properties="nav"/>
  <item id="font" href="font.otf" media-type="font/otf"/>
  <item id="cover-image" href="images/cover.png" media-type="image/png"/>
 </manifest>
</package>`, zipEntry{
		name: "OPS/images/cover.png", body: "png", method: zip.Store,
	})
	result, err := validateBytes(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	metadata := result.Metadata
	if metadata.Title != "Legacy Book" ||
		metadata.CoverPath != "OPS/images/cover.png" ||
		metadata.CoverMediaType != "image/png" ||
		len(metadata.Identifiers) != 1 ||
		metadata.Identifiers[0].Scheme != "uuid" ||
		len(metadata.Contributors) != 2 ||
		metadata.Contributors[0].Role != "author" ||
		metadata.Contributors[1].Role != "illustrator" ||
		len(metadata.Series) != 1 ||
		metadata.Series[0].Position == nil ||
		*metadata.Series[0].Position != 3 {
		t.Fatalf("legacy metadata: %+v", metadata)
	}
}

func TestValidateBoundsMetadataEntries(t *testing.T) {
	data := epubWithPackage(t, `
<package xmlns="http://www.idpf.org/2007/opf"
 xmlns:dc="http://purl.org/dc/elements/1.1/">
 <metadata>
  <dc:subject>one</dc:subject>
  <dc:subject>two</dc:subject>
  <dc:subject>three</dc:subject>
  <dc:subject>four</dc:subject>
  <dc:subject>five</dc:subject>
  <dc:subject>six</dc:subject>
 </metadata>
 <manifest>
  <item href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  <item href="font.otf" media-type="font/otf"/>
 </manifest>
</package>`)
	limits := DefaultLimits()
	limits.MaxEntries = 5
	_, err := validateBytes(data, limits)
	requireCode(t, err, CodeArchiveLimits)
}

func TestValidateRejectsDuplicateManifestIDs(t *testing.T) {
	data := epubWithPackage(t, `
<package xmlns="http://www.idpf.org/2007/opf">
 <metadata/>
 <manifest>
  <item id="duplicate" href="nav.xhtml"
    media-type="application/xhtml+xml" properties="nav"/>
  <item id="duplicate" href="font.otf" media-type="font/otf"/>
 </manifest>
</package>`)
	_, err := validateBytes(data, DefaultLimits())
	requireCode(t, err, CodeInvalidEPUB)
}

func TestValidateExtractsOnlyDirectOPFMetadata(t *testing.T) {
	data := epubWithPackage(t, `
<package xmlns="http://www.idpf.org/2007/opf"
 xmlns:dc="http://purl.org/dc/elements/1.1/">
 <metadata><dc:title>Actual Title</dc:title></metadata>
 <guide><metadata><dc:title>Spoofed Title</dc:title></metadata></guide>
 <manifest>
  <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"
    properties="nav"/>
  <item id="font" href="font.otf" media-type="font/otf"/>
 </manifest>
</package>`)
	result, err := validateBytes(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata.Title != "Actual Title" {
		t.Fatalf("extracted nested metadata: %+v", result.Metadata)
	}
}

func TestValidateIgnoresNestedPackageMetadata(t *testing.T) {
	data := epubWithPackage(t, `
<package xmlns="http://www.idpf.org/2007/opf"
 xmlns:dc="http://purl.org/dc/elements/1.1/">
 <metadata><dc:title>Actual Title</dc:title></metadata>
 <package><metadata><dc:title>Spoofed Title</dc:title></metadata></package>
 <manifest>
  <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"
    properties="nav"/>
  <item id="font" href="font.otf" media-type="font/otf"/>
 </manifest>
</package>`)
	result, err := validateBytes(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata.Title != "Actual Title" {
		t.Fatalf("extracted nested package metadata: %+v", result.Metadata)
	}
}

func TestValidateDeduplicatesMetadataWithoutChangingOrder(t *testing.T) {
	data := epubWithPackage(t, `
<package xmlns="http://www.idpf.org/2007/opf"
 xmlns:dc="http://purl.org/dc/elements/1.1/">
 <metadata>
  <dc:language>en</dc:language><dc:language>en</dc:language>
  <dc:subject>One</dc:subject><dc:subject>One</dc:subject>
  <dc:identifier>doi:first</dc:identifier><dc:identifier>doi:first</dc:identifier>
  <dc:creator>Ada</dc:creator><dc:creator>Ada</dc:creator>
 </metadata>
 <manifest>
  <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"
    properties="nav"/>
  <item id="font" href="font.otf" media-type="font/otf"/>
 </manifest>
</package>`)
	result, err := validateBytes(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	metadata := result.Metadata
	if len(metadata.Languages) != 1 || metadata.Languages[0] != "en" ||
		len(metadata.Subjects) != 1 || metadata.Subjects[0] != "One" ||
		len(metadata.Identifiers) != 1 ||
		metadata.Identifiers[0] != (Identifier{
			Scheme: "doi", Value: "doi:first",
		}) ||
		len(metadata.Contributors) != 1 ||
		metadata.Contributors[0] != (Contributor{
			Name: "Ada", Role: "author",
		}) {
		t.Fatalf("deduplicated metadata: %+v", metadata)
	}
}

// TestValidateKeepsEveryRoleOfOneContributor guards the shape Standard
// Ebooks writes on every book it publishes: one `dc:creator` carrying
// several `role` refinements, the authoring one not first. Reading only
// the first leaves the publication with no author at all, and whatever
// asks for one then picks the font designer.
func TestValidateKeepsEveryRoleOfOneContributor(t *testing.T) {
	data := epubWithPackage(t, `
<package xmlns="http://www.idpf.org/2007/opf"
 xmlns:dc="http://purl.org/dc/elements/1.1/">
 <metadata>
  <dc:title>A Study in Scarlet</dc:title>
  <dc:contributor id="type-designer">The League of Moveable Type</dc:contributor>
  <meta property="role" refines="#type-designer" scheme="marc:relators">tyd</meta>
  <dc:creator id="author">Arthur Conan Doyle</dc:creator>
  <meta property="role" refines="#author" scheme="marc:relators">ann</meta>
  <meta property="role" refines="#author" scheme="marc:relators">aut</meta>
 </metadata>
 <manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/></manifest>
 <spine><itemref idref="c"/></spine>
</package>`)

	result, err := Validate(t.Context(), bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	var authors []string
	for _, c := range result.Metadata.Contributors {
		if c.Role == "author" {
			authors = append(authors, c.Name)
		}
	}
	if len(authors) != 1 || authors[0] != "Arthur Conan Doyle" {
		t.Fatalf("authors: %+v (all: %+v)", authors, result.Metadata.Contributors)
	}
}
