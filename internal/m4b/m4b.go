package m4b

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// RangeFunc reads an inclusive byte range [start, end] from an audio file.
type RangeFunc func(ctx context.Context, start, end int64) ([]byte, error)

// SeekerRangeReader adapts an io.ReadSeeker and file size into a RangeFunc.
func SeekerRangeReader(r io.ReadSeeker, fileSize int64) RangeFunc {
	return func(_ context.Context, start, end int64) ([]byte, error) {
		if start < 0 || end < start || end >= fileSize {
			return nil, fmt.Errorf("m4b: range %d..%d out of bounds (size %d)", start, end, fileSize)
		}
		length := end - start + 1
		if _, err := r.Seek(start, io.SeekStart); err != nil {
			return nil, err
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return buf, nil
	}
}

// Chapter is one timed chapter in an audiobook.
type Chapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Metadata holds extracted M4B container tags, duration, and chapter markers.
type Metadata struct {
	Title      string
	Authors    []string
	Narrators  []string
	SeriesName string
	ASIN       string
	ISBN       string
	Duration   float64
	Chapters   []Chapter
}

const (
	maxTopBoxes       = 64
	maxChildBoxes     = 256
	maxChapters       = 2000
	maxTotalSamples   = 20_000_000
	maxWindowSamples  = 65_536
	maxSnippetSamples = 4096
	maxStsdBytes      = 16 << 10
	maxSttsBytes      = 512 << 10
	maxStscBytes      = 512 << 10
	maxStcoBytes      = 1 << 20
	maxStszWinBytes   = 256 << 10
	maxChplBytes      = 256 << 10
	maxIlstBytes      = 512 << 10
	maxTagValueBytes  = 16 << 10
	maxCueBytes       = 512 << 10
	maxClipBytes      = 2 << 20
)

type boxRef struct {
	typ         string
	offset      int64
	size        int64
	hdrLen      int64
	payloadOff  int64
	payloadSize int64
}

func readBoxHeader(ctx context.Context, readRange RangeFunc, pos, limit int64) (boxRef, error) {
	if pos < 0 || pos+8 > limit {
		return boxRef{}, errors.New("m4b: truncated box header")
	}
	hdrEnd := pos + 15
	if hdrEnd >= limit {
		hdrEnd = limit - 1
	}
	hdr, err := readRange(ctx, pos, hdrEnd)
	if err != nil {
		return boxRef{}, err
	}
	if len(hdr) < 8 {
		return boxRef{}, errors.New("m4b: short box header")
	}
	sz32 := binary.BigEndian.Uint32(hdr[0:4])
	typ := string(hdr[4:8])
	var boxSize int64
	hdrLen := int64(8)
	switch sz32 {
	case 1:
		if len(hdr) < 16 {
			return boxRef{}, errors.New("m4b: truncated 64-bit box header")
		}
		u64 := binary.BigEndian.Uint64(hdr[8:16])
		if u64 > math.MaxInt64 {
			return boxRef{}, errors.New("m4b: overflowing 64-bit box size")
		}
		boxSize = int64(u64)
		hdrLen = 16
	case 0:
		boxSize = limit - pos
	default:
		boxSize = int64(sz32)
	}
	if boxSize < hdrLen || pos+boxSize > limit || pos+boxSize < pos {
		return boxRef{}, fmt.Errorf("m4b: invalid %q box size %d at %d", typ, boxSize, pos)
	}
	return boxRef{
		typ:         typ,
		offset:      pos,
		size:        boxSize,
		hdrLen:      hdrLen,
		payloadOff:  pos + hdrLen,
		payloadSize: boxSize - hdrLen,
	}, nil
}

func listChildBoxes(ctx context.Context, readRange RangeFunc, start, limit int64, maxCount int) ([]boxRef, error) {
	var out []boxRef
	pos := start
	for step := 0; step < maxCount && pos+8 <= limit; step++ {
		b, err := readBoxHeader(ctx, readRange, pos, limit)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
		pos += b.size
	}
	return out, nil
}

func readBoxPayload(ctx context.Context, readRange RangeFunc, b boxRef, maxBytes int64) ([]byte, error) {
	if b.payloadSize < 0 || b.payloadSize > maxBytes {
		return nil, fmt.Errorf("m4b: %q payload size %d exceeds limit %d", b.typ, b.payloadSize, maxBytes)
	}
	if b.payloadSize == 0 {
		return nil, nil
	}
	return readRange(ctx, b.payloadOff, b.payloadOff+b.payloadSize-1)
}

func findMoovBox(ctx context.Context, readRange RangeFunc, fileSize int64) (boxRef, error) {
	boxes, err := listChildBoxes(ctx, readRange, 0, fileSize, maxTopBoxes)
	if err != nil {
		return boxRef{}, err
	}
	for _, b := range boxes {
		if b.typ == "moov" {
			return b, nil
		}
	}
	return boxRef{}, errors.New("m4b: moov box not found")
}

// ReadMetadataSeeker extracts duration, iTunes tags, and chapters from an M4B ReadSeeker.
func ReadMetadataSeeker(r io.ReadSeeker, fileSize int64) (Metadata, error) {
	return ReadMetadata(context.Background(), SeekerRangeReader(r, fileSize), fileSize)
}

// ReadMetadata extracts duration, iTunes tags, and chapters (`chpl` in `moov/udta`
// or `trak/udta`, plus QuickTime `text`/`sbtl` chapter tracks) without reading
// large sample tables or cover art into memory.
func ReadMetadata(ctx context.Context, readRange RangeFunc, fileSize int64) (Metadata, error) {
	if fileSize < 16 {
		return Metadata{}, errors.New("m4b: file too small")
	}
	moov, err := findMoovBox(ctx, readRange, fileSize)
	if err != nil {
		return Metadata{}, err
	}
	moovChildren, err := listChildBoxes(ctx, readRange, moov.payloadOff, moov.offset+moov.size, maxChildBoxes)
	if err != nil {
		return Metadata{}, err
	}

	var meta Metadata
	var textTrackChapters []Chapter

	for _, child := range moovChildren {
		switch child.typ {
		case "mvhd":
			p, err := readBoxPayload(ctx, readRange, child, 256)
			if err != nil {
				return Metadata{}, err
			}
			dur, err := parseMvhdDuration(p)
			if err != nil {
				return Metadata{}, err
			}
			meta.Duration = dur

		case "udta":
			parseUdtaMetadata(ctx, readRange, child, &meta)

		case "trak":
			trakChildren, err := listChildBoxes(ctx, readRange, child.payloadOff, child.offset+child.size, maxChildBoxes)
			if err != nil {
				continue
			}
			for _, tc := range trakChildren {
				switch tc.typ {
				case "udta":
					parseUdtaMetadata(ctx, readRange, tc, &meta)
				case "mdia":
					if chs, ok := parseTextTrackChapters(ctx, readRange, tc); ok && len(chs) > len(textTrackChapters) {
						textTrackChapters = chs
					}
				}
			}
		}
	}

	if len(meta.Chapters) == 0 && len(textTrackChapters) > 0 {
		meta.Chapters = textTrackChapters
	}
	finalizeChapters(&meta.Chapters, meta.Duration)
	return meta, nil
}

func parseMvhdDuration(p []byte) (float64, error) {
	if len(p) < 20 {
		return 0, errors.New("m4b: short mvhd")
	}
	ver := p[0]
	switch ver {
	case 0:
		ts := binary.BigEndian.Uint32(p[12:16])
		dur := binary.BigEndian.Uint32(p[16:20])
		if ts == 0 {
			return 0, errors.New("m4b: zero mvhd timescale")
		}
		return float64(dur) / float64(ts), nil
	case 1:
		if len(p) < 32 {
			return 0, errors.New("m4b: short v1 mvhd")
		}
		ts := binary.BigEndian.Uint32(p[20:24])
		dur := binary.BigEndian.Uint64(p[24:32])
		if ts == 0 {
			return 0, errors.New("m4b: zero mvhd timescale")
		}
		return float64(dur) / float64(ts), nil
	default:
		return 0, fmt.Errorf("m4b: unsupported mvhd version %d", ver)
	}
}

func parseUdtaMetadata(ctx context.Context, readRange RangeFunc, udta boxRef, meta *Metadata) {
	children, err := listChildBoxes(ctx, readRange, udta.payloadOff, udta.offset+udta.size, maxChildBoxes)
	if err != nil {
		return
	}
	for _, c := range children {
		switch c.typ {
		case "chpl":
			p, err := readBoxPayload(ctx, readRange, c, maxChplBytes)
			if err != nil {
				continue
			}
			if chs := parseChplPayload(p); len(chs) > len(meta.Chapters) {
				meta.Chapters = chs
			}
		case "meta":
			if c.payloadSize <= 4 {
				continue
			}
			metaChildren, err := listChildBoxes(ctx, readRange, c.payloadOff+4, c.offset+c.size, maxChildBoxes)
			if err != nil {
				continue
			}
			for _, mc := range metaChildren {
				if mc.typ == "ilst" {
					parseIlst(ctx, readRange, mc, meta)
				}
			}
		}
	}
}

func parseChplPayload(p []byte) []Chapter {
	if len(p) < 9 {
		return nil
	}
	ver := p[0]
	var count int
	var pos int
	if ver == 0 {
		count = int(p[4])
		pos = 5
	} else {
		// FFmpeg writes a 1-byte chapter count at offset 8 (after 4-byte version/flags and 4-byte reserved).
		// Some encoders write a 32-bit big-endian count at offset 8..12 when byte 8 is 0.
		if p[8] != 0 {
			count = int(p[8])
			pos = 9
		} else if len(p) >= 12 {
			c32 := binary.BigEndian.Uint32(p[8:12])
			if c32 > 0 && c32 <= maxChapters {
				count = int(c32)
				pos = 12
			}
		}
	}
	if count <= 0 || count > maxChapters {
		return nil
	}
	out := make([]Chapter, 0, count)
	for i := 0; i < count && pos+9 <= len(p); i++ {
		rawStart := binary.BigEndian.Uint64(p[pos : pos+8])
		titleLen := int(p[pos+8])
		pos += 9
		if pos+titleLen > len(p) {
			break
		}
		title := strings.TrimSpace(cleanUTF8(p[pos : pos+titleLen]))
		pos += titleLen
		startSec := float64(rawStart) / 1e7
		out = append(out, Chapter{
			Title: title,
			Start: startSec,
		})
	}
	return out
}

func parseIlst(ctx context.Context, readRange RangeFunc, ilst boxRef, meta *Metadata) {
	items, err := listChildBoxes(ctx, readRange, ilst.payloadOff, ilst.offset+ilst.size, maxChildBoxes)
	if err != nil {
		return
	}
	for _, item := range items {
		if item.typ == "covr" || item.payloadSize <= 0 || item.payloadSize > maxIlstBytes {
			continue
		}
		if item.typ == "----" {
			mean, name, val := parseFreeformAtom(ctx, readRange, item)
			_ = mean
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "asin", "audible_asin":
				if meta.ASIN == "" {
					meta.ASIN = strings.TrimSpace(val)
				}
			case "isbn":
				if meta.ISBN == "" {
					meta.ISBN = strings.TrimSpace(val)
				}
			case "series", "MVNM":
				if meta.SeriesName == "" {
					meta.SeriesName = strings.TrimSpace(val)
				}
			case "narrator", "composer":
				if len(meta.Narrators) == 0 && strings.TrimSpace(val) != "" {
					meta.Narrators = splitNames(val)
				}
			}
			continue
		}
		val := readDataAtomString(ctx, readRange, item)
		if val == "" {
			continue
		}
		switch item.typ {
		case "\xa9nam":
			if meta.Title == "" {
				meta.Title = val
			}
		case "\xa9ART", "aART":
			if len(meta.Authors) == 0 {
				meta.Authors = splitNames(val)
			}
		case "\xa9wrt", "\xa9nrt":
			if len(meta.Narrators) == 0 {
				meta.Narrators = splitNames(val)
			}
		case "\xa9grp", "tvsh":
			if meta.SeriesName == "" {
				meta.SeriesName = val
			}
		}
	}
}

func readDataAtomString(ctx context.Context, readRange RangeFunc, item boxRef) string {
	children, err := listChildBoxes(ctx, readRange, item.payloadOff, item.offset+item.size, 16)
	if err != nil {
		return ""
	}
	for _, c := range children {
		if c.typ != "data" || c.payloadSize <= 8 || c.payloadSize > maxTagValueBytes {
			continue
		}
		p, err := readBoxPayload(ctx, readRange, c, maxTagValueBytes)
		if err != nil || len(p) <= 8 {
			continue
		}
		// data atom header: 4-byte type indicator + 4-byte locale; UTF-8 text is type 1.
		dataType := binary.BigEndian.Uint32(p[0:4])
		if dataType != 1 {
			continue
		}
		return strings.TrimSpace(cleanUTF8(p[8:]))
	}
	return ""
}

func parseFreeformAtom(ctx context.Context, readRange RangeFunc, item boxRef) (mean, name, val string) {
	children, err := listChildBoxes(ctx, readRange, item.payloadOff, item.offset+item.size, 16)
	if err != nil {
		return "", "", ""
	}
	for _, c := range children {
		if c.payloadSize <= 4 || c.payloadSize > maxTagValueBytes {
			continue
		}
		p, err := readBoxPayload(ctx, readRange, c, maxTagValueBytes)
		if err != nil {
			continue
		}
		switch c.typ {
		case "mean":
			mean = strings.TrimSpace(cleanUTF8(p[4:]))
		case "name":
			name = strings.TrimSpace(cleanUTF8(p[4:]))
		case "data":
			if len(p) > 8 && binary.BigEndian.Uint32(p[0:4]) == 1 {
				val = strings.TrimSpace(cleanUTF8(p[8:]))
			}
		}
	}
	return mean, name, val
}

func splitNames(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ';' || r == '/' || r == ','
	}) {
		p := strings.TrimSpace(part)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 && strings.TrimSpace(s) != "" {
		return []string{strings.TrimSpace(s)}
	}
	return out
}

func parseTextTrackChapters(ctx context.Context, readRange RangeFunc, mdia boxRef) ([]Chapter, bool) {
	mdiaChildren, err := listChildBoxes(ctx, readRange, mdia.payloadOff, mdia.offset+mdia.size, maxChildBoxes)
	if err != nil {
		return nil, false
	}
	var timescale uint32
	isText := false
	var stblRef *boxRef
	for _, mc := range mdiaChildren {
		switch mc.typ {
		case "mdhd":
			p, err := readBoxPayload(ctx, readRange, mc, 128)
			if err != nil || len(p) < 16 {
				return nil, false
			}
			if p[0] == 0 {
				timescale = binary.BigEndian.Uint32(p[12:16])
			} else if p[0] == 1 && len(p) >= 24 {
				timescale = binary.BigEndian.Uint32(p[20:24])
			}
		case "hdlr":
			p, err := readBoxPayload(ctx, readRange, mc, 1024)
			if err != nil || len(p) < 12 {
				return nil, false
			}
			sub := string(p[8:12])
			if sub == "text" || sub == "sbtl" {
				isText = true
			}
		case "minf":
			minfChildren, err := listChildBoxes(ctx, readRange, mc.payloadOff, mc.offset+mc.size, maxChildBoxes)
			if err != nil {
				return nil, false
			}
			for i := range minfChildren {
				if minfChildren[i].typ == "stbl" {
					stblRef = &minfChildren[i]
				}
			}
		}
	}
	if !isText || timescale == 0 || stblRef == nil {
		return nil, false
	}

	stblChildren, err := listChildBoxes(ctx, readRange, stblRef.payloadOff, stblRef.offset+stblRef.size, maxChildBoxes)
	if err != nil {
		return nil, false
	}
	var (
		stts   []sttsEntry
		stsc   []stscEntry
		sizes  []uint32
		chunks []uint64
	)
	for _, sc := range stblChildren {
		switch sc.typ {
		case "stts":
			p, err := readBoxPayload(ctx, readRange, sc, 64<<10)
			if err != nil {
				return nil, false
			}
			stts, err = decodeStts(p, maxChapters)
			if err != nil {
				return nil, false
			}
		case "stsc":
			p, err := readBoxPayload(ctx, readRange, sc, 64<<10)
			if err != nil {
				return nil, false
			}
			stsc, err = decodeStsc(p, maxChapters)
			if err != nil {
				return nil, false
			}
		case "stsz":
			p, err := readBoxPayload(ctx, readRange, sc, 64<<10)
			if err != nil || len(p) < 12 {
				return nil, false
			}
			defSize := binary.BigEndian.Uint32(p[4:8])
			n := binary.BigEndian.Uint32(p[8:12])
			if n == 0 || n > maxChapters {
				return nil, false
			}
			sizes = make([]uint32, n)
			if defSize == 0 {
				if int(n)*4+12 > len(p) {
					return nil, false
				}
				for i := uint32(0); i < n; i++ {
					sizes[i] = binary.BigEndian.Uint32(p[12+i*4 : 16+i*4])
				}
			} else {
				for i := range sizes {
					sizes[i] = defSize
				}
			}
		case "stco":
			p, err := readBoxPayload(ctx, readRange, sc, 64<<10)
			if err != nil {
				return nil, false
			}
			chunks, err = decodeStco(p, maxChapters)
			if err != nil {
				return nil, false
			}
		case "co64":
			p, err := readBoxPayload(ctx, readRange, sc, 64<<10)
			if err != nil {
				return nil, false
			}
			chunks, err = decodeCo64(p, maxChapters)
			if err != nil {
				return nil, false
			}
		}
	}
	if len(stts) == 0 || len(stsc) == 0 || len(sizes) == 0 || len(chunks) == 0 {
		return nil, false
	}

	// Compute start timestamp of each text sample.
	totalSamples := len(sizes)
	starts := make([]float64, 0, totalSamples)
	deltas := make([]float64, 0, totalSamples)
	var curTick uint64
	for _, e := range stts {
		for c := uint32(0); c < e.count && len(starts) < totalSamples; c++ {
			starts = append(starts, float64(curTick)/float64(timescale))
			deltas = append(deltas, float64(e.delta)/float64(timescale))
			curTick += uint64(e.delta)
		}
	}
	if len(starts) == 0 {
		return nil, false
	}

	// Resolve file offsets of each text sample and read its title.
	var out []Chapter
	curSample := 0
	numChunks := uint32(len(chunks))
	for i, sc := range stsc {
		if sc.firstChunk == 0 {
			continue
		}
		lastChunk := numChunks
		if i+1 < len(stsc) && stsc[i+1].firstChunk > 0 {
			lastChunk = stsc[i+1].firstChunk - 1
		}
		for ch := sc.firstChunk; ch <= lastChunk && ch <= numChunks; ch++ {
			chunkOff := chunks[ch-1]
			for k := uint32(0); k < sc.samplesPerChunk && curSample < len(starts); k++ {
				sz := sizes[curSample]
				if sz >= 2 && sz <= 4096 {
					raw, err := readRange(ctx, int64(chunkOff), int64(chunkOff)+int64(sz)-1)
					if err == nil && len(raw) >= 2 {
						strLen := int(binary.BigEndian.Uint16(raw[0:2]))
						if 2+strLen <= len(raw) {
							title := strings.TrimSpace(cleanUTF8(raw[2 : 2+strLen]))
							if title != "" {
								out = append(out, Chapter{
									Title: title,
									Start: starts[curSample],
									End:   starts[curSample] + deltas[curSample],
								})
							}
						}
					}
				}
				chunkOff += uint64(sz)
				curSample++
			}
		}
	}
	return out, len(out) > 0
}

func finalizeChapters(chapters *[]Chapter, totalDuration float64) {
	chs := *chapters
	if len(chs) == 0 {
		return
	}
	cleaned := make([]Chapter, 0, len(chs))
	for i, c := range chs {
		title := strings.TrimSpace(c.Title)
		if title == "" {
			title = fmt.Sprintf("Chapter %d", i+1)
		}
		if c.Start < 0 || math.IsNaN(c.Start) || math.IsInf(c.Start, 0) {
			continue
		}
		if len(cleaned) > 0 && c.Start <= cleaned[len(cleaned)-1].Start {
			continue
		}
		cleaned = append(cleaned, Chapter{
			Title: title,
			Start: c.Start,
			End:   c.End,
		})
	}
	for i := range cleaned {
		if i+1 < len(cleaned) {
			cleaned[i].End = cleaned[i+1].Start
		} else if totalDuration > cleaned[i].Start {
			cleaned[i].End = totalDuration
		} else if cleaned[i].End <= cleaned[i].Start {
			cleaned[i].End = cleaned[i].Start + 1
		}
	}
	*chapters = cleaned
}

var (
	cueTrackRe = regexp.MustCompile(`(?i)^\s*TRACK\s+(\d+)\s+AUDIO\b`)
	cueTitleRe = regexp.MustCompile(`(?i)^\s*TITLE\s+"([^"]*)"`)
	cueIndexRe = regexp.MustCompile(`(?i)^\s*INDEX\s+01\s+(\d+):(\d{2}):(\d{2})\b`)
)

// ParseCueChapters parses a companion .cue sheet into chapters.
func ParseCueChapters(cueBytes []byte, totalDuration float64) []Chapter {
	if len(cueBytes) == 0 || len(cueBytes) > maxCueBytes {
		return nil
	}
	lines := strings.Split(cleanUTF8(cueBytes), "\n")
	type cueTrack struct {
		title    string
		start    float64
		hasIndex bool
	}
	var (
		tracks  []cueTrack
		current *cueTrack
	)
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if m := cueTrackRe.FindStringSubmatch(line); m != nil {
			if current != nil && current.hasIndex {
				tracks = append(tracks, *current)
				if len(tracks) >= maxChapters {
					break
				}
			}
			current = &cueTrack{}
			continue
		}
		if current == nil {
			continue
		}
		if m := cueTitleRe.FindStringSubmatch(line); m != nil {
			current.title = strings.TrimSpace(m[1])
			continue
		}
		if m := cueIndexRe.FindStringSubmatch(line); m != nil {
			mm, _ := strconv.Atoi(m[1])
			ss, _ := strconv.Atoi(m[2])
			ff, _ := strconv.Atoi(m[3])
			current.start = float64(mm)*60.0 + float64(ss) + float64(ff)/75.0
			current.hasIndex = true
		}
	}
	if current != nil && current.hasIndex && len(tracks) < maxChapters {
		tracks = append(tracks, *current)
	}
	if len(tracks) == 0 {
		return nil
	}
	out := make([]Chapter, len(tracks))
	for i, tr := range tracks {
		out[i] = Chapter{
			Title: tr.title,
			Start: tr.start,
		}
	}
	finalizeChapters(&out, totalDuration)
	return out
}

func cleanUTF8(b []byte) string {
	b = bytes.TrimRight(b, "\x00")
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "")
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

type soundTrackMeta struct {
	timescale      uint32
	stsdBox        []byte
	stts           []sttsEntry
	stsc           []stscEntry
	chunks         []uint64
	defSampleSize  uint32
	totalSamples   uint32
	stszDataOffset int64
	stszDataLimit  int64
}

func decodeStts(p []byte, maxEntries uint32) ([]sttsEntry, error) {
	if len(p) < 8 {
		return nil, errors.New("m4b: short stts")
	}
	n := binary.BigEndian.Uint32(p[4:8])
	if n == 0 || n > maxEntries || int64(n)*8+8 > int64(len(p)) {
		return nil, errors.New("m4b: invalid stts entry count")
	}
	out := make([]sttsEntry, n)
	for i := uint32(0); i < n; i++ {
		off := 8 + i*8
		out[i] = sttsEntry{
			count: binary.BigEndian.Uint32(p[off : off+4]),
			delta: binary.BigEndian.Uint32(p[off+4 : off+8]),
		}
	}
	return out, nil
}

func decodeStsc(p []byte, maxEntries uint32) ([]stscEntry, error) {
	if len(p) < 8 {
		return nil, errors.New("m4b: short stsc")
	}
	n := binary.BigEndian.Uint32(p[4:8])
	if n == 0 || n > maxEntries || int64(n)*12+8 > int64(len(p)) {
		return nil, errors.New("m4b: invalid stsc entry count")
	}
	out := make([]stscEntry, n)
	for i := uint32(0); i < n; i++ {
		off := 8 + i*12
		out[i] = stscEntry{
			firstChunk:      binary.BigEndian.Uint32(p[off : off+4]),
			samplesPerChunk: binary.BigEndian.Uint32(p[off+4 : off+8]),
			descIndex:       binary.BigEndian.Uint32(p[off+8 : off+12]),
		}
	}
	return out, nil
}

func decodeStco(p []byte, maxEntries uint32) ([]uint64, error) {
	if len(p) < 8 {
		return nil, errors.New("m4b: short stco")
	}
	n := binary.BigEndian.Uint32(p[4:8])
	if n == 0 || n > maxEntries || int64(n)*4+8 > int64(len(p)) {
		return nil, errors.New("m4b: invalid stco entry count")
	}
	out := make([]uint64, n)
	for i := uint32(0); i < n; i++ {
		off := 8 + i*4
		out[i] = uint64(binary.BigEndian.Uint32(p[off : off+4]))
	}
	return out, nil
}

func decodeCo64(p []byte, maxEntries uint32) ([]uint64, error) {
	if len(p) < 8 {
		return nil, errors.New("m4b: short co64")
	}
	n := binary.BigEndian.Uint32(p[4:8])
	if n == 0 || n > maxEntries || int64(n)*8+8 > int64(len(p)) {
		return nil, errors.New("m4b: invalid co64 entry count")
	}
	out := make([]uint64, n)
	for i := uint32(0); i < n; i++ {
		off := 8 + i*8
		out[i] = binary.BigEndian.Uint64(p[off : off+8])
	}
	return out, nil
}

// SliceSnippet extracts a self-contained .m4a audio clip covering [t0, t1] seconds
// from an M4B/M4A/MP4 container using bounded box-header walking and a targeted
// stsz sample-size window read (never loading the full moov or full stsz table).
func SliceSnippet(ctx context.Context, readRange RangeFunc, fileSize int64, t0, t1 float64) ([]byte, string, error) {
	if fileSize < 16 {
		return nil, "", errors.New("m4b: file too small")
	}
	if t0 < 0 {
		t0 = 0
	}
	if t1 <= t0 {
		return nil, "", errors.New("m4b: invalid snippet window")
	}

	moov, err := findMoovBox(ctx, readRange, fileSize)
	if err != nil {
		return nil, "", err
	}
	trk, err := locateSoundTrack(ctx, readRange, moov)
	if err != nil {
		return nil, "", err
	}
	return extractWindowedM4A(ctx, readRange, fileSize, trk, t0, t1)
}

func locateSoundTrack(ctx context.Context, readRange RangeFunc, moov boxRef) (*soundTrackMeta, error) {
	children, err := listChildBoxes(ctx, readRange, moov.payloadOff, moov.offset+moov.size, maxChildBoxes)
	if err != nil {
		return nil, err
	}
	for _, c := range children {
		if c.typ != "trak" {
			continue
		}
		trk, ok, err := inspectSoundTrak(ctx, readRange, c)
		if err != nil {
			return nil, err
		}
		if ok {
			return trk, nil
		}
	}
	return nil, errors.New("m4b: no sound track in moov")
}

func inspectSoundTrak(ctx context.Context, readRange RangeFunc, trak boxRef) (*soundTrackMeta, bool, error) {
	trakChildren, err := listChildBoxes(ctx, readRange, trak.payloadOff, trak.offset+trak.size, maxChildBoxes)
	if err != nil {
		return nil, false, err
	}
	for _, tc := range trakChildren {
		if tc.typ != "mdia" {
			continue
		}
		mdiaChildren, err := listChildBoxes(ctx, readRange, tc.payloadOff, tc.offset+tc.size, maxChildBoxes)
		if err != nil {
			return nil, false, err
		}
		var (
			timescale uint32
			isSound   bool
			stblRef   *boxRef
		)
		for _, mc := range mdiaChildren {
			switch mc.typ {
			case "mdhd":
				p, err := readBoxPayload(ctx, readRange, mc, 128)
				if err != nil || len(p) < 16 {
					return nil, false, errors.New("m4b: short mdhd")
				}
				if p[0] == 0 {
					timescale = binary.BigEndian.Uint32(p[12:16])
				} else if p[0] == 1 && len(p) >= 24 {
					timescale = binary.BigEndian.Uint32(p[20:24])
				}
			case "hdlr":
				p, err := readBoxPayload(ctx, readRange, mc, 1024)
				if err != nil || len(p) < 12 {
					return nil, false, errors.New("m4b: short hdlr")
				}
				if string(p[8:12]) == "soun" {
					isSound = true
				}
			case "minf":
				minfChildren, err := listChildBoxes(ctx, readRange, mc.payloadOff, mc.offset+mc.size, maxChildBoxes)
				if err != nil {
					return nil, false, err
				}
				for i := range minfChildren {
					if minfChildren[i].typ == "stbl" {
						stblRef = &minfChildren[i]
					}
				}
			}
		}
		if !isSound {
			return nil, false, nil
		}
		if timescale == 0 || stblRef == nil {
			return nil, false, errors.New("m4b: incomplete sound track")
		}
		trk, err := parseSoundStbl(ctx, readRange, *stblRef, timescale)
		if err != nil {
			return nil, false, err
		}
		return trk, true, nil
	}
	return nil, false, nil
}

func parseSoundStbl(ctx context.Context, readRange RangeFunc, stbl boxRef, timescale uint32) (*soundTrackMeta, error) {
	children, err := listChildBoxes(ctx, readRange, stbl.payloadOff, stbl.offset+stbl.size, maxChildBoxes)
	if err != nil {
		return nil, err
	}
	t := &soundTrackMeta{timescale: timescale}
	for _, c := range children {
		switch c.typ {
		case "stsd":
			if c.size > maxStsdBytes {
				return nil, fmt.Errorf("m4b: stsd size %d exceeds limit", c.size)
			}
			raw, err := readRange(ctx, c.offset, c.offset+c.size-1)
			if err != nil {
				return nil, err
			}
			t.stsdBox = raw

		case "stts":
			p, err := readBoxPayload(ctx, readRange, c, maxSttsBytes)
			if err != nil {
				return nil, err
			}
			t.stts, err = decodeStts(p, maxSttsBytes/8)
			if err != nil {
				return nil, err
			}

		case "stsc":
			p, err := readBoxPayload(ctx, readRange, c, maxStscBytes)
			if err != nil {
				return nil, err
			}
			t.stsc, err = decodeStsc(p, maxStscBytes/12)
			if err != nil {
				return nil, err
			}

		case "stsz":
			if c.payloadSize < 12 {
				return nil, errors.New("m4b: short stsz")
			}
			hdr, err := readRange(ctx, c.payloadOff, c.payloadOff+11)
			if err != nil {
				return nil, err
			}
			t.defSampleSize = binary.BigEndian.Uint32(hdr[4:8])
			t.totalSamples = binary.BigEndian.Uint32(hdr[8:12])
			if t.totalSamples == 0 || t.totalSamples > maxTotalSamples {
				return nil, fmt.Errorf("m4b: invalid stsz sample count %d", t.totalSamples)
			}
			if t.defSampleSize == 0 {
				needBytes := int64(t.totalSamples)*4 + 12
				if c.payloadSize < needBytes {
					return nil, errors.New("m4b: truncated stsz table")
				}
				t.stszDataOffset = c.payloadOff + 12
				t.stszDataLimit = c.offset + c.size
			}

		case "stco":
			p, err := readBoxPayload(ctx, readRange, c, maxStcoBytes)
			if err != nil {
				return nil, err
			}
			t.chunks, err = decodeStco(p, maxStcoBytes/4)
			if err != nil {
				return nil, err
			}

		case "co64":
			p, err := readBoxPayload(ctx, readRange, c, maxStcoBytes)
			if err != nil {
				return nil, err
			}
			t.chunks, err = decodeCo64(p, maxStcoBytes/8)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(t.stsdBox) == 0 || len(t.stts) == 0 || len(t.stsc) == 0 || t.totalSamples == 0 || len(t.chunks) == 0 {
		return nil, errors.New("m4b: sound track missing required sample tables")
	}
	return t, nil
}

type sampleSpan struct {
	offset uint64
	size   uint32
	delta  uint32
}

func extractWindowedM4A(ctx context.Context, readRange RangeFunc, fileSize int64, t *soundTrackMeta, t0, t1 float64) ([]byte, string, error) {
	tick0 := uint64(t0 * float64(t.timescale))
	tick1 := uint64(t1 * float64(t.timescale))
	if tick1 <= tick0 {
		tick1 = tick0 + uint64(t.timescale)
	}

	totalSamples := int(t.totalSamples)
	var sttsTotal uint64
	for _, e := range t.stts {
		sttsTotal += uint64(e.count)
	}
	if sttsTotal != uint64(t.totalSamples) {
		return nil, "", errors.New("m4b: inconsistent stts and stsz sample counts")
	}

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
				if nextTick >= tick1 || len(deltas) >= maxSnippetSamples {
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
	if s0 >= 0 && s0+len(deltas) < s1 {
		s1 = s0 + len(deltas)
	}
	if s0 < 0 || s1 <= s0 || len(deltas) != s1-s0 {
		return nil, "", errors.New("m4b: no audio samples in requested window")
	}

	// Find the chunk covering s0 so we know the earliest sample index whose size is needed.
	firstNeededSample := -1
	curSample := 0
	numChunks := uint32(len(t.chunks))
	for i, sc := range t.stsc {
		if sc.firstChunk == 0 || sc.samplesPerChunk == 0 {
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
			firstNeededSample = curSample
			break
		}
		if firstNeededSample >= 0 {
			break
		}
	}
	if firstNeededSample < 0 || s1-firstNeededSample > maxWindowSamples {
		return nil, "", errors.New("m4b: sample window exceeds chunk bounds")
	}

	// Read only the required stsz window [firstNeededSample, s1).
	windowLen := s1 - firstNeededSample
	windowSizes := make([]uint32, windowLen)
	if t.defSampleSize != 0 {
		for i := range windowSizes {
			windowSizes[i] = t.defSampleSize
		}
	} else {
		byteStart := t.stszDataOffset + int64(firstNeededSample)*4
		byteEnd := t.stszDataOffset + int64(s1)*4 - 1
		if byteEnd-byteStart+1 > maxStszWinBytes || byteEnd >= t.stszDataLimit {
			return nil, "", errors.New("m4b: stsz window out of bounds")
		}
		rawSizes, err := readRange(ctx, byteStart, byteEnd)
		if err != nil {
			return nil, "", err
		}
		if len(rawSizes) < windowLen*4 {
			return nil, "", errors.New("m4b: short stsz window read")
		}
		for i := 0; i < windowLen; i++ {
			windowSizes[i] = binary.BigEndian.Uint32(rawSizes[i*4 : (i+1)*4])
		}
	}

	// Resolve byte spans for samples [s0, s1).
	spans := make([]sampleSpan, 0, s1-s0)
	var totalSampleBytes uint64
	curSample = 0
	for i, sc := range t.stsc {
		if sc.firstChunk == 0 || sc.samplesPerChunk == 0 {
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
				if curSample < firstNeededSample || curSample-firstNeededSample >= len(windowSizes) {
					return nil, "", errors.New("m4b: sample index out of stsz window")
				}
				sz := windowSizes[curSample-firstNeededSample]
				if uint64(sz) > maxClipBytes || chunkOff > uint64(fileSize) || uint64(sz) > uint64(fileSize)-chunkOff {
					return nil, "", errors.New("m4b: sample span exceeds file or clip bounds")
				}
				if curSample >= s0 && curSample < s1 {
					if curSample-s0 >= len(deltas) {
						return nil, "", errors.New("m4b: sample index out of stts window")
					}
					if len(spans) > 0 {
						prev := spans[len(spans)-1]
						if chunkOff < prev.offset+uint64(prev.size) {
							return nil, "", errors.New("m4b: non-monotonic or overlapping sample offset")
						}
					}
					totalSampleBytes += uint64(sz)
					if totalSampleBytes > maxClipBytes {
						return nil, "", errors.New("m4b: cumulative sample payload exceeds maximum clip size")
					}
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
	if len(spans) == 0 || totalSampleBytes == 0 || totalSampleBytes > maxClipBytes {
		return nil, "", errors.New("m4b: could not resolve sample offsets")
	}

	minOff := spans[0].offset
	maxEnd := spans[len(spans)-1].offset + uint64(spans[len(spans)-1].size)
	if maxEnd <= minOff || int64(maxEnd) > fileSize || maxEnd-minOff > maxClipBytes {
		return nil, "", fmt.Errorf("m4b: sample byte span %d..%d out of bounds", minOff, maxEnd)
	}

	rawRange, err := readRange(ctx, int64(minOff), int64(maxEnd)-1)
	if err != nil {
		return nil, "", err
	}
	if uint64(len(rawRange)) < maxEnd-minOff {
		return nil, "", errors.New("m4b: short range read for audio samples")
	}

	var mdatPayload bytes.Buffer
	mdatPayload.Grow(int(totalSampleBytes))
	for _, sp := range spans {
		if sp.offset < minOff || sp.offset+uint64(sp.size) > maxEnd {
			return nil, "", errors.New("m4b: non-monotonic sample offset")
		}
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
			putU32(b, 0)
			putU32(b, 0)
			putU32(b, timescale)
			putU32(b, totalDuration)
			putU32(b, 0x00010000)
			putU16(b, 0x0100)
			b.Write(make([]byte, 10))
			for _, v := range []uint32{0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000} {
				putU32(b, v)
			}
			b.Write(make([]byte, 24))
			putU32(b, 2)
		})
		tkhd := makeFullBox("tkhd", 0, 3, func(b *bytes.Buffer) {
			putU32(b, 0)
			putU32(b, 0)
			putU32(b, 1)
			putU32(b, 0)
			putU32(b, totalDuration)
			b.Write(make([]byte, 8))
			putU16(b, 0)
			putU16(b, 0)
			putU16(b, 0x0100)
			putU16(b, 0)
			for _, v := range []uint32{0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000} {
				putU32(b, v)
			}
			putU32(b, 0)
			putU32(b, 0)
		})
		mdhd := makeFullBox("mdhd", 0, 0, func(b *bytes.Buffer) {
			putU32(b, 0)
			putU32(b, 0)
			putU32(b, timescale)
			putU32(b, totalDuration)
			putU16(b, 0x55c4)
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
			putU32(b, 1)
			putU32(b, uint32(len(spans)))
			putU32(b, 1)
		})
		stsz := makeFullBox("stsz", 0, 0, func(b *bytes.Buffer) {
			putU32(b, 0)
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

func makeBox(typ string, body func(*bytes.Buffer)) []byte {
	var payload bytes.Buffer
	body(&payload)
	var out bytes.Buffer
	putU32(&out, uint32(8+payload.Len()))
	out.WriteString(typ[:4])
	out.Write(payload.Bytes())
	return out.Bytes()
}

func makeFullBox(typ string, version byte, flags uint32, body func(*bytes.Buffer)) []byte {
	return makeBox(typ, func(b *bytes.Buffer) {
		vf := (uint32(version) << 24) | (flags & 0x00ffffff)
		putU32(b, vf)
		body(b)
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
