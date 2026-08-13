package metadata

import (
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

type tagEntry = SetEntry[string, struct{}]
type tagAssertion = Assertion[string, struct{}]

func tags(names ...string) []tagAssertion {
	assertions := make([]tagAssertion, 0, len(names))
	for _, name := range names {
		assertions = append(assertions, tagAssertion{Key: NormalizeName(name)})
	}
	return assertions
}

func TestNormalizeName(t *testing.T) {
	tests := map[string]string{
		"Frank Herbert":    "frank herbert",
		"frank  herbert":   "frank herbert",
		"\tFRANK\nHERBERT": "frank herbert",
		"  ":               "",
		"Dune":             "dune",
	}
	for input, want := range tests {
		if got := NormalizeName(input); got != want {
			t.Fatalf("NormalizeName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMergeSet(t *testing.T) {
	tests := []struct {
		name      string
		current   []tagEntry
		incoming  []tagAssertion
		source    store.MetadataSource
		setLocked bool
		want      []tagEntry
		changed   bool
	}{{
		name:     "empty set accepts an assertion",
		incoming: tags("sci-fi", "classic"),
		source:   store.MetadataEmbedded,
		want: []tagEntry{
			{Key: "sci-fi", Source: store.MetadataEmbedded},
			{Key: "classic", Source: store.MetadataEmbedded},
		},
		changed: true,
	}, {
		name:    "an empty assertion never empties the set",
		current: []tagEntry{{Key: "sci-fi", Source: store.MetadataEmbedded}},
		source:  store.MetadataEmbedded,
		want:    []tagEntry{{Key: "sci-fi", Source: store.MetadataEmbedded}},
	}, {
		name:     "an unknown source is ignored",
		current:  []tagEntry{{Key: "sci-fi", Source: store.MetadataEmbedded}},
		incoming: tags("romance"),
		source:   store.MetadataSource("guess"),
		want:     []tagEntry{{Key: "sci-fi", Source: store.MetadataEmbedded}},
	}, {
		name: "the same source drops what it no longer asserts",
		current: []tagEntry{
			{Key: "sci-fi", Source: store.MetadataEmbedded},
			{Key: "typo", Source: store.MetadataEmbedded},
		},
		incoming: tags("sci-fi"),
		source:   store.MetadataEmbedded,
		want:     []tagEntry{{Key: "sci-fi", Source: store.MetadataEmbedded}},
		changed:  true,
	}, {
		name:     "locked rows survive a rescan that omits them",
		current:  []tagEntry{{Key: "favourite", Source: store.MetadataManual, Locked: true}},
		incoming: tags("sci-fi"),
		source:   store.MetadataEmbedded,
		want: []tagEntry{
			{Key: "favourite", Source: store.MetadataManual, Locked: true},
			{Key: "sci-fi", Source: store.MetadataEmbedded},
		},
		changed: true,
	}, {
		name:     "a locked row is not re-sourced when reasserted",
		current:  []tagEntry{{Key: "sci-fi", Source: store.MetadataManual, Locked: true}},
		incoming: tags("sci-fi"),
		source:   store.MetadataEmbedded,
		want:     []tagEntry{{Key: "sci-fi", Source: store.MetadataManual, Locked: true}},
	}, {
		name:     "a stronger unlocked row survives a weaker assertion",
		current:  []tagEntry{{Key: "sci-fi", Source: store.MetadataExternal}},
		incoming: tags("classic"),
		source:   store.MetadataFilename,
		want: []tagEntry{
			{Key: "sci-fi", Source: store.MetadataExternal},
			{Key: "classic", Source: store.MetadataFilename},
		},
		changed: true,
	}, {
		name:     "a weaker assertion does not downgrade a repeated row",
		current:  []tagEntry{{Key: "sci-fi", Source: store.MetadataExternal}},
		incoming: tags("sci-fi"),
		source:   store.MetadataEmbedded,
		want:     []tagEntry{{Key: "sci-fi", Source: store.MetadataExternal}},
	}, {
		name:     "a stronger source takes over a weaker unlocked row",
		current:  []tagEntry{{Key: "sci-fi", Source: store.MetadataEmbedded}},
		incoming: tags("sci-fi"),
		source:   store.MetadataExternal,
		want:     []tagEntry{{Key: "sci-fi", Source: store.MetadataExternal}},
		changed:  true,
	}, {
		name: "a stronger source drops weaker rows it omits",
		current: []tagEntry{
			{Key: "sci-fi", Source: store.MetadataEmbedded},
			{Key: "guessed", Source: store.MetadataFilename},
		},
		incoming: tags("sci-fi"),
		source:   store.MetadataExternal,
		want:     []tagEntry{{Key: "sci-fi", Source: store.MetadataExternal}},
		changed:  true,
	}, {
		name:     "duplicate assertions collapse",
		incoming: tags("Sci-Fi", "sci-fi", "SCI-FI"),
		source:   store.MetadataEmbedded,
		want:     []tagEntry{{Key: "sci-fi", Source: store.MetadataEmbedded}},
		changed:  true,
	}, {
		name:      "a set-level lock rejects the assertion",
		current:   []tagEntry{{Key: "sci-fi", Source: store.MetadataEmbedded}},
		incoming:  tags("classic"),
		source:    store.MetadataManual,
		setLocked: true,
		want:      []tagEntry{{Key: "sci-fi", Source: store.MetadataEmbedded}},
	}, {
		name: "surviving rows keep their order",
		current: []tagEntry{
			{Key: "a", Source: store.MetadataEmbedded},
			{Key: "b", Source: store.MetadataManual, Locked: true},
			{Key: "c", Source: store.MetadataEmbedded},
		},
		incoming: tags("c", "a", "d"),
		source:   store.MetadataEmbedded,
		want: []tagEntry{
			{Key: "a", Source: store.MetadataEmbedded},
			{Key: "b", Source: store.MetadataManual, Locked: true},
			{Key: "c", Source: store.MetadataEmbedded},
			{Key: "d", Source: store.MetadataEmbedded},
		},
		changed: true,
	}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := MergeSet(
				tc.current, tc.incoming, tc.source, tc.setLocked)
			assertEntries(t, got, tc.want)
			if changed != tc.changed {
				t.Fatalf("MergeSet() changed = %v, want %v", changed, tc.changed)
			}
		})
	}
}

func TestMergeSetIsIdempotent(t *testing.T) {
	current := []tagEntry{
		{Key: "favourite", Source: store.MetadataManual, Locked: true},
		{Key: "stale", Source: store.MetadataEmbedded},
	}
	incoming := tags("sci-fi", "classic")
	merged, changed := MergeSet(current, incoming, store.MetadataEmbedded, false)
	if !changed {
		t.Fatalf("first merge reported no change")
	}
	replayed, changed := MergeSet(merged, incoming, store.MetadataEmbedded, false)
	if changed {
		t.Fatalf("replaying the same assertion changed the set")
	}
	assertEntries(t, replayed, merged)
}

func TestMergeSetCarriesPayload(t *testing.T) {
	type position struct {
		Number float64
		Known  bool
	}
	current := []SetEntry[string, position]{
		{Key: "dune", Value: position{1, true}, Source: store.MetadataEmbedded},
		{Key: "locked", Value: position{9, true}, Source: store.MetadataManual, Locked: true},
	}
	incoming := []Assertion[string, position]{
		{Key: "dune", Value: position{2, true}},
		{Key: "locked", Value: position{3, true}},
	}
	got, changed := MergeSet(current, incoming, store.MetadataFilename, false)
	if !changed {
		t.Fatalf("a changed payload reported no change")
	}
	want := []SetEntry[string, position]{
		{Key: "dune", Value: position{2, true}, Source: store.MetadataFilename},
		{Key: "locked", Value: position{9, true}, Source: store.MetadataManual, Locked: true},
	}
	assertEntries(t, got, want)
}

func TestManualSet(t *testing.T) {
	current := []tagEntry{
		{Key: "sci-fi", Source: store.MetadataEmbedded},
		{Key: "wrong", Source: store.MetadataManual, Locked: true},
	}
	got, changed := ManualSet(current, tags("classic", "Classic"))
	if !changed {
		t.Fatalf("ManualSet reported no change")
	}
	want := []tagEntry{{Key: "classic", Source: store.MetadataManual, Locked: true}}
	assertEntries(t, got, want)

	if _, changed := ManualSet(got, tags("classic")); changed {
		t.Fatalf("ManualSet is not idempotent")
	}

	emptied, changed := ManualSet(got, nil)
	if !changed || len(emptied) != 0 {
		t.Fatalf("ManualSet(nil) = %+v, changed %v", emptied, changed)
	}
	rescanned, changed := MergeSet(
		emptied, tags("classic"), store.MetadataEmbedded, false)
	if !changed || len(rescanned) != 1 {
		t.Fatalf("an unlocked emptied set should accept a later rescan: %+v",
			rescanned)
	}
	locked, changed := MergeSet(
		emptied, tags("classic"), store.MetadataEmbedded, true)
	if changed || len(locked) != 0 {
		t.Fatalf("a locked emptied set was resurrected by a rescan: %+v", locked)
	}
}

func assertEntries[K comparable, V comparable](
	t *testing.T, got, want []SetEntry[K, V],
) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("set = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("set[%d] = %+v, want %+v (full %+v)", i, got[i], want[i], got)
		}
	}
}
