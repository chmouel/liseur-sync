package epub

import (
	"crypto/sha1"
	"encoding/hex"
	"io"
	"strings"
)

// fontObfuscationLength maps the two font-obfuscation algorithms EPUB
// permits (validateEncryption rejects any other) to how many leading
// bytes of the entry they scramble. Everything past that point is the
// font's real bytes, untouched.
var fontObfuscationLength = map[string]int64{
	"http://www.idpf.org/2008/embedding": 1040,
	"http://ns.adobe.com/pdf/enc#RC":     1024,
}

// fontObfuscationKey derives the XOR key for algorithm from the
// publication's dc:identifier, matching the two schemes' key derivations:
// IDPF hashes the raw identifier with SHA-1, Adobe hex-decodes it after
// stripping the "urn:uuid:" prefix and hyphens. An empty or malformed
// identifier yields no usable key, in which case the caller must not
// deobfuscate rather than serve corrupted bytes.
func fontObfuscationKey(identifier, algorithm string) []byte {
	if algorithm == "http://ns.adobe.com/pdf/enc#RC" {
		trimmed := strings.ReplaceAll(strings.ReplaceAll(identifier, "urn:uuid:", ""), "-", "")
		key, err := hex.DecodeString(trimmed)
		if err != nil || len(key) == 0 {
			return nil
		}
		return key
	}
	key := sha1.Sum([]byte(identifier))
	return key[:]
}

// deobfuscatingReader wraps an archive entry's reader and reverses XOR
// font obfuscation over its first obfuscationLength bytes, matching
// go-toolkit's own pkg/parser/epub/deobfuscator.go byte-for-byte. It exists
// because this package serves resources through its own raw ZIP fast path
// (OpenResource, OpenIndexedResource), which never passes through the
// toolkit's fetcher pipeline where that deobfuscation would otherwise run
// automatically.
type deobfuscatingReader struct {
	r                 io.ReadCloser
	key               []byte
	obfuscationLength int64
	pos               int64
}

func newDeobfuscatingReader(r io.ReadCloser, identifier, algorithm string) io.ReadCloser {
	length, ok := fontObfuscationLength[algorithm]
	if !ok {
		return r
	}
	key := fontObfuscationKey(identifier, algorithm)
	if len(key) == 0 {
		return r
	}
	return &deobfuscatingReader{r: r, key: key, obfuscationLength: length}
}

func (d *deobfuscatingReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if n > 0 && d.pos < d.obfuscationLength {
		limit := d.obfuscationLength - d.pos
		if limit > int64(n) {
			limit = int64(n)
		}
		olen := int64(len(d.key))
		for i := int64(0); i < limit; i++ {
			p[i] ^= d.key[(d.pos+i)%olen]
		}
	}
	d.pos += int64(n)
	return n, err
}

func (d *deobfuscatingReader) Close() error { return d.r.Close() }
