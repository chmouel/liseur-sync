package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	opfNamespace = "http://www.idpf.org/2007/opf"
	dcNamespace  = "http://purl.org/dc/elements/1.1/"
)

// Metadata is the bounded embedded catalog metadata extracted from an OPF.
type Metadata struct {
	Title          string        `json:"title,omitempty"`
	Subtitle       string        `json:"subtitle,omitempty"`
	Description    string        `json:"description,omitempty"`
	Publisher      string        `json:"publisher,omitempty"`
	PublishedDate  string        `json:"published_date,omitempty"`
	Identifiers    []Identifier  `json:"identifiers,omitempty"`
	Languages      []string      `json:"languages,omitempty"`
	Subjects       []string      `json:"subjects,omitempty"`
	Series         []Series      `json:"series,omitempty"`
	Contributors   []Contributor `json:"contributors,omitempty"`
	CoverPath      string        `json:"cover_path,omitempty"`
	CoverMediaType string        `json:"cover_media_type,omitempty"`
}

// Identifier is one embedded publication identifier.
type Identifier struct {
	Scheme string `json:"scheme,omitempty"`
	Value  string `json:"value"`
}

// Series is one embedded series membership.
type Series struct {
	Name     string   `json:"name"`
	Position *float64 `json:"position,omitempty"`
}

// Contributor is one embedded contributor and normalized role.
type Contributor struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type rawMetadataValue struct {
	kind   string
	id     string
	value  string
	role   string
	event  string
	scheme string
}

type rawMeta struct {
	id       string
	name     string
	property string
	refines  string
	scheme   string
	content  string
	value    string
}

type metadataCapture struct {
	depth int
	value rawMetadataValue
	meta  rawMeta
	text  strings.Builder
}

type metadataRefinement struct {
	property string
	value    string
	scheme   string
}

func extractPackageMetadata(
	ctx context.Context,
	value []byte,
	entries map[string]*zip.File,
	details packageDetails,
	limits Limits,
) (Metadata, error) {
	decoder := xml.NewDecoder(bytes.NewReader(value))
	var stack []xml.Name
	var values []rawMetadataValue
	var metas []rawMeta
	var capture *metadataCapture
	count := 0
	depth := 0
	metadataDepth := 0
	for {
		if err := ctx.Err(); err != nil {
			return Metadata{}, err
		}
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return buildMetadata(values, metas, details, entries), nil
		}
		if err != nil {
			return Metadata{}, validationError(CodeInvalidEPUB, err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			parent := xml.Name{}
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			depth++
			stack = append(stack, typed.Name)
			if depth > limits.MaxXMLDepth {
				return Metadata{}, validationError(
					CodeArchiveLimits, errors.New("package XML is too deep"))
			}
			if typed.Name == (xml.Name{
				Space: opfNamespace, Local: "metadata",
			}) && parent == (xml.Name{
				Space: opfNamespace, Local: "package",
			}) && depth == 2 {
				metadataDepth = depth
				continue
			}
			if metadataDepth == 0 || depth != metadataDepth+1 ||
				parent != (xml.Name{
					Space: opfNamespace, Local: "metadata",
				}) {
				continue
			}
			if typed.Name.Space == dcNamespace &&
				isExtractedDCElement(typed.Name.Local) {
				count++
				if count > limits.MaxEntries {
					return Metadata{}, validationError(
						CodeArchiveLimits,
						errors.New("package metadata exceeds entry limit"))
				}
				capture = &metadataCapture{
					depth: depth,
					value: rawMetadataValue{
						kind: strings.ToLower(typed.Name.Local),
						id:   unqualifiedAttribute(typed.Attr, "id"),
						role: namespacedAttribute(
							typed.Attr, opfNamespace, "role"),
						event: namespacedAttribute(
							typed.Attr, opfNamespace, "event"),
						scheme: namespacedAttribute(
							typed.Attr, opfNamespace, "scheme"),
					},
				}
				continue
			}
			if typed.Name == (xml.Name{
				Space: opfNamespace, Local: "meta",
			}) {
				count++
				if count > limits.MaxEntries {
					return Metadata{}, validationError(
						CodeArchiveLimits,
						errors.New("package metadata exceeds entry limit"))
				}
				capture = &metadataCapture{
					depth: depth,
					meta: rawMeta{
						id:       unqualifiedAttribute(typed.Attr, "id"),
						name:     unqualifiedAttribute(typed.Attr, "name"),
						property: unqualifiedAttribute(typed.Attr, "property"),
						refines:  unqualifiedAttribute(typed.Attr, "refines"),
						scheme:   unqualifiedAttribute(typed.Attr, "scheme"),
						content:  unqualifiedAttribute(typed.Attr, "content"),
					},
				}
			}
		case xml.CharData:
			if capture != nil {
				capture.text.Write([]byte(typed))
			}
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1] != typed.Name {
				return Metadata{}, validationError(
					CodeInvalidEPUB,
					errors.New("invalid package XML structure"))
			}
			if capture != nil && capture.depth == depth {
				text := normalizeMetadataText(capture.text.String())
				if capture.value.kind != "" {
					capture.value.value = text
					if text != "" {
						values = append(values, capture.value)
					}
				} else {
					capture.meta.value = text
					if capture.meta.content != "" {
						capture.meta.value = normalizeMetadataText(
							capture.meta.content)
					}
					metas = append(metas, capture.meta)
				}
				capture = nil
			}
			if metadataDepth == depth && typed.Name == (xml.Name{
				Space: opfNamespace, Local: "metadata",
			}) {
				metadataDepth = 0
			}
			stack = stack[:len(stack)-1]
			depth--
		case xml.Directive:
			if err := checkDirective(typed); err != nil {
				return Metadata{}, err
			}
		}
	}
}

func isExtractedDCElement(local string) bool {
	switch strings.ToLower(local) {
	case "title", "description", "publisher", "date", "identifier",
		"language", "subject", "creator", "contributor":
		return true
	default:
		return false
	}
}

func namespacedAttribute(attributes []xml.Attr, namespace, local string) string {
	for _, attribute := range attributes {
		if attribute.Name.Space == namespace && attribute.Name.Local == local {
			return attribute.Value
		}
	}
	return ""
}

func normalizeMetadataText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func buildMetadata(
	values []rawMetadataValue,
	metas []rawMeta,
	details packageDetails,
	entries map[string]*zip.File,
) Metadata {
	refinements := make(map[string][]metadataRefinement)
	legacy := make(map[string]string)
	for _, meta := range metas {
		if meta.refines != "" && meta.property != "" {
			target := strings.TrimPrefix(strings.TrimSpace(meta.refines), "#")
			if target != "" && meta.value != "" {
				refinements[target] = append(refinements[target],
					metadataRefinement{
						property: strings.ToLower(strings.TrimSpace(meta.property)),
						value:    meta.value,
						scheme:   meta.scheme,
					})
			}
		}
		if meta.name != "" && meta.value != "" {
			key := strings.ToLower(strings.TrimSpace(meta.name))
			if _, exists := legacy[key]; !exists {
				legacy[key] = meta.value
			}
		}
	}

	var metadata Metadata
	var fallbackTitle string
	var fallbackDate string
	var publicationDate string
	identifierSeen := make(map[Identifier]struct{})
	languageSeen := make(map[string]struct{})
	subjectSeen := make(map[string]struct{})
	contributorSeen := make(map[Contributor]struct{})
	seriesSeen := make(map[string]struct{})
	for _, value := range values {
		switch value.kind {
		case "title":
			titleType := refinementValue(refinements[value.id], "title-type")
			switch strings.ToLower(titleType) {
			case "subtitle":
				if metadata.Subtitle == "" {
					metadata.Subtitle = value.value
				}
			case "main":
				if metadata.Title == "" {
					metadata.Title = value.value
				}
			default:
				if fallbackTitle == "" {
					fallbackTitle = value.value
				}
			}
		case "description":
			if metadata.Description == "" {
				metadata.Description = value.value
			}
		case "publisher":
			if metadata.Publisher == "" {
				metadata.Publisher = value.value
			}
		case "date":
			event := value.event
			if refined := refinementValue(refinements[value.id], "event"); refined != "" {
				event = refined
			}
			if strings.EqualFold(event, "publication") {
				if publicationDate == "" {
					publicationDate = value.value
				}
			} else if event == "" && fallbackDate == "" {
				fallbackDate = value.value
			}
		case "identifier":
			appendIdentifier(&metadata.Identifiers, identifierSeen, Identifier{
				Scheme: identifierScheme(value, refinements[value.id]),
				Value:  value.value,
			})
		case "language":
			appendUniqueString(
				&metadata.Languages, languageSeen, strings.ToLower(value.value))
		case "subject":
			appendUniqueString(&metadata.Subjects, subjectSeen, value.value)
		case "creator", "contributor":
			roles := refinementValues(refinements[value.id], "role")
			if len(roles) == 0 && value.role != "" {
				roles = []string{value.role}
			}
			if len(roles) == 0 {
				if value.kind == "creator" {
					roles = []string{"author"}
				} else {
					roles = []string{"contributor"}
				}
			}
			// One person can be credited more than once for the same
			// book, and EPUB 3 says so with several `role` refinements
			// on one entry. Standard Ebooks writes `ann` and `aut` on
			// its authors, so reading only the first loses the author
			// on every book they publish.
			for _, role := range roles {
				appendContributor(&metadata.Contributors, contributorSeen, Contributor{
					Name: value.value, Role: normalizeContributorRole(role),
				})
			}
		}
	}
	if metadata.Title == "" {
		metadata.Title = fallbackTitle
	}
	if publicationDate != "" {
		metadata.PublishedDate = publicationDate
	} else {
		metadata.PublishedDate = fallbackDate
	}

	seriesName := legacy["calibre:series"]
	seriesPosition := parseSeriesPosition(legacy["calibre:series_index"])
	if seriesName != "" {
		appendSeries(&metadata.Series, seriesSeen, Series{
			Name: seriesName, Position: seriesPosition,
		})
	}
	for _, meta := range metas {
		if strings.EqualFold(meta.property, "belongs-to-collection") &&
			meta.value != "" &&
			strings.EqualFold(
				refinementValue(refinements[meta.id], "collection-type"),
				"series") {
			appendSeries(&metadata.Series, seriesSeen, Series{
				Name: meta.value,
				Position: parseSeriesPosition(
					refinementValue(refinements[meta.id], "group-position")),
			})
		}
	}

	cover := details.cover
	if cover == nil {
		if id := legacy["cover"]; id != "" {
			if item, ok := details.manifestByID[id]; ok {
				cover = &item
			}
		}
	}
	if cover != nil {
		if file, ok := entries[cover.path]; ok && file.Mode().IsRegular() {
			metadata.CoverPath = cover.path
			metadata.CoverMediaType = cover.mediaType
		}
	}
	return metadata
}

func refinementValue(
	refinements []metadataRefinement,
	property string,
) string {
	for _, refinement := range refinements {
		if refinement.property == property {
			return refinement.value
		}
	}
	return ""
}

// refinementValues is refinementValue for a property that may legally
// appear more than once on one entry, such as a contributor's role.
func refinementValues(
	refinements []metadataRefinement,
	property string,
) []string {
	var values []string
	for _, refinement := range refinements {
		if refinement.property == property {
			values = append(values, refinement.value)
		}
	}
	return values
}

func inferIdentifierScheme(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.HasPrefix(lower, "urn:isbn:"):
		return "isbn"
	case strings.HasPrefix(lower, "urn:uuid:"):
		return "uuid"
	case strings.HasPrefix(lower, "doi:"):
		return "doi"
	default:
		return ""
	}
}

func identifierScheme(
	value rawMetadataValue,
	refinements []metadataRefinement,
) string {
	if scheme := normalizeIdentifierScheme(value.scheme); scheme != "" {
		return scheme
	}
	for _, refinement := range refinements {
		if refinement.property != "identifier-type" {
			continue
		}
		if scheme := identifierTypeScheme(
			refinement.value, refinement.scheme); scheme != "" {
			return scheme
		}
	}
	return inferIdentifierScheme(value.value)
}

func normalizeIdentifierScheme(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "":
		return ""
	case "isbn", "isbn-10", "isbn-13":
		return "isbn"
	case "uuid":
		return "uuid"
	case "doi":
		return "doi"
	case "ismn", "ismn-10", "ismn-13":
		return "ismn"
	case "lccn":
		return "lccn"
	case "oclc":
		return "oclc"
	default:
		return strings.ToLower(strings.TrimSpace(scheme))
	}
}

func identifierTypeScheme(value, scheme string) string {
	if strings.EqualFold(strings.TrimSpace(scheme), "onix:codelist5") {
		switch strings.TrimSpace(value) {
		case "02", "15":
			return "isbn"
		case "03":
			return "gtin"
		case "04":
			return "upc"
		case "06":
			return "doi"
		case "13":
			return "lccn"
		case "05", "25":
			return "ismn"
		case "17":
			return "legal-deposit"
		case "22":
			return "urn"
		case "23":
			return "oclc"
		default:
			return ""
		}
	}
	return normalizeIdentifierScheme(value)
}

func normalizeContributorRole(role string) string {
	value := strings.ToLower(strings.TrimSpace(role))
	if index := strings.LastIndexAny(value, "/#"); index >= 0 {
		value = value[index+1:]
	}
	switch value {
	case "aut", "author":
		return "author"
	case "edt", "editor":
		return "editor"
	case "trl", "translator":
		return "translator"
	case "ill", "illustrator":
		return "illustrator"
	default:
		return value
	}
}

func parseSeriesPosition(value string) *float64 {
	if value == "" {
		return nil
	}
	position, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsInf(position, 0) || math.IsNaN(position) {
		return nil
	}
	return &position
}

func appendUniqueString(
	values *[]string,
	seen map[string]struct{},
	value string,
) {
	if value == "" {
		return
	}
	if _, exists := seen[value]; exists {
		return
	}
	seen[value] = struct{}{}
	*values = append(*values, value)
}

func appendIdentifier(
	values *[]Identifier,
	seen map[Identifier]struct{},
	value Identifier,
) {
	if _, exists := seen[value]; exists {
		return
	}
	seen[value] = struct{}{}
	*values = append(*values, value)
}

func appendContributor(
	values *[]Contributor,
	seen map[Contributor]struct{},
	value Contributor,
) {
	if value.Name == "" || value.Role == "" {
		return
	}
	if _, exists := seen[value]; exists {
		return
	}
	seen[value] = struct{}{}
	*values = append(*values, value)
}

func appendSeries(
	values *[]Series,
	seen map[string]struct{},
	value Series,
) {
	if _, exists := seen[value.Name]; exists {
		return
	}
	seen[value.Name] = struct{}{}
	*values = append(*values, value)
}

// ParseMetadataDocument reads a standalone OPF package document — the
// metadata.opf Calibre writes beside each book, rather than one inside
// a container.
//
// It is the same parser the validator runs on an EPUB's own package
// document, given no zip entries to resolve against: the fields that
// depend on the manifest, a cover path above all, come back empty
// because there is no manifest to point into. Everything the metadata
// element itself carries is read exactly as it is from a publication,
// which is the point — one OPF reader in this codebase, not two that
// disagree in a corner.
func ParseMetadataDocument(
	ctx context.Context, document []byte, limits Limits,
) (Metadata, error) {
	if !limits.valid() {
		limits = DefaultLimits()
	}
	if int64(len(document)) > limits.MaxMetadataBytes {
		return Metadata{}, validationError(CodeInvalidEPUB,
			errors.New("metadata document is larger than the limit"))
	}
	return extractPackageMetadata(ctx, document, nil, packageDetails{}, limits)
}
