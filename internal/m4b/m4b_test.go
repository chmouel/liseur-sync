package m4b

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"
)

func TestReadMetadataChplAndTags(t *testing.T) {
	t.Parallel()

	// Build an M4B with mvhd (duration 3600s), udta/meta/ilst tags (with a large covr to skip),
	// and udta/chpl with FFmpeg 1-byte chapter count at offset 8.
	mvhd := makeFullBox("mvhd", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 0)
		putU32(b, 0)
		putU32(b, 1000)      // timescale = 1000
		putU32(b, 3_600_000) // duration = 3600s
	})

	dataTextAtom := func(s string) []byte {
		return makeBox("data", func(b *bytes.Buffer) {
			putU32(b, 1) // UTF-8
			putU32(b, 0)
			b.WriteString(s)
		})
	}
	ilst := makeBox("ilst", func(b *bytes.Buffer) {
		b.Write(makeBox("\xa9nam", func(ib *bytes.Buffer) { ib.Write(dataTextAtom("Disquiet Gods")) }))
		b.Write(makeBox("\xa9ART", func(ib *bytes.Buffer) { ib.Write(dataTextAtom("Christopher Ruocchio")) }))
		b.Write(makeBox("\xa9wrt", func(ib *bytes.Buffer) { ib.Write(dataTextAtom("Samuel Roukin")) }))
		b.Write(makeBox("\xa9grp", func(ib *bytes.Buffer) { ib.Write(dataTextAtom("The Sun Eater")) }))
		b.Write(makeBox("covr", func(ib *bytes.Buffer) { ib.Write(bytes.Repeat([]byte{0xff}, 1024)) }))
		b.Write(makeBox("----", func(ib *bytes.Buffer) {
			ib.Write(makeFullBox("mean", 0, 0, func(mb *bytes.Buffer) { mb.WriteString("com.apple.iTunes") }))
			ib.Write(makeFullBox("name", 0, 0, func(nb *bytes.Buffer) { nb.WriteString("ASIN") }))
			ib.Write(dataTextAtom("B0CW123456"))
		}))
	})
	metaBox := makeFullBox("meta", 0, 0, func(b *bytes.Buffer) {
		b.Write(ilst)
	})

	chpl := makeFullBox("chpl", 1, 0, func(b *bytes.Buffer) {
		putU32(b, 0)   // reserved
		b.WriteByte(2) // 1-byte FFmpeg chapter count at offset 8 of payload
		var ts [8]byte
		binary.BigEndian.PutUint64(ts[:], 0)
		b.Write(ts[:])
		b.WriteByte(uint8(len("Chapter 1")))
		b.WriteString("Chapter 1")

		binary.BigEndian.PutUint64(ts[:], 1800*10_000_000) // 1800s in 100ns units
		b.Write(ts[:])
		b.WriteByte(uint8(len("Chapter 2")))
		b.WriteString("Chapter 2")
	})

	udta := makeBox("udta", func(b *bytes.Buffer) {
		b.Write(metaBox)
		b.Write(chpl)
	})
	moov := makeBox("moov", func(b *bytes.Buffer) {
		b.Write(mvhd)
		b.Write(udta)
	})
	ftyp := makeBox("ftyp", func(b *bytes.Buffer) {
		b.WriteString("M4B ")
		putU32(b, 0)
	})
	raw := append(ftyp, moov...)

	meta, err := ReadMetadataSeeker(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("ReadMetadataSeeker: %v", err)
	}
	if meta.Title != "Disquiet Gods" {
		t.Fatalf("Title = %q, want Disquiet Gods", meta.Title)
	}
	if len(meta.Authors) != 1 || meta.Authors[0] != "Christopher Ruocchio" {
		t.Fatalf("Authors = %v", meta.Authors)
	}
	if len(meta.Narrators) != 1 || meta.Narrators[0] != "Samuel Roukin" {
		t.Fatalf("Narrators = %v", meta.Narrators)
	}
	if meta.SeriesName != "The Sun Eater" {
		t.Fatalf("SeriesName = %q", meta.SeriesName)
	}
	if meta.ASIN != "B0CW123456" {
		t.Fatalf("ASIN = %q", meta.ASIN)
	}
	if meta.Duration != 3600 {
		t.Fatalf("Duration = %v, want 3600", meta.Duration)
	}
	if len(meta.Chapters) != 2 {
		t.Fatalf("Chapters = %+v, want 2 chapters", meta.Chapters)
	}
	if meta.Chapters[0].Title != "Chapter 1" || meta.Chapters[0].Start != 0 || meta.Chapters[0].End != 1800 {
		t.Fatalf("Chapters[0] = %+v", meta.Chapters[0])
	}
	if meta.Chapters[1].Title != "Chapter 2" || meta.Chapters[1].Start != 1800 || meta.Chapters[1].End != 3600 {
		t.Fatalf("Chapters[1] = %+v", meta.Chapters[1])
	}
}

func TestReadMetadataTrakChplAndTextTrack(t *testing.T) {
	t.Parallel()

	// Build a QuickTime text chapter track with 2 chapter samples in mdat.
	sample1 := append([]byte{0, 9}, []byte("Chapter 1")...)
	sample2 := append([]byte{0, 9}, []byte("Chapter 2")...)
	ftyp := makeBox("ftyp", func(b *bytes.Buffer) {
		b.WriteString("M4B ")
		putU32(b, 0)
	})
	mdatOffset := len(ftyp) + 8
	mdat := makeBox("mdat", func(b *bytes.Buffer) {
		b.Write(sample1)
		b.Write(sample2)
	})

	mvhd := makeFullBox("mvhd", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 0)
		putU32(b, 0)
		putU32(b, 1000)
		putU32(b, 120_000) // 120s
	})
	mdhd := makeFullBox("mdhd", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 0)
		putU32(b, 0)
		putU32(b, 1000)
		putU32(b, 120_000)
	})
	hdlr := makeFullBox("hdlr", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 0)
		b.WriteString("text")
		b.Write(make([]byte, 12))
	})
	stts := makeFullBox("stts", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 1)
		putU32(b, 2)
		putU32(b, 60_000) // 60s each
	})
	stsc := makeFullBox("stsc", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 1)
		putU32(b, 1)
		putU32(b, 2)
		putU32(b, 1)
	})
	stsz := makeFullBox("stsz", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 0)
		putU32(b, 2)
		putU32(b, uint32(len(sample1)))
		putU32(b, uint32(len(sample2)))
	})
	stco := makeFullBox("stco", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 1)
		putU32(b, uint32(mdatOffset))
	})
	stbl := makeBox("stbl", func(b *bytes.Buffer) {
		b.Write(stts)
		b.Write(stsc)
		b.Write(stsz)
		b.Write(stco)
	})
	minf := makeBox("minf", func(b *bytes.Buffer) { b.Write(stbl) })
	mdia := makeBox("mdia", func(b *bytes.Buffer) {
		b.Write(mdhd)
		b.Write(hdlr)
		b.Write(minf)
	})
	trak := makeBox("trak", func(b *bytes.Buffer) { b.Write(mdia) })
	moov := makeBox("moov", func(b *bytes.Buffer) {
		b.Write(mvhd)
		b.Write(trak)
	})

	var file bytes.Buffer
	file.Write(ftyp)
	file.Write(mdat)
	file.Write(moov)
	raw := file.Bytes()

	meta, err := ReadMetadataSeeker(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("ReadMetadataSeeker: %v", err)
	}
	if len(meta.Chapters) != 2 {
		t.Fatalf("Chapters = %+v, want 2", meta.Chapters)
	}
	if meta.Chapters[0].Title != "Chapter 1" || meta.Chapters[0].Start != 0 || meta.Chapters[0].End != 60 {
		t.Fatalf("Chapters[0] = %+v", meta.Chapters[0])
	}
	if meta.Chapters[1].Title != "Chapter 2" || meta.Chapters[1].Start != 60 || meta.Chapters[1].End != 120 {
		t.Fatalf("Chapters[1] = %+v", meta.Chapters[1])
	}
}

func TestParseCueChapters(t *testing.T) {
	t.Parallel()

	cue := []byte(`REM GENRE Audiobook
TITLE "Shadows Upon Time"
FILE "Book 7 - Shadows upon Time.m4b" MP3
  TRACK 01 AUDIO
    TITLE "Opening Credits"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Chapter 1: The Quiet"
    INDEX 01 01:15:30
  TRACK 03 AUDIO
    TITLE "Chapter 2: The Loud"
    INDEX 01 125:00:00
`)
	chs := ParseCueChapters(cue, 10000)
	if len(chs) != 3 {
		t.Fatalf("len(chs) = %d, want 3", len(chs))
	}
	if chs[0].Title != "Opening Credits" || chs[0].Start != 0 || chs[0].End != 75.4 {
		t.Fatalf("chs[0] = %+v", chs[0])
	}
	if chs[1].Title != "Chapter 1: The Quiet" || chs[1].Start != 75.4 || chs[1].End != 7500 {
		t.Fatalf("chs[1] = %+v", chs[1])
	}
	if chs[2].Title != "Chapter 2: The Loud" || chs[2].Start != 7500 || chs[2].End != 10000 {
		t.Fatalf("chs[2] = %+v", chs[2])
	}
}

func TestSliceSnippetLargeMoovWindowedStsz(t *testing.T) {
	t.Parallel()

	// Construct a synthetic M4B whose stsz table has 2,000,000 samples (8 MB > old 6 MB limit),
	// with 100 samples per chunk (20,000 chunks), timescale = 1000, delta = 1000 (1s/sample).
	const (
		totalSamples    = 2_000_000
		samplesPerChunk = 100
		numChunks       = totalSamples / samplesPerChunk
		sampleBytes     = 16
	)

	ftyp := makeBox("ftyp", func(b *bytes.Buffer) {
		b.WriteString("M4B ")
		putU32(b, 0)
	})
	// We only materialize actual audio bytes for chunk #100 (samples 10,000..10,099) at offset 64.
	const targetAudioOffset = 64

	stsd := makeFullBox("stsd", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 1)
		b.Write(makeBox("mp4a", func(mb *bytes.Buffer) {
			mb.Write(make([]byte, 28))
		}))
	})
	stts := makeFullBox("stts", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 1)
		putU32(b, totalSamples)
		putU32(b, 1000) // 1s per sample
	})
	stsc := makeFullBox("stsc", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 1)
		putU32(b, 1)
		putU32(b, samplesPerChunk)
		putU32(b, 1)
	})
	stsz := makeFullBox("stsz", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 0) // variable sizes -> 8 MB table
		putU32(b, totalSamples)
		for i := 0; i < totalSamples; i++ {
			putU32(b, sampleBytes)
		}
	})
	stco := makeFullBox("stco", 0, 0, func(b *bytes.Buffer) {
		putU32(b, numChunks)
		for i := 0; i < numChunks; i++ {
			putU32(b, uint32(targetAudioOffset+i*samplesPerChunk*sampleBytes))
		}
	})
	stbl := makeBox("stbl", func(b *bytes.Buffer) {
		b.Write(stsd)
		b.Write(stts)
		b.Write(stsc)
		b.Write(stsz)
		b.Write(stco)
	})
	minf := makeBox("minf", func(b *bytes.Buffer) { b.Write(stbl) })
	mdhd := makeFullBox("mdhd", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 0)
		putU32(b, 0)
		putU32(b, 1000)
		putU32(b, totalSamples*1000)
	})
	hdlr := makeFullBox("hdlr", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 0)
		b.WriteString("soun")
		b.Write(make([]byte, 12))
	})
	mdia := makeBox("mdia", func(b *bytes.Buffer) {
		b.Write(mdhd)
		b.Write(hdlr)
		b.Write(minf)
	})
	trak := makeBox("trak", func(b *bytes.Buffer) { b.Write(mdia) })
	moov := makeBox("moov", func(b *bytes.Buffer) { b.Write(trak) })
	if len(moov) <= 6<<20 {
		t.Fatalf("expected moov > 6 MiB, got %d", len(moov))
	}

	virtualAudioEnd := int64(targetAudioOffset + totalSamples*sampleBytes)
	mdatHdr := make([]byte, 16)
	binary.BigEndian.PutUint32(mdatHdr[0:4], 1)
	copy(mdatHdr[4:8], "mdat")
	binary.BigEndian.PutUint64(mdatHdr[8:16], uint64(virtualAudioEnd-int64(len(ftyp))))
	moovStart := virtualAudioEnd
	virtualFileSize := moovStart + int64(len(moov))

	var maxSingleRead int64
	readRange := func(_ context.Context, start, end int64) ([]byte, error) {
		reqLen := end - start + 1
		if reqLen > maxSingleRead {
			maxSingleRead = reqLen
		}
		out := make([]byte, reqLen)
		for i := int64(0); i < reqLen; i++ {
			pos := start + i
			switch {
			case pos < int64(len(ftyp)):
				out[i] = ftyp[pos]
			case pos < int64(len(ftyp))+16:
				out[i] = mdatHdr[pos-int64(len(ftyp))]
			case pos >= moovStart && pos < virtualFileSize:
				out[i] = moov[pos-moovStart]
			default:
				out[i] = byte(pos & 0xff)
			}
		}
		return out, nil
	}

	clip, name, err := SliceSnippet(context.Background(), readRange, virtualFileSize, 10005, 10015)
	if err != nil {
		t.Fatalf("SliceSnippet on 8 MiB moov failed: %v", err)
	}
	if name != "snippet.m4a" || len(clip) == 0 {
		t.Fatalf("unexpected clip name=%q len=%d", name, len(clip))
	}
	// Verify no single range read ever loaded the 8 MB stsz table (stco is 80 KB, stsz window is <1 KB).
	if maxSingleRead > 128<<10 {
		t.Fatalf("maxSingleRead = %d bytes, expected <= 128 KiB", maxSingleRead)
	}
}

func TestMalformedBoxesRejected(t *testing.T) {
	t.Parallel()

	// 1. Overflowing 64-bit box size.
	var bad64 [24]byte
	binary.BigEndian.PutUint32(bad64[0:4], 1)
	copy(bad64[4:8], "moov")
	binary.BigEndian.PutUint64(bad64[8:16], ^uint64(0))
	if _, err := ReadMetadataSeeker(bytes.NewReader(bad64[:]), int64(len(bad64))); err == nil {
		t.Fatal("expected error for overflowing 64-bit box size")
	}

	// 2. Box smaller than its 8-byte header.
	var smallBox [16]byte
	binary.BigEndian.PutUint32(smallBox[0:4], 4)
	copy(smallBox[4:8], "moov")
	if _, err := ReadMetadataSeeker(bytes.NewReader(smallBox[:]), int64(len(smallBox))); err == nil {
		t.Fatal("expected error for box size < header")
	}

	// 3. Valid non-overlapping control fixture succeeds.
	validM4B := buildCustomStblM4B(4, 1, []uint32{256, 256, 256, 256}, []uint32{4096, 4352, 4608, 4864}, 4)
	serveHeaderAndAudio := func(hdr []byte) RangeFunc {
		var fullHdr bytes.Buffer
		fullHdr.Write(hdr)
		putU32(&fullHdr, 0)
		fullHdr.WriteString("mdat")
		hdrBytes := fullHdr.Bytes()
		return func(_ context.Context, start, end int64) ([]byte, error) {
			out := make([]byte, end-start+1)
			if start < int64(len(hdrBytes)) {
				endHdr := int64(len(hdrBytes))
				if end+1 < endHdr {
					endHdr = end + 1
				}
				copy(out, hdrBytes[start:endHdr])
			}
			for i := range out {
				if start+int64(i) >= int64(len(hdrBytes)) {
					out[i] = 0x55
				}
			}
			return out, nil
		}
	}
	if clip, _, err := SliceSnippet(context.Background(), serveHeaderAndAudio(validM4B), int64(len(validM4B))+8192, 0, 4); err != nil || len(clip) == 0 {
		t.Fatalf("expected valid non-overlapping fixture to succeed, got len=%d err=%v", len(clip), err)
	}

	// 4. Overlapping chunks (identical chunk offsets) are rejected even when individual sizes are small.
	overlapSmallM4B := buildCustomStblM4B(4, 1, []uint32{256, 256, 256, 256}, []uint32{4096, 4096, 4096, 4096}, 4)
	if _, _, err := SliceSnippet(context.Background(), serveHeaderAndAudio(overlapSmallM4B), int64(len(overlapSmallM4B))+8192, 0, 4); err == nil || !strings.Contains(err.Error(), "overlapping") {
		t.Fatalf("expected overlapping chunk error, got %v", err)
	}

	// 5. Cumulative sample sizes exceeding maxClipBytes (2 MiB) are rejected.
	oversizeM4B := buildCustomStblM4B(3, 1, []uint32{1 << 20, 1 << 20, 1 << 20}, []uint32{4096, 4096 + (1 << 20), 4096 + (2 << 20)}, 3)
	if _, _, err := SliceSnippet(context.Background(), serveHeaderAndAudio(oversizeM4B), int64(len(oversizeM4B))+(4<<20), 0, 3); err == nil || !strings.Contains(err.Error(), "exceeds maximum clip size") {
		t.Fatalf("expected clip size limit error, got %v", err)
	}

	// 6. Inconsistent sample counts (stts sample count < stsz sample count) must return an error, not panic.
	mismatchM4B := buildCustomStblM4B(2, 1, []uint32{128, 128}, []uint32{4096, 4224}, 1)
	if _, _, err := SliceSnippet(context.Background(), serveHeaderAndAudio(mismatchM4B), int64(len(mismatchM4B))+8192, 0, 2); err == nil || !strings.Contains(err.Error(), "inconsistent stts and stsz sample counts") {
		t.Fatalf("expected stts/stsz mismatch error, got %v", err)
	}
}

func buildCustomStblM4B(stszSamples, samplesPerChunk uint32, sizes, chunkOffsets []uint32, sttsSamples uint32) []byte {
	ftyp := makeBox("ftyp", func(b *bytes.Buffer) {
		b.WriteString("M4B ")
		putU32(b, 0)
	})
	stsd := makeFullBox("stsd", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 1)
		b.Write(makeBox("mp4a", func(mb *bytes.Buffer) {
			mb.Write(make([]byte, 28))
		}))
	})
	stts := makeFullBox("stts", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 1)
		putU32(b, sttsSamples)
		putU32(b, 1000)
	})
	stsc := makeFullBox("stsc", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 1)
		putU32(b, 1)
		putU32(b, samplesPerChunk)
		putU32(b, 1)
	})
	stsz := makeFullBox("stsz", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 0)
		putU32(b, stszSamples)
		for _, sz := range sizes {
			putU32(b, sz)
		}
	})
	stco := makeFullBox("stco", 0, 0, func(b *bytes.Buffer) {
		putU32(b, uint32(len(chunkOffsets)))
		for _, off := range chunkOffsets {
			putU32(b, off)
		}
	})
	stbl := makeBox("stbl", func(b *bytes.Buffer) {
		b.Write(stsd)
		b.Write(stts)
		b.Write(stsc)
		b.Write(stsz)
		b.Write(stco)
	})
	minf := makeBox("minf", func(b *bytes.Buffer) { b.Write(stbl) })
	mdhd := makeFullBox("mdhd", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 0)
		putU32(b, 0)
		putU32(b, 1000)
		putU32(b, stszSamples*1000)
	})
	hdlr := makeFullBox("hdlr", 0, 0, func(b *bytes.Buffer) {
		putU32(b, 0)
		b.WriteString("soun")
		b.Write(make([]byte, 12))
	})
	mdia := makeBox("mdia", func(b *bytes.Buffer) {
		b.Write(mdhd)
		b.Write(hdlr)
		b.Write(minf)
	})
	trak := makeBox("trak", func(b *bytes.Buffer) { b.Write(mdia) })
	moov := makeBox("moov", func(b *bytes.Buffer) { b.Write(trak) })
	var out bytes.Buffer
	out.Write(ftyp)
	out.Write(moov)
	return out.Bytes()
}
