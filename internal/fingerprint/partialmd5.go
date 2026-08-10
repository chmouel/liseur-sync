// Package fingerprint implements KOReader's "partial MD5" document
// fingerprint (util.partialMD5 in koreader/frontend/util.lua), used as
// the partial-md5 alias kind. It samples 1024 bytes at offsets
// lshift(1024, 2*i) for i = -1..10 while a sample exists:
// offsets 256, 1024, 4096, 16384, ... No file size is included.
package fingerprint

import (
	"crypto/md5"
	"encoding/hex"
	"io"
)

// PartialMD5 computes the fingerprint over a reader (must support
// seeking for faithful semantics; the stream is buffered).
func PartialMD5(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return PartialMD5Bytes(data), nil
}

// PartialMD5Bytes computes the fingerprint over an in-memory buffer.
func PartialMD5Bytes(data []byte) string {
	h := md5.New()
	const size = 1024
	for i := -1; i <= 10; i++ {
		var pos int64
		if i < 0 {
			pos = 1024 >> 2 // lshift(1024, -2) == 256 in KOReader's Lua
		} else {
			pos = int64(1024) << uint(2*i)
		}
		if pos >= int64(len(data)) {
			break
		}
		end := pos + size
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		h.Write(data[pos:end])
	}
	return hex.EncodeToString(h.Sum(nil))
}
