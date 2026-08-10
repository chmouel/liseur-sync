package fingerprint

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"testing"
)

// reference re-implements KOReader's loop literally for comparison.
func reference(data []byte) string {
	h := md5.New()
	for i := -1; i <= 10; i++ {
		var pos int
		if i < 0 {
			pos = 1024 >> 2
		} else {
			pos = 1024 << uint(2*i)
		}
		if pos >= len(data) {
			break
		}
		end := pos + 1024
		if end > len(data) {
			end = len(data)
		}
		h.Write(data[pos:end])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestPartialMD5MatchesReference(t *testing.T) {
	for _, n := range []int{0, 100, 255, 256, 300, 1024, 2000, 5000, 100_000, 5_000_000} {
		data := bytes.Repeat([]byte("ab"), n/2+1)[:n]
		if got, want := PartialMD5Bytes(data), reference(data); got != want {
			t.Fatalf("n=%d: got %s want %s", n, got, want)
		}
	}
}

func TestEmptyFile(t *testing.T) {
	// No samples at all -> md5 of nothing.
	if got := PartialMD5Bytes(nil); got != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Fatalf("empty: got %s", got)
	}
}

func TestReaderPath(t *testing.T) {
	data := bytes.Repeat([]byte("xyz"), 10_000)
	viaReader, err := PartialMD5(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if viaReader != PartialMD5Bytes(data) {
		t.Fatal("reader/bytes mismatch")
	}
}
