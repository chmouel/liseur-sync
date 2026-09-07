package epub

import (
	"crypto/sha1"
	"encoding/hex"
	"io"
	"regexp"
	"strings"

	"github.com/readium/go-toolkit/pkg/manifest"
)

// fontObfuscationLength maps the two font-obfuscation algorithms EPUB
// permits (validateEncryption rejects any other) to how many leading
// bytes of the entry they scramble. Everything past that point is the
// font's real bytes, untouched.
var fontObfuscationLength = map[string]int64{
	"http://www.idpf.org/2008/embedding": 1040,
	"http://ns.adobe.com/pdf/enc#RC":     1024,
}

const adobeObfuscation = "http://ns.adobe.com/pdf/enc#RC"

// uuidPattern matches a UUID's canonical hyphenated form anywhere inside a
// string. This reader's previous foliate-js implementation scanned every
// declared dc:identifier for one rather than trusting the package's
// unique-identifier to be a UUID, because Adobe's obfuscation always keys
// off a UUID even when a publisher's unique-identifier is something else
// (an ISBN, say) and the UUID sits in another, unmarked identifier.
var uuidPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// stripFontIdentifierWhitespace removes the ASCII space, tab, CR and LF
// characters IDPF's font-obfuscation key derivation strips before hashing
// the identifier, matching both go-toolkit's and foliate-js's behavior.
func stripFontIdentifierWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return r
	}, s)
}

// fontObfuscationIdentifier picks the raw identifier string an algorithm
// keys off of. IDPF hashes the package's unique-identifier, whitespace
// stripped — that one is EPUB's obfuscation identifier by definition.
// Adobe's variant requires a UUID specifically, so every declared
// identifier is searched for one rather than assuming the primary
// identifier is it. An empty return means no usable identifier was found.
func fontObfuscationIdentifier(metadata manifest.Metadata, algorithm string) string {
	if algorithm != adobeObfuscation {
		return stripFontIdentifierWhitespace(metadata.Identifier)
	}
	candidates := make([]string, 0, len(metadata.AltIdentifiers)+1)
	candidates = append(candidates, metadata.Identifier)
	for _, alt := range metadata.AltIdentifiers {
		candidates = append(candidates, alt.Value)
	}
	for _, candidate := range candidates {
		if uuid := uuidPattern.FindString(candidate); uuid != "" {
			return uuid
		}
	}
	return ""
}

// fontObfuscationKey derives the XOR key for algorithm: IDPF hashes the
// identifier with SHA-1, Adobe hex-decodes the UUID's digits after
// stripping hyphens. An empty or malformed identifier yields no usable
// key, in which case the caller must not deobfuscate rather than serve
// corrupted bytes.
func fontObfuscationKey(metadata manifest.Metadata, algorithm string) []byte {
	identifier := fontObfuscationIdentifier(metadata, algorithm)
	if identifier == "" {
		return nil
	}
	if algorithm == adobeObfuscation {
		key, err := hex.DecodeString(strings.ReplaceAll(identifier, "-", ""))
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

func newDeobfuscatingReader(r io.ReadCloser, metadata manifest.Metadata, algorithm string) io.ReadCloser {
	length, ok := fontObfuscationLength[algorithm]
	if !ok {
		return r
	}
	key := fontObfuscationKey(metadata, algorithm)
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
