package reading

import "testing"

func TestResolve(t *testing.T) {
	position := func(v float64) *float64 { return &v }
	cases := []struct {
		name     string
		choice   Override
		position *float64
		partial  bool
		want     Status
	}{
		{
			name: "explicit finished at 0.4", choice: OverrideFinished,
			position: position(0.4), want: StatusFinished,
		},
		{
			name: "explicit finished with no position", choice: OverrideFinished,
			want: StatusFinished,
		},
		{
			name: "explicit unread above the threshold", choice: OverrideUnread,
			position: position(0.99), want: StatusUnread,
		},
		{
			name: "explicit unread with no position", choice: OverrideUnread,
			want: StatusUnread,
		},
		{
			name: "automatic derives again", choice: OverrideAutomatic,
			position: position(0.5), want: StatusReading,
		},
		{
			name: "automatic and finished by position", choice: OverrideAutomatic,
			position: position(0.98), want: StatusFinished,
		},
		{
			name: "no choice, no position", want: StatusReadyToRead,
		},
		{
			name: "no choice at zero", position: position(0), want: StatusReadyToRead,
		},
		{
			name: "partial coverage at 0.998 stays reading", position: position(0.998),
			partial: true, want: StatusReading,
		},
		{
			name: "full coverage at 0.998 is finished", position: position(0.998),
			want: StatusFinished,
		},
		{
			name: "unknown coverage at 0.998 is finished", position: position(0.998),
			want: StatusFinished,
		},
		{
			name: "0.96 is reading", position: position(0.96), want: StatusReading,
		},
		{
			name: "0.97 is finished", position: position(FinishedProgression), want: StatusFinished,
		},
		{
			name:     "a historical position with no coverage still finishes",
			position: position(0.999), want: StatusFinished,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Resolve(c.choice, c.position, c.partial); got != c.want {
				t.Fatalf("Resolve(%q, %v, %t) = %q, want %q",
					c.choice, c.position, c.partial, got, c.want)
			}
		})
	}
}

func TestDeriveThresholdIsLiseurs(t *testing.T) {
	if FinishedProgression != 0.97 {
		t.Fatalf("FinishedProgression = %v, want 0.97", FinishedProgression)
	}
	below, above := FinishedProgression-0.0001, FinishedProgression
	if got := Derive(&below, false); got != StatusReading {
		t.Fatalf("just below the threshold = %q", got)
	}
	if got := Derive(&above, false); got != StatusFinished {
		t.Fatalf("at the threshold = %q", got)
	}
}

// TestUnreadIsNotTheDerivedPile: the explicit choice and the derived
// no-position result share a shelf, but a client that has the choice
// must not be told the book was never opened.
func TestUnreadIsNotTheDerivedPile(t *testing.T) {
	zero := 0.0
	if got := Resolve(OverrideNone, &zero, false); got != StatusReadyToRead {
		t.Fatalf("derived at zero = %q, want %q", got, StatusReadyToRead)
	}
	if got := Resolve(OverrideUnread, &zero, false); got != StatusUnread {
		t.Fatalf("explicit unread = %q, want %q", got, StatusUnread)
	}
	for status, want := range map[Status]bool{
		StatusUnread: true, StatusReadyToRead: true,
		StatusReading: false, StatusFinished: false, "": false,
	} {
		if got := status.UnreadShelf(); got != want {
			t.Fatalf("%q.UnreadShelf() = %t, want %t", status, got, want)
		}
	}
}

func TestExplicitExtendedStatusOverridesPosition(t *testing.T) {
	for _, tc := range []struct {
		choice Override
		want   Status
	}{
		{OverrideWantToRead, StatusWantToRead},
		{OverrideReading, StatusReading},
		{OverridePaused, StatusPaused},
		{OverrideDidNotFinish, StatusDidNotFinish},
	} {
		t.Run(string(tc.choice), func(t *testing.T) {
			for _, progress := range []float64{0, 0.5, 1} {
				if got := Resolve(tc.choice, &progress, false); got != tc.want {
					t.Fatalf("progress %v: got %q, want %q", progress, got, tc.want)
				}
			}
		})
	}
}
