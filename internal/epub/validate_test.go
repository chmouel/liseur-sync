package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

type zipEntry struct {
	name   string
	body   string
	method uint16
	mode   uint32
}

func makeEPUB(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: entry.method}
		if entry.mode != 0 {
			header.SetMode(os.FileMode(entry.mode))
		}
		target, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(target, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func validEntries() []zipEntry {
	return []zipEntry{
		{name: "mimetype", body: "application/epub+zip", method: zip.Store},
		{
			name: "META-INF/container.xml", method: zip.Deflate,
			body: `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OPS/book.opf"
 media-type="application/oebps-package+xml"/></rootfiles></container>`,
		},
		{
			name: "OPS/book.opf", method: zip.Deflate,
			body: `<package xmlns="http://www.idpf.org/2007/opf">` +
				`<metadata><title>Book</title></metadata>` +
				`<manifest><item href="nav.xhtml" media-type="application/xhtml+xml"` +
				` properties="nav"/><item href="font.otf"` +
				` media-type="font/otf"/></manifest></package>`,
		},
		{
			name: "OPS/nav.xhtml", method: zip.Deflate,
			body: `<html xmlns="http://www.w3.org/1999/xhtml">` +
				`<body><nav/></body></html>`,
		},
		{name: "OPS/font.otf", body: "font", method: zip.Store},
	}
}

func validateBytes(data []byte, limits Limits) (Result, error) {
	return Validate(
		context.Background(), bytes.NewReader(data), int64(len(data)), limits)
}

func requireCode(t *testing.T, err error, want Code) {
	t.Helper()
	got, ok := ErrorCode(err)
	if !ok || got != want {
		t.Fatalf("validation error: got %q (%v) want %q", got, err, want)
	}
}

func TestValidateMinimalEPUB(t *testing.T) {
	data := makeEPUB(t, validEntries()...)
	result, err := validateBytes(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if result.PackagePath != "OPS/book.opf" ||
		result.NavigationPath != "OPS/nav.xhtml" || result.Encrypted {
		t.Fatalf("validation result: %+v", result)
	}
}

func TestValidateRejectsUnsafeAndMalformedArchives(t *testing.T) {
	tests := []struct {
		name    string
		entries []zipEntry
		code    Code
	}{
		{
			name: "mimetype not first",
			entries: append(
				[]zipEntry{{name: "other", body: "x", method: zip.Store}},
				validEntries()...),
			code: CodeInvalidEPUB,
		},
		{
			name: "compressed mimetype",
			entries: func() []zipEntry {
				entries := validEntries()
				entries[0].method = zip.Deflate
				return entries
			}(),
			code: CodeInvalidEPUB,
		},
		{
			name: "zip slip",
			entries: append(
				validEntries(),
				zipEntry{name: "../escape", body: "x", method: zip.Store}),
			code: CodeUnsafeArchive,
		},
		{
			name: "symlink",
			entries: append(
				validEntries(),
				zipEntry{
					name: "OPS/link", body: "target", method: zip.Store,
					mode: uint32(os.ModeSymlink | 0o777),
				}),
			code: CodeUnsafeArchive,
		},
		{
			name: "duplicate",
			entries: append(
				validEntries(),
				zipEntry{name: "OPS/nav.xhtml", body: "x", method: zip.Store}),
			code: CodeUnsafeArchive,
		},
		{
			name: "malformed container",
			entries: func() []zipEntry {
				entries := validEntries()
				entries[1].body = `<container><rootfiles>`
				return entries
			}(),
			code: CodeInvalidEPUB,
		},
		{
			name:    "missing package",
			entries: validEntries()[:2],
			code:    CodeInvalidEPUB,
		},
		{
			name: "malformed navigation",
			entries: func() []zipEntry {
				entries := validEntries()
				entries[3].body = `<html><body>`
				return entries
			}(),
			code: CodeInvalidEPUB,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateBytes(
				makeEPUB(t, test.entries...), DefaultLimits())
			requireCode(t, err, test.code)
		})
	}
}

func TestValidateEnforcesArchiveAndXMLLimits(t *testing.T) {
	t.Run("entry count", func(t *testing.T) {
		limits := DefaultLimits()
		limits.MaxEntries = 3
		_, err := validateBytes(makeEPUB(t, validEntries()...), limits)
		requireCode(t, err, CodeArchiveLimits)
	})
	t.Run("central directory bytes", func(t *testing.T) {
		limits := DefaultLimits()
		limits.MaxDirectoryBytes = 64
		_, err := validateBytes(makeEPUB(t, validEntries()...), limits)
		requireCode(t, err, CodeArchiveLimits)
	})
	t.Run("entry bytes", func(t *testing.T) {
		entries := validEntries()
		entries = append(entries,
			zipEntry{name: "OPS/large", body: "12345", method: zip.Store})
		limits := DefaultLimits()
		limits.MaxEntryBytes = 4
		_, err := validateBytes(makeEPUB(t, entries...), limits)
		requireCode(t, err, CodeArchiveLimits)
	})
	t.Run("expanded total", func(t *testing.T) {
		limits := DefaultLimits()
		limits.MaxUncompressedBytes = 100
		_, err := validateBytes(makeEPUB(t, validEntries()...), limits)
		requireCode(t, err, CodeArchiveLimits)
	})
	t.Run("compression ratio", func(t *testing.T) {
		entries := validEntries()
		entries = append(entries, zipEntry{
			name: "OPS/repeated", body: strings.Repeat("a", 10_000),
			method: zip.Deflate,
		})
		limits := DefaultLimits()
		limits.MaxCompressionRatio = 2
		_, err := validateBytes(makeEPUB(t, entries...), limits)
		requireCode(t, err, CodeArchiveLimits)
	})
	t.Run("metadata bytes", func(t *testing.T) {
		limits := DefaultLimits()
		limits.MaxMetadataBytes = 32
		_, err := validateBytes(makeEPUB(t, validEntries()...), limits)
		requireCode(t, err, CodeArchiveLimits)
	})
	t.Run("XML depth", func(t *testing.T) {
		limits := DefaultLimits()
		limits.MaxXMLDepth = 2
		_, err := validateBytes(makeEPUB(t, validEntries()...), limits)
		requireCode(t, err, CodeArchiveLimits)
	})
}

func TestValidateEncryptionPolicy(t *testing.T) {
	encryption := func(algorithm string) zipEntry {
		return zipEntry{
			name: "META-INF/encryption.xml", method: zip.Deflate,
			body: `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"` +
				` xmlns:enc="http://www.w3.org/2001/04/xmlenc#">` +
				`<enc:EncryptedData><enc:EncryptionMethod Algorithm="` +
				algorithm + `"/><enc:CipherData>` +
				`<enc:CipherReference URI="OPS/font.otf"/>` +
				`</enc:CipherData></enc:EncryptedData></encryption>`,
		}
	}
	t.Run("IDPF font obfuscation", func(t *testing.T) {
		entries := append(validEntries(),
			encryption("http://www.idpf.org/2008/embedding"))
		result, err := validateBytes(
			makeEPUB(t, entries...), DefaultLimits())
		if err != nil || !result.Encrypted {
			t.Fatalf("font obfuscation: %+v %v", result, err)
		}
	})
	t.Run("unsupported DRM", func(t *testing.T) {
		entries := append(validEntries(),
			encryption("http://www.w3.org/2001/04/xmlenc#aes256-cbc"))
		_, err := validateBytes(
			makeEPUB(t, entries...), DefaultLimits())
		requireCode(t, err, CodeUnsupportedDRM)
	})
	t.Run("duplicate method", func(t *testing.T) {
		entries := append(validEntries(), zipEntry{
			name: "META-INF/encryption.xml", method: zip.Deflate,
			body: `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"` +
				` xmlns:enc="http://www.w3.org/2001/04/xmlenc#">` +
				`<enc:EncryptedData>` +
				`<enc:EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#aes256-cbc"/>` +
				`<enc:EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/>` +
				`<enc:CipherData><enc:CipherReference URI="OPS/font.otf"/>` +
				`</enc:CipherData></enc:EncryptedData></encryption>`,
		})
		_, err := validateBytes(
			makeEPUB(t, entries...), DefaultLimits())
		requireCode(t, err, CodeInvalidEPUB)
	})
}

func TestValidateRejectsFontAlgorithmOnPublicationContent(t *testing.T) {
	entries := append(validEntries(),
		zipEntry{
			name: "OPS/chapter.xhtml", body: "<html/>", method: zip.Store,
		},
		zipEntry{
			name: "META-INF/encryption.xml", method: zip.Deflate,
			body: `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"` +
				` xmlns:enc="http://www.w3.org/2001/04/xmlenc#">` +
				`<enc:EncryptedData>` +
				`<enc:EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/>` +
				`<enc:CipherData><enc:CipherReference URI="OPS/chapter.xhtml"/>` +
				`</enc:CipherData></enc:EncryptedData></encryption>`,
		})
	_, err := validateBytes(makeEPUB(t, entries...), DefaultLimits())
	requireCode(t, err, CodeUnsupportedDRM)
}

func TestValidateHonorsCancellation(t *testing.T) {
	data := makeEPUB(t, validEntries()...)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Validate(ctx, bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled validation: %v", err)
	}
}

func TestValidateRejectsInvalidLimitsAsCallerError(t *testing.T) {
	data := makeEPUB(t, validEntries()...)
	limits := DefaultLimits()
	limits.MaxEntries = 0
	_, err := Validate(
		context.Background(), bytes.NewReader(data), int64(len(data)), limits)
	if !errors.Is(err, ErrInvalidValidationInput) {
		t.Fatalf("invalid limits: %v", err)
	}
	if _, ok := ErrorCode(err); ok {
		t.Fatalf("invalid limits were classified as content: %v", err)
	}
}

func TestValidateRequiresSingleDocumentRoots(t *testing.T) {
	t.Run("empty package", func(t *testing.T) {
		entries := validEntries()
		entries[2].body = ""
		_, err := validateBytes(
			makeEPUB(t, entries...), DefaultLimits())
		requireCode(t, err, CodeInvalidEPUB)
	})
	t.Run("multiple navigation roots", func(t *testing.T) {
		entries := validEntries()
		entries[3].body =
			`<html xmlns="http://www.w3.org/1999/xhtml"/>` +
				`<html xmlns="http://www.w3.org/1999/xhtml"/>`
		_, err := validateBytes(
			makeEPUB(t, entries...), DefaultLimits())
		requireCode(t, err, CodeInvalidEPUB)
	})
}

func TestValidateRejectsXMLDirectives(t *testing.T) {
	entries := validEntries()
	entries[1].body = `<!DOCTYPE container [<!ENTITY x SYSTEM "file:///etc/passwd">]>` +
		`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">` +
		`<rootfiles><rootfile full-path="OPS/book.opf"` +
		` media-type="application/oebps-package+xml"/></rootfiles></container>`
	_, err := validateBytes(makeEPUB(t, entries...), DefaultLimits())
	requireCode(t, err, CodeInvalidEPUB)
	if errors.Is(err, io.EOF) {
		t.Fatal("directive rejection was reported as EOF")
	}
}

// TestValidateAcceptsDeflatedDirectoryEntries is the regression for a
// real book that was refused: common EPUB writers store directory
// entries deflated, and an empty deflate stream is a couple of bytes
// long. Only the uncompressed size says whether a directory carries
// content.
func TestValidateAcceptsDeflatedDirectoryEntries(t *testing.T) {
	entries := validEntries()
	entries = append(entries,
		zipEntry{name: "OPS/", method: zip.Deflate, mode: 0o40755},
		zipEntry{name: "META-INF/", method: zip.Deflate, mode: 0o40755})
	data := makeEPUB(t, entries...)
	if _, err := validateBytes(data, DefaultLimits()); err != nil {
		t.Fatalf("deflated directory entries refused: %v", err)
	}
}

// TestValidateRejectsDirectoriesCarryingContent keeps the check that
// matters: a directory entry with actual bytes in it is a malformed
// archive, whatever its compressed size. Go's zip writer refuses to
// produce one, so the size is patched into the finished archive.
func TestValidateRejectsDirectoriesCarryingContent(t *testing.T) {
	entries := validEntries()
	entries = append(entries,
		zipEntry{name: "OPS/", method: zip.Deflate, mode: 0o40755})
	data := makeEPUB(t, entries...)
	patched := setDirectoryUncompressedSize(t, data, "OPS/", 8)
	_, err := validateBytes(patched, DefaultLimits())
	requireCode(t, err, CodeUnsafeArchive)
}

// setDirectoryUncompressedSize rewrites the uncompressed size recorded
// for one entry in the central directory, which is what the reader
// trusts. Building the archive this way keeps the test honest about what
// the validator sees rather than about what Go is willing to write.
func setDirectoryUncompressedSize(
	t *testing.T, data []byte, name string, size uint32,
) []byte {
	t.Helper()
	patched := append([]byte(nil), data...)
	signature := []byte{0x50, 0x4b, 0x01, 0x02}
	target := []byte(name)
	for i := 0; i+46 <= len(patched); i++ {
		if !bytes.Equal(patched[i:i+4], signature) {
			continue
		}
		nameLen := int(binary.LittleEndian.Uint16(patched[i+28 : i+30]))
		if i+46+nameLen > len(patched) ||
			!bytes.Equal(patched[i+46:i+46+nameLen], target) {
			continue
		}
		binary.LittleEndian.PutUint32(patched[i+24:i+28], size)
		return patched
	}
	t.Fatalf("central directory entry %q not found", name)
	return nil
}

// TestValidateAcceptsADoctype covers the other real book that was
// refused. EPUB 3 navigation documents are XHTML5 and carry a doctype;
// EPUB 2 packages often carry a public one. Neither is a threat.
func TestValidateAcceptsADoctype(t *testing.T) {
	for _, doctype := range []string{
		"<!DOCTYPE html>",
		`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.1//EN" ` +
			`"http://www.w3.org/TR/xhtml11/DTD/xhtml11.dtd">`,
		"<!doctype html>",
	} {
		entries := validEntries()
		for i := range entries {
			if entries[i].name == "OPS/nav.xhtml" {
				entries[i].body = doctype + entries[i].body
			}
		}
		if _, err := validateBytes(
			makeEPUB(t, entries...), DefaultLimits(),
		); err != nil {
			t.Fatalf("doctype %q refused: %v", doctype, err)
		}
	}
}

// TestValidateRejectsEntityDeclarations is what the blanket directive ban
// was really protecting against, and it must survive relaxing that ban: a
// declared entity is the setup for expanding a small file into a huge one
// or for reading a file the server never meant to expose.
func TestValidateRejectsEntityDeclarations(t *testing.T) {
	bombs := []string{
		`<!DOCTYPE p [<!ENTITY a "aaaaaaaaaa">]>`,
		`<!DOCTYPE p [<!entity a "aaa">]>`,
		`<!DOCTYPE p [<!ENTITY xxe SYSTEM "file:///etc/passwd">]>`,
	}
	for _, bomb := range bombs {
		for _, target := range []string{"OPS/nav.xhtml", "OPS/book.opf"} {
			entries := validEntries()
			for i := range entries {
				if entries[i].name == target {
					entries[i].body = bomb + entries[i].body
				}
			}
			_, err := validateBytes(makeEPUB(t, entries...), DefaultLimits())
			requireCode(t, err, CodeInvalidEPUB)
		}
	}
}

// TestValidateAcceptsLegacyFontMediaTypes: obfuscated fonts are declared
// with whatever spelling the producing tool used. The bytes are the same,
// and rejecting the label rejected a whole book as if it were DRM.
func TestValidateAcceptsLegacyFontMediaTypes(t *testing.T) {
	for _, mediaType := range []string{
		"application/x-font-truetype",
		"application/x-truetype-font",
		"font/truetype",
		"application/vnd.ms-opentype",
	} {
		entries := validEntries()
		for i := range entries {
			if entries[i].name == "OPS/book.opf" {
				entries[i].body = strings.Replace(
					entries[i].body, `media-type="font/otf"`,
					`media-type="`+mediaType+`"`, 1)
			}
		}
		entries = append(entries, zipEntry{
			name: "META-INF/encryption.xml", method: zip.Deflate,
			body: `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"` +
				` xmlns:enc="http://www.w3.org/2001/04/xmlenc#">` +
				`<enc:EncryptedData><enc:EncryptionMethod` +
				` Algorithm="http://www.idpf.org/2008/embedding"/>` +
				`<enc:CipherData><enc:CipherReference URI="OPS/font.otf"/>` +
				`</enc:CipherData></enc:EncryptedData></encryption>`,
		})
		result, err := validateBytes(makeEPUB(t, entries...), DefaultLimits())
		if err != nil {
			t.Fatalf("font media type %q refused: %v", mediaType, err)
		}
		if !result.Encrypted {
			t.Fatalf("font media type %q: obfuscation not reported", mediaType)
		}
	}
}

// TestValidateStillRefusesRealEncryption: relaxing the media-type list
// must not turn actual DRM into an accepted book.
func TestValidateStillRefusesRealEncryption(t *testing.T) {
	entries := append(validEntries(), zipEntry{
		name: "META-INF/encryption.xml", method: zip.Deflate,
		body: `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"` +
			` xmlns:enc="http://www.w3.org/2001/04/xmlenc#">` +
			`<enc:EncryptedData><enc:EncryptionMethod` +
			` Algorithm="http://www.w3.org/2001/04/xmlenc#aes256-cbc"/>` +
			`<enc:CipherData><enc:CipherReference URI="OPS/font.otf"/>` +
			`</enc:CipherData></enc:EncryptedData></encryption>`,
	})
	_, err := validateBytes(makeEPUB(t, entries...), DefaultLimits())
	requireCode(t, err, CodeUnsupportedDRM)
}

// TestValidateRefusesEncryptedNonFonts: the font media-type list is what
// separates harmless obfuscation from encrypted content, so a file that
// is encrypted while claiming not to be a font must still be refused.
// Widening that list must never widen this.
func TestValidateRefusesEncryptedNonFonts(t *testing.T) {
	for _, mediaType := range []string{
		"application/octet-stream",
		"application/xhtml+xml",
		"image/jpeg",
	} {
		entries := validEntries()
		for i := range entries {
			if entries[i].name == "OPS/book.opf" {
				entries[i].body = strings.Replace(
					entries[i].body, `media-type="font/otf"`,
					`media-type="`+mediaType+`"`, 1)
			}
		}
		entries = append(entries, zipEntry{
			name: "META-INF/encryption.xml", method: zip.Deflate,
			body: `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"` +
				` xmlns:enc="http://www.w3.org/2001/04/xmlenc#">` +
				`<enc:EncryptedData><enc:EncryptionMethod` +
				` Algorithm="http://www.idpf.org/2008/embedding"/>` +
				`<enc:CipherData><enc:CipherReference URI="OPS/font.otf"/>` +
				`</enc:CipherData></enc:EncryptedData></encryption>`,
		})
		_, err := validateBytes(makeEPUB(t, entries...), DefaultLimits())
		requireCode(t, err, CodeUnsupportedDRM)
	}
}
