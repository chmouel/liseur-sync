package audiomap

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestAlignChaptersAndLocatorAt(t *testing.T) {
	res := []Resource{
		{Href: "Text/front.xhtml", Start: 0.0, End: 0.1},
		{Href: "Text/ch1.xhtml", Start: 0.1, End: 0.55},
		{Href: "Text/ch2.xhtml", Start: 0.55, End: 1.0},
	}
	bookChaps := []BookChapter{
		{Title: "Chapter 1: The Arrival", Href: "Text/ch1.xhtml", Start: 0.1, End: 0.55},
		{Title: "Chapter 2: The Departure", Href: "Text/ch2.xhtml", Start: 0.55, End: 1.0},
	}
	audioChaps := []Chapter{
		{Title: "Opening Credits", Start: 0, End: 10},
		{Title: "1 - The Arrival", Start: 10, End: 510},
		{Title: "2 - The Departure", Start: 510, End: 1010},
	}

	m, err := Align(bookChaps, res, audioChaps, 1010)
	if err != nil {
		t.Fatalf("Align failed: %v", err)
	}
	if len(m.Segments) != 2 {
		t.Fatalf("len(Segments) = %d, want 2", len(m.Segments))
	}
	if m.Segments[0].Href != "Text/ch1.xhtml" || m.Segments[1].Href != "Text/ch2.xhtml" {
		t.Fatalf("unexpected segment hrefs: %+v", m.Segments)
	}

	// Unmapped opening credits returns ok=false.
	if _, _, ok := m.ToBook(5); ok {
		t.Fatalf("expected ToBook(5) during opening credits to return ok=false")
	}

	// Halfway through Chapter 1 (t = 260s -> 50% of 10..510 -> BookStart 0.1 + 0.5*(0.45) = 0.325).
	p, seg, ok := m.ToBook(260)
	if !ok {
		t.Fatalf("expected ToBook(260) to succeed")
	}
	if math.Abs(p-0.325) > 1e-6 || seg.Href != "Text/ch1.xhtml" {
		t.Fatalf("ToBook(260) = (%v, %+v), want 0.325 in Text/ch1.xhtml", p, seg)
	}

	rawLoc, err := m.LocatorAt(p, seg, false)
	if err != nil {
		t.Fatalf("LocatorAt failed: %v", err)
	}
	var loc struct {
		Href      string `json:"href"`
		Locations struct {
			Progression *float64 `json:"progression"`
		} `json:"locations"`
	}
	if err := json.Unmarshal(rawLoc, &loc); err != nil {
		t.Fatalf("Unmarshal locator: %v", err)
	}
	if loc.Href != "Text/ch1.xhtml" || loc.Locations.Progression == nil {
		t.Fatalf("unexpected LocatorAt: %s", string(rawLoc))
	}
	if math.Abs(*loc.Locations.Progression-0.5) > 1e-6 {
		t.Fatalf("chapter progression = %v, want 0.5", *loc.Locations.Progression)
	}
}

func TestMatchTranscriptDetailInXHTMLAndAmbiguityMargin(t *testing.T) {
	doc := []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<body>
<p>The old lighthouse stood silent against the crashing winter waves while the keeper trimmed the brass lamp.</p>
<p>Hours passed in quiet solitude as the storm gathered strength across the northern bay and shook the shutters.</p>
<p>At midnight the distant foghorn sounded three times through the freezing rain and woke the sleeping harbour.</p>
</body>
</html>`)

	_, conf, margin, before, highlight, after, ok := MatchTranscriptDetailInXHTML(doc, "while the keeper trimmed the brass lamp")
	if !ok {
		t.Fatalf("expected unambiguous match to succeed")
	}
	if conf < 0.8 || margin < 0.25 {
		t.Fatalf("expected high confidence and margin, got confidence=%v margin=%v", conf, margin)
	}
	if !strings.Contains(strings.ToLower(before+highlight), "trimmed") {
		t.Fatalf("expected context around matched transcript, got before=%q highlight=%q after=%q", before, highlight, after)
	}

	// Now test an ambiguous repeated refrain appearing identically twice in the chapter.
	repeatedDoc := []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<body>
<p>Nevermore said the raven perched above the chamber door in the bleak December midnight.</p>
<p>Many other verses follow here with distinct words about sorrow and lost Lenore in the lamplight.</p>
<p>Nevermore said the raven perched above the chamber door in the bleak December midnight.</p>
</body>
</html>`)

	_, ambigConf, ambigMargin, _, _, _, _ := MatchTranscriptDetailInXHTML(repeatedDoc, "nevermore said the raven perched above the chamber door")
	if ambigMargin >= 0.15 {
		t.Fatalf("expected repeated passage to have near-zero runner-up margin, got margin=%v (confidence=%v)", ambigMargin, ambigConf)
	}

	// Adjacent repeated passages within the padded search window must also have zero margin.
	adjacentDoc := []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><p>red blue green yellow red blue green yellow</p></body></html>`)
	_, adjConf, adjMargin, _, _, _, _ := MatchTranscriptDetailInXHTML(adjacentDoc, "red blue green yellow")
	if adjMargin >= 0.15 {
		t.Fatalf("expected adjacent repeated passage to have zero runner-up margin, got margin=%v (confidence=%v)", adjMargin, adjConf)
	}
}

func TestToAudioLocatorDoesNotMapUncoveredFrontMatter(t *testing.T) {
	m := Mapping{
		Resources: []Resource{
			{Href: "OEBPS/ch1.xhtml", Start: 0, End: 1},
		},
		Segments: []Segment{
			{Href: "OEBPS/ch1.xhtml", BookStart: 0.5, BookEnd: 1.0, AudioStart: 0, AudioEnd: 100},
		},
	}
	loc := []byte(`{"href":"OEBPS/ch1.xhtml","locations":{"progression":0.1}}`)
	if at, mapped, _ := m.ToAudioLocator(0.1, loc, nil); mapped {
		t.Fatalf("expected unmapped front-matter position 0.1 to return mapped=false, got audioTime=%v", at)
	}
}

func TestExtractTextContextAtFractionAndLocatorRoundTrip(t *testing.T) {
	doc := []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<body>
<p>First paragraph of the chapter introducing the travellers at the mountain pass before dawn.</p>
<p>Second paragraph describing the steep descent into the valley where the river glimmered below.</p>
<p>Third paragraph concluding the evening march beside the ancient stone bridge in the forest.</p>
</body>
</html>`)

	before, highlight, after := ExtractTextContextAtFraction(doc, 0.55)
	if highlight == "" {
		t.Fatalf("expected non-empty highlight at fraction 0.55")
	}
	frac, conf, ok := MatchLocatorTextInXHTML(doc, before, highlight, after)
	if !ok || conf < 0.7 {
		t.Fatalf("MatchLocatorTextInXHTML failed: frac=%v conf=%v ok=%v", frac, conf, ok)
	}
	if math.Abs(frac-0.55) > 0.12 {
		t.Fatalf("round-trip fraction = %v, want near 0.55", frac)
	}
}
