package webui

import (
	"io"
	"net/http"
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
