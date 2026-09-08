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
		{insights.Span7Days, "Day by day"},
		{insights.Span90Days, "Week by week"},
		{insights.SpanAllTime, "Year by year"},
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
	today := end.Format("2 Jan 2006")
	yesterday := end.AddDate(0, 0, -1).Format("2 Jan 2006")
	if !strings.Contains(body, "40 min during "+today) {
		t.Errorf("the sitting is not on the day it ended (%s)", today)
	}
	if !strings.Contains(body, "Nothing read during "+yesterday) {
		t.Errorf("the day it started (%s) should be empty", yesterday)
	}
}

// TestDashboardChartShape: the bars follow the span. A day is worth a
// bar while the days still fit; past that they are grouped, because a
// year of daily bars is a picket fence.
func TestDashboardChartShape(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)
	// Something to draw: an empty span is an empty card, whatever the
	// bucket would have been.
	ed := seedWork(t, st, "shape-work", "Something to chart")
	at := time.Now().Add(-2 * time.Hour)
	seedSession(t, st, "shape-1", ed, at, at.Add(30*time.Minute))

	for span, bucket := range map[string]string{
		"7d":         "bars-day",
		"30d":        "bars-day",
		"this_month": "bars-day",
		"90d":        "bars-week",
		"365d":       "bars-month",
		"all":        "bars-year",
	} {
		_, body := page(t, ts, cookie, "/ui?span="+span)
		if !strings.Contains(body, bucket) {
			t.Errorf("span %s: chart is not %s", span, bucket)
		}
		if strings.Contains(body, "heatweek") {
			t.Errorf("span %s: the bars view drew the calendar", span)
		}
	}
}

// TestDashboardChartTabs: the calendar is the other view of the same
// card, offered only where a grid of it would say something, and
// reachable without a line of script.
func TestDashboardChartTabs(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	_, short := page(t, ts, cookie, "/ui?span=7d")
	if strings.Contains(short, "charttabs") {
		t.Error("a week is too short to be worth a calendar grid")
	}

	_, long := page(t, ts, cookie, "/ui?span=365d")
	if !strings.Contains(long, "charttabs") {
		t.Error("a year should offer the calendar")
	}
	if !strings.Contains(long, "chart=chart-calendar") {
		t.Error("the calendar tab does not link to the calendar")
	}
	if !strings.Contains(long, "span=365d") {
		t.Error("the tab drops the span, so switching view switches span")
	}

	_, cal := page(t, ts, cookie, "/ui?span=365d&chart=chart-calendar")
	if !strings.Contains(cal, "heatweek") {
		t.Error("asking for the calendar did not draw it")
	}
	if strings.Contains(cal, "daybars") {
		t.Error("both views are on screen at once")
	}
	// The heading names the picture, and the calendar draws days even
	// where the bars would have been months.
	if !strings.Contains(cal, "Day by day") || strings.Contains(cal, "Month by month") {
		t.Error("the calendar is headed as if it were the bars")
	}
	if !strings.Contains(long, "Month by month") {
		t.Error("a year of bars should be headed Month by month")
	}

	// A span too short for a grid gets the bars whatever was last
	// chosen, rather than an empty card.
	_, week := page(t, ts, cookie, "/ui?span=7d")
	if !strings.Contains(week, "daybars") || strings.Contains(week, "heatweek") {
		t.Error("a week should fall back to the bars")
	}
}

// TestDashboardHeatmapAlignsWeekdays: every column is a Monday-to-Sunday
// week, which is the only reason a square's row means anything.
func TestDashboardHeatmapAlignsWeekdays(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	_, body := page(t, ts, cookie, "/ui?span=365d&chart=chart-calendar")
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
	if !strings.Contains(body, ">1 h 30 min<") {
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
	if !strings.Contains(body, `<p class="headline-value">1 h</p>`) {
		t.Error("the sitting running past the span was counted in the total")
	}
	if !strings.Contains(body, `<span class="num">1</span><span class="lbl">sittings</span>`) {
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

func TestDashboardHeroSpotlight(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)

	ed := seedWork(t, st, "hero-work", "The Star Rover")
	end := time.Now().Add(-10 * time.Minute)
	if _, err := st.AppendOps(t.Context(), "u1", "reader", []store.Op{{
		OpID: "op-hero", WorkID: ed.WorkID, ClientTS: end, Progression: 0.42,
		Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}

	_, body := page(t, ts, cookie, "/ui")
	if !strings.Contains(body, `class="dash-hero-card"`) {
		t.Error("dashboard does not contain dash-hero-card")
	}
	if !strings.Contains(body, "The Star Rover") {
		t.Error("hero card does not contain work title")
	}
	if !strings.Contains(body, "Currently Reading") {
		t.Error("hero card missing Currently Reading badge")
	}
	if !strings.Contains(body, "42%") {
		t.Error("hero card missing progress 42%")
	}
}

func TestDashboardSecondaryShelf(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)

	ed1 := seedWork(t, st, "first-work", "First Book")
	ed2 := seedWork(t, st, "second-work", "Second Book")

	t1 := time.Now().Add(-10 * time.Minute)
	t2 := time.Now().Add(-2 * time.Hour)

	if _, err := st.AppendOps(t.Context(), "u1", "reader", []store.Op{
		{
			OpID: "op-1", WorkID: ed1.WorkID, ClientTS: t1, Progression: 0.65,
			Origin: store.OriginNative,
		},
		{
			OpID: "op-2", WorkID: ed2.WorkID, ClientTS: t2, Progression: 0.20,
			Origin: store.OriginNative,
		},
	}); err != nil {
		t.Fatal(err)
	}

	_, body := page(t, ts, cookie, "/ui")
	if !strings.Contains(body, `class="dash-hero-card"`) {
		t.Error("missing hero card")
	}
	if !strings.Contains(body, "First Book") {
		t.Error("missing first book in hero")
	}
	if !strings.Contains(body, `class="card dash-shelf-card"`) {
		t.Error("missing secondary shelf card")
	}
	if !strings.Contains(body, "More books in progress") {
		t.Error("missing More books in progress header")
	}
	if !strings.Contains(body, "Second Book") {
		t.Error("missing second book on shelf")
	}
}

func TestDashboardEmptyReadingDesk(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)

	_, body := page(t, ts, cookie, "/ui")
	if !strings.Contains(body, `class="card dash-empty-desk"`) {
		t.Error("missing dash-empty-desk when no books in progress")
	}
	if !strings.Contains(body, "Your reading desk is clear") {
		t.Error("missing clear desk message")
	}
}

// TestDashboardThisMonthSpan is the span the app has and the web did
// not: it starts on the first of the month, so a sitting from last
// month is outside it while today's is inside.
func TestDashboardThisMonthSpan(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)

	now := time.Now()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	if now.Sub(first) < 3*time.Hour {
		t.Skip("run within hours of the first: nothing this month to separate")
	}
	thisMonth := seedWork(t, st, "month-work", "Read this month")
	lastMonth := seedWork(t, st, "before-work", "Read last month")
	seedSession(t, st, "month-1", thisMonth, now.Add(-2*time.Hour), now.Add(-time.Hour))
	before := first.Add(-26 * time.Hour)
	seedSession(t, st, "before-1", lastMonth, before, before.Add(time.Hour))

	_, body := page(t, ts, cookie, "/ui?span=this_month")
	if !strings.Contains(body, `value="this_month" selected`) {
		t.Error("this_month not selected in the picker")
	}
	if !strings.Contains(body, "This month") {
		t.Error("This month label missing")
	}
	if !strings.Contains(body, "Read this month") {
		t.Error("a sitting from this month is missing from the by-book list")
	}
	if strings.Contains(body, "Read last month") {
		t.Error("a sitting from before the first is inside this month")
	}
	if !strings.Contains(body, "bars-day") {
		t.Error("this_month should bucket by day")
	}
}

// TestDashboardCountsBooksReadAndFinished pins what the two counts
// mean: read from is works with a sitting in the span, finished is
// those of them now at or past the finished mark.
func TestDashboardCountsBooksReadAndFinished(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)

	now := time.Now()
	done := seedWork(t, st, "done-work", "A book I finished")
	going := seedWork(t, st, "going-work", "A book I am still on")
	seedSession(t, st, "going-1", going, now.Add(-3*time.Hour), now.Add(-2*time.Hour))
	err := st.AppendSessions(t.Context(), "u1", []store.Session{{
		SessionID: "done-1", WorkID: done.WorkID, EditionSHA: &done.SHA256,
		DeviceID: "reader", StartedAt: now.Add(-2 * time.Hour), EndedAt: now.Add(-time.Hour),
		StartProg: 0.8, EndProg: 1, Origin: store.OriginNative,
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Finished is a position, not a session: the sitting says how far it
	// went, the op log says where the reader now stands.
	if _, err := st.AppendOps(t.Context(), "u1", "reader", []store.Op{{
		OpID: "op-done", WorkID: done.WorkID, ClientTS: now.Add(-time.Hour),
		Progression: 1, Origin: store.OriginNative,
	}, {
		OpID: "op-going", WorkID: going.WorkID, ClientTS: now.Add(-2 * time.Hour),
		Progression: 0.3, Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}

	_, body := page(t, ts, cookie, "/ui?span=7d")
	for _, want := range []string{
		`<span class="num">2</span><span class="lbl">books read from</span>`,
		`<span class="num">1</span><span class="lbl">books finished</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !strings.Contains(body, "Finished</span>") {
		t.Error("the finished book is not marked finished in the by-book list")
	}
}

// dashboardCookies fetches a page and hands back what it asked the
// browser to remember alongside the body.
func dashboardCookies(
	t *testing.T, ts *httptest.Server, jar []*http.Cookie, path string,
) ([]*http.Cookie, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	for _, c := range jar {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.Cookies(), string(b)
}

// TestDashboardRemembersBothSpanAndChart is the bug a link carrying two
// preferences used to have: each was read from the request's cookie and
// written back separately, so the second write undid the first and the
// next bare dashboard came back on the old span.
func TestDashboardRemembersBothSpanAndChart(t *testing.T) {
	ts, _ := testServer(t)
	login := loginCookie(t, ts)

	// Somewhere to come back from.
	page(t, ts, login, "/ui?span=30d&chart=chart-bars")

	set, _ := dashboardCookies(t, ts, []*http.Cookie{login},
		"/ui?span=365d&chart=chart-calendar")
	var saved *http.Cookie
	for _, c := range set {
		if c.Name == "liseur_ui" {
			saved = c
		}
	}
	if saved == nil {
		t.Fatal("the dashboard did not write the preference cookie")
	}
	if !strings.Contains(saved.Value, "365d") || !strings.Contains(saved.Value, chartCalendar) {
		t.Fatalf("one of the two preferences was dropped: %q", saved.Value)
	}

	_, back := dashboardCookies(t, ts, []*http.Cookie{login, saved}, "/ui")
	if !strings.Contains(back, `value="365d" selected`) {
		t.Error("coming back, the span was not the one that was asked for")
	}
	if !strings.Contains(back, "heatweek") {
		t.Error("coming back, the calendar was not the view that was asked for")
	}
}

// TestWorkStartedOnTheDayReadingEnded keeps the book page agreeing with
// itself over time. A sitting that runs past midnight is archived under
// the day it ended, so the day it started must not be the one the page
// calls "started": that date would move the moment the sitting aged out
// of the raw log.
func TestWorkStartedOnTheDayReadingEnded(t *testing.T) {
	ts, st := testServer(t)
	cookie := loginCookie(t, ts)

	ed := seedWork(t, st, "midnight-work", "Read past midnight")
	end := time.Date(2026, time.September, 2, 0, 30, 0, 0, time.UTC)
	seedSession(t, st, "midnight-1", ed, end.Add(-time.Hour), end)

	_, body := page(t, ts, cookie, "/ui/works/midnight-work")
	if !strings.Contains(body, "2 Sep 2026") {
		t.Error("the page does not date the reading by the day it ended")
	}
	if strings.Contains(body, "1 Sep 2026") {
		t.Error("the page dates the reading by the day it began")
	}
}
