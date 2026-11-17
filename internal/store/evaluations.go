package store

import (
	"errors"
	"time"
)

var ErrEvaluationMergeConflict = errors.New("store: evaluation heads are superseded")

type EvaluationWrite struct {
	ID       string
	WriterID string
	Revision int64
	Field    string
	Value    string
}

type EvaluationField struct {
	ID        string
	Field     string
	Value     string
	UpdatedAt time.Time
}
