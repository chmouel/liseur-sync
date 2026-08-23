package webui

import (
	"context"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/insights"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Laying out the dashboard's two charts.
//
// Both are computed here rather than in the template because both need
// arithmetic — a week boundary, a running month, a bar height as a
// percentage of the busiest day — and a template that can do arithmetic
// is a template nobody can test.

// chartHeading names the chart for the span it is drawing. "This past
// week" is only true when it is one; thirty bars under that heading is
// the page telling the reader something they can see is false.
func chartHeading(span insights.Span) string {
	if span == insights.Span7Days {
		return "This past week"
	}
	return "Day by day"
}

// barCaptionEvery is how often a bar is captioned once they cannot all
// be. Seven puts the labels on the same weekday all the way along, so
// the axis reads as a series of weeks rather than as an arbitrary tick.
const barCaptionEvery = 7

type barChart struct {
	Bars  []barCell
	Label string
}

type barCell struct {
	Class   string
	Caption string
	Title   string
}

// layOutBars turns a run of days into a row of bars.
//
// Heights are relative to the busiest day in the span, because the
// question a bar chart answers is "which days were the big ones" and an
// absolute scale would flatten a quiet fortnight into nothing. The
// colour stays absolute (cellClass), so a green bar means the same
// amount of reading here as it does on the heatmap.
func layOutBars(days []DayCell) barChart {
	chart := barChart{Bars: make([]barCell, 0, len(days))}
	busiest := 0.0
	for _, d := range days {
		if d.Minutes > busiest {
			busiest = d.Minutes
		}
	}
	if busiest <= 0 {
		busiest = 1
	}
	last := len(days) - 1
	// A week fits its own letters. Longer than that and captioning
	// every bar turns the axis into a smear, so it goes to one a week,
	// counted back from the end — that way today always carries its
	// own label, whatever the span happens to divide into.
	everyBar := len(days) <= barCaptionEvery
	for i, d := range days {
		caption := ""
		if everyBar || (last-i)%barCaptionEvery == 0 {
			caption = dayCaption(d.Date)
		}
		chart.Bars = append(chart.Bars, barCell{
			Class:   cellClass(d.Minutes) + " " + barHeightClass(d.Minutes/busiest),
			Caption: caption,
			Title:   d.Date + " — " + f0(d.Minutes) + " min",
		})
	}
	chart.Label = chartLabel(days)
	return chart
}

// barHeightClass is how tall a bar is drawn, as one of the stylesheet's
// five-percent steps. A class rather than an inline height because the
// CSP has no `unsafe-inline` and this UI has answered that question
// once already, for progress bars (pctClass, ADR-0011).
//
// A day with any reading on it gets the smallest visible step even when
// the arithmetic rounds it to nothing: a bar of zero height reads as a
// day with no reading at all, which is a different fact.
func barHeightClass(fraction float64) string {
	if fraction > 0 && fraction < 0.05 {
		fraction = 0.05
	}
	return pctClass(fraction)
}

// dayCaption is the short weekday a bar is labelled with.
func dayCaption(day string) string {
	d, err := time.Parse(insights.DayFormat, day)
	if err != nil {
		return ""
	}
	return d.Format("Mon")
}

// daysInWeek is how many rows the heatmap has, and how long a column is.
const daysInWeek = 7

type heatGrid struct {
	Weeks []heatWeek
	Label string
}

type heatWeek struct {
	// Month captions this column when its first real day opens a month
	// the column before it did not, which puts the name where the month
	// actually starts rather than at a fixed interval that drifts off it.
	Month string
	Days  []heatCell
}

type heatCell struct {
	Date    string
	Minutes float64
	Class   string
	Title   string
	Blank   bool
}

// layOutHeatmap arranges days into columns of a week, one weekday a row.
//
// The leading blanks are what make every row one weekday, the way a
// wall calendar reads. Without them the grid is a spiral: a square's
// row would mean nothing, and the shape a reader is here to see — the
// weekends, the fortnight away — would not be there to see.
//
// The month label lives inside its own column rather than in a row of
// its own, so the two cannot drift apart however the columns are sized.
//
// Weeks start on Monday. The server does not know the reader's locale,
// and guessing it from a timezone would be worse than picking one;
// Monday is what the ISO week is, and what the rest of this UI assumes.
func layOutHeatmap(days []DayCell) heatGrid {
	grid := heatGrid{Label: chartLabel(days)}
	if len(days) == 0 {
		return grid
	}
	first, err := time.Parse(insights.DayFormat, days[0].Date)
	if err != nil {
		return grid
	}

	cells := make([]heatCell, 0, len(days)+2*daysInWeek)
	leading := (int(first.Weekday()) + 6) % daysInWeek // Monday is 0
	for range leading {
		cells = append(cells, heatCell{Blank: true})
	}
	for _, d := range days {
		cells = append(cells, heatCell{
			Date:    d.Date,
			Minutes: d.Minutes,
			Class:   cellClass(d.Minutes),
			Title:   d.Date + " — " + f0(d.Minutes) + " min",
		})
	}
	// Pad the last column out to a full week: a ragged column would let
	// its squares fall on the wrong weekday rows.
	for len(cells)%daysInWeek != 0 {
		cells = append(cells, heatCell{Blank: true})
	}

	var running time.Month
	for i := 0; i < len(cells); i += daysInWeek {
		week := heatWeek{Days: cells[i : i+daysInWeek]}
		if opens, name := opensMonth(week.Days, running); name != "" {
			running, week.Month = opens, name
		}
		grid.Weeks = append(grid.Weeks, week)
	}
	return grid
}

// opensMonth reports the month a column opens, and its name, when that
// is not the month the column before it was already in.
//
// Every real day in the column is looked at, not only the first. A
// month rarely starts on the day a column does, and captioning the
// column after the one holding the first of the month would put every
// label up to six days late — a label that has drifted off the thing
// it names is worse than no label.
func opensMonth(week []heatCell, running time.Month) (time.Month, string) {
	for _, c := range week {
		if c.Blank {
			continue
		}
		d, err := time.Parse(insights.DayFormat, c.Date)
		if err != nil {
			return running, ""
		}
		if d.Month() != running {
			return d.Month(), d.Format("Jan")
		}
	}
	return running, ""
}

// chartLabel is what a screen reader is told instead of the picture.
func chartLabel(days []DayCell) string {
	if len(days) == 0 {
		return "no reading recorded"
	}
	total := 0.0
	active := 0
	for _, d := range days {
		total += d.Minutes
		if d.Minutes > 0 {
			active++
		}
	}
	return fmt.Sprintf("daily reading minutes, %s to %s: %s minutes over %d days with reading",
		days[0].Date, days[len(days)-1].Date, f0(total), active)
}

// pageCounter turns a sitting's progress into a number of pages.
//
// The same arithmetic the API and the per-work page do: how far through
// the book the reader got, times how many pages the edition has. It is
// an approximation and says so, but a page count is what a reader
// recognises and a progression fraction is not.
//
// The editions are remembered for the length of one request because a
// dashboard is a few books read many times over: without the map, a
// year of reading is a year of lookups for the same handful of rows.
type pageCounter struct {
	st       store.Store
	editions map[string]*int64
}

func newPageCounter(st store.Store) *pageCounter {
	return &pageCounter{st: st, editions: map[string]*int64{}}
}

func (p *pageCounter) of(ctx context.Context, ses store.Session) float64 {
	delta := ses.EndProg - ses.StartProg
	if delta <= 0 || ses.EditionSHA == nil {
		return 0
	}
	count, seen := p.editions[*ses.EditionSHA]
	if !seen {
		if ed, err := p.st.EditionBySHA(ctx, ses.UserID, *ses.EditionSHA); err == nil {
			count = ed.PageCount
		}
		p.editions[*ses.EditionSHA] = count
	}
	if count == nil {
		return 0
	}
	return delta * float64(*count)
}
