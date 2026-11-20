package store

import (
	"errors"
	"time"
)

var ErrMetadataMergeConflict = errors.New("store: metadata heads are superseded")

// The fields a reader may override on a work.
const (
	MetadataTitle       = "title"
	MetadataAuthor      = "author"
	MetadataDescription = "description"
)

// WorkMetadataWrite is one edit of one override field. Value is canonical
// JSON: a string, or the literal null for "no override".
type WorkMetadataWrite struct {
	ID       string
	WriterID string
	Revision int64
	Field    string
	Value    string
}

type WorkMetadataField struct {
	ID        string
	Field     string
	Value     string
	UpdatedAt time.Time
}

// WorkCoverOverride is a reader's own cover for a work. An empty SHA256 is
// the tombstone the reader wrote to say "none": Data is then nil.
type WorkCoverOverride struct {
	SHA256    string
	Data      []byte
	UpdatedAt time.Time
}

// WorkMetadata is everything a reader has said about one work's metadata.
// A nil Cover means no cover choice was ever recorded, which is not the
// same as a tombstone.
type WorkMetadata struct {
	Fields []WorkMetadataField
	Cover  *WorkCoverOverride
}
