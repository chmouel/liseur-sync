// Package metadata implements the field-level precedence and lock engine
// that every ingest source shares. It is pure: it performs no I/O and holds
// no state, so both the ingestion workers and the edit handlers can reuse
// the same decisions.
package metadata

import (
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Rank reports the precedence of a metadata source. Higher ranks win. An
// unset or unrecognized source ranks below every real source so that a
// legacy row without provenance never blocks a genuine candidate.
func Rank(source store.MetadataSource) int {
	switch source {
	case store.MetadataEmbedded:
		return 1
	case store.MetadataFilename:
		return 2
	case store.MetadataExternal:
		return 3
	case store.MetadataManual:
		return 4
	default:
		return 0
	}
}

// KnownSource reports whether source is one of the four precedence stages.
func KnownSource(source store.MetadataSource) bool {
	return Rank(source) > 0
}

// Field is one editable scalar catalog value with its provenance.
type Field struct {
	Value  string
	Source store.MetadataSource
	Locked bool
}

// Candidate is one proposed value for a Field.
type Candidate struct {
	Value  string
	Source store.MetadataSource
}

// Apply resolves one candidate against the current field and reports the
// resulting field and whether anything changed. The rules are:
//
//   - A blank candidate is always ignored. A source that cannot determine a
//     value leaves the field alone rather than clearing it, so an ambiguous
//     rescan can never erase catalog data.
//   - A candidate from an unknown source is ignored.
//   - A locked field only accepts manual candidates, so a rescan or an
//     external lookup never overwrites a user's correction.
//   - An empty unlocked field accepts any candidate.
//   - A non-empty unlocked field accepts a candidate of strictly higher
//     precedence, or one from the same source, which is how a re-extraction
//     of changed content refreshes its own earlier value.
//   - A manual candidate locks the field.
func Apply(current Field, candidate Candidate) (Field, bool) {
	value := strings.TrimSpace(candidate.Value)
	if value == "" || !KnownSource(candidate.Source) {
		return current, false
	}
	if !accepts(current, candidate.Source) {
		return current, false
	}
	next := Field{
		Value:  value,
		Source: candidate.Source,
		Locked: current.Locked || candidate.Source == store.MetadataManual,
	}
	return next, next != current
}

func accepts(current Field, source store.MetadataSource) bool {
	if current.Locked {
		return source == store.MetadataManual
	}
	if strings.TrimSpace(current.Value) == "" {
		return true
	}
	return Rank(source) > Rank(current.Source) || source == current.Source
}

// ManualClear blanks a field on explicit user request and locks it, which is
// how a user suppresses a wrong embedded value without a later rescan
// restoring it. It is deliberately the only way a field becomes empty.
func ManualClear(current Field) (Field, bool) {
	next := Field{Source: store.MetadataManual, Locked: true}
	return next, next != current
}

// SetLocked returns the field with its manual lock flag set to locked.
func SetLocked(current Field, locked bool) (Field, bool) {
	next := current
	next.Locked = locked
	return next, next != current
}
