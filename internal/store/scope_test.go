package store

import "testing"

func TestNormalizeScopes(t *testing.T) {
	got, err := NormalizeScopes([]Scope{
		ScopeLibraryRead, ScopeSync, ScopeLibraryRead, ScopeReadInsights,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "sync,read-insights,library-read" {
		t.Fatalf("canonical scopes = %q", got)
	}
	if _, err := NormalizeScopes(nil); err == nil {
		t.Fatal("empty scope set accepted")
	}
	if _, err := NormalizeScopes([]Scope{"unknown"}); err == nil {
		t.Fatal("unknown scope accepted")
	}
	if scope, ok := (ScopeSet{ScopeSync}).Legacy(); !ok || scope != ScopeSync {
		t.Fatalf("singleton legacy scope = %q, %v", scope, ok)
	}
	if _, ok := (ScopeSet{ScopeSync, ScopeLibraryRead}).Legacy(); ok {
		t.Fatal("multi-scope set exposed a legacy scalar")
	}
}
