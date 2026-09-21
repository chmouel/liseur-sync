package workident_test

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"testing"

	"github.com/chmouel/liseur-sync/internal/workident"
)

// sampled builds the byte sequence KOReader feeds its MD5 for a file of
// the given contents, written out longhand rather than as a formula.
// The offsets are the literal ones, so a change to the production
// shift arithmetic has to disagree with a list somebody can read.
func sampled(data []byte) []byte {
	offsets := []int{
		0, 1024, 4096, 16384, 65536, 262144,
		1048576, 4194304, 16777216, 67108864,
		268435456, 1073741824,
	}
	var out []byte
	for _, off := range offsets {
		if off >= len(data) {
			break
		}
		end := off + 1024
		if end > len(data) {
			end = len(data)
		}
		out = append(out, data[off:end]...)
	}
	return out
}

func pattern(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i*7 + i/251)
	}
	return data
}

func TestPartialMD5MatchesKOReaderSampling(t *testing.T) {
	for _, size := range []int{1, 512, 1024, 1025, 2048, 5000, 70000, 300000} {
		data := pattern(size)
		want := md5.Sum(sampled(data))
		got, err := workident.PartialMD5(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if got != hex.EncodeToString(want[:]) {
			t.Errorf("size %d: got %s, want %s",
				size, got, hex.EncodeToString(want[:]))
		}
	}
}

// A file no larger than one block is sampled once, from the start, so
// its fingerprint is the MD5 of the whole thing. This is the case that
// would break first if the i = -1 overflow were "tidied up" into a
// shift that did not land on offset zero.
func TestPartialMD5OfAShortFileIsTheWholeFile(t *testing.T) {
	data := pattern(600)
	want := md5.Sum(data)
	got, err := workident.PartialMD5(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if got != hex.EncodeToString(want[:]) {
		t.Errorf("got %s, want %s", got, hex.EncodeToString(want[:]))
	}
}

// An empty file contributes no samples at all, so it fingerprints as
// the MD5 of nothing. It is a degenerate case, but a catalog can hold a
// zero-byte file and the digest must not depend on a read past the end.
func TestPartialMD5OfAnEmptyFile(t *testing.T) {
	got, err := workident.PartialMD5(bytes.NewReader(nil), 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := "d41d8cd98f00b204e9800998ecf8427e"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// The value is a wire contract with other people's software, so it is
// pinned. If this changes, every position a KOReader device or a peer
// server ever stored for a book stops being found.
func TestPartialMD5IsPinned(t *testing.T) {
	got, err := workident.PartialMD5(bytes.NewReader(pattern(5000)), 5000)
	if err != nil {
		t.Fatal(err)
	}
	want := md5.Sum(sampled(pattern(5000)))
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("got %s, want %s", got, hex.EncodeToString(want[:]))
	}
	if len(got) != 32 {
		t.Fatalf("fingerprint is not an MD5 hex digest: %q", got)
	}
}
