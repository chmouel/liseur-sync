package audiobookshelf

import (
	"context"
	"errors"
	"strings"

	"github.com/chmouel/liseur-sync/internal/m4b"
)

// RangeReader reads an inclusive byte range [start, end] of an item's audio file.
type RangeReader func(ctx context.Context, itemID, ino string, start, end int64) ([]byte, error)

const maxClipBytes = 2 << 20

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

	return m4b.SliceSnippet(ctx, func(ctx context.Context, start, end int64) ([]byte, error) {
		return readRange(ctx, item.ID, target.Ino, start, end)
	}, target.Size, localT0, localT1)
}
