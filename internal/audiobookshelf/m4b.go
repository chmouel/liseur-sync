package audiobookshelf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// RangeReader reads an inclusive byte range [start, end] of an item's audio file.
type RangeReader func(ctx context.Context, itemID, ino string, start, end int64) ([]byte, error)

const (
	maxMoovBytes = 6 << 20
	maxClipBytes = 2 << 20
)

// SliceAudioSnippet extracts a short audio clip [t0, t1] (in seconds) from an
// audiobook item using HTTP Range requests, returning a self-contained .m4a or
// .mp3 payload suitable for speech-to-text transcription.
func SliceAudioSnippet(ctx context.Context, readRange RangeReader, item Item, t0, t1 float64) ([]byte, string, error) {
	if len(item.AudioFiles) == 0 {
		return nil, "", errors.New("audiobookshelf: item has no audio files")
	}
	if t0 < 0 {
		t0 = 0
	}
	if t1 <= t0 {
		return nil, "", errors.New("audiobookshelf: invalid snippet window")
	}

	// Locate the audio file covering t0.
	offset := 0.0
	var target AudioFile
	found := false
	for _, af := range item.AudioFiles {
		if af.Duration <= 0 {
			continue
		}
		if t0 < offset+af.Duration || af == item.AudioFiles[len(item.AudioFiles)-1] {
			target = af
			found = true
			break
		}
		offset += af.Duration
	}
	if !found || target.Ino == "" || target.Size <= 0 {
		return nil, "", errors.New("audiobookshelf: no readable audio file for timestamp")
	}

	localT0 := t0 - offset
	localT1 := t1 - offset
	if localT0 < 0 {
		localT0 = 0
	}
	if localT1 > target.Duration {
		localT1 = target.Duration
	}
	if localT1 <= localT0 {
		return nil, "", errors.New("audiobookshelf: empty local snippet window")
	}

	ext := strings.ToLower(strings.TrimPrefix(target.Ext, "."))
	mime := strings.ToLower(target.MimeType)
	if ext == "mp3" || strings.Contains(mime, "mpeg") || strings.Contains(mime, "mp3") {
		b0 := int64((localT0 / target.Duration) * float64(target.Size))
		b1 := int64((localT1 / target.Duration) * float64(target.Size))
		if b1 <= b0 {
			b1 = b0 + 32768
		}
		if b1 >= target.Size {
			b1 = target.Size - 1
		}
		if b1-b0+1 > maxClipBytes {
			b1 = b0 + maxClipBytes - 1
		}
		raw, err := readRange(ctx, item.ID, target.Ino, b0, b1)
		if err != nil {
			return nil, "", err
		}
		return raw, "snippet.mp3", nil
	}

	// ISO BMFF (M4B / M4A / MP4) extraction.
	moovPayload, err := fetchMoovBox(ctx, readRange, item.ID, target.Ino, target.Size)
	if err != nil {
		return nil, "", err
	}
	trk, err := parseSoundTrack(moovPayload)
	if err != nil {
		return nil, "", err
	}
	return extractM4ASnippet(ctx, readRange, item.ID, target.Ino, target.Size, trk, localT0, localT1)
}

func fetchMoovBox(ctx context.Context, readRange RangeReader, itemID, ino string, fileSize int64) ([]byte, error) {
	pos := int64(0)
	for step := 0; step < 32 && pos+8 <= fileSize; step++ {
		hdrEnd := pos + 15
		if hdrEnd >= fileSize {
			hdrEnd = fileSize - 1
		}
		hdr, err := readRange(ctx, itemID, ino, pos, hdrEnd)
		if err != nil {
			return nil, err
		}
		if len(hdr) < 8 {
			break
		}
		boxSize := int64(binary.BigEndian.Uint32(hdr[0:4]))
		boxType := string(hdr[4:8])
		hdrLen := int64(8)
		if boxSize == 1 {
			if len(hdr) < 16 {
				return nil, errors.New("audiobookshelf: truncated 64-bit box header")
			}
			boxSize = int64(binary.BigEndian.Uint64(hdr[8:16]))
			hdrLen = 16
		} else if boxSize == 0 {
			boxSize = fileSize - pos
		}
		if boxSize < hdrLen || pos+boxSize > fileSize {
			return nil, fmt.Errorf("audiobookshelf: invalid %q box size %d", boxType, boxSize)
		}
		if boxType == "moov" {
			payloadSize := boxSize - hdrLen
			if payloadSize <= 0 || payloadSize > maxMoovBytes {
				return nil, fmt.Errorf("audiobookshelf: moov size %d out of bounds", payloadSize)
			}
			return readRange(ctx, itemID, ino, pos+hdrLen, pos+boxSize-1)
		}
		pos += boxSize
	}
	return nil, errors.New("audiobookshelf: moov box not found")
}

type mp4Track struct {
	timescale uint32
	stsdBox   []byte
	stts      []sttsEntry
	stsc      []stscEntry
	defSize   uint32
	sizes     []uint32
	chunks    []uint64
}

type sttsEntry struct {
	count uint32
	delta uint32
}

type stscEntry struct {
	firstChunk      uint32
	samplesPerChunk uint32
	descIndex       uint32
}

func parseSoundTrack(moov []byte) (*mp4Track, error) {
	for _, b := range iterBoxes(moov) {
		if b.typ != "trak" {
			continue
		}
		trk, ok, err := parseTrak(b.payload)
		if err != nil {
			return nil, err
		}
		if ok {
			return trk, nil
		}
	}
	return nil, errors.New("audiobookshelf: no sound track in moov")
}

func parseTrak(trak []byte) (*mp4Track, bool, error) {
	for _, b := range iterBoxes(trak) {
		if b.typ != "mdia" {
			continue
		}
		var timescale uint32
		isSound := false
		var stbl []byte
		for _, mb := range iterBoxes(b.payload) {
			switch mb.typ {
			case "mdhd":
				if len(mb.payload) < 16 {
					return nil, false, errors.New("audiobookshelf: short mdhd")
				}
				ver := mb.payload[0]
				if ver == 0 {
					timescale = binary.BigEndian.Uint32(mb.payload[12:16])
				} else if ver == 1 && len(mb.payload) >= 24 {
					timescale = binary.BigEndian.Uint32(mb.payload[20:24])
				}
			case "hdlr":
				if len(mb.payload) >= 12 && string(mb.payload[8:12]) == "soun" {
					isSound = true
				}
			case "minf":
				for _, nb := range iterBoxes(mb.payload) {
					if nb.typ == "stbl" {
						stbl = nb.payload
					}
				}
			}
		}
		if !isSound {
			return nil, false, nil
		}
		if timescale == 0 || len(stbl) == 0 {
			return nil, false, errors.New("audiobookshelf: incomplete sound track")
		}
		trk, err := parseStbl(stbl, timescale)
		return trk, err == nil, err
	}
	return nil, false, nil
}

func parseStbl(stbl []byte, timescale uint32) (*mp4Track, error) {
	t := &mp4Track{timescale: timescale}
	for _, b := range iterBoxes(stbl) {
		p := b.payload
		switch b.typ {
		case "stsd":
			t.stsdBox = b.raw
		case "stts":
			if len(p) < 8 {
				return nil, errors.New("audiobookshelf: short stts")
			}
			n := binary.BigEndian.Uint32(p[4:8])
			if int(n)*8+8 > len(p) {
				return nil, errors.New("audiobookshelf: truncated stts")
			}
			t.stts = make([]sttsEntry, n)
			for i := uint32(0); i < n; i++ {
				off := 8 + i*8
				t.stts[i] = sttsEntry{
					count: binary.BigEndian.Uint32(p[off : off+4]),
					delta: binary.BigEndian.Uint32(p[off+4 : off+8]),
				}
			}
		case "stsc":
			if len(p) < 8 {
				return nil, errors.New("audiobookshelf: short stsc")
			}
			n := binary.BigEndian.Uint32(p[4:8])
			if int(n)*12+8 > len(p) {
				return nil, errors.New("audiobookshelf: truncated stsc")
			}
			t.stsc = make([]stscEntry, n)
			for i := uint32(0); i < n; i++ {
				off := 8 + i*12
				t.stsc[i] = stscEntry{
					firstChunk:      binary.BigEndian.Uint32(p[off : off+4]),
					samplesPerChunk: binary.BigEndian.Uint32(p[off+4 : off+8]),
					descIndex:       binary.BigEndian.Uint32(p[off+8 : off+12]),
				}
			}
		case "stsz":
			if len(p) < 12 {
				return nil, errors.New("audiobookshelf: short stsz")
			}
			t.defSize = binary.BigEndian.Uint32(p[4:8])
			n := binary.BigEndian.Uint32(p[8:12])
			if t.defSize == 0 {
				if int(n)*4+12 > len(p) {
					return nil, errors.New("audiobookshelf: truncated stsz")
				}
				t.sizes = make([]uint32, n)
				for i := uint32(0); i < n; i++ {
					off := 12 + i*4
					t.sizes[i] = binary.BigEndian.Uint32(p[off : off+4])
				}
			} else {
				t.sizes = make([]uint32, n)
				for i := range t.sizes {
					t.sizes[i] = t.defSize
				}
			}
		case "stco":
			if len(p) < 8 {
				return nil, errors.New("audiobookshelf: short stco")
			}
			n := binary.BigEndian.Uint32(p[4:8])
			if int(n)*4+8 > len(p) {
				return nil, errors.New("audiobookshelf: truncated stco")
			}
			t.chunks = make([]uint64, n)
			for i := uint32(0); i < n; i++ {
				off := 8 + i*4
				t.chunks[i] = uint64(binary.BigEndian.Uint32(p[off : off+4]))
			}
		case "co64":
			if len(p) < 8 {
				return nil, errors.New("audiobookshelf: short co64")
			}
			n := binary.BigEndian.Uint32(p[4:8])
			if int(n)*8+8 > len(p) {
				return nil, errors.New("audiobookshelf: truncated co64")
			}
			t.chunks = make([]uint64, n)
			for i := uint32(0); i < n; i++ {
				off := 8 + i*8
				t.chunks[i] = binary.BigEndian.Uint64(p[off : off+8])
			}
		}
	}
	if len(t.stsdBox) == 0 || len(t.stts) == 0 || len(t.stsc) == 0 || len(t.sizes) == 0 || len(t.chunks) == 0 {
		return nil, errors.New("audiobookshelf: sound track missing required sample tables")
	}
	return t, nil
}

type sampleSpan struct {
	offset uint64
	size   uint32
	delta  uint32
}

func extractM4ASnippet(ctx context.Context, readRange RangeReader, itemID, ino string, fileSize int64, t *mp4Track, t0, t1 float64) ([]byte, string, error) {
	tick0 := uint64(t0 * float64(t.timescale))
	tick1 := uint64(t1 * float64(t.timescale))
	if tick1 <= tick0 {
		tick1 = tick0 + uint64(t.timescale)
	}

	// Map sample indices [s0, s1) and their deltas from stts.
	totalSamples := len(t.sizes)
	s0, s1 := -1, totalSamples
	var curTick uint64
	sampleIdx := 0
	var deltas []uint32
	for _, e := range t.stts {
		for c := uint32(0); c < e.count && sampleIdx < totalSamples; c++ {
			nextTick := curTick + uint64(e.delta)
			if s0 < 0 && nextTick > tick0 {
				s0 = sampleIdx
			}
			if s0 >= 0 && sampleIdx < s1 {
				deltas = append(deltas, e.delta)
				if nextTick >= tick1 {
					s1 = sampleIdx + 1
					break
				}
			}
			curTick = nextTick
			sampleIdx++
		}
		if s1 < totalSamples {
			break
		}
	}
	if s0 < 0 || s1 <= s0 {
		return nil, "", errors.New("audiobookshelf: no audio samples in requested window")
	}

	// Walk chunks via stsc + chunks + sizes to find byte offsets of samples [s0, s1).
	spans := make([]sampleSpan, 0, s1-s0)
	curSample := 0
	numChunks := uint32(len(t.chunks))
	for i, sc := range t.stsc {
		if sc.firstChunk == 0 {
			continue
		}
		lastChunk := numChunks
		if i+1 < len(t.stsc) && t.stsc[i+1].firstChunk > 0 {
			lastChunk = t.stsc[i+1].firstChunk - 1
		}
		for ch := sc.firstChunk; ch <= lastChunk && ch <= numChunks; ch++ {
			chunkEndSample := curSample + int(sc.samplesPerChunk)
			if chunkEndSample <= s0 {
				curSample = chunkEndSample
				continue
			}
			chunkOff := t.chunks[ch-1]
			for k := uint32(0); k < sc.samplesPerChunk && curSample < totalSamples; k++ {
				sz := t.sizes[curSample]
				if curSample >= s0 && curSample < s1 {
					spans = append(spans, sampleSpan{
						offset: chunkOff,
						size:   sz,
						delta:  deltas[curSample-s0],
					})
				}
				chunkOff += uint64(sz)
				curSample++
				if curSample >= s1 {
					break
				}
			}
			if curSample >= s1 {
				break
			}
		}
		if curSample >= s1 {
			break
		}
	}
	if len(spans) == 0 {
		return nil, "", errors.New("audiobookshelf: could not resolve sample offsets")
	}

	minOff := spans[0].offset
	maxEnd := spans[len(spans)-1].offset + uint64(spans[len(spans)-1].size)
	if maxEnd <= minOff || int64(maxEnd) > fileSize || maxEnd-minOff > maxClipBytes {
		return nil, "", fmt.Errorf("audiobookshelf: sample byte span %d..%d out of bounds", minOff, maxEnd)
	}

	rawRange, err := readRange(ctx, itemID, ino, int64(minOff), int64(maxEnd)-1)
	if err != nil {
		return nil, "", err
	}
	if uint64(len(rawRange)) < maxEnd-minOff {
		return nil, "", errors.New("audiobookshelf: short range read for audio samples")
	}

	var mdatPayload bytes.Buffer
	for _, sp := range spans {
		rel := sp.offset - minOff
		mdatPayload.Write(rawRange[rel : rel+uint64(sp.size)])
	}

	m4a := buildSingleChunkM4A(t.timescale, t.stsdBox, spans, mdatPayload.Bytes())
	return m4a, "snippet.m4a", nil
}

func buildSingleChunkM4A(timescale uint32, stsdBox []byte, spans []sampleSpan, mdatPayload []byte) []byte {
	var totalDuration uint32
	for _, sp := range spans {
		totalDuration += sp.delta
	}

	ftyp := makeBox("ftyp", func(b *bytes.Buffer) {
		b.WriteString("M4A ")
		putU32(b, 0x200)
		b.WriteString("M4A isommp42")
	})

	buildMoov := func(chunkOffset uint32) []byte {
		mvhd := makeFullBox("mvhd", 0, 0, func(b *bytes.Buffer) {
			putU32(b, 0)             // creation_time
			putU32(b, 0)             // modification_time
			putU32(b, timescale)     // timescale
			putU32(b, totalDuration) // duration
			putU32(b, 0x00010000)    // rate = 1.0
			putU16(b, 0x0100)        // volume = 1.0
			b.Write(make([]byte, 10))
			for _, v := range []uint32{0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000} {
				putU32(b, v)
			}
			b.Write(make([]byte, 24))
			putU32(b, 2) // next_track_ID
		})
		tkhd := makeFullBox("tkhd", 0, 3, func(b *bytes.Buffer) {
			putU32(b, 0)             // creation_time
			putU32(b, 0)             // modification_time
			putU32(b, 1)             // track_ID
			putU32(b, 0)             // reserved
			putU32(b, totalDuration) // duration
			b.Write(make([]byte, 8))
			putU16(b, 0)      // layer
			putU16(b, 0)      // alternate_group
			putU16(b, 0x0100) // volume = 1.0
			putU16(b, 0)
			for _, v := range []uint32{0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000} {
				putU32(b, v)
			}
			putU32(b, 0) // width
			putU32(b, 0) // height
		})
		mdhd := makeFullBox("mdhd", 0, 0, func(b *bytes.Buffer) {
			putU32(b, 0)
			putU32(b, 0)
			putU32(b, timescale)
			putU32(b, totalDuration)
			putU16(b, 0x55c4) // "und"
			putU16(b, 0)
		})
		hdlr := makeFullBox("hdlr", 0, 0, func(b *bytes.Buffer) {
			putU32(b, 0)
			b.WriteString("soun")
			b.Write(make([]byte, 12))
			b.WriteString("SoundHandler\x00")
		})
		smhd := makeFullBox("smhd", 0, 0, func(b *bytes.Buffer) {
			putU16(b, 0)
			putU16(b, 0)
		})
		dinf := makeBox("dinf", func(b *bytes.Buffer) {
			b.Write(makeFullBox("dref", 0, 0, func(d *bytes.Buffer) {
				putU32(d, 1)
				d.Write(makeFullBox("url ", 0, 1, func(*bytes.Buffer) {}))
			}))
		})

		// Run-length encode stts deltas.
		var rle []sttsEntry
		for _, sp := range spans {
			if len(rle) > 0 && rle[len(rle)-1].delta == sp.delta {
				rle[len(rle)-1].count++
			} else {
				rle = append(rle, sttsEntry{count: 1, delta: sp.delta})
			}
		}
		stts := makeFullBox("stts", 0, 0, func(b *bytes.Buffer) {
			putU32(b, uint32(len(rle)))
			for _, e := range rle {
				putU32(b, e.count)
				putU32(b, e.delta)
			}
		})
		stsc := makeFullBox("stsc", 0, 0, func(b *bytes.Buffer) {
			putU32(b, 1)
			putU32(b, 1)                  // first_chunk
			putU32(b, uint32(len(spans))) // samples_per_chunk
			putU32(b, 1)                  // sample_description_index
		})
		stsz := makeFullBox("stsz", 0, 0, func(b *bytes.Buffer) {
			putU32(b, 0) // variable sample size
			putU32(b, uint32(len(spans)))
			for _, sp := range spans {
				putU32(b, sp.size)
			}
		})
		stco := makeFullBox("stco", 0, 0, func(b *bytes.Buffer) {
			putU32(b, 1)
			putU32(b, chunkOffset)
		})
		stbl := makeBox("stbl", func(b *bytes.Buffer) {
			b.Write(stsdBox)
			b.Write(stts)
			b.Write(stsc)
			b.Write(stsz)
			b.Write(stco)
		})
		minf := makeBox("minf", func(b *bytes.Buffer) {
			b.Write(smhd)
			b.Write(dinf)
			b.Write(stbl)
		})
		mdia := makeBox("mdia", func(b *bytes.Buffer) {
			b.Write(mdhd)
			b.Write(hdlr)
			b.Write(minf)
		})
		trak := makeBox("trak", func(b *bytes.Buffer) {
			b.Write(tkhd)
			b.Write(mdia)
		})
		return makeBox("moov", func(b *bytes.Buffer) {
			b.Write(mvhd)
			b.Write(trak)
		})
	}

	moov0 := buildMoov(0)
	mdatOffset := uint32(len(ftyp) + len(moov0) + 8)
	moov := buildMoov(mdatOffset)
	mdat := makeBox("mdat", func(b *bytes.Buffer) {
		b.Write(mdatPayload)
	})

	var out bytes.Buffer
	out.Write(ftyp)
	out.Write(moov)
	out.Write(mdat)
	return out.Bytes()
}

type mp4Box struct {
	typ     string
	raw     []byte
	payload []byte
}

func iterBoxes(buf []byte) []mp4Box {
	var out []mp4Box
	for len(buf) >= 8 {
		sz := int(binary.BigEndian.Uint32(buf[0:4]))
		typ := string(buf[4:8])
		hdr := 8
		if sz == 1 {
			if len(buf) < 16 {
				break
			}
			sz = int(binary.BigEndian.Uint64(buf[8:16]))
			hdr = 16
		} else if sz == 0 {
			sz = len(buf)
		}
		if sz < hdr || sz > len(buf) {
			break
		}
		out = append(out, mp4Box{
			typ:     typ,
			raw:     buf[:sz],
			payload: buf[hdr:sz],
		})
		buf = buf[sz:]
	}
	return out
}

func makeBox(typ string, fn func(*bytes.Buffer)) []byte {
	var body bytes.Buffer
	fn(&body)
	var out bytes.Buffer
	putU32(&out, uint32(8+body.Len()))
	out.WriteString(typ)
	out.Write(body.Bytes())
	return out.Bytes()
}

func makeFullBox(typ string, version byte, flags uint32, fn func(*bytes.Buffer)) []byte {
	return makeBox(typ, func(b *bytes.Buffer) {
		b.WriteByte(version)
		b.WriteByte(byte(flags >> 16))
		b.WriteByte(byte(flags >> 8))
		b.WriteByte(byte(flags))
		fn(b)
	})
}

func putU32(b *bytes.Buffer, v uint32) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	b.Write(tmp[:])
}

func putU16(b *bytes.Buffer, v uint16) {
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], v)
	b.Write(tmp[:])
}
