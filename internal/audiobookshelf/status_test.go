package audiobookshelf

import (
	"fmt"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// markRead appends an explicit finished act at the work's current
// position, the way a phone's "mark read" does (ADR-0066).
func (f *absFixture) markRead(t *testing.T, l store.AudiobookLink, p float64, at time.Time) {
	t.Helper()
	_, err := f.st.AppendOps(t.Context(), f.user.ID, "phone", []store.Op{{
		OpID:          fmt.Sprintf("read:%v:%v", p, at),
		WorkID:        l.WorkID,
		Progression:   p,
		ClientTS:      at,
		Origin:        store.OriginNative,
		Kind:          store.OpKindStatus,
		ReadingStatus: store.ReadingStatusFinished,
	}})
	if err != nil {
		t.Fatal(err)
	}
}

// TestExplicitFinishedAtLowProgressReachesAudiobookshelf: a book marked
// read by hand at 40 percent finishes its linked full audiobook. The
// status act is the newest thing that happened to the work, so the next
// pass must exchange again instead of treating the unchanged position as
// an agreed echo.
func TestExplicitFinishedAtLowProgressReachesAudiobookshelf(t *testing.T) {
	titles := []string{"Chapter 1", "Chapter 2", "Chapter 3"}
	f := newABSFixture(t, titles, titles)
	l := f.link(t)
	now := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	f.read(t, l, .4, now)
	f.remote(now.Add(-time.Second), .3, false)
	if rep := f.pass(t); rep.Pushed != 1 || rep.Pulled != 0 {
		t.Fatalf("first pass: %+v", rep)
	}
	f.markRead(t, l, .4, now.Add(time.Second))
	before := f.patches
	if rep := f.pass(t); rep.Pushed != 1 || rep.Pulled != 0 {
		t.Fatalf("a finished act did not reach the audiobook: %+v", rep)
	}
	f.mu.Lock()
	pushed := f.progress
	f.mu.Unlock()
	if pushed == nil || !pushed.IsFinished {
		t.Fatalf("pushed progress = %+v, want finished", pushed)
	}
	if f.patches <= before {
		t.Fatal("no PATCH was sent to Audiobookshelf")
	}
	// And the agreed mark is recognised on the next pass rather than
	// pushed again.
	patches := f.patches
	if rep := f.pass(t); rep.Pushed != 0 || f.patches != patches {
		t.Fatalf("the finished mark was not recognised as agreed: %+v patches=%d->%d", rep, patches, f.patches)
	}
}
