package metadata

import (
	"math/rand"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

var srcs = []store.MetadataSource{
	store.MetadataEmbedded, store.MetadataFilename,
	store.MetadataExternal, store.MetadataManual, store.MetadataSource("x"), "",
}

func randState(r *rand.Rand) []SetEntry[string, int] {
	keys := []string{"a", "b", "c", "d"}
	out := []SetEntry[string, int]{}
	for _, k := range keys {
		if r.Intn(2) == 0 {
			continue
		}
		out = append(out, SetEntry[string, int]{
			Key: k, Value: r.Intn(3),
			Source: srcs[r.Intn(len(srcs))], Locked: r.Intn(4) == 0,
		})
	}
	return out
}

func randAssert(r *rand.Rand) []Assertion[string, int] {
	keys := []string{"a", "b", "c", "d", "e"}
	out := []Assertion[string, int]{}
	for _, k := range keys {
		if r.Intn(2) == 0 {
			continue
		}
		out = append(out, Assertion[string, int]{Key: k, Value: r.Intn(3)})
	}
	return out
}

func clone(s []SetEntry[string, int]) []SetEntry[string, int] {
	return append([]SetEntry[string, int]{}, s...)
}

func index(s []SetEntry[string, int]) map[string]SetEntry[string, int] {
	m := map[string]SetEntry[string, int]{}
	for _, e := range s {
		if _, dup := m[e.Key]; dup {
			panic("duplicate key in result: " + e.Key)
		}
		m[e.Key] = e
	}
	return m
}

func TestScratchProps(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 200000; i++ {
		cur := randState(r)
		before := clone(cur)
		inc := randAssert(r)
		src := srcs[r.Intn(len(srcs))]
		locked := r.Intn(8) == 0
		got, changed := MergeSet(cur, inc, src, locked)
		gi := index(got)
		bi := index(before)

		// input not mutated
		for j := range before {
			if j < len(cur) && cur[j] != before[j] {
				t.Fatalf("input mutated: %+v -> %+v", before, cur)
			}
		}
		// changed accuracy
		if changed != !equalSets(before, got) {
			t.Fatalf("changed mismatch %v %+v %+v", changed, before, got)
		}
		// locked rows preserved verbatim
		for _, e := range before {
			if e.Locked {
				if g, ok := gi[e.Key]; !ok || g != e {
					t.Fatalf("lost/changed locked row %+v -> %+v (inc %+v src %q locked %v)", e, got, inc, src, locked)
				}
			}
		}
		// no source downgrade for surviving keys
		for k, g := range gi {
			if b, ok := bi[k]; ok && Rank(g.Source) < Rank(b.Source) {
				t.Fatalf("downgrade %+v -> %+v (src %q)", b, g, src)
			}
		}
		// never empties a non-empty set
		if len(before) > 0 && len(got) == 0 {
			t.Fatalf("emptied set: %+v inc %+v src %q", before, inc, src)
		}
		// idempotent
		again, ch2 := MergeSet(got, inc, src, locked)
		if ch2 || !equalSets(got, again) {
			t.Fatalf("not idempotent: %+v -> %+v (%v) inc %+v src %q", got, again, ch2, inc, src)
		}
		// every asserted key present when accepted
		if !locked && len(inc) > 0 && KnownSource(src) {
			for _, a := range inc {
				if _, ok := gi[a.Key]; !ok {
					t.Fatalf("asserted key %q missing from %+v", a.Key, got)
				}
			}
		}
	}
}
