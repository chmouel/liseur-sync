package metadata

import (
	"math/rand/v2"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestMergeSetProperties pins the invariants a table test cannot cover, by
// replaying randomized merges over every combination of source, lock, and
// membership. A metadata set is durable user data, so a merge must never
// lose a locked row, weaken provenance, or empty a set.
func TestMergeSetProperties(t *testing.T) {
	sources := []store.MetadataSource{
		store.MetadataEmbedded,
		store.MetadataFilename,
		store.MetadataExternal,
		store.MetadataManual,
		store.MetadataSource("unrecognized"),
		store.MetadataSource(""),
	}
	random := rand.New(rand.NewPCG(1, 2))
	randomRows := func() []SetEntry[string, int] {
		rows := []SetEntry[string, int]{}
		for _, key := range []string{"a", "b", "c", "d"} {
			if random.IntN(2) == 0 {
				continue
			}
			rows = append(rows, SetEntry[string, int]{
				Key:    key,
				Value:  random.IntN(3),
				Source: sources[random.IntN(len(sources))],
				Locked: random.IntN(4) == 0,
			})
		}
		return rows
	}
	randomAssertion := func() []Assertion[string, int] {
		assertion := []Assertion[string, int]{}
		for _, key := range []string{"a", "b", "c", "d", "e"} {
			if random.IntN(2) == 0 {
				continue
			}
			assertion = append(assertion,
				Assertion[string, int]{Key: key, Value: random.IntN(3)})
		}
		return assertion
	}

	for i := 0; i < 20000; i++ {
		current := randomRows()
		before := append([]SetEntry[string, int](nil), current...)
		incoming := randomAssertion()
		source := sources[random.IntN(len(sources))]
		setLocked := random.IntN(8) == 0

		merged, changed := MergeSet(current, incoming, source, setLocked)
		context := func() string {
			t.Helper()
			return "current " + render(before) + " assertion " +
				renderAssertion(incoming) + " source " + string(source)
		}

		if !equalSets(before, current) {
			t.Fatalf("MergeSet mutated its input: %s", context())
		}
		mergedByKey := indexEntries(t, merged)
		if changed != !equalSets(before, merged) {
			t.Fatalf("changed = %v but result is %s: %s",
				changed, render(merged), context())
		}
		if len(before) > 0 && len(merged) == 0 {
			t.Fatalf("a merge emptied a set: %s", context())
		}
		for _, row := range before {
			if !row.Locked {
				continue
			}
			if got, ok := mergedByKey[row.Key]; !ok || got != row {
				t.Fatalf("locked row %+v was lost or altered: %s", row, context())
			}
		}
		for _, row := range before {
			got, ok := mergedByKey[row.Key]
			if ok && Rank(got.Source) < Rank(row.Source) {
				t.Fatalf("row %+v was downgraded to %+v: %s", row, got, context())
			}
		}
		replayed, replayChanged := MergeSet(merged, incoming, source, setLocked)
		if replayChanged || !equalSets(merged, replayed) {
			t.Fatalf("replay changed a settled set to %s: %s",
				render(replayed), context())
		}
		if setLocked || len(incoming) == 0 || !KnownSource(source) {
			if !equalSets(before, merged) {
				t.Fatalf("a rejected assertion changed the set: %s", context())
			}
			continue
		}
		for _, assertion := range incoming {
			if _, ok := mergedByKey[assertion.Key]; !ok {
				t.Fatalf("accepted assertion dropped key %q: %s",
					assertion.Key, context())
			}
		}
	}
}

func indexEntries(
	t *testing.T, rows []SetEntry[string, int],
) map[string]SetEntry[string, int] {
	t.Helper()
	byKey := make(map[string]SetEntry[string, int], len(rows))
	for _, row := range rows {
		if _, duplicate := byKey[row.Key]; duplicate {
			t.Fatalf("duplicate key %q in %s", row.Key, render(rows))
		}
		byKey[row.Key] = row
	}
	return byKey
}

func render(rows []SetEntry[string, int]) string {
	out := "["
	for i, row := range rows {
		if i > 0 {
			out += " "
		}
		out += row.Key + "=" + string(row.Source)
		if row.Locked {
			out += "!"
		}
	}
	return out + "]"
}

func renderAssertion(assertions []Assertion[string, int]) string {
	out := "["
	for i, assertion := range assertions {
		if i > 0 {
			out += " "
		}
		out += assertion.Key
	}
	return out + "]"
}
