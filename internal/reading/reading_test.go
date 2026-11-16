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
		StatusUnread: true, StatusReadyToRead: true, StatusWantToRead: true,
		StatusReading: false, StatusFinished: false, StatusPaused: false,
		StatusDidNotFinish: false, "": false,
	} {
		if got := status.UnreadShelf(); got != want {
			t.Fatalf("%q.UnreadShelf() = %t, want %t", status, got, want)
		}
	}
}

// TestShelfAndNextUpMatrix: the shelf a status belongs on and whether it
// may be offered as what to read next are decided here, once, for every
// surface. A paused or unfinished book is a deliberate "not now" or
// "never", so it is neither unread nor next up; a book being read is
// next up even when the reader said so before recording a position.
func TestShelfAndNextUpMatrix(t *testing.T) {
	for _, tc := range []struct {
		status   Status
		unread   bool
		nextUp   bool
		shelfWhy string
	}{
		{StatusReadyToRead, true, true, "derived, never opened"},
		{StatusUnread, true, true, "put back on the pile"},
		{StatusWantToRead, true, true, "waiting to be read"},
		{StatusReading, false, true, "being read"},
		{StatusPaused, false, false, "put down for now"},
		{StatusDidNotFinish, false, false, "given up on"},
		{StatusFinished, false, false, "read"},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			if got := tc.status.UnreadShelf(); got != tc.unread {
				t.Errorf("%q.UnreadShelf() = %t, want %t (%s)",
					tc.status, got, tc.unread, tc.shelfWhy)
			}
			if got := tc.status.NextUp(); got != tc.nextUp {
				t.Errorf("%q.NextUp() = %t, want %t (%s)",
					tc.status, got, tc.nextUp, tc.shelfWhy)
			}
		})
	}
}

// TestAutomaticWithdrawsEveryChoice: automatic is the one act that hands
// the question back to the position, and it does so for every choice, not
// just the two ADR-0066 shipped with.
func TestAutomaticWithdrawsEveryChoice(t *testing.T) {
	position := func(v float64) *float64 { return &v }
	for _, choice := range []Override{
		OverrideWantToRead, OverrideReading, OverridePaused,
		OverrideDidNotFinish, OverrideFinished, OverrideUnread,
	} {
		t.Run(string(choice), func(t *testing.T) {
			for _, tc := range []struct {
				progress *float64
				want     Status
			}{
				{position(0.5), StatusReading},
				{position(0.98), StatusFinished},
				{position(0), StatusReadyToRead},
				{nil, StatusReadyToRead},
			} {
				if got := Resolve(OverrideAutomatic, tc.progress, false); got != tc.want {
					t.Fatalf("automatic over %s at %v = %q, want %q",
						choice, tc.progress, got, tc.want)
				}
			}
		})
	}
}

// TestPartialCoverageNeverFinishesOnItsOwn: the coverage a position
// carries caps what the position can say, whatever choice sits beside it.
// An explicit finished is a statement about the book rather than about the
// recording, so it still wins (ADR-0066).
func TestPartialCoverageNeverFinishesOnItsOwn(t *testing.T) {
	nearlyDone := 0.998
	for _, choice := range []Override{
		OverrideNone, OverrideAutomatic, OverrideReading, OverrideWantToRead,
		OverridePaused, OverrideDidNotFinish,
	} {
		if got := Resolve(choice, &nearlyDone, true); got == StatusFinished {
			t.Errorf("a partial recording finished the book with choice %q", choice)
		}
	}
	if got := Resolve(OverrideFinished, &nearlyDone, true); got != StatusFinished {
		t.Errorf("an explicit finished lost to partial coverage: %q", got)
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
