package calibre

import (
	"encoding/xml"
	"strconv"
	"strings"
)

// Calibre writes a metadata.opf beside every book and reads it back as
// the record of what it believes about that book. Leaving it out is not
// fatal — metadata.db is the authority (ADR-0022) — but a Calibre
// library without one is a library where "check book" reports every
// uploaded book as damaged, and where a curator who rebuilds the
// database from the tree loses everything the upload knew.
//
// This is deliberately the small OPF: the fields the publication itself
// supplied, in the shape Calibre's own writer uses. It is not an attempt
// to reproduce Calibre's serialiser.

// opf renders the book's metadata.opf.
func (b NewBook) opf() string {
	var sb strings.Builder
	sb.WriteString(xml.Header)
	sb.WriteString(`<package xmlns="http://www.idpf.org/2007/opf" ` +
		`unique-identifier="uuid_id" version="2.0">` + "\n")
	sb.WriteString(`  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" ` +
		`xmlns:opf="http://www.idpf.org/2007/opf">` + "\n")
	element(&sb, "dc:title", b.Title, nil)
	for _, author := range cleanStrings(b.Authors) {
		element(&sb, "dc:creator", author, map[string]string{
			"opf:role":    "aut",
			"opf:file-as": AuthorSort(author),
		})
	}
	element(&sb, "dc:publisher", strings.TrimSpace(b.Publisher), nil)
	element(&sb, "dc:description", strings.TrimSpace(b.Description), nil)
	for _, code := range cleanStrings(b.Languages) {
		element(&sb, "dc:language", code, nil)
	}
	for _, tag := range cleanStrings(b.Tags) {
		element(&sb, "dc:subject", tag, nil)
	}
	if b.Published != nil {
		element(&sb, "dc:date", b.Published.UTC().Format("2006-01-02T15:04:05Z"), nil)
	}
	for _, ident := range b.Identifiers {
		kind := strings.ToLower(strings.TrimSpace(ident.Type))
		value := strings.TrimSpace(ident.Value)
		if kind == "" || value == "" {
			continue
		}
		element(&sb, "dc:identifier", value, map[string]string{"opf:scheme": kind})
	}
	if series := strings.TrimSpace(b.Series); series != "" {
		meta(&sb, "calibre:series", series)
		index := b.SeriesIndex
		if index == 0 {
			index = 1
		}
		meta(&sb, "calibre:series_index",
			strconv.FormatFloat(index, 'f', -1, 64))
	}
	sb.WriteString("  </metadata>\n")
	sb.WriteString(`  <guide></guide>` + "\n")
	sb.WriteString("</package>\n")
	return sb.String()
}

func element(sb *strings.Builder, name, value string, attrs map[string]string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	sb.WriteString("    <" + name)
	// The attribute names are literals from this file, so the order is
	// fixed by hand rather than by ranging over a map.
	for _, key := range []string{"opf:role", "opf:file-as", "opf:scheme"} {
		if attr, ok := attrs[key]; ok && attr != "" {
			sb.WriteString(" " + key + `="` + escape(attr) + `"`)
		}
	}
	sb.WriteString(">" + escape(value) + "</" + name + ">\n")
}

func meta(sb *strings.Builder, name, content string) {
	sb.WriteString(`    <meta name="` + name + `" content="` +
		escape(content) + `"/>` + "\n")
}

func escape(value string) string {
	var sb strings.Builder
	_ = xml.EscapeText(&sb, []byte(value))
	return sb.String()
}
