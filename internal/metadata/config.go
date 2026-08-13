package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// pathPatternsKey names the library configuration entry that holds the
// layout list. The column it lives in is shared with whatever a later
// feature needs to record about a library, so this package reads and writes
// exactly this one key and leaves the rest of the document alone.
const pathPatternsKey = "path_patterns"

// ErrInvalidLibraryConfig reports a library configuration document this
// server cannot act on: not an object, or holding a layout list it does not
// recognize.
//
// It is deliberately not a fallback to the defaults. A library configured
// for one layout that is silently parsed with another produces books whose
// authors and series are wrong, and a wrong value costs an operator more
// than an unparsed one: nothing lists the books that were misfiled.
var ErrInvalidLibraryConfig = errors.New("metadata: invalid library configuration")

// PathPatternsFromConfig reads the layout list a library is configured with.
//
// An absent key means the library never expressed a preference and gets the
// conservative defaults. An empty list is a different answer: it means an
// operator decided this library's filenames say nothing worth reading, and
// it disables path parsing rather than restoring the defaults.
func PathPatternsFromConfig(raw []byte) ([]PathPattern, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return DefaultPathPatterns(), nil
	}
	fields, err := decodeLibraryConfig(raw)
	if err != nil {
		return nil, err
	}
	value, ok := fields[pathPatternsKey]
	if !ok || string(bytes.TrimSpace(value)) == "null" {
		return DefaultPathPatterns(), nil
	}
	var names []string
	if err := json.Unmarshal(value, &names); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalidLibraryConfig,
			pathPatternsKey, err)
	}
	return validatePathPatterns(names)
}

// WithPathPatterns returns raw with its layout list replaced, preserving
// every other key so that writing this setting never drops a setting this
// version of the server does not know about.
//
// A nil list removes the key, which restores the defaults; an empty non-nil
// list is recorded as an empty list, which disables path parsing.
func WithPathPatterns(raw []byte, patterns []PathPattern) ([]byte, error) {
	fields := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(raw)) != 0 {
		decoded, err := decodeLibraryConfig(raw)
		if err != nil {
			return nil, err
		}
		fields = decoded
	}
	if patterns == nil {
		delete(fields, pathPatternsKey)
	} else {
		names := make([]string, 0, len(patterns))
		for _, pattern := range patterns {
			names = append(names, string(pattern))
		}
		// The same validation the read side runs, so a document this
		// package writes is always one it can read back.
		if _, err := validatePathPatterns(names); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(names)
		if err != nil {
			return nil, err
		}
		fields[pathPatternsKey] = encoded
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return json.Marshal(fields)
}

// ParsePathPatterns reads a comma-separated layout list as an operator would
// type it. The empty string is not accepted here: a caller that means "no
// layouts" has to say so in a way that cannot be a slip of the keyboard.
func ParsePathPatterns(list string) ([]PathPattern, error) {
	names := strings.Split(list, ",")
	for i, name := range names {
		names[i] = strings.TrimSpace(name)
	}
	return validatePathPatterns(names)
}

// FormatPathPatterns renders a layout list the way ParsePathPatterns reads
// it, so what an operator is shown can be handed straight back.
func FormatPathPatterns(patterns []PathPattern) string {
	names := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		names = append(names, string(pattern))
	}
	return strings.Join(names, ",")
}

// AllPathPatterns lists every layout this server recognizes, in the order an
// operator should consider them.
func AllPathPatterns() []PathPattern {
	return []PathPattern{
		PatternAuthorTitle,
		PatternAuthorSeriesTitle,
		PatternSeriesAuthorTitle,
		PatternFlatAuthorSeriesTitle,
	}
}

// decodeLibraryConfig rejects anything that is not a JSON object, because
// the column holds a document whose other keys must survive a write.
func decodeLibraryConfig(raw []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLibraryConfig, err)
	}
	if fields == nil {
		return nil, fmt.Errorf("%w: configuration is null", ErrInvalidLibraryConfig)
	}
	return fields, nil
}

// validatePathPatterns rejects an unknown or repeated layout. Order is
// meaningful — it decides which of two layouts claiming the same depth wins
// — so a repeat is an operator saying two different things about one
// position rather than a harmless duplicate.
func validatePathPatterns(names []string) ([]PathPattern, error) {
	patterns := make([]PathPattern, 0, len(names))
	seen := make(map[PathPattern]bool, len(names))
	for _, name := range names {
		pattern := PathPattern(strings.TrimSpace(name))
		if !KnownPathPattern(pattern) {
			return nil, fmt.Errorf("%w: unknown layout %q",
				ErrInvalidLibraryConfig, name)
		}
		if seen[pattern] {
			return nil, fmt.Errorf("%w: layout %q listed twice",
				ErrInvalidLibraryConfig, name)
		}
		seen[pattern] = true
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

// PathPatternsConfigured reports whether a library states a layout list of
// its own. It is the difference between a library that happens to agree
// with the defaults and one that has never been configured, which is what
// an operator needs to know before changing it.
func PathPatternsConfigured(raw []byte) (bool, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false, nil
	}
	fields, err := decodeLibraryConfig(raw)
	if err != nil {
		return false, err
	}
	value, ok := fields[pathPatternsKey]
	return ok && string(bytes.TrimSpace(value)) != "null", nil
}
