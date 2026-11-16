package webui

import (
	"testing"

	"github.com/chmouel/liseur-sync/internal/reading"
)

// TestNextUpVolumeReadsTheResolvedStatus: an explicit choice decides what
// "next up" means (ADR-0066). A volume marked read before it was ever
// opened has no position at all, and must not be offered as the next
// thing to start; a volume put back on the pile keeps its saved place and
// is offered again.
func TestNextUpVolumeReadsTheResolvedStatus(t *testing.T) {
	half := 0.4
	for _, tc := range []struct {
		name   string
		vols   []SeriesVolume
		wanted string
	}{
		{
			name: "finished without a position is not next up",
			vols: []SeriesVolume{
				{BookID: "one", Status: reading.StatusFinished, CanRead: true},
				{BookID: "two", Status: reading.StatusReadyToRead, CanRead: true},
			},
			wanted: "two",
		},
		{
			name: "a volume put back on the pile keeps its place",
			vols: []SeriesVolume{
				{BookID: "one", Status: reading.StatusUnread, Progression: &half, CanRead: true},
				{BookID: "two", Status: reading.StatusReadyToRead, CanRead: true},
			},
			wanted: "one",
		},
		{
			name: "a volume being read still beats an unstarted one",
			vols: []SeriesVolume{
				{BookID: "one", Status: reading.StatusReadyToRead, CanRead: true},
				{BookID: "two", Status: reading.StatusReading, Progression: &half, CanRead: true},
			},
			wanted: "two",
		},
		{
			name: "a series read to the end offers nothing",
			vols: []SeriesVolume{
				{BookID: "one", Status: reading.StatusFinished, CanRead: true},
				{BookID: "two", Status: reading.StatusFinished, Progression: &half, CanRead: true},
			},
			wanted: "",
		},
		{
			// ADR-0067: a book waiting to be read is unstarted and on
			// the unread shelf, so it is the natural thing to offer.
			name: "a volume waiting to be read is next up",
			vols: []SeriesVolume{
				{BookID: "one", Status: reading.StatusFinished, Progression: &half, CanRead: true},
				{BookID: "two", Status: reading.StatusWantToRead, CanRead: true},
			},
			wanted: "two",
		},
		{
			// Explicit reading before any position: started, and it
			// beats a volume merely waiting its turn.
			name: "explicit reading with no position is next up",
			vols: []SeriesVolume{
				{BookID: "one", Status: reading.StatusWantToRead, CanRead: true},
				{BookID: "two", Status: reading.StatusReading, CanRead: true},
			},
			wanted: "two",
		},
		{
			// Put down for now is a deliberate "not now": offering it
			// back would be arguing with the reader.
			name: "a paused volume is not next up",
			vols: []SeriesVolume{
				{BookID: "one", Status: reading.StatusPaused, Progression: &half, CanRead: true},
				{BookID: "two", Status: reading.StatusFinished, CanRead: true},
			},
			wanted: "",
		},
		{
			name: "a volume the reader gave up on is not next up",
			vols: []SeriesVolume{
				{BookID: "one", Status: reading.StatusDidNotFinish, Progression: &half, CanRead: true},
				{BookID: "two", Status: reading.StatusPaused, CanRead: true},
			},
			wanted: "",
		},
		{
			// A pending decision is still a decision: a paused book does
			// not get offered just because a later volume is unread.
			name: "a paused volume does not hide the one waiting behind it",
			vols: []SeriesVolume{
				{BookID: "one", Status: reading.StatusPaused, Progression: &half, CanRead: true},
				{BookID: "two", Status: reading.StatusUnread, CanRead: true},
			},
			wanted: "two",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nextUpVolume(tc.vols)
			if tc.wanted == "" {
				if got != nil {
					t.Fatalf("next up = %q, want none", got.BookID)
				}
				return
			}
			if got == nil || got.BookID != tc.wanted {
				t.Fatalf("next up = %+v, want %q", got, tc.wanted)
			}
		})
	}
}
