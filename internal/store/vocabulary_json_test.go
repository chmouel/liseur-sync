package store

import (
	"strings"
	"testing"
	"time"
)

func TestSameVocabularyOccurrencesComparesNumbersByValue(t *testing.T) {
	equal := [][2]string{
		{`[1e2]`, `[100]`},
		{`[1.50]`, `[15e-1]`},
		{`[0]`, `[-0.0e5]`},
		{`[12345678901234567890123]`, `[1.2345678901234567890123E+22]`},
		{`[1e999999]`, `[10e999998]`},
		{`[1e999999,1e999999,1e999999]`, `[1.0e999999,100e999997,0.1e1000000]`},
	}
	for _, c := range equal {
		if !sameVocabularyOccurrences([]byte(c[0]), []byte(c[1])) {
			t.Errorf("%s and %s should compare equal", c[0], c[1])
		}
	}
	unequal := [][2]string{
		{`[1e2]`, `[101]`},
		{`[1]`, `[-1]`},
		{`[1e999999]`, `[1e999998]`},
		{`[12345678901234567890123]`, `[12345678901234567890124]`},
	}
	for _, c := range unequal {
		if sameVocabularyOccurrences([]byte(c[0]), []byte(c[1])) {
			t.Errorf("%s and %s should differ", c[0], c[1])
		}
	}
}

func TestSameVocabularyOccurrencesLargeExponentsStayCheap(t *testing.T) {
	locator := `[` + strings.TrimSuffix(strings.Repeat(`1e999999,`, 1600), ",") + `]`
	start := time.Now()
	if !sameVocabularyOccurrences([]byte(locator), []byte(locator)) {
		t.Fatal("identical payloads compared unequal")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("comparison took %s", d)
	}
}
