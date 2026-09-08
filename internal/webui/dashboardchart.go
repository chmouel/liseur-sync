package webui

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"time"

	"github.com/chmouel/liseur-sync/internal/insights"
)

// Laying out the dashboard's two charts.
//
// Both are computed here rather than in the template because both need
// arithmetic — a week boundary, a running month, a bar height as a
// percentage of the busiest day — and a template that can do arithmetic
// is a template nobody can test.

// chartHeading names the chart for the span it is drawing.
// chartHeading names the picture on screen, not the span. The calendar
// draws one cell per day whatever the span is, so it stays "Day by day"
// where the bars would have widened to months.
func chartHeading(span insights.Span, chart string) string {
	if chart == chartCalendar {
		return insights.BucketDay.Heading()
	}
	return span.Bucket().Heading()
}

// spanCaption is what the headline total says underneath itself.
//
// The wording is the Android app's, minus its "on every device"
// qualifier: there, that distinguishes the phone's own reading from the
// server's. Here every figure on the page is already every device, so
// saying it would be answering a question nobody asked.
func spanCaption(span insights.Span) string {
	if span == insights.SpanAllTime {
		return "In total"
	}
	return span.Label()
}

// paceValue is progression per hour as a percentage, which is the one
// pace figure that means anything across books of different lengths.
func paceValue(perHour float64) string {
	return strconv.Itoa(int(math.Round(perHour*100))) + "%/h"
}

// chartHref is a link to the other view of the activity card, carrying
// the span so that switching view does not silently switch span.
func chartHref(span insights.Span, chart string) string {
	return "?span=" + url.QueryEscape(string(span)) + "&chart=" + url.QueryEscape(chart)
}

// barCaptionEvery is how often a daily bar is captioned once they cannot
// all be. Seven puts the labels on the same weekday all the way along,
// so the axis reads as a series of weeks rather than as an arbitrary
// tick. Wider buckets are captioned one for one: there are few enough
// of them, and a month with no name on it is a month nobody can find.
const barCaptionEvery = 7

type barChart struct {
	Bars  []barCell
	Label string
}

type barCell struct {
	Class   string
	Caption string
	Amount  string
	Title   string
}

// bucketSeries groups a run of days into the buckets one bar covers.
//
// Every bucket the span touches is emitted, including the empty ones. A
// chart that skipped a silent February would draw January next to March
// and read as an unbroken run, which is the opposite of what happened.
type bucketCell struct {
	// Start is the first day the bucket holds, which is what names it.
	Start   time.Time
	Minutes float64
	// Days is how many of the span's days fell in this bucket, so a
	// clipped first or last bucket can say so.
	Days int
}

func bucketSeries(days []DayCell, bucket insights.Bucket) []bucketCell {
	if bucket == insights.BucketDay {
		cells := make([]bucketCell, 0, len(days))
		for _, d := range days {
			day, err := time.Parse(insights.DayFormat, d.Date)
			if err != nil {
				continue
			}
			cells = append(cells, bucketCell{Start: day, Minutes: d.Minutes, Days: 1})
		}
		return cells
	}
	var cells []bucketCell
	for _, d := range days {
		day, err := time.Parse(insights.DayFormat, d.Date)
		if err != nil {
			continue
		}
		start := bucketStart(day, bucket)
		if n := len(cells); n > 0 && cells[n-1].Start.Equal(start) {
			cells[n-1].Minutes += d.Minutes
			cells[n-1].Days++
			continue
		}
		cells = append(cells, bucketCell{Start: start, Minutes: d.Minutes, Days: 1})
	}
	return cells
}

// bucketStart is the first day of the bucket a day belongs to.
//
// Weeks start on Monday, as the heatmap's columns do. The server does
// not know the reader's locale, and guessing it from a timezone would
// be worse than picking one; Monday is what the ISO week is, and what
// the rest of this UI already assumes.
func bucketStart(day time.Time, bucket insights.Bucket) time.Time {
	switch bucket {
	case insights.BucketWeek:
		back := (int(day.Weekday()) + 6) % daysInWeek
		return day.AddDate(0, 0, -back)
	case insights.BucketMonth:
		return time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, day.Location())
	case insights.BucketYear:
		return time.Date(day.Year(), 1, 1, 0, 0, 0, 0, day.Location())
	default:
		return day
	}
}

// bucketCaption is what one bar is labelled with.
func bucketCaption(start time.Time, bucket insights.Bucket) string {
	switch bucket {
	case insights.BucketWeek:
		return start.Format("2 Jan")
	case insights.BucketMonth:
		return start.Format("Jan")
	case insights.BucketYear:
		return start.Format("2006")
	default:
		return start.Format("Mon")
	}
}

// bucketTitle is the whole answer, for a tooltip and a screen reader:
// which stretch of time, and how much reading in it.
func bucketTitle(c bucketCell, bucket insights.Bucket) string {
	when := c.Start.Format("2 Jan 2006")
	switch bucket {
	case insights.BucketWeek:
		when = "week of " + c.Start.Format("2 Jan 2006")
	case insights.BucketMonth:
		when = c.Start.Format("January 2006")
	case insights.BucketYear:
		when = c.Start.Format("2006")
	}
	if c.Minutes <= 0 {
		return "Nothing read during " + when
	}
	return readingMinutes(c.Minutes) + " during " + when
}

// layOutBars turns a run of days into a row of bars, one per bucket.
//
// Heights are relative to the busiest bucket in the span, because the
// question a bar chart answers is "which were the big ones" and an
// absolute scale would flatten a quiet fortnight into nothing. The
// colour stays absolute (cellClass), so a green bar means the same
// amount of reading here as it does on the heatmap.
func layOutBars(days []DayCell, bucket insights.Bucket) barChart {
	cells := bucketSeries(days, bucket)
	chart := barChart{Bars: make([]barCell, 0, len(cells))}
	busiest := 0.0
	for _, c := range cells {
		if c.Minutes > busiest {
			busiest = c.Minutes
		}
	}
	if busiest <= 0 {
		busiest = 1
	}
	last := len(cells) - 1
	// A week fits its own letters. Longer than that and captioning
	// every daily bar turns the axis into a smear, so it goes to one a
	// week, counted back from the end — that way today always carries
	// its own label, whatever the span happens to divide into. A wider
	// bucket is few enough to name every time.
	everyBar := bucket != insights.BucketDay || len(cells) <= barCaptionEvery
	// The amount is only written above the bars when there are few
	// enough of them for the numbers not to collide; past that the
	// height is the answer and the tooltip has the rest.
	labelled := len(cells) <= barCaptionEvery
	for i, c := range cells {
		caption := ""
		if everyBar || (last-i)%barCaptionEvery == 0 {
			caption = bucketCaption(c.Start, bucket)
		}
		amount := ""
		if labelled {
			amount = compactMinutes(c.Minutes)
		}
		chart.Bars = append(chart.Bars, barCell{
			Class:   cellBucketClass(c) + " " + barHeightClass(c.Minutes/busiest),
			Caption: caption,
			Amount:  amount,
			Title:   bucketTitle(c, bucket),
		})
	}
	chart.Label = chartLabel(days)
	return chart
}

// cellBucketClass colours a bar on the same absolute scale the heatmap
// uses, per day rather than per bucket: a busy week is not a busy day
// seven times over, and colouring a month by its total would paint
// every month the darkest shade there is.
func cellBucketClass(c bucketCell) string {
	if c.Days <= 0 {
		return cellClass(0)
	}
	return cellClass(c.Minutes / float64(c.Days))
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
			Title:   heatTitle(d),
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
	return fmt.Sprintf("daily reading, %s to %s: %s over %d days with reading",
		days[0].Date, days[len(days)-1].Date, readingMinutes(total), active)
}

// heatTitle is one square's whole answer: which day, and how much
// reading on it.
func heatTitle(d DayCell) string {
	when := d.Date
	if day, err := time.Parse(insights.DayFormat, d.Date); err == nil {
		when = day.Format("2 Jan 2006")
	}
	if d.Minutes <= 0 {
		return "Nothing read on " + when
	}
	return readingMinutes(d.Minutes) + " on " + when
}
