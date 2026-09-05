package webui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/insights"
	"github.com/chmouel/liseur-sync/internal/store"
)

// seedWork puts a work with one edition behind the test user.
func seedWork(t *testing.T, st store.Store, id, title string) store.Edition {
	t.Helper()
	w := store.Work{ID: id, UserID: "u1", Title: title, CreatedAt: time.Now()}
	ed := store.Edition{UserID: "u1", SHA256: id + "-edition", WorkID: id}
	if err := st.CreateWork(t.Context(), w, &ed, nil); err != nil {
		t.Fatal(err)
	}
	return ed
}

// seedSession records one sitting, given in the server's own timezone.
func seedSession(t *testing.T, st store.Store, id string, ed store.Edition, start, end time.Time) {
	t.Helper()
	err := st.AppendSessions(t.Context(), "u1", []store.Session{{
		SessionID: id, WorkID: ed.WorkID, EditionSHA: &ed.SHA256, DeviceID: "reader",
		StartedAt: start, EndedAt: end, StartProg: 0.1, EndProg: 0.2,
		Origin: store.OriginNative,
	}})
	if err != nil {
		t.Fatal(err)
	}
}

// TestDashboardSpanPicker is the picker doing what a picker is for: the
// page comes back describing the span that was asked for, and offering
// the others.
func TestDashboardSpanPicker(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	for _, tc := range []struct {
		span  insights.Span
		wants string
	}{
		{insights.Span7Days, "This past week"},
		{insights.Span90Days, "Day by day"},
		{insights.SpanAllTime, "Day by day"},
	} {
		_, body := page(t, ts, cookie, "/ui?span="+string(tc.span))
		if !strings.Contains(body, tc.wants) {
			t.Errorf("span %s: heading %q missing", tc.span, tc.wants)
		}
		if !strings.Contains(body, `value="`+string(tc.span)+`" selected`) {
			t.Errorf("span %s: not selected in the picker", tc.span)
		}
		if !strings.Contains(body, tc.span.Label()) {
			t.Errorf("span %s: label %q missing", tc.span, tc.span.Label())
		}
	}
}

func TestDashboardSessionUsesDeviceName(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	ed := seedWork(t, st, "named-work", "A named book")
	seedSession(t, st, "named-session", ed, time.Now().Add(-time.Hour), time.Now())
	if err := st.CreateToken(t.Context(), store.Token{
		ID: "named-device-token", UserID: "u1", DeviceID: "reader",
		Name: "Boox Palma", Scopes: store.ScopeSet{store.ScopeSync},
		SHA256: "named-device-hash", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	_, body := page(t, ts, cookie, "/ui?span=7d")
	want := `<span class="device-name" title="Boox Palma">Boox Palma</span>`
	if !strings.Contains(body, want) {
		t.Fatalf("dashboard did not show the device name %q: %s", want, body)
	}
}

// TestDashboardSpanFallsBackToDefault is a span nobody offers. An
// unknown token in a URL must not be an error page or an empty chart;
// it is the default, which is what a reader who typed nothing gets.
func TestDashboardSpanFallsBackToDefault(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	_, body := page(t, ts, cookie, "/ui?span=fortnight")
	if !strings.Contains(body, `value="`+string(insights.DefaultSpan)+`" selected`) {
		t.Fatal("an unknown span did not fall back to the default")
	}
}

// TestDashboardSpanRemembered is the point of writing it to the cookie:
// a reader who chose ninety days and comes back to a bare /ui gets
// ninety days, not thirty.
func TestDashboardSpanRemembered(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	req, _ := http.NewRequest("GET", ts.URL+"/ui?span=90d", nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	var prefs *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == prefsCookie {
			prefs = c
		}
	}
	if prefs == nil {
		t.Fatal("choosing a span did not write the preference cookie")
	}
	if !strings.Contains(prefs.Value, "span-90d") {
		t.Fatalf("preference cookie does not carry the span: %q", prefs.Value)
	}

	// And a bare /ui carrying it back comes up on ninety days.
	req, _ = http.NewRequest("GET", ts.URL+"/ui", nil)
	req.AddCookie(cookie)
	req.AddCookie(prefs)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	if !strings.Contains(body, `value="90d" selected`) {
		t.Fatal("a bare /ui did not remember the chosen span")
	}
}

// TestDashboardSpanQueryBeatsCookie: a link to a span is a link to that
// span, whatever the browser last chose.
func TestDashboardSpanQueryBeatsCookie(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	req, _ := http.NewRequest("GET", ts.URL+"/ui?span=7d", nil)
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: prefsCookie, Value: "dark.grid.series.span-365d"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if body := readAll(t, resp); !strings.Contains(body, `value="7d" selected`) {
		t.Fatal("the query parameter lost to the cookie")
	}
}

// TestDashboardPlacesSessionOnTheDayItEnded is the disagreement this
// set out to remove. A sitting that runs past midnight belongs to one
// day, and the API, the app and this page have to name the same one.
func TestDashboardPlacesSessionOnTheDayItEnded(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	ed := seedWork(t, st, "late-work", "Read past midnight")

	// Yesterday at half eleven until ten past midnight today.
	end := time.Now().Truncate(24 * time.Hour).Add(10 * time.Minute)
	seedSession(t, st, "midnight", ed, end.Add(-40*time.Minute), end)

	_, body := page(t, ts, cookie, "/ui?span=7d")
	today := end.Format(insights.DayFormat)
	yesterday := end.AddDate(0, 0, -1).Format(insights.DayFormat)
	if !strings.Contains(body, today+" — 40 min") {
		t.Errorf("the sitting is not on the day it ended (%s)", today)
	}
	if !strings.Contains(body, yesterday+" — 0 min") {
		t.Errorf("the day it started (%s) should be empty", yesterday)
	}
}

// TestDashboardChartShape: short spans are bars, long ones are the
// calendar. Thirty-one days of squares is unreadable and a year of bars
// is a picket fence.
func TestDashboardChartShape(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	if _, body := page(t, ts, cookie, "/ui?span=30d"); !strings.Contains(body, "daybars") ||
		strings.Contains(body, "heatweek") {
		t.Error("thirty days should be bars")
	}
	if _, body := page(t, ts, cookie, "/ui?span=365d"); !strings.Contains(body, "heatweek") ||
		strings.Contains(body, "daybars") {
		t.Error("a year should be the calendar")
	}
}

// TestDashboardHeatmapAlignsWeekdays: every column is a Monday-to-Sunday
// week, which is the only reason a square's row means anything.
func TestDashboardHeatmapAlignsWeekdays(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	_, body := page(t, ts, cookie, "/ui?span=365d")
	weeks := strings.Count(body, `class="heatweek"`)
	if weeks < 52 {
		t.Fatalf("a year is %d columns, want at least 52", weeks)
	}
	// A column is seven cells whether they carry a day or not.
	cells := strings.Count(body, `class="cell`)
	if cells != weeks*7 {
		t.Fatalf("%d cells over %d columns; the grid is ragged", cells, weeks)
	}
	if !strings.Contains(body, `class="heatmonth"`) {
		t.Fatal("the calendar has no month labels")
	}
}

// TestDashboardShowsEmptyDays: a fortnight with two gaps in it is the
// information. A chart that quietly skipped them would read as a run.
func TestDashboardShowsEmptyDays(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	ed := seedWork(t, st, "gap-work", "One sitting only")

	end := time.Now().Add(-2 * time.Hour)
	seedSession(t, st, "one", ed, end.Add(-30*time.Minute), end)

	_, body := page(t, ts, cookie, "/ui?span=7d")
	if bars := strings.Count(body, `class="daybar"`); bars != 7 {
		t.Fatalf("%d bars for a seven-day span, want 7", bars)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestWorkCountsRollupsOnce is the bug this page had all along: the
// rolled-up totals were read inside the loop over the sessions still
// held in full, so a work with three current sittings counted its
// history three times. The longer a book had been read, the more wrong
// its page was about it.
func TestWorkCountsRollupsOnce(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	ed := seedWork(t, st, "long-read", "A book with a past")

	// Three sittings of ten minutes each still held in full.
	base := time.Now().Add(-3 * time.Hour)
	for i := range 3 {
		at := base.Add(time.Duration(i) * time.Hour)
		seedSession(t, st, "recent-"+string(rune('a'+i)), ed, at, at.Add(10*time.Minute))
	}

	// And an hour of reading from before the compaction cut-off.
	err := st.ApplyRollups(t.Context(), "u1", []store.SessionRollup{{
		UserID: "u1", WorkID: ed.WorkID, Day: "2024-01-01",
		ActiveSeconds: 3600, Pages: 40, ProgDelta: 0.2, SessionCount: 2,
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, body := page(t, ts, cookie, "/ui/works/"+ed.WorkID)
	// Thirty minutes raw plus sixty rolled up, counted once.
	if !strings.Contains(body, ">90<") {
		t.Error("minutes are not 30 raw + 60 rolled up, counted once")
	}
	// Three sittings plus the two the rollup stands for.
	if !strings.Contains(body, ">5<") {
		t.Error("sessions are not 3 raw + 2 rolled up, counted once")
	}
}

func TestWorkSessionsAreNewestFirst(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	ed := seedWork(t, st, "ordered", "Two sittings")
	old := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)
	seedSession(t, st, "old", ed, old, old.Add(10*time.Minute))
	seedSession(t, st, "new", ed, newer, newer.Add(10*time.Minute))
	_, body := page(t, ts, cookie, "/ui/works/"+ed.WorkID)
	oldIndex, newIndex := strings.Index(body, "Aug 1 09:00"), strings.Index(body, "Aug 1 10:00")
	if oldIndex < 0 || newIndex < 0 || newIndex >= oldIndex {
		t.Fatal("the recent session log did not put the newest sitting first")
	}
}

func TestWorkTotalsIncludeAllSessions(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	ed := seedWork(t, st, "many-sittings", "Many short sittings")
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	sessions := make([]store.Session, 10_001)
	for i := range sessions {
		at := base.Add(time.Duration(i) * time.Minute)
		sessions[i] = store.Session{
			SessionID: fmt.Sprintf("s-%d", i), WorkID: ed.WorkID, DeviceID: "reader",
			StartedAt: at, EndedAt: at.Add(time.Minute), Origin: store.OriginNative,
		}
	}
	if err := st.AppendSessions(t.Context(), "u1", sessions); err != nil {
		t.Fatal(err)
	}
	_, body := page(t, ts, cookie, "/ui/works/"+ed.WorkID)
	if !strings.Contains(body, ">10001<") {
		t.Fatal("the work total was truncated to the session log's limit")
	}
}

type failedStatsStore struct {
	store.Store
}

func (*failedStatsStore) StatisticsSnapshot(context.Context, string, []string) (store.StatsSnapshot, error) {
	return store.StatsSnapshot{}, errors.New("statistics read failed")
}

func TestDashboardAndWorkRefusePartialStatistics(t *testing.T) {
	s := &Server{St: &failedStatsStore{}}
	for _, path := range []string{"/ui", "/ui/works/w"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.SetPathValue("id", "w")
		w := httptest.NewRecorder()
		if path == "/ui" {
			s.handleDashboard(w, req, store.AuthSession{}, &store.User{ID: "u1"})
		} else {
			s.handleWork(w, req, store.AuthSession{}, &store.User{ID: "u1"})
		}
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s: got %d, want an explicit failure", path, w.Code)
		}
	}
}

type dashboardLinksStore struct {
	store.Store
	lookups []string
	err     error
}

func (s *dashboardLinksStore) WorkBookIDs(_ context.Context, _, workID string) ([]string, error) {
	s.lookups = append(s.lookups, workID)
	return nil, s.err
}

func TestDashboardResolvesOnlyDisplayedWorks(t *testing.T) {
	works := make([]store.WorkSummary, 100)
	progression := 0.5
	for i := range works {
		at := time.Now().Add(-time.Duration(i) * time.Hour)
		works[i] = store.WorkSummary{
			Work:        store.Work{ID: fmt.Sprintf("w-%d", i)},
			Progression: &progression, LastActive: &at,
		}
	}
	st := &dashboardLinksStore{}
	s := &Server{St: st}
	rows, err := s.linkReadingWorks(httptest.NewRequest(http.MethodGet, "/ui", nil), "u1",
		continueReading(works, nil, time.UTC))
	if err != nil || len(rows) != continueReadingLimit || len(st.lookups) != continueReadingLimit {
		t.Fatalf("rows=%d lookups=%d err=%v", len(rows), len(st.lookups), err)
	}
	for i, id := range st.lookups {
		if id != fmt.Sprintf("w-%d", i) {
			t.Fatalf("resolved an undisplayed work: %s", id)
		}
	}
}

func TestDashboardLinkErrorsPropagate(t *testing.T) {
	failed := errors.New("store unavailable")
	s := &Server{St: &dashboardLinksStore{err: failed}}
	if _, err := s.linkReadingWorks(httptest.NewRequest(http.MethodGet, "/ui", nil), "u1",
		[]WorkRow{{ID: "w"}}); !errors.Is(err, failed) {
		t.Fatalf("got %v, want store failure", err)
	}
}

func TestDashboardStreakRequiresPositiveActivity(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	snapshot := store.StatsSnapshot{Timezone: "UTC", Sessions: []store.Session{{
		StartedAt: now.Add(-time.Hour), EndedAt: now, IdleMs: int64(time.Hour / time.Millisecond),
	}}}
	got, err := insights.Build(snapshot, insights.Window{}, now)
	if err != nil || got.Summary.StreakDays != 0 {
		t.Fatalf("idle-only sitting created streak %d: %v", got.Summary.StreakDays, err)
	}
}

func TestDashboardUsesReportedPagesWithoutEdition(t *testing.T) {
	pages := 1.0
	if got, err := insights.Pages(store.Session{ReportedPages: &pages}, nil); err != nil || got != 1 {
		t.Fatalf("reported page count lost: %f: %v", got, err)
	}
}

// TestDashboardExcludesSessionEndingPastTheSpan: the store answers with
// an overlap, so a sitting that started inside the span and ran out the
// far end of it comes back too. The window counts by the day a sitting
// ended, and so must this page, or the totals include reading that has
// not finished happening.
func TestDashboardExcludesSessionEndingPastTheSpan(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	ed := seedWork(t, st, "overrun", "Still being read")

	// Starts before midnight tonight and ends after it, so it belongs
	// to tomorrow and to no span that stops at today.
	midnight := time.Now().Truncate(24*time.Hour).AddDate(0, 0, 1)
	seedSession(t, st, "overrun-1", ed, midnight.Add(-20*time.Minute), midnight.Add(30*time.Minute))
	// And an ordinary hour today, so the page has something to say.
	today := time.Now().Add(-2 * time.Hour)
	seedSession(t, st, "today-1", ed, today, today.Add(time.Hour))

	_, body := page(t, ts, cookie, "/ui?span=7d")
	if !strings.Contains(body, `<span class="num">60</span><span class="lbl">minutes</span>`) {
		t.Error("the sitting running past the span was counted in the total")
	}
	if !strings.Contains(body, `<span class="num">1</span><span class="lbl">sessions</span>`) {
		t.Error("the sitting running past the span was counted as a session")
	}
}

// TestDashboardCountsPagesFromSessions: the page card was fed only by
// rollups, so a reader whose history had not been compacted yet saw no
// pages at all.
func TestDashboardCountsPagesFromSessions(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)

	w := store.Work{ID: "paged", UserID: "u1", Title: "A book with pages", CreatedAt: time.Now()}
	count := int64(400)
	ed := store.Edition{UserID: "u1", SHA256: "paged-edition", WorkID: w.ID, PageCount: &count}
	if err := st.CreateWork(t.Context(), w, &ed, nil); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-2 * time.Hour)
	err := st.AppendSessions(t.Context(), "u1", []store.Session{{
		SessionID: "paged-1", WorkID: w.ID, EditionSHA: &ed.SHA256, DeviceID: "reader",
		StartedAt: at, EndedAt: at.Add(time.Hour), StartProg: 0.10, EndProg: 0.20,
		Origin: store.OriginNative,
	}})
	if err != nil {
		t.Fatal(err)
	}

	_, body := page(t, ts, cookie, "/ui?span=7d")
	// A tenth of four hundred pages.
	if !strings.Contains(body, `<span class="num">40</span><span class="lbl">pages</span>`) {
		t.Error("pages read in sittings still held in full are not counted")
	}
}

// TestHeatmapLabelsTheColumnHoldingTheFirst: a month almost never
// starts on the day a column does. Captioning the column after the one
// that holds the first of the month puts every label up to six days
// late, drifting off the thing it names.
func TestHeatmapLabelsTheColumnHoldingTheFirst(t *testing.T) {
	// A Wednesday, so the column runs Mon 29 Sep to Sun 5 Oct and the
	// first of October falls in the middle of it.
	days := []DayCell{}
	for d := time.Date(2025, 9, 29, 0, 0, 0, 0, time.UTC); d.Before(time.Date(2025, 10, 13, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		days = append(days, DayCell{Date: d.Format(insights.DayFormat)})
	}
	grid := layOutHeatmap(days)
	if len(grid.Weeks) != 2 {
		t.Fatalf("%d columns, want 2", len(grid.Weeks))
	}
	if grid.Weeks[0].Month != "Sep" {
		t.Errorf("first column is %q, want Sep", grid.Weeks[0].Month)
	}
	if grid.Weeks[1].Month != "Oct" {
		t.Errorf("the column holding 1 October is %q, want Oct", grid.Weeks[1].Month)
	}
}

// TestDaySeriesStartsAtRealReading: a sitting that was all pause leaves
// a day behind without leaving any reading behind, and an all-time
// chart should not open on it.
func TestDaySeriesStartsAtRealReading(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	win := insights.SpanAllTime.Window(now, time.UTC)
	cells := daySeries(win, map[string]float64{
		"2026-01-01": 0,
		"2026-03-08": 45,
	}, now, time.UTC)
	if len(cells) == 0 || cells[0].Date != "2026-03-08" {
		t.Fatalf("the chart opens on %v, want 2026-03-08", cells[0].Date)
	}
}
