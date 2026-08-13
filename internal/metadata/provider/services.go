package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/chmouel/liseur-sync/internal/metadata"
)

// OpenLibrary queries openlibrary.org, which is free, run by the
// Internet Archive, and needs no key — which is why it is the default
// suggestion for somebody who wants this at all.
type OpenLibrary struct{}

func (OpenLibrary) Name() string    { return "openlibrary" }
func (OpenLibrary) Hosts() []string { return []string{"openlibrary.org"} }

// Lookup asks by ISBN when the book has one and by title and author
// otherwise. Both go to the same search endpoint, because the dedicated
// ISBN endpoint answers with an edition record that omits the subjects
// and the author names, which are most of what anybody wants back.
func (o OpenLibrary) Lookup(ctx context.Context, f *Fetcher, q Query) ([]Candidate, error) {
	query := url.Values{}
	byIdentifier := false
	if isbn := q.isbn(); isbn != "" {
		query.Set("isbn", isbn)
		byIdentifier = true
	} else if q.Title != "" {
		query.Set("title", q.Title)
		if q.Author != "" {
			query.Set("author", q.Author)
		}
	} else {
		return nil, nil // nothing to ask about
	}
	query.Set("limit", "5")
	query.Set("fields", strings.Join([]string{
		"key", "title", "subtitle", "author_name", "first_publish_year",
		"publisher", "language", "subject", "isbn", "cover_i",
	}, ","))

	target := &url.URL{
		Scheme: "https", Host: "openlibrary.org",
		Path: "/search.json", RawQuery: query.Encode(),
	}
	body, err := f.Get(ctx, target)
	if err != nil || body == nil {
		return nil, err
	}

	var payload struct {
		Docs []struct {
			Key              string   `json:"key"`
			Title            string   `json:"title"`
			Subtitle         string   `json:"subtitle"`
			AuthorName       []string `json:"author_name"`
			FirstPublishYear int      `json:"first_publish_year"`
			Publisher        []string `json:"publisher"`
			Language         []string `json:"language"`
			Subject          []string `json:"subject"`
			CoverID          int      `json:"cover_i"`
		} `json:"docs"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("openlibrary: %w", err)
	}

	candidates := make([]Candidate, 0, len(payload.Docs))
	for _, doc := range payload.Docs {
		if strings.TrimSpace(doc.Title) == "" {
			continue
		}
		c := Candidate{
			Provider:     o.Name(),
			ByIdentifier: byIdentifier,
			Title:        strings.TrimSpace(doc.Title),
			Subtitle:     strings.TrimSpace(doc.Subtitle),
			Languages:    trimmed(doc.Language),
			// Subjects are capped because OpenLibrary returns hundreds
			// for a well-known book, and a candidate that adds two
			// hundred tags to a shelf is not a suggestion anybody can
			// review.
			Tags: trimmed(atMost(doc.Subject, 12)),
		}
		if doc.FirstPublishYear > 0 {
			c.PublishedDate = strconv.Itoa(doc.FirstPublishYear)
		}
		if len(doc.Publisher) > 0 {
			c.Publisher = strings.TrimSpace(doc.Publisher[0])
		}
		for _, name := range doc.AuthorName {
			if name = strings.TrimSpace(name); name != "" {
				c.Contributors = append(c.Contributors,
					metadata.ContributorKey{Name: name, Role: "author"})
			}
		}
		if doc.Key != "" {
			c.URL = "https://openlibrary.org" + doc.Key
		}
		if doc.CoverID > 0 {
			c.CoverURL = "https://covers.openlibrary.org/b/id/" +
				strconv.Itoa(doc.CoverID) + "-L.jpg"
		}
		c.Score = score(q, c, byIdentifier)
		candidates = append(candidates, c)
	}
	return candidates, nil
}

// GoogleBooks queries the public Books API. It needs no key for the
// volume search, which is the only call made here; a deployment that has
// one does not need to say so.
type GoogleBooks struct{}

func (GoogleBooks) Name() string    { return "googlebooks" }
func (GoogleBooks) Hosts() []string { return []string{"www.googleapis.com"} }

func (g GoogleBooks) Lookup(ctx context.Context, f *Fetcher, q Query) ([]Candidate, error) {
	var term string
	byIdentifier := false
	if isbn := q.isbn(); isbn != "" {
		term = "isbn:" + isbn
		byIdentifier = true
	} else if q.Title != "" {
		term = `intitle:"` + strings.ReplaceAll(q.Title, `"`, "") + `"`
		if q.Author != "" {
			term += ` inauthor:"` + strings.ReplaceAll(q.Author, `"`, "") + `"`
		}
	} else {
		return nil, nil
	}

	query := url.Values{}
	query.Set("q", term)
	query.Set("maxResults", "5")
	query.Set("printType", "books")
	target := &url.URL{
		Scheme: "https", Host: "www.googleapis.com",
		Path: "/books/v1/volumes", RawQuery: query.Encode(),
	}
	body, err := f.Get(ctx, target)
	if err != nil || body == nil {
		return nil, err
	}

	var payload struct {
		Items []struct {
			VolumeInfo struct {
				Title               string   `json:"title"`
				Subtitle            string   `json:"subtitle"`
				Authors             []string `json:"authors"`
				Publisher           string   `json:"publisher"`
				PublishedDate       string   `json:"publishedDate"`
				Description         string   `json:"description"`
				Categories          []string `json:"categories"`
				Language            string   `json:"language"`
				InfoLink            string   `json:"infoLink"`
				IndustryIdentifiers []struct {
					Type       string `json:"type"`
					Identifier string `json:"identifier"`
				} `json:"industryIdentifiers"`
				ImageLinks struct {
					Thumbnail string `json:"thumbnail"`
				} `json:"imageLinks"`
			} `json:"volumeInfo"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("googlebooks: %w", err)
	}

	candidates := make([]Candidate, 0, len(payload.Items))
	for _, item := range payload.Items {
		info := item.VolumeInfo
		if strings.TrimSpace(info.Title) == "" {
			continue
		}
		c := Candidate{
			Provider:      g.Name(),
			ByIdentifier:  byIdentifier,
			Title:         strings.TrimSpace(info.Title),
			Subtitle:      strings.TrimSpace(info.Subtitle),
			Description:   strings.TrimSpace(info.Description),
			Publisher:     strings.TrimSpace(info.Publisher),
			PublishedDate: strings.TrimSpace(info.PublishedDate),
			Tags:          trimmed(atMost(info.Categories, 12)),
			URL:           info.InfoLink,
		}
		if language := strings.TrimSpace(info.Language); language != "" {
			c.Languages = []string{language}
		}
		for _, name := range info.Authors {
			if name = strings.TrimSpace(name); name != "" {
				c.Contributors = append(c.Contributors,
					metadata.ContributorKey{Name: name, Role: "author"})
			}
		}
		// The thumbnail arrives over http on this API often enough to
		// matter; a mixed-content image in the UI is a broken image.
		if link := info.ImageLinks.Thumbnail; link != "" {
			c.CoverURL = strings.Replace(link, "http://", "https://", 1)
		}
		for _, id := range info.IndustryIdentifiers {
			if !strings.HasPrefix(id.Type, "ISBN") {
				continue
			}
			c.Identifiers = append(c.Identifiers, metadata.IdentifierKey{
				Scheme: strings.ToLower(id.Type), Value: id.Identifier,
			})
		}
		c.Score = score(q, c, byIdentifier)
		candidates = append(candidates, c)
	}
	return candidates, nil
}

func trimmed(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[strings.ToLower(value)] {
			continue
		}
		seen[strings.ToLower(value)] = true
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func atMost(values []string, limit int) []string {
	if len(values) > limit {
		return values[:limit]
	}
	return values
}
