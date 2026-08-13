package epub

import (
	"archive/zip"
	"bytes"
	"context"
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
