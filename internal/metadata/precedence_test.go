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
