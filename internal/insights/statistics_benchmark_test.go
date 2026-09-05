package insights_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/insights"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/postgres"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
)

// Run with:
// go test ./internal/insights -run '^TestStatisticsReadRegression$' -bench '^BenchmarkStatisticsReads$' -benchmem -benchtime=3x -timeout=300s
//
// Set LISEUR_STATS_BENCH_POSTGRES_DSN explicitly to additionally test PostgreSQL.
// It must point to a dedicated disposable database created for this harness:
// the harness migrates and seeds it. Without it, only SQLite runs. This does
// not consult the application's or store suite's DSN. Remove the disposable
// database after use.
//
// The baseline follows summary + works in internal/api/insights.go at
// 2fe346a9ddf218a788129aac6bde991e0d6c6cd6, without HTTP/JSON overhead.
// It does NOT invent a per-work session query: that handler already grouped one
// SessionsInRange result. For E eligible sessions and W historical works, its
// reads are 6 + 2*E + 3*W, plus 2 for a bounded window's lifetime streak.
// Every counted old helper executes one SELECT in both current stores.
//
// StatisticsSnapshot executes 6 SELECTs (timezone/revision, sessions, rollups,
// works, positions, editions), then ceil(candidateIDs/500) archived reads.
// Build executes no reads. Transaction control is excluded from these counts;
// snapshot-reads/op is source-derived, not SQL tracing.
//
// These warm, local, synthetic measurements are not a production latency
// promise. Build also produces calendar/coverage data absent from the compared
// old summary + works outputs. Seeds, equivalence checks and cleanup are untimed.

const statisticsSessionCount = 12_000
const statisticsWorkCount = 24

var statisticsNow = time.Date(2026, 9, 5, 20, 0, 0, 0, time.UTC)

type statisticsFixture struct {
	st       store.Store
	userID   string
	sessions []store.Session
	loc      *time.Location
}

func statisticsBackends() []string {
	backends := []string{"sqlite"}
	if os.Getenv("LISEUR_STATS_BENCH_POSTGRES_DSN") != "" {
		backends = append(backends, "postgres")
	}
	return backends
}

func seedStatistics(tb testing.TB, backend string) statisticsFixture {
	tb.Helper()
	var st store.Store
	var err error
	switch backend {
	case "sqlite":
		st, err = sqlite.Open(filepath.Join(tb.TempDir(), "statistics.db"))
	case "postgres":
		dsn := os.Getenv("LISEUR_STATS_BENCH_POSTGRES_DSN")
		if dsn == "" {
			tb.Fatal("PostgreSQL requires LISEUR_STATS_BENCH_POSTGRES_DSN for a dedicated disposable database")
		}
		st, err = postgres.Open(dsn)
	default:
		tb.Fatalf("unknown benchmark backend %q", backend)
	}
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := st.Close(); err != nil {
			tb.Error(err)
		}
	})
	ctx := tb.Context()
	if err := st.Migrate(ctx); err != nil {
		tb.Fatal(err)
	}
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		tb.Fatal(err)
	}
	hash, err := auth.HashPassword("statistics-benchmark-only")
	if err != nil {
		tb.Fatal(err)
	}
	userID := store.NewID()
	if err := st.CreateUser(ctx, store.User{
		ID: userID, Name: "statistics-" + userID, Argon2Hash: hash,
		Timezone: loc.String(), CreatedAt: statisticsNow,
	}); err != nil {
		tb.Fatal(err)
	}
	works := make([]store.Work, statisticsWorkCount)
	editions := make([]string, statisticsWorkCount)
	var ops []store.Op
	for i := range works {
		works[i] = store.Work{
			ID: fmt.Sprintf("%s-work-%02d", userID, i), UserID: userID,
			Title: fmt.Sprintf("Synthetic work %02d", i), Author: "Benchmark reader",
			CreatedAt: statisticsNow,
		}
		editions[i] = fmt.Sprintf("%064x", i+1)
		pages := int64(256 + i*64)
		edition := store.Edition{
			UserID: userID, WorkID: works[i].ID, SHA256: editions[i],
			PageCount: &pages, MetaJSON: []byte("{}"),
		}
		if err := st.CreateWork(ctx, works[i], &edition, nil); err != nil {
			tb.Fatal(err)
		}
		// Two heads ensure "latest position" is exercised, not just metadata.
		for j := range 2 {
			ops = append(ops, store.Op{
				OpID: fmt.Sprintf("position-%d-%d", i, j), WorkID: works[i].ID,
				EditionSHA: &editions[i], ClientTS: statisticsNow.Add(time.Duration(j-2) * time.Hour),
				Progression: float64(j+1) / 4, Origin: store.OriginNative,
				LocatorJSON: []byte("{}"),
			})
		}
	}
	if _, err := st.AppendOps(ctx, userID, "statistics-device", ops); err != nil {
		tb.Fatal(err)
	}
	f := statisticsFixture{st: st, userID: userID, loc: loc}
	// Eight non-overlapping sittings per day across 1,500 days, including DST
	// and leap years. Each work's 500 sessions share its edition. Raw native
	// sessions without ActiveMs/ReportedPages/source keys keep old and v2
	// semantics comparable; no crossing midnight, inference or legacy rollups.
	first := time.Date(2026, 9, 5, 10, 0, 0, 0, loc).AddDate(0, 0, -1499)
	for i := range statisticsSessionCount {
		work := i % statisticsWorkCount
		start := first.AddDate(0, 0, i/8).Add(time.Duration(i%8) * time.Hour)
		ses := store.Session{
			UserID: userID, SessionID: fmt.Sprintf("session-%05d", i),
			WorkID: works[work].ID, EditionSHA: &editions[work], DeviceID: "statistics-device",
			StartedAt: start, EndedAt: start.Add(time.Duration(20+work%5) * time.Minute),
			StartProg: 0.25, EndProg: 0.25 + 1.0/64, IdleMs: 120_000,
			Origin: store.OriginNative,
		}
		if i%11 == 0 {
			ses.EndProg = 0.125 // Rereading adds time, not negative pages.
		}
		if i%13 == 0 {
			ses.EditionSHA = nil // Unknown pages are not fabricated.
		}
		f.sessions = append(f.sessions, ses)
	}
	for start := 0; start < len(f.sessions); start += 500 {
		if err := st.AppendSessions(ctx, userID, f.sessions[start:min(start+500, len(f.sessions))]); err != nil {
			tb.Fatal(err)
		}
	}
	return f
}

func statisticsWindows(loc *time.Location) []struct {
	name string
	win  insights.Window
} {
	return []struct {
		name string
		win  insights.Window
	}{
		{"all", insights.Window{}},
		{"30d", insights.ParseWindow("", "", "30d", "", loc, statisticsNow)},
		{"historical-year", insights.ParseWindow("2023-07-01", "2024-06-30", "", "", loc, statisticsNow)},
	}
}

func TestStatisticsReadRegression(t *testing.T) {
	for _, backend := range statisticsBackends() {
		t.Run(backend, func(t *testing.T) {
			f := seedStatistics(t, backend)
			for _, window := range statisticsWindows(f.loc) {
				t.Run(window.name, func(t *testing.T) {
					checkStatistics(t, f, window.win, nil)
				})
			}
			// Exercise the archive loader's 500-ID boundary. These live IDs
			// have no tombstones; candidate lookups must not change aggregates.
			plain, err := f.st.StatisticsSnapshot(t.Context(), f.userID, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range []int{500, 501, 1001} {
				t.Run(fmt.Sprintf("candidates-%d", n), func(t *testing.T) {
					got, err := f.st.StatisticsSnapshot(t.Context(), f.userID, statisticsCandidates(f, n))
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, plain) {
						t.Fatal("live candidate IDs changed the snapshot")
					}
				})
			}
		})
	}
}

func BenchmarkStatisticsReads(b *testing.B) {
	for _, backend := range statisticsBackends() {
		b.Run(backend, func(b *testing.B) {
			b.StopTimer()
			f := seedStatistics(b, backend)
			for _, window := range statisticsWindows(f.loc) {
				b.Run(window.name, func(b *testing.B) {
					b.StopTimer()
					reads := checkStatistics(b, f, window.win, nil)
					b.Run("former-summary-works", func(b *testing.B) {
						b.ReportAllocs()
						b.ResetTimer()
						b.StartTimer()
						for i := 0; i < b.N; i++ {
							if _, _, err := formerStatistics(b.Context(), f.st, f.userID, window.win); err != nil {
								b.Fatal(err)
							}
						}
						b.StopTimer()
						b.ReportMetric(float64(reads), "helper-reads/op")
					})
					b.Run("snapshot-build", func(b *testing.B) {
						benchmarkSnapshot(b, f, window.win, nil)
					})
				})
			}
			b.Run("all-candidates-1001/snapshot-build", func(b *testing.B) {
				b.StopTimer()
				ids := statisticsCandidates(f, 1001)
				checkStatistics(b, f, insights.Window{}, ids)
				benchmarkSnapshot(b, f, insights.Window{}, ids)
			})
		})
	}
}

func statisticsCandidates(f statisticsFixture, n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = f.sessions[i].SessionID
	}
	return ids
}

func benchmarkSnapshot(b *testing.B, f statisticsFixture, win insights.Window, ids []string) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		snap, err := f.st.StatisticsSnapshot(b.Context(), f.userID, ids)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := insights.Build(snap, win, statisticsNow); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(6+(len(ids)+499)/500), "snapshot-reads/op")
}

func checkStatistics(tb testing.TB, f statisticsFixture, win insights.Window, ids []string) int {
	tb.Helper()
	old, reads, err := formerStatistics(tb.Context(), f.st, f.userID, win)
	if err != nil {
		tb.Fatal(err)
	}
	snap, err := f.st.StatisticsSnapshot(tb.Context(), f.userID, ids)
	if err != nil {
		tb.Fatal(err)
	}
	if len(snap.Sessions) != statisticsSessionCount || len(snap.Works) != statisticsWorkCount ||
		len(snap.Editions) != statisticsWorkCount || len(snap.Positions) != statisticsWorkCount ||
		len(snap.Rollups) != 0 || len(snap.Archived) != 0 {
		tb.Fatal("fixture is not the expected raw, multi-work history")
	}
	got, err := insights.Build(snap, win, statisticsNow)
	if err != nil {
		tb.Fatal(err)
	}
	var sessions, eligible int
	var active, pages float64
	for _, ses := range f.sessions {
		if ses.ActiveMs != nil || ses.ReportedPages != nil || ses.SourceKey != nil ||
			ses.Origin != store.OriginNative ||
			ses.StartedAt.In(f.loc).Format(insights.DayFormat) != ses.EndedAt.In(f.loc).Format(insights.DayFormat) {
			tb.Fatalf("non-legacy benchmark session %s", ses.SessionID)
		}
		if !win.HoldsSession(ses) {
			continue
		}
		sessions++
		active += ses.EndedAt.Sub(ses.StartedAt).Seconds() - float64(ses.IdleMs)/1000
		if ses.EndProg > ses.StartProg && ses.EditionSHA != nil {
			eligible++
			pages += (ses.EndProg - ses.StartProg) * float64(*snap.Editions[*ses.EditionSHA].PageCount)
		}
	}
	wantReads := 6 + 2*eligible + 3*statisticsWorkCount
	if !win.Unbounded() {
		wantReads += 2
	}
	if reads != wantReads || got.Summary.Sessions != sessions || old.Summary.Sessions != sessions {
		tb.Fatalf("reads=%d want=%d; session totals old=%d new=%d want=%d",
			reads, wantReads, old.Summary.Sessions, got.Summary.Sessions, sessions)
	}
	statisticsNear(tb, "fixture minutes", got.Summary.TotalActiveMinutes, active/60)
	statisticsNear(tb, "fixture pages", got.Summary.TotalPages, pages)
	statisticsNear(tb, "summary minutes", got.Summary.TotalActiveMinutes, old.Summary.TotalActiveMinutes)
	statisticsNear(tb, "summary pages", got.Summary.TotalPages, old.Summary.TotalPages)
	statisticsNear(tb, "summary speed", got.Summary.SpeedProgPerHour, old.Summary.SpeedProgPerHour)
	if got.Summary.StreakDays != old.Summary.StreakDays || got.Summary.StreakDays != 1500 {
		tb.Fatalf("streak old=%d new=%d want=1500", old.Summary.StreakDays, got.Summary.StreakDays)
	}
	if len(got.Works) != len(old.Works) {
		tb.Fatalf("work count old=%d new=%d", len(old.Works), len(got.Works))
	}
	for _, want := range old.Works {
		work, ok := got.ByWork[want.WorkID]
		if !ok || work.Title != want.Title || work.Author != want.Author || work.Sessions != want.Sessions ||
			work.LastReadAt == nil || want.LastReadAt == nil || !work.LastReadAt.Equal(*want.LastReadAt) {
			tb.Fatalf("work metadata differs: old=%+v new=%+v", want, work)
		}
		statisticsNear(tb, "work minutes", work.TotalActiveMinutes, want.TotalActiveMinutes)
		statisticsNear(tb, "work pages", work.TotalPages, want.TotalPages)
		statisticsNear(tb, "work progression", work.CurrentProgression, want.CurrentProgression)
		if (work.ETASeconds == nil) != (want.ETASeconds == nil) {
			tb.Fatalf("ETA availability differs for %s", want.WorkID)
		}
		if work.ETASeconds != nil {
			statisticsNear(tb, "work ETA", *work.ETASeconds, *want.ETASeconds)
		}
	}
	return reads
}

func statisticsNear(tb testing.TB, field string, got, want float64) {
	tb.Helper()
	if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
		tb.Fatalf("%s: got %.12g want %.12g", field, got, want)
	}
}

// formerStatistics preserves the old successful read path for summary + works.
// checkStatistics enforces a legacy-compatible fixture outside timing. For
// these same-day sessions, the old splitDays result is simply the ending day.
// Errors are surfaced, not timed as the old metadata/position fallback.
func formerStatistics(ctx context.Context, st store.Store, userID string, win insights.Window) (insights.Result, int, error) {
	var out insights.Result
	reads := 0
	location := func() (*time.Location, error) {
		reads++
		u, err := st.UserByID(ctx, userID)
		if err != nil {
			return nil, err
		}
		return time.LoadLocation(u.Timezone)
	}
	pageCount := func(ses store.Session) (float64, error) {
		delta := ses.EndProg - ses.StartProg
		if delta <= 0 || ses.EditionSHA == nil {
			return 0, nil
		}
		reads++
		ed, err := st.EditionBySHA(ctx, userID, *ses.EditionSHA)
		if err != nil || ed.PageCount == nil {
			return 0, err
		}
		return delta * float64(*ed.PageCount), nil
	}
	loc, err := location()
	if err != nil {
		return out, reads, err
	}
	from, to := win.SessionBounds(statisticsNow, loc)
	reads++
	sessions, err := st.SessionsInRange(ctx, userID, from, to)
	if err != nil {
		return out, reads, err
	}
	days := make(map[string]bool)
	var active, delta float64
	for _, ses := range sessions {
		if !win.HoldsSession(ses) {
			continue
		}
		pages, err := pageCount(ses)
		if err != nil {
			return out, reads, err
		}
		out.Summary.Sessions++
		out.Summary.TotalPages += pages
		active += insights.ActiveSeconds(ses)
		delta += math.Max(0, ses.EndProg-ses.StartProg)
		days[ses.EndedAt.In(loc).Format(insights.DayFormat)] = true
	}
	first, last := win.DayBounds(statisticsNow, loc)
	reads++
	rollups, err := st.RollupsInRange(ctx, userID, first, last)
	if err != nil {
		return out, reads, err
	}
	if len(rollups) != 0 {
		return out, reads, fmt.Errorf("legacy comparison requires raw sessions, not rollups")
	}
	if !win.Unbounded() {
		from := statisticsNow.AddDate(0, 0, -insights.StreakLookbackDays)
		reads++
		all, err := st.SessionsInRange(ctx, userID, from, statisticsNow)
		if err != nil {
			return out, reads, err
		}
		for _, ses := range all {
			days[ses.EndedAt.In(loc).Format(insights.DayFormat)] = true
		}
		reads++
		rollups, err := st.RollupsInRange(ctx, userID,
			from.In(loc).Format(insights.DayFormat), statisticsNow.In(loc).Format(insights.DayFormat))
		if err != nil {
			return out, reads, err
		}
		if len(rollups) != 0 {
			return out, reads, fmt.Errorf("legacy streak comparison requires raw sessions")
		}
	}
	out.Summary.TotalActiveMinutes = active / 60
	if active > 0 {
		out.Summary.SpeedProgPerHour = delta / active * 3600
	}
	out.Summary.StreakDays = insights.StreakDays(days, loc, statisticsNow)

	reads++
	workIDs, err := st.WorkIDsWithInsights(ctx, userID)
	if err != nil {
		return out, reads, err
	}
	loc, err = location()
	if err != nil {
		return out, reads, err
	}
	from, to = win.SessionBounds(statisticsNow, loc)
	reads++
	sessions, err = st.SessionsInRange(ctx, userID, from, to)
	if err != nil {
		return out, reads, err
	}
	byWork := make(map[string][]store.Session)
	for _, ses := range sessions {
		byWork[ses.WorkID] = append(byWork[ses.WorkID], ses)
	}
	for _, id := range workIDs {
		work := insights.Work{WorkID: id}
		var active, delta float64
		for _, ses := range byWork[id] {
			if !win.HoldsSession(ses) {
				continue
			}
			pages, err := pageCount(ses)
			if err != nil {
				return out, reads, err
			}
			work.Sessions++
			active += insights.ActiveSeconds(ses)
			work.TotalPages += pages
			delta += math.Max(0, ses.EndProg-ses.StartProg)
			if work.LastReadAt == nil || ses.EndedAt.After(*work.LastReadAt) {
				at := ses.EndedAt
				work.LastReadAt = &at
			}
		}
		reads++
		rollups, err := st.RollupsForWork(ctx, userID, id)
		if err != nil {
			return out, reads, err
		}
		if len(rollups) != 0 {
			return out, reads, fmt.Errorf("legacy work comparison requires raw sessions")
		}
		reads++
		ops, err := st.Positions(ctx, userID, id, 1)
		if err != nil {
			return out, reads, err
		}
		if len(ops) > 0 {
			work.CurrentProgression = ops[0].Progression
			if active > 0 && delta > 0 && work.CurrentProgression < 1 {
				eta := (1 - work.CurrentProgression) / (delta / active)
				work.ETASeconds = &eta
			}
		}
		reads++
		meta, err := st.WorkByID(ctx, userID, id)
		if err != nil {
			return out, reads, err
		}
		work.Title, work.Author = meta.Title, meta.Author
		work.TotalActiveMinutes = active / 60
		if work.Sessions > 0 || work.TotalActiveMinutes > 0 {
			out.Works = append(out.Works, work)
		}
	}
	sort.Slice(out.Works, func(i, j int) bool {
		return out.Works[i].TotalActiveMinutes > out.Works[j].TotalActiveMinutes
	})
	return out, reads, nil
}
