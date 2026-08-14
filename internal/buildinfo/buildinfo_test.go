package buildinfo

import "testing"

// TestGetAlwaysAnswers pins the property the overview page depends on:
// there is no build of this server for which the version is empty. An
// unstamped `go test` binary is the least informative case there is,
// and it still has to produce something an operator can read.
func TestGetAlwaysAnswers(t *testing.T) {
	i := Get()
	if i.Version == "" {
		t.Fatal("version is empty")
	}
	if i.Short() == "" {
		t.Fatal("short form is empty")
	}
	if got, want := Get(), i; got != want {
		t.Fatalf("Get is not stable: %+v vs %+v", got, want)
	}
}

func TestShortRevision(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"abc", "abc"},
		{"0123456789abcdef0123", "0123456789ab"},
	} {
		if got := shortRevision(tc.in); got != tc.want {
			t.Fatalf("shortRevision(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShortMarksAModifiedTree(t *testing.T) {
	i := Info{Version: "v1.2.3"}
	if got := i.Short(); got != "v1.2.3" {
		t.Fatalf("got %q", got)
	}
	i.Modified = true
	if got := i.Short(); got != "v1.2.3 (modified)" {
		t.Fatalf("got %q", got)
	}
}
