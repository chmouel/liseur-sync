package workident

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// partialMD5Block is both the size of each sampled block and the base
// the sample offsets are derived from. KOReader uses one constant for
// both; keeping that is the point, because the value is not ours to
// choose.
const partialMD5Block = 1024

// PartialMD5 is KOReader's document fingerprint: the MD5 of twelve
// kilobyte-sized samples taken at exponentially spaced offsets rather
// than of the whole file.
//
// It exists because it is the name every KOReader-speaking peer calls a
// document by. KOReader's own sync plugin sends it, and a server that
// stores reading positions for KOReader devices keys them by it, so a
// book this server holds is unrecognisable to any of them until it can
// produce the same digest for the same bytes.
//
// The offsets are 1024 << (2*i) for i from -1 to 10, evaluated with
// 32-bit shift semantics. That is not a tidy formula misremembered: at
// i = -1 the shift count wraps to 30 and the result overflows to zero,
// which is how the first sample comes to be taken at the start of the
// file. KOReader has always computed it that way, so reproducing the
// overflow is the whole requirement — a "corrected" formula would agree
// with nobody.
//
// A file shorter than an offset simply stops contributing there, and a
// final sample may be short. Both follow from reading at a position
// past the end and are what KOReader does.
//
// The digest samples roughly twelve kilobytes, so it is a weak
// fingerprint by construction: two different files can produce the same
// one. Callers must treat a match as evidence and not as proof, which
// is why it sorts below a full SHA-256 in AliasOrder.
func PartialMD5(r io.ReaderAt, size int64) (string, error) {
	digest := md5.New()
	buf := make([]byte, partialMD5Block)
	for i := -1; i <= 10; i++ {
		// The shift count is masked to five bits and the shift is done
		// in 32 bits, reproducing KOReader's arithmetic exactly. At
		// i = -1 that yields a shift of 30 and an offset of 0.
		shift := uint32(2*i) & 31
		offset := int64(uint32(partialMD5Block) << shift)
		if offset >= size {
			break
		}
		n, err := r.ReadAt(buf, offset)
		if n > 0 {
			digest.Write(buf[:n])
		}
		if err != nil {
			// A short final sample reports EOF alongside the bytes it
			// did return; anything else is a real read failure.
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("partial md5 at offset %d: %w", offset, err)
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
