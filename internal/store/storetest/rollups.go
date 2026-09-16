package storetest

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// RollupsRejectStaleEditionPageCount needs a backend-specific metadata write:
// the public store API currently registers editions without updating them.
func RollupsRejectStaleEditionPageCount(t *testing.T, s store.Store, updatePages func(string, string, *int64) error) {
	ctx := t.Context()
	user := MkUser(t, s, "stale-edition")
	work := MkWork(t, s, user, "stale-edition-work", "stale-edition-sha")
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		old, fresh *int64
	}{
		{"changed-page-count", Ptr(int64(100)), Ptr(int64(200))},
		{"previously-unknown-page-count", nil, Ptr(int64(200))},
		{"now-unknown-page-count", Ptr(int64(100)), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := updatePages(user.ID, "stale-edition-sha", tc.old); err != nil {
				t.Fatal(err)
			}
			sessions := []store.Session{
				{
					SessionID: tc.name + "-native", WorkID: work.ID, EditionSHA: Ptr("stale-edition-sha"),
					DeviceID: "reader", StartedAt: now, EndedAt: now.Add(10 * time.Minute),
					StartProg: 0, EndProg: 0.25, ActiveMs: Ptr(int64(300000)), Origin: store.OriginNative,
				},
				{
					SessionID: tc.name + "-inferred", WorkID: work.ID, EditionSHA: Ptr("stale-edition-sha"),
					DeviceID: "reader", StartedAt: now, EndedAt: now.Add(10 * time.Minute),
					StartProg: 0.25, EndProg: 0.375, Origin: store.OriginInferred,
				},
				{
					SessionID: tc.name + "-reported", WorkID: work.ID, EditionSHA: Ptr("stale-edition-sha"),
					DeviceID: "reader", StartedAt: now, EndedAt: now.Add(time.Minute),
					StartProg: 0.375, EndProg: 0.625, ReportedPages: Ptr(12.5), Origin: store.OriginNative,
				},
			}
			if err := s.AppendSessions(ctx, user.ID, sessions); err != nil {
				t.Fatal(err)
			}
			ids := []string{sessions[0].SessionID, sessions[1].SessionID, sessions[2].SessionID}
			before, err := s.StatisticsSnapshot(ctx, user.ID, ids)
			if err != nil {
				t.Fatal(err)
			}
			edition := before.Editions["stale-edition-sha"]
			pages := 12.5
			if edition.PageCount != nil {
				pages += 0.375 * float64(*edition.PageCount)
			}
			rollup := store.SessionRollup{
				UserID: user.ID, WorkID: work.ID, Day: now.Format("2006-01-02"), Timezone: user.Timezone,
				AttributionVersion: 2, ActiveSeconds: 960, Pages: pages, ProgDelta: 0.625,
				SessionCount: 3, MeasuredActiveSeconds: 360, MeasuredProgDelta: 0.5,
			}
			exactActiveMs := int64(960000)
			rollup.ComparisonActiveMs = &exactActiveMs
			if err := updatePages(user.ID, edition.SHA256, tc.fresh); err != nil {
				t.Fatal(err)
			}
			if err := s.ApplyRollups(ctx, user.ID, []store.SessionRollup{rollup}, sessions); !errors.Is(err, store.ErrConflict) {
				t.Fatalf("stale edition must refuse compaction, got %v", err)
			}
			after, err := s.StatisticsSnapshot(ctx, user.ID, ids)
			if err != nil {
				t.Fatal(err)
			}
			if len(after.Sessions) != 3 || !reflect.DeepEqual(after.Rollups, before.Rollups) || len(after.Archived) != 0 {
				t.Fatalf("refused compaction changed history: %+v", after)
			}

			rollup.Pages = 12.5
			if tc.fresh != nil {
				rollup.Pages += 0.375 * float64(*tc.fresh)
			}
			if err := s.ApplyRollups(ctx, user.ID, []store.SessionRollup{rollup}, sessions); err != nil {
				t.Fatalf("fresh metadata retry: %v", err)
			}
			after, err = s.StatisticsSnapshot(ctx, user.ID, ids)
			if err != nil {
				t.Fatal(err)
			}
			if len(after.Sessions) != 0 || len(after.Archived) != 3 || len(after.Rollups) != len(before.Rollups)+1 {
				t.Fatalf("successful retry lost archive proofs: %+v", after)
			}
			var proofs []store.ArchivedSession
			for _, ses := range sessions {
				proof := after.Archived[ses.SessionID]
				if !proof.Present || proof.Fingerprint != store.SessionFingerprint(ses) {
					t.Fatalf("invalid archive proof for %s: %+v", ses.SessionID, proof)
				}
				proofs = append(proofs, proof)
			}
			if err := store.ValidateRollupContributions([]store.SessionRollup{rollup}, proofs); err != nil {
				t.Fatalf("fresh bucket differs from archived contributions: %v", err)
			}
			got := after.Rollups[len(after.Rollups)-1]
			if !reflect.DeepEqual(got, rollup) {
				t.Fatalf("fresh bucket: got %+v, want %+v", got, rollup)
			}
			now = now.Add(24 * time.Hour)
		})
	}
}

func testRollupsCountSessionsThatNeedNoEdition(t *testing.T, open OpenFunc) {
	ctx := t.Context()
	s := open(t)
	user := MkUser(t, s, "no-edition-needed")
	work := MkWork(t, s, user, "no-edition-work", "no-edition-sha")
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	day := now.In(mustLoad(t, user.Timezone)).Format("2006-01-02")

	// A sitting that reports its own page count and one that made no
	// progress. Neither multiplies a page count by anything, so the
	// rollup must not make its edition's metadata a precondition.
	reported := store.Session{
		SessionID: "reported", WorkID: work.ID, EditionSHA: Ptr("no-edition-sha"),
		DeviceID: "reader", StartedAt: now, EndedAt: now.Add(time.Minute),
		StartProg: 0, EndProg: 0.25, ReportedPages: Ptr(12.5), Origin: store.OriginNative,
	}
	idle := store.Session{
		SessionID: "idle", WorkID: work.ID, EditionSHA: Ptr("no-edition-sha"),
		DeviceID: "reader", StartedAt: now, EndedAt: now.Add(time.Minute),
		StartProg: 0.5, EndProg: 0.5, Origin: store.OriginNative,
	}
	if err := s.AppendSessions(ctx, user.ID, []store.Session{reported, idle}); err != nil {
		t.Fatal(err)
	}
	if shas := store.EditionSHAsNeedingPages([]store.Session{reported, idle}); len(shas) != 0 {
		t.Fatalf("rollup would read editions it never consults: %v", shas)
	}
	active := 60.0
	rollup := store.SessionRollup{
		UserID: user.ID, WorkID: work.ID, Day: day, Timezone: user.Timezone,
		AttributionVersion: 2, ActiveSeconds: 2 * active, Pages: 12.5,
		ProgDelta: 0.25, SessionCount: 2,
		MeasuredActiveSeconds: 2 * active, MeasuredProgDelta: 0.25,
	}
	if err := s.ApplyRollups(ctx, user.ID, []store.SessionRollup{rollup},
		[]store.Session{reported, idle}); err != nil {
		t.Fatalf("sittings whose pages never read an edition were refused: %v", err)
	}
	after, err := s.StatisticsSnapshot(ctx, user.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Rollups) != 1 || after.Rollups[0].Pages != 12.5 ||
		after.Rollups[0].SessionCount != 2 {
		t.Fatalf("reported pages did not survive the rollup: %+v", after.Rollups)
	}
}

// testEditionSHAsNeedingPages pins the predicate that keeps the rollup's
// edition preload in step with what actually reads an edition. A preload
// that asks for more than this refuses sittings that would have counted
// fine; one that asks for less falls back to a per-session query.
func testEditionSHAsNeedingPages(t *testing.T) {
	sha := Ptr("sha")
	for _, tc := range []struct {
		name string
		ses  store.Session
		want bool
	}{
		{"reads-its-edition", store.Session{EditionSHA: sha, StartProg: 0, EndProg: 0.25}, true},
		{"reports-its-own-pages", store.Session{EditionSHA: sha, StartProg: 0, EndProg: 0.25,
			ReportedPages: Ptr(12.5)}, false},
		{"no-progression", store.Session{EditionSHA: sha, StartProg: 0.5, EndProg: 0.5}, false},
		{"backwards", store.Session{EditionSHA: sha, StartProg: 0.5, EndProg: 0.25}, false},
		{"names-no-edition", store.Session{StartProg: 0, EndProg: 0.25}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := store.SessionNeedsEditionPages(tc.ses); got != tc.want {
				t.Fatalf("SessionNeedsEditionPages = %v, want %v", got, tc.want)
			}
		})
	}
	mixed := []store.Session{
		{EditionSHA: Ptr("a"), StartProg: 0, EndProg: 0.25},
		{EditionSHA: Ptr("b"), StartProg: 0, EndProg: 0.25, ReportedPages: Ptr(1.0)},
		{EditionSHA: Ptr("a"), StartProg: 0, EndProg: 0.5},
	}
	if got := store.EditionSHAsNeedingPages(mixed); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("EditionSHAsNeedingPages = %v, want [a]", got)
	}
}

// testRollupsRejectForeignAccountTimezone covers the guard that reads the
// account's zone inside the transaction: a zone changed after the batch
// was built defers the batch instead of filing it under the wrong day.
func testRollupsRejectForeignAccountTimezone(t *testing.T, open OpenFunc) {
	ctx := t.Context()
	s := open(t)
	user := MkUser(t, s, "tz-race")
	work := MkWork(t, s, user, "tz-race-work", "tz-race-sha")
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	ses := store.Session{
		SessionID: "tz-race-ses", WorkID: work.ID, EditionSHA: Ptr("tz-race-sha"),
		DeviceID: "reader", StartedAt: now, EndedAt: now.Add(10 * time.Minute),
		StartProg: 0, EndProg: 0.25, ActiveMs: Ptr(int64(300000)), Origin: store.OriginNative,
	}
	if err := s.AppendSessions(ctx, user.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	// Built against the account's zone, then the reader moves.
	rollup := store.SessionRollup{
		UserID: user.ID, WorkID: work.ID,
		Day:      now.In(mustLoad(t, user.Timezone)).Format("2006-01-02"),
		Timezone: user.Timezone, AttributionVersion: 2,
		ActiveSeconds: 300, Pages: 0.25 * 462, ProgDelta: 0.25, SessionCount: 1,
		MeasuredActiveSeconds: 300, MeasuredProgDelta: 0.25,
	}
	if err := s.UpdateUserSettings(ctx, user.ID, store.UserSettings{
		Timezone: "America/New_York", KosyncEnabled: user.KosyncEnabled,
		KopluginEnabled: user.KopluginEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyRollups(ctx, user.ID, []store.SessionRollup{rollup},
		[]store.Session{ses}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a rollup in a zone the account no longer keeps must defer, got %v", err)
	}
	after, err := s.StatisticsSnapshot(ctx, user.ID, []string{ses.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Sessions) != 1 || len(after.Rollups) != 0 || len(after.Archived) != 0 {
		t.Fatalf("a deferred batch changed history: %+v", after)
	}
}

// testRollupsAccumulateIntoAnExistingBucket covers a day that is rolled
// up more than once: a device uploading older sittings for a day already
// archived, or a work/day straddling a rollup batch. The second apply
// takes the upsert's DO UPDATE branch, which SQLite used to fail
// outright, because it overrode the stats-revision trigger's own
// OR IGNORE with the firing statement's conflict policy. A batch that
// cannot be applied is never deleted, so those sittings deferred every
// hour, forever.
func testRollupsAccumulateIntoAnExistingBucket(t *testing.T, open OpenFunc) {
	ctx := t.Context()
	s := open(t)
	user := MkUser(t, s, "rebucket")
	work := MkWork(t, s, user, "rebucket-work", "rebucket-sha")
	day := "2026-09-04"
	at := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)

	apply := func(id string, minutes int) error {
		ses := store.Session{
			SessionID: id, WorkID: work.ID, EditionSHA: Ptr("rebucket-sha"),
			DeviceID: "reader", StartedAt: at, EndedAt: at.Add(time.Duration(minutes) * time.Minute),
			StartProg: 0, EndProg: 0.1, ActiveMs: Ptr(int64(minutes) * 60000),
			Origin: store.OriginNative,
		}
		if err := s.AppendSessions(ctx, user.ID, []store.Session{ses}); err != nil {
			t.Fatal(err)
		}
		rollup := store.SessionRollup{
			UserID: user.ID, WorkID: work.ID, Day: day,
			Timezone: user.Timezone, AttributionVersion: 2,
			ActiveSeconds: float64(minutes) * 60, Pages: 0.1 * 462, ProgDelta: 0.1, SessionCount: 1,
			MeasuredActiveSeconds: float64(minutes) * 60, MeasuredProgDelta: 0.1,
		}
		return s.ApplyRollups(ctx, user.ID, []store.SessionRollup{rollup}, []store.Session{ses})
	}
	if err := apply("rebucket-first", 5); err != nil {
		t.Fatal(err)
	}
	if err := apply("rebucket-second", 7); err != nil {
		t.Fatalf("a second sitting for an archived day must accumulate, got %v", err)
	}
	got, err := s.RollupsForWork(ctx, user.ID, work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want one accumulated bucket, got %+v", got)
	}
	if got[0].SessionCount != 2 || got[0].ActiveSeconds != 720 {
		t.Fatalf("the second sitting did not accumulate: %+v", got[0])
	}
	left, err := s.SessionsEndedBefore(ctx, user.ID, at.Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("an applied batch must be deleted, %d left", len(left))
	}
}

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func testV2RollupsRejectMismatchedContributions(t *testing.T, open OpenFunc) {
	first, second := int64(math.MaxInt64-1), int64(2)
	overflowing := []store.SessionRollup{{
		WorkID: "work", Day: "2026-09-04", Timezone: "UTC",
		AttributionVersion: 2, SessionCount: 2,
	}}
	if err := store.PrepareRollupContributions(overflowing, []store.ArchivedSession{
		{WorkID: "work", Day: "2026-09-04", Timezone: "UTC", ComparisonActiveMs: &first},
		{WorkID: "work", Day: "2026-09-04", Timezone: "UTC", ComparisonActiveMs: &second},
	}); err != nil {
		t.Fatalf("overflowing optional evidence blocked compaction: %v", err)
	}
	if overflowing[0].ComparisonActiveMs != nil {
		t.Fatalf("overflowing exact evidence was retained: %+v", overflowing[0])
	}

	s := open(t)
	ctx := t.Context()
	user := MkUser(t, s, "rollup-contributions")
	work := MkWork(t, s, user, "contribution-work", "contribution-sha")
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	ses := store.Session{
		SessionID: "contribution-session", WorkID: work.ID, DeviceID: "reader",
		StartedAt: now, EndedAt: now.Add(10001 * time.Microsecond), IdleMs: 1,
		StartProg: 0, EndProg: 0.25, ReportedPages: Ptr(12.5), Origin: store.OriginNative,
	}
	if err := s.AppendSessions(ctx, user.ID, []store.Session{ses}); err != nil {
		t.Fatal(err)
	}
	active := ses.EndedAt.Sub(ses.StartedAt).Seconds() - float64(ses.IdleMs)/1000
	rollup := store.SessionRollup{
		UserID: user.ID, WorkID: work.ID, Day: "2026-09-04", Timezone: user.Timezone, AttributionVersion: 2,
		ActiveSeconds: active, Pages: 12.5, ProgDelta: 0.25, SessionCount: 1,
		MeasuredActiveSeconds: active, MeasuredProgDelta: 0.25,
	}
	for _, tc := range []struct {
		name   string
		change func(*store.SessionRollup)
	}{
		{"active-seconds", func(r *store.SessionRollup) { r.ActiveSeconds++ }},
		{"pages", func(r *store.SessionRollup) { r.Pages++ }},
		{"progression", func(r *store.SessionRollup) { r.ProgDelta++ }},
		{"session-count", func(r *store.SessionRollup) { r.SessionCount++ }},
		{"measured-active-seconds", func(r *store.SessionRollup) { r.MeasuredActiveSeconds++ }},
		{"measured-progression", func(r *store.SessionRollup) { r.MeasuredProgDelta++ }},
		{"work", func(r *store.SessionRollup) { r.WorkID = "other-work" }},
		{"day", func(r *store.SessionRollup) { r.Day = "2026-09-03" }},
		{"nonfinite-pages", func(r *store.SessionRollup) { r.Pages = math.Inf(1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := rollup
			tc.change(&changed)
			if err := s.ApplyRollups(ctx, user.ID, []store.SessionRollup{changed}, []store.Session{ses}); !errors.Is(err, store.ErrConflict) {
				t.Fatalf("mismatched bucket must conflict, got %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		rollups []store.SessionRollup
	}{
		{"duplicate-bucket", []store.SessionRollup{rollup, rollup}},
		{"unbacked-bucket", []store.SessionRollup{rollup, {
			UserID: user.ID, WorkID: work.ID, Day: "2026-09-05", Timezone: user.Timezone,
			AttributionVersion: 2, SessionCount: 1,
		}}},
		{"unbacked-timezone", []store.SessionRollup{rollup, {
			// A third zone: the base rollup now carries the account's own
			// (Europe/Paris), so reusing that here would make this case a
			// duplicate bucket and it would stop testing timezones at all.
			UserID: user.ID, WorkID: work.ID, Day: rollup.Day, Timezone: "America/New_York",
			AttributionVersion: 2, SessionCount: 1,
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.ApplyRollups(ctx, user.ID, tc.rollups, []store.Session{ses}); !errors.Is(err, store.ErrConflict) {
				t.Fatalf("unbacked bucket must conflict, got %v", err)
			}
		})
	}
	before, err := s.StatisticsSnapshot(ctx, user.ID, []string{ses.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Sessions) != 1 || len(before.Rollups) != 0 || len(before.Archived) != 0 {
		t.Fatalf("refused compaction changed history: %+v", before)
	}
	if err := s.ApplyRollups(ctx, user.ID, []store.SessionRollup{rollup}, []store.Session{ses}); err != nil {
		t.Fatalf("matching contributions must compact: %v", err)
	}
	after, err := s.StatisticsSnapshot(ctx, user.ID, []string{ses.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Rollups) != 1 || after.Rollups[0].ComparisonActiveMs == nil ||
		*after.Rollups[0].ComparisonActiveMs != 9 {
		t.Fatalf("rollup lost per-session millisecond evidence: %+v", after.Rollups)
	}
	proof := after.Archived[ses.SessionID]
	if proof.ComparisonActiveMs == nil || *proof.ComparisonActiveMs != 9 {
		t.Fatalf("archive proof lost per-session millisecond evidence: %+v", proof)
	}
}
