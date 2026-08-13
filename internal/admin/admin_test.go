package admin

import (
	"os"
	"strings"
	"testing"
)

// TestUsageListsEverySubcommand is the regression test for a usage string
// that had been copied and then left behind: the command line advertised
// four subcommands while Run dispatched ten, so create-library and the
// rest were undiscoverable. Anything Run accepts must be documented.
func TestUsageListsEverySubcommand(t *testing.T) {
	source, err := os.ReadFile("admin.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(source)
	start := strings.Index(body, "switch args[0] {")
	if start < 0 {
		t.Fatal("cannot find the dispatch switch")
	}
	end := strings.Index(body[start:], "\n\tdefault:")
	if end < 0 {
		t.Fatal("cannot find the end of the dispatch switch")
	}
	dispatch := body[start : start+end]

	var found int
	for _, line := range strings.Split(dispatch, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "case ")
		if !ok {
			continue
		}
		for _, quoted := range strings.Split(rest, ",") {
			name := strings.Trim(strings.TrimSuffix(strings.TrimSpace(quoted), ":"), `"`)
			switch name {
			case "", "help", "-h", "--help":
				continue
			}
			found++
			if !strings.Contains(Usage, name) {
				t.Errorf("Run dispatches %q but Usage does not mention it", name)
			}
		}
	}
	if found < 8 {
		t.Fatalf("only found %d subcommands; the parser is wrong", found)
	}
}
