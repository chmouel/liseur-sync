package webui

import (
	"strings"
	"testing"
)

func TestSanitizeDescription(t *testing.T) {
	calibre := `<div><div align="justify">Il y a cinq cent mille ans, ` +
		`une supernova…</div><div align="justify">Suite ici : ` +
		`<a href="http://www.amazon.fr/dp/B00X">http://www.amazon.fr/dp/B00X` +
		`</a></div></div>`

	cases := []struct {
		name    string
		in      string
		want    []string
		notWant []string
	}{{
		name:    "script element and its content are dropped",
		in:      `<p>ok</p><script>alert(1)</script>`,
		want:    []string{"<p>ok</p>"},
		notWant: []string{"script", "alert"},
	}, {
		name:    "style and template subtrees go too",
		in:      `<style>body{display:none}</style><template>x</template>after`,
		want:    []string{"after"},
		notWant: []string{"display:none", "style", "template", ">x"},
	}, {
		name:    "event handler attributes are stripped",
		in:      `<p onclick="steal()">click</p><div onerror=x>d</div>`,
		want:    []string{"<p>click</p>", "<div>d</div>"},
		notWant: []string{"onclick", "onerror", "steal"},
	}, {
		name:    "javascript href is refused but the text survives",
		in:      `<a href="javascript:alert(1)">go</a>`,
		want:    []string{"go"},
		notWant: []string{"<a", "javascript"},
	}, {
		name:    "obfuscated javascript href is refused",
		in:      `<a href="java&#09;script:alert(1)">go</a>`,
		want:    []string{"go"},
		notWant: []string{"<a", "script"},
	}, {
		name:    "uppercase and spaced javascript href is refused",
		in:      `<a href="  JAVAscript:alert(1)">go</a>`,
		want:    []string{"go"},
		notWant: []string{"<a", "avascript"},
	}, {
		name:    "data and vbscript hrefs are refused",
		in:      `<a href="data:text/html,<b>x">a</a><a href="VBScript:msgbox">b</a>`,
		notWant: []string{"<a", "data:", "vbscript", "VBScript"},
	}, {
		name:    "img is dropped entirely, payload and all",
		in:      `<p>before<img src=x onerror="alert(1)">after</p>`,
		want:    []string{"<p>beforeafter</p>"},
		notWant: []string{"<img", "onerror", "src"},
	}, {
		name: "an allowed link keeps its href and gains rel and target",
		in:   `<a href="https://example.org/x?a=1#f" class="c">see</a>`,
		want: []string{
			`<a href="https://example.org/x?a=1#f" ` +
				`rel="nofollow noopener noreferrer" target="_blank">see</a>`,
		},
		notWant: []string{"class"},
	}, {
		name:    "relative, scheme-relative and mailto links are allowed",
		in:      `<a href="/x">a</a><a href="//h/x">b</a><a href="mailto:x@y">c</a>`,
		want:    []string{`href="/x"`, `href="//h/x"`, `href="mailto:x@y"`},
		notWant: []string{"target=\"_self\""},
	}, {
		name:    "unknown elements are unwrapped keeping their text",
		in:      `<marquee><table><tr><td>kept</td></tr></table></marquee>`,
		want:    []string{"kept"},
		notWant: []string{"marquee", "<table", "<td"},
	}, {
		name: "the calibre blurb renders as markup, not as tags",
		want: []string{
			"<div>Il y a cinq cent mille ans, une supernova…</div>",
			`<a href="http://www.amazon.fr/dp/B00X" ` +
				`rel="nofollow noopener noreferrer" target="_blank">`,
			"http://www.amazon.fr/dp/B00X</a>",
		},
		in:      calibre,
		notWant: []string{"&lt;", "align="},
	}, {
		name:    "plain text keeps its line breaks",
		in:      "First line\nsecond line\n\nlast",
		want:    []string{"First line<br>second line<br><br>last"},
		notWant: []string{"<p>"},
	}, {
		name:    "plain text is escaped, not interpreted",
		in:      "5 > 3 && \"quoted\"",
		want:    []string{"&gt;", "&amp;&amp;"},
		notWant: []string{"5 > 3"},
	}, {
		name:    "newlines are left alone when the markup lays itself out",
		in:      "<p>one\ntwo</p>",
		want:    []string{"<p>one\ntwo</p>"},
		notWant: []string{"<br>"},
	}, {
		name:    "an unclosed tag cannot swallow the rest of the page",
		in:      `<div><em>hanging`,
		want:    []string{"<div><em>hanging</em></div>"},
		notWant: []string{"<em>hanging</div>"},
	}, {
		name:    "a stray closing tag is not echoed",
		in:      `plain</div></body></html>`,
		want:    []string{"plain"},
		notWant: []string{"</div>", "</body>", "</html>"},
	}, {
		name:    "comments and doctypes are dropped",
		in:      `<!DOCTYPE html><!-- secret -->visible<?pi x?>`,
		want:    []string{"visible"},
		notWant: []string{"secret", "DOCTYPE", "<?"},
	}, {
		name: "empty input stays empty",
		in:   "   \n  ",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeDescription(tc.in)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("want %q in output, got:\n%s", w, got)
				}
			}
			for _, n := range tc.notWant {
				if strings.Contains(got, n) {
					t.Errorf("did not want %q in output, got:\n%s", n, got)
				}
			}
			if tc.in != "" && len(tc.want) == 0 && len(tc.notWant) == 0 &&
				strings.TrimSpace(got) != "" {
				t.Errorf("want empty output, got %q", got)
			}
			if strings.Count(got, "<script") != 0 {
				t.Errorf("a script tag reached the output:\n%s", got)
			}
		})
	}
}

func TestSanitizeDescriptionCapsLongInput(t *testing.T) {
	long := "<p>" + strings.Repeat("a", 1<<20) + "</p>"
	got := sanitizeDescription(long)
	if len(got) > maxSanitizedBytes+64 {
		t.Fatalf("output not capped: %d bytes", len(got))
	}
	if !strings.HasSuffix(got, "</p>") {
		t.Fatalf("truncated output left an element open: %q",
			got[max(0, len(got)-40):])
	}
}

func TestSanitizeDescriptionCapsDeepNesting(t *testing.T) {
	const levels = 4000
	deep := strings.Repeat("<div>", levels) + "bottom" +
		strings.Repeat("</div>", levels)
	got := sanitizeDescription(deep)
	if !strings.Contains(got, "bottom") {
		t.Fatalf("the text at the bottom was lost:\n%s", got)
	}
	if n := strings.Count(got, "<div>"); n > maxSanitizedDepth {
		t.Fatalf("nesting not capped: %d levels", n)
	}
	if strings.Count(got, "<div>") != strings.Count(got, "</div>") {
		t.Fatalf("unbalanced output: %d open, %d closed",
			strings.Count(got, "<div>"), strings.Count(got, "</div>"))
	}
}

func TestSafeHref(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"https://example.org", true},
		{"http://example.org/a b", true},
		{"mailto:a@b.c", true},
		{"/relative", true},
		{"relative.html", true},
		{"//host/path", true},
		{"#anchor", true},
		{"?q=1", true},
		{"javascript:alert(1)", false},
		{"JaVaScRiPt:alert(1)", false},
		{"  java\tscript:alert(1)", false},
		{"java\nscript:alert(1)", false},
		{"data:text/html;base64,PHNjcmlwdD4=", false},
		{"vbscript:msgbox", false},
		{"file:///etc/passwd", false},
		{"", false},
	}
	for _, tc := range cases {
		if _, ok := safeHref(tc.in); ok != tc.ok {
			t.Errorf("safeHref(%q) = %v, want %v", tc.in, ok, tc.ok)
		}
	}
}
