package metadata

import (
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

func TestRankOrdersPrecedenceStages(t *testing.T) {
	if Rank(store.MetadataEmbedded) >= Rank(store.MetadataFilename) ||
		Rank(store.MetadataFilename) >= Rank(store.MetadataExternal) ||
		Rank(store.MetadataExternal) >= Rank(store.MetadataManual) {
		t.Fatalf("precedence is not strictly ascending")
	}
	if Rank(store.MetadataSource("")) != 0 ||
		Rank(store.MetadataSource("bogus")) != 0 {
		t.Fatalf("unknown sources must rank below every real source")
	}
	if KnownSource(store.MetadataSource("bogus")) {
		t.Fatalf("bogus source reported as known")
	}
}

func TestApply(t *testing.T) {
	tests := []struct {
		name      string
		current   Field
		candidate Candidate
		want      Field
		changed   bool
	}{{
		name:      "empty field accepts lowest precedence",
		candidate: Candidate{Value: "Dune", Source: store.MetadataEmbedded},
		want:      Field{Value: "Dune", Source: store.MetadataEmbedded},
		changed:   true,
	}, {
		name:      "blank candidate never clears a value",
		current:   Field{Value: "Dune", Source: store.MetadataEmbedded},
		candidate: Candidate{Value: "   ", Source: store.MetadataManual},
		want:      Field{Value: "Dune", Source: store.MetadataEmbedded},
	}, {
		name:      "unknown source is ignored",
		current:   Field{Value: "Dune", Source: store.MetadataEmbedded},
		candidate: Candidate{Value: "Duna", Source: store.MetadataSource("guess")},
		want:      Field{Value: "Dune", Source: store.MetadataEmbedded},
	}, {
		name:      "higher precedence replaces lower",
		current:   Field{Value: "Dune", Source: store.MetadataEmbedded},
		candidate: Candidate{Value: "Dune Messiah", Source: store.MetadataFilename},
		want:      Field{Value: "Dune Messiah", Source: store.MetadataFilename},
		changed:   true,
	}, {
		name:      "lower precedence never replaces higher",
		current:   Field{Value: "Dune", Source: store.MetadataExternal},
		candidate: Candidate{Value: "02 - Dune", Source: store.MetadataFilename},
		want:      Field{Value: "Dune", Source: store.MetadataExternal},
	}, {
		name:      "same source refreshes in place",
		current:   Field{Value: "Dune", Source: store.MetadataEmbedded},
		candidate: Candidate{Value: "Dune (Revised)", Source: store.MetadataEmbedded},
		want:      Field{Value: "Dune (Revised)", Source: store.MetadataEmbedded},
		changed:   true,
	}, {
		name:      "identical value from same source does not change",
		current:   Field{Value: "Dune", Source: store.MetadataEmbedded},
		candidate: Candidate{Value: "  Dune  ", Source: store.MetadataEmbedded},
		want:      Field{Value: "Dune", Source: store.MetadataEmbedded},
	}, {
		name:      "lock survives a rescan",
		current:   Field{Value: "Dune", Source: store.MetadataManual, Locked: true},
		candidate: Candidate{Value: "dune", Source: store.MetadataEmbedded},
		want:      Field{Value: "Dune", Source: store.MetadataManual, Locked: true},
	}, {
		name:      "lock survives an external lookup",
		current:   Field{Value: "Dune", Source: store.MetadataEmbedded, Locked: true},
		candidate: Candidate{Value: "Dune: A Novel", Source: store.MetadataExternal},
		want:      Field{Value: "Dune", Source: store.MetadataEmbedded, Locked: true},
	}, {
		name:      "lock survives a rescan from its own source",
		current:   Field{Value: "Dune", Source: store.MetadataEmbedded, Locked: true},
		candidate: Candidate{Value: "dune", Source: store.MetadataEmbedded},
		want:      Field{Value: "Dune", Source: store.MetadataEmbedded, Locked: true},
	}, {
		name:      "manual edit overrides a lock",
		current:   Field{Value: "Dune", Source: store.MetadataEmbedded, Locked: true},
		candidate: Candidate{Value: "Dune Messiah", Source: store.MetadataManual},
		want:      Field{Value: "Dune Messiah", Source: store.MetadataManual, Locked: true},
		changed:   true,
	}, {
		name:      "manual edit locks the field",
		current:   Field{Value: "Dune", Source: store.MetadataEmbedded},
		candidate: Candidate{Value: "Dune Messiah", Source: store.MetadataManual},
		want:      Field{Value: "Dune Messiah", Source: store.MetadataManual, Locked: true},
		changed:   true,
	}, {
		name:      "a locked empty field rejects a rescan",
		current:   Field{Source: store.MetadataManual, Locked: true},
		candidate: Candidate{Value: "Dune", Source: store.MetadataEmbedded},
		want:      Field{Source: store.MetadataManual, Locked: true},
	}, {
		name:      "an unset source loses to any real source",
		current:   Field{Value: "dune.epub"},
		candidate: Candidate{Value: "Dune", Source: store.MetadataEmbedded},
		want:      Field{Value: "Dune", Source: store.MetadataEmbedded},
		changed:   true,
	}, {
		name:      "candidate value is trimmed",
		candidate: Candidate{Value: "\t Dune \n", Source: store.MetadataFilename},
		want:      Field{Value: "Dune", Source: store.MetadataFilename},
		changed:   true,
	}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := Apply(tc.current, tc.candidate)
			if got != tc.want {
				t.Fatalf("Apply() = %+v, want %+v", got, tc.want)
			}
			if changed != tc.changed {
				t.Fatalf("Apply() changed = %v, want %v", changed, tc.changed)
			}
		})
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	candidates := []Candidate{
		{Value: "Dune", Source: store.MetadataEmbedded},
		{Value: "Dune Messiah", Source: store.MetadataFilename},
		{Value: "", Source: store.MetadataExternal},
		{Value: "Children of Dune", Source: store.MetadataManual},
		{Value: "Dune", Source: store.MetadataEmbedded},
	}
	var field Field
	for _, candidate := range candidates {
		field, _ = Apply(field, candidate)
	}
	for _, candidate := range candidates {
		replayed, changed := Apply(field, candidate)
		if candidate.Source != store.MetadataManual && changed {
			t.Fatalf("replaying %+v changed a settled field", candidate)
		}
		if candidate.Source != store.MetadataManual && replayed != field {
			t.Fatalf("replay = %+v, want %+v", replayed, field)
		}
	}
	want := Field{
		Value: "Children of Dune", Source: store.MetadataManual, Locked: true}
	if field != want {
		t.Fatalf("settled field = %+v, want %+v", field, want)
	}
}

func TestManualClearAndSetLocked(t *testing.T) {
	current := Field{Value: "Dune", Source: store.MetadataEmbedded}
	cleared, changed := ManualClear(current)
	if !changed {
		t.Fatalf("ManualClear reported no change")
	}
	want := Field{Source: store.MetadataManual, Locked: true}
	if cleared != want {
		t.Fatalf("ManualClear() = %+v, want %+v", cleared, want)
	}
	if _, changed := ManualClear(cleared); changed {
		t.Fatalf("ManualClear is not idempotent")
	}
	restored, changed := Apply(cleared, Candidate{
		Value: "Dune", Source: store.MetadataEmbedded})
	if changed || restored != cleared {
		t.Fatalf("a rescan restored a manually cleared field")
	}

	unlocked, changed := SetLocked(cleared, false)
	if !changed || unlocked.Locked {
		t.Fatalf("SetLocked(false) = %+v, changed %v", unlocked, changed)
	}
	if _, changed := SetLocked(unlocked, false); changed {
		t.Fatalf("SetLocked is not idempotent")
	}
	reapplied, changed := Apply(unlocked, Candidate{
		Value: "Dune", Source: store.MetadataEmbedded})
	if !changed ||
		reapplied != (Field{Value: "Dune", Source: store.MetadataEmbedded}) {
		t.Fatalf("unlocked empty field did not accept a rescan: %+v", reapplied)
	}
}

func TestCalibreOutranksEverythingButAHuman(t *testing.T) {
	t.Parallel()
	// The EPUB said one thing and Calibre says another; Calibre is the
	// point of pointing this server at a Calibre library.
	embedded := Field{Value: "dune", Source: store.MetadataEmbedded}
	calibre, changed := Apply(embedded, Candidate{
		Value: "Dune", Source: store.MetadataCalibre})
	if !changed || calibre.Value != "Dune" ||
		calibre.Source != store.MetadataCalibre {
		t.Fatalf("Calibre did not win over the EPUB: %+v", calibre)
	}
	// A lookup service does not get to undo it.
	if _, changed := Apply(calibre, Candidate{
		Value: "Dune (1965)", Source: store.MetadataExternal}); changed {
		t.Fatal("an external lookup overwrote Calibre")
	}
	// A later Calibre read refreshes its own value.
	corrected, changed := Apply(calibre, Candidate{
		Value: "Dune: Book One", Source: store.MetadataCalibre})
	if !changed || corrected.Value != "Dune: Book One" {
		t.Fatalf("Calibre did not refresh its own value: %+v", corrected)
	}
	// A human does, and locks it against every later refresh.
	edited, changed := Apply(corrected, Candidate{
		Value: "Dune", Source: store.MetadataManual})
	if !changed || !edited.Locked {
		t.Fatalf("a manual edit did not win and lock: %+v", edited)
	}
	if _, changed := Apply(edited, Candidate{
		Value: "Dune: Book One", Source: store.MetadataCalibre}); changed {
		t.Fatal("a Calibre refresh clobbered a manual edit")
	}
}

func TestClearByLeavesATombstone(t *testing.T) {
	t.Parallel()
	current := Field{Value: "A blurb", Source: store.MetadataCalibre}
	cleared, changed := ClearBy(current, store.MetadataCalibre)
	if !changed || cleared.Value != "" ||
		cleared.Source != store.MetadataCalibre || cleared.Locked {
		t.Fatalf("ClearBy = %+v, changed %v", cleared, changed)
	}
	if _, changed := ClearBy(cleared, store.MetadataCalibre); changed {
		t.Fatal("ClearBy is not idempotent")
	}
	// The whole point: the EPUB does not refill what Calibre emptied.
	if _, changed := Apply(cleared, Candidate{
		Value: "A blurb from the file", Source: store.MetadataEmbedded,
	}); changed {
		t.Fatal("the EPUB refilled a field Calibre cleared")
	}
	if _, changed := Apply(cleared, Candidate{
		Value: "A blurb from a lookup", Source: store.MetadataExternal,
	}); changed {
		t.Fatal("a lookup refilled a field Calibre cleared")
	}
	// Calibre itself, and a human, still can.
	refilled, changed := Apply(cleared, Candidate{
		Value: "A better blurb", Source: store.MetadataCalibre})
	if !changed || refilled.Value != "A better blurb" {
		t.Fatalf("Calibre could not refill its own tombstone: %+v", refilled)
	}
	edited, changed := Apply(cleared, Candidate{
		Value: "Mine", Source: store.MetadataManual})
	if !changed || !edited.Locked || edited.Value != "Mine" {
		t.Fatalf("a human could not refill a Calibre tombstone: %+v", edited)
	}
}

func TestClearByRefusesWhatItMayNotWrite(t *testing.T) {
	t.Parallel()
	locked := Field{Value: "Mine", Source: store.MetadataManual, Locked: true}
	if _, changed := ClearBy(locked, store.MetadataCalibre); changed {
		t.Fatal("Calibre cleared a manually locked field")
	}
	stronger := Field{Value: "From Calibre", Source: store.MetadataCalibre}
	if _, changed := ClearBy(stronger, store.MetadataEmbedded); changed {
		t.Fatal("the EPUB cleared a field Calibre owns")
	}
	if _, changed := ClearBy(stronger, ""); changed {
		t.Fatal("an unknown source cleared a field")
	}
}

func TestAnEmptyFieldWithoutProvenanceStillAcceptsAnything(t *testing.T) {
	t.Parallel()
	// Rows written before provenance existed must not become tombstones.
	filled, changed := Apply(Field{}, Candidate{
		Value: "Dune", Source: store.MetadataEmbedded})
	if !changed || filled.Value != "Dune" {
		t.Fatalf("an empty field refused a candidate: %+v", filled)
	}
}
