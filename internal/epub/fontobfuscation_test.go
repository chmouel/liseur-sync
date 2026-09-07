package epub

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"strings"
	"testing"
)

// obfuscate mirrors the XOR both algorithms use, so a test can construct
// obfuscated bytes and confirm newDeobfuscatingReader reverses them back to
// the original — the same transform applied twice is the identity.
func obfuscate(data []byte, key []byte, length int64) []byte {
	out := append([]byte(nil), data...)
	limit := length
	if limit > int64(len(out)) {
		limit = int64(len(out))
	}
	olen := int64(len(key))
	for i := int64(0); i < limit; i++ {
		out[i] ^= key[i%olen]
	}
	return out
}

func TestDeobfuscatingReaderReversesIDPF(t *testing.T) {
	identifier := "urn:uuid:12345678-1234-1234-1234-123456789abc"
	key := func() []byte { s := sha1.Sum([]byte(identifier)); return s[:] }()
	original := bytes.Repeat([]byte("font-bytes-"), 200) // > 1040 bytes
	obfuscated := obfuscate(original, key, 1040)

	r := newDeobfuscatingReader(io.NopCloser(bytes.NewReader(obfuscated)), identifier, "http://www.idpf.org/2008/embedding")
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("deobfuscation did not reverse the IDPF transform")
	}
}

func TestDeobfuscatingReaderReversesAdobe(t *testing.T) {
	identifier := "urn:uuid:12345678-1234-1234-1234-123456789abc"
	trimmed := strings.ReplaceAll(strings.ReplaceAll(identifier, "urn:uuid:", ""), "-", "")
	key, err := hex.DecodeString(trimmed)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	original := bytes.Repeat([]byte("font-bytes-"), 200) // > 1024 bytes
	obfuscated := obfuscate(original, key, 1024)

	r := newDeobfuscatingReader(io.NopCloser(bytes.NewReader(obfuscated)), identifier, "http://ns.adobe.com/pdf/enc#RC")
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("deobfuscation did not reverse the Adobe transform")
	}
}

// TestDeobfuscatingReaderWorksAcrossSmallReads exercises the reader with a
// small buffer so the obfuscated portion spans several Read calls, the way
// io.Copy or a chunked HTTP writer would drive it.
func TestDeobfuscatingReaderWorksAcrossSmallReads(t *testing.T) {
	identifier := "urn:uuid:12345678-1234-1234-1234-123456789abc"
	key := func() []byte { s := sha1.Sum([]byte(identifier)); return s[:] }()
	original := bytes.Repeat([]byte("0123456789"), 200) // 2000 bytes, > 1040
	obfuscated := obfuscate(original, key, 1040)

	r := newDeobfuscatingReader(io.NopCloser(bytes.NewReader(obfuscated)), identifier, "http://www.idpf.org/2008/embedding")
	var out bytes.Buffer
	buf := make([]byte, 7)
	for {
		n, err := r.Read(buf)
		out.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	if !bytes.Equal(out.Bytes(), original) {
		t.Fatalf("deobfuscation across small reads did not reverse the transform")
	}
}

func TestDeobfuscatingReaderSkipsUnknownAlgorithm(t *testing.T) {
	original := []byte("font bytes unchanged")
	r := newDeobfuscatingReader(io.NopCloser(bytes.NewReader(original)), "urn:uuid:anything", "http://example.com/unknown")
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("unknown algorithm must pass bytes through unchanged")
	}
}
