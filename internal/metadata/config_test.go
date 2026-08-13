package metadata_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/chmouel/liseur-sync/internal/metadata"
)

func TestPathPatternsFromConfigDefaults(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string][]byte{
		"nil":          nil,
		"empty":        {},
		"blank":        []byte("   "),
		"empty object": []byte(`{}`),
		"other keys":   []byte(`{"something_else":true}`),
		"null list":    []byte(`{"path_patterns":null}`),
	} {
		got, err := metadata.PathPatternsFromConfig(raw)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if !reflect.DeepEqual(got, metadata.DefaultPathPatterns()) {
			t.Fatalf("%s: got %v, want defaults %v",
				name, got, metadata.DefaultPathPatterns())
		}
	}
}

func TestPathPatternsFromConfigReadsList(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"path_patterns":["series/author-title","author/title"]}`)
	got, err := metadata.PathPatternsFromConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []metadata.PathPattern{
		metadata.PatternSeriesAuthorTitle,
		metadata.PatternAuthorTitle,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// An empty list is the operator saying this library's filenames are not
// worth reading. Returning the defaults for it would re-enable exactly the
// parsing they turned off.
func TestPathPatternsFromConfigEmptyListDisablesParsing(t *testing.T) {
	t.Parallel()
	got, err := metadata.PathPatternsFromConfig([]byte(`{"path_patterns":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("empty list decoded as nil, which is indistinguishable from unset")
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want no patterns", got)
	}
	if candidate := metadata.ParsePath("Author/Title.epub", got); candidate.Confidence != metadata.ConfidenceNone {
		t.Fatalf("parsing still happened: %+v", candidate)
	}
}

func TestPathPatternsFromConfigRejectsBadDocuments(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string][]byte{
		"not json":      []byte(`{`),
		"not an object": []byte(`["author/title"]`),
		"json null":     []byte(`null`),
		"list not array": []byte(
			`{"path_patterns":"author/title"}`),
		"unknown layout": []byte(`{"path_patterns":["author/isbn"]}`),
		"repeated layout": []byte(
			`{"path_patterns":["author/title","author/title"]}`),
	} {
		got, err := metadata.PathPatternsFromConfig(raw)
		if !errors.Is(err, metadata.ErrInvalidLibraryConfig) {
			t.Fatalf("%s: got (%v, %v), want ErrInvalidLibraryConfig", name, got, err)
		}
		if got != nil {
			t.Fatalf("%s: returned patterns %v alongside an error", name, got)
		}
	}
}

func TestWithPathPatternsPreservesOtherKeys(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"keep":{"nested":1},"path_patterns":["author/title"]}`)
	encoded, err := metadata.WithPathPatterns(raw, []metadata.PathPattern{
		metadata.PatternFlatAuthorSeriesTitle,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("re-encoded document is not JSON: %v", err)
	}
	if string(fields["keep"]) != `{"nested":1}` {
		t.Fatalf("unrelated key lost or rewritten: %s", fields["keep"])
	}
	got, err := metadata.PathPatternsFromConfig(encoded)
	if err != nil {
		t.Fatalf("re-read failed: %v", err)
	}
	want := []metadata.PathPattern{metadata.PatternFlatAuthorSeriesTitle}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWithPathPatternsNilRestoresDefaults(t *testing.T) {
	t.Parallel()
	encoded, err := metadata.WithPathPatterns(
		[]byte(`{"keep":1,"path_patterns":[]}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(encoded) != `{"keep":1}` {
		t.Fatalf("got %s, want the key removed", encoded)
	}
	got, err := metadata.PathPatternsFromConfig(encoded)
	if err != nil {
		t.Fatalf("re-read failed: %v", err)
	}
	if !reflect.DeepEqual(got, metadata.DefaultPathPatterns()) {
		t.Fatalf("got %v, want defaults", got)
	}
}

// A library that only ever held this one setting should end up with no
// document at all rather than an empty object, so an unconfigured library
// looks unconfigured in the database.
func TestWithPathPatternsClearingTheOnlyKeyYieldsNoDocument(t *testing.T) {
	t.Parallel()
	encoded, err := metadata.WithPathPatterns([]byte(`{"path_patterns":[]}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if encoded != nil {
		t.Fatalf("got %q, want nil", encoded)
	}
}

func TestWithPathPatternsEmptyListIsRecorded(t *testing.T) {
	t.Parallel()
	encoded, err := metadata.WithPathPatterns(nil, []metadata.PathPattern{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(encoded) != `{"path_patterns":[]}` {
		t.Fatalf("got %s, want an explicit empty list", encoded)
	}
}

func TestWithPathPatternsRejectsBadInput(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		raw      []byte
		patterns []metadata.PathPattern
	}{
		"unknown layout": {
			patterns: []metadata.PathPattern{"author/isbn"},
		},
		"repeated layout": {
			patterns: []metadata.PathPattern{
				metadata.PatternAuthorTitle, metadata.PatternAuthorTitle},
		},
		"undecodable existing document": {
			raw:      []byte(`["not an object"]`),
			patterns: []metadata.PathPattern{metadata.PatternAuthorTitle},
		},
	}
	for name, tc := range cases {
		encoded, err := metadata.WithPathPatterns(tc.raw, tc.patterns)
		if !errors.Is(err, metadata.ErrInvalidLibraryConfig) {
			t.Fatalf("%s: got (%s, %v), want ErrInvalidLibraryConfig",
				name, encoded, err)
		}
		if encoded != nil {
			t.Fatalf("%s: returned a document alongside an error: %s", name, encoded)
		}
	}
}

func TestParsePathPatternsRoundTrips(t *testing.T) {
	t.Parallel()
	list := metadata.FormatPathPatterns(metadata.AllPathPatterns())
	got, err := metadata.ParsePathPatterns(list)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, metadata.AllPathPatterns()) {
		t.Fatalf("got %v, want %v", got, metadata.AllPathPatterns())
	}
}

func TestParsePathPatternsToleratesSpacingButNotEmptiness(t *testing.T) {
	t.Parallel()
	got, err := metadata.ParsePathPatterns(" author/title , author/series/title ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []metadata.PathPattern{
		metadata.PatternAuthorTitle, metadata.PatternAuthorSeriesTitle,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, list := range []string{"", "  ", "author/title,"} {
		if got, err := metadata.ParsePathPatterns(list); err == nil {
			t.Fatalf("%q: parsed as %v, want an error", list, got)
		}
	}
}

func TestAllPathPatternsCoversEveryKnownLayout(t *testing.T) {
	t.Parallel()
	for _, pattern := range metadata.AllPathPatterns() {
		if !metadata.KnownPathPattern(pattern) {
			t.Fatalf("%q is listed but not recognized", pattern)
		}
	}
	// The defaults are a subset, so a layout added to one list and not the
	// other is caught here rather than by an operator.
	for _, pattern := range metadata.DefaultPathPatterns() {
		found := false
		for _, known := range metadata.AllPathPatterns() {
			if known == pattern {
				found = true
			}
		}
		if !found {
			t.Fatalf("default layout %q is missing from AllPathPatterns", pattern)
		}
	}
	if len(metadata.AllPathPatterns()) <= len(metadata.DefaultPathPatterns()) {
		t.Fatal("AllPathPatterns must offer more than the defaults, " +
			"or per-library configuration has nothing to choose between")
	}
}

func TestPathPatternsConfiguredSeparatesChoiceFromAgreement(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		raw  []byte
		want bool
	}{
		"unset":                {nil, false},
		"empty document":       {[]byte(`{}`), false},
		"other settings only":  {[]byte(`{"future_setting":42}`), false},
		"explicit null":        {[]byte(`{"path_patterns":null}`), false},
		"explicitly none":      {[]byte(`{"path_patterns":[]}`), true},
		"same as the defaults": {[]byte(`{"path_patterns":["author/title"]}`), true},
		"alongside other keys": {[]byte(`{"a":1,"path_patterns":["author/title"]}`), true},
	}
	for name, tc := range cases {
		got, err := metadata.PathPatternsConfigured(tc.raw)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestPathPatternsConfiguredReportsAnUnreadableDocument(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string][]byte{
		"not json":      []byte(`{`),
		"not an object": []byte(`["author/title"]`),
	} {
		got, err := metadata.PathPatternsConfigured(raw)
		if !errors.Is(err, metadata.ErrInvalidLibraryConfig) {
			t.Fatalf("%s: err = %v, want ErrInvalidLibraryConfig", name, err)
		}
		if got {
			t.Fatalf("%s: claimed a configuration it could not read", name)
		}
	}
}
