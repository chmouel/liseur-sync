package audiomap

import (
	"strings"
	"testing"
)

func TestMatchTranscriptDetailInXHTMLPreservesExactPunctuation(t *testing.T) {
	doc := []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<body>
<p>Before they left the station, Mary whispered, "Don't stop, John—they're already watching us," and hurried down the platform.</p>
</body>
</html>`)

	_, conf, margin, before, highlight, after, ok := MatchTranscriptDetailInXHTML(
		doc,
		"Don't stop, John, they're already watching us",
	)
	if !ok {
		t.Fatalf("expected MatchTranscriptDetailInXHTML to match")
	}
	if conf < 0.9 || margin < 0.25 {
		t.Fatalf("unexpected confidence=%v margin=%v", conf, margin)
	}
	if highlight != "Don't stop, John—they're already watching us" {
		t.Fatalf("highlight = %q, want exact punctuation preserved", highlight)
	}
	if !strings.HasSuffix(before, `whispered, "`) {
		t.Fatalf("before = %q, want trailing quote preserved before highlight", before)
	}
	if !strings.HasPrefix(after, `," and hurried`) {
		t.Fatalf("after = %q, want leading comma and quote preserved after highlight", after)
	}
	fullVisible := `Before they left the station, Mary whispered, "Don't stop, John—they're already watching us," and hurried down the platform.`
	if !strings.Contains(fullVisible, before+highlight+after) {
		t.Fatalf("combined anchor %q is not an exact substring of visible text %q", before+highlight+after, fullVisible)
	}
}
