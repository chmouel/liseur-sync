package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func testEvaluations(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := context.Background()
	u := MkUser(t, s, "reader")
	other := MkUser(t, s, "other")
	w := MkWork(t, s, u, "book", "edition")
	revisions := map[string]int64{"rate-1": 1, "review-1": 2, "rate-2": 3, "review-delete": 4}
	put := func(id, field, value string) {
		t.Helper()
		if err := s.WriteEvaluation(ctx, u.ID, w.ID, store.EvaluationWrite{ID: id, WriterID: "installation", Revision: revisions[id], Field: field, Value: value}); err != nil {
			t.Fatal(err)
		}
	}
	put("rate-1", "rating", "9")
	put("review-1", "review", `"Read again"`)
	put("rate-2", "rating", "6")
	put("rate-1", "rating", "9")
	fields, err := s.ReadEvaluation(ctx, u.ID, w.ID)
	if err != nil || len(fields) != 2 || fields[0].Value != "6" || fields[1].Value != `"Read again"` {
		t.Fatalf("independent edits and delayed retry: %+v %v", fields, err)
	}
	if err := s.WriteEvaluation(ctx, u.ID, w.ID, store.EvaluationWrite{ID: "rate-1", WriterID: "installation", Revision: 1, Field: "rating", Value: "8"}); !errors.Is(err, store.ErrIDMismatch) {
		t.Fatalf("id mismatch: %v", err)
	}
	put("review-delete", "review", "null")
	put("review-1", "review", `"Read again"`)
	fields, err = s.ReadEvaluation(ctx, u.ID, w.ID)
	if err != nil || fields[1].Value != "null" {
		t.Fatalf("deletion resurrected: %+v %v", fields, err)
	}
	if _, err := s.ReadEvaluation(ctx, other.ID, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user read: %v", err)
	}
	if err := s.WriteEvaluation(ctx, other.ID, w.ID, store.EvaluationWrite{ID: "other", WriterID: "installation", Revision: 1, Field: "rating", Value: "2"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user write: %v", err)
	}
	survivor := MkWork(t, s, u, "survivor", "other-edition")
	if err := s.MergeWorks(ctx, u.ID, w.ID, survivor.ID); err != nil {
		t.Fatal(err)
	}
	fields, err = s.ReadEvaluation(ctx, u.ID, survivor.ID)
	if err != nil || len(fields) != 2 || fields[1].Value != "null" {
		t.Fatalf("merge lost evaluation: %+v %v", fields, err)
	}
	if err := s.WriteEvaluation(ctx, u.ID, survivor.ID, store.EvaluationWrite{ID: "review-1", WriterID: "installation", Revision: 2, Field: "review", Value: `"Read again"`}); err != nil {
		t.Fatal(err)
	}
	fields, _ = s.ReadEvaluation(ctx, u.ID, survivor.ID)
	if fields[1].Value != "null" {
		t.Fatal("merge lost retry evidence")
	}
	folder := MkFolder(t, s, "evaluation-gc", store.FolderCalibre)
	calibreID := int64(1)
	now := time.Now()
	doReconcile(t, s, folder.ID, []store.ObservedBook{
		{CalibreID: &calibreID, RelativePath: "Other/book.epub", SizeBytes: 1, MTime: now, ContentSHA256: "other", Title: "Other"},
	}, true, now)
	if _, err := s.WorkByID(ctx, u.ID, survivor.ID); err != nil {
		t.Fatalf("catalog collection lost an evaluation: %v", err)
	}

	t.Run("late first delivery after a work merge", func(t *testing.T) {
		from := MkWork(t, s, u, "late-from", "late-from-edition")
		into := MkWork(t, s, u, "late-into", "late-into-edition")
		deletion := store.EvaluationWrite{ID: "newer-removal", WriterID: "same-installation", Revision: 20, Field: "review", Value: "null"}
		if err := s.WriteEvaluation(ctx, u.ID, from.ID, deletion); err != nil {
			t.Fatal(err)
		}
		if err := s.MergeWorks(ctx, u.ID, from.ID, into.ID); err != nil {
			t.Fatal(err)
		}
		late := store.EvaluationWrite{ID: "late-first-delivery", WriterID: deletion.WriterID, Revision: 10, Field: "review", Value: `"Obsolete review"`}
		for range 2 {
			if err := s.WriteEvaluation(ctx, u.ID, into.ID, late); err != nil {
				t.Fatal(err)
			}
		}
		fields, err := s.ReadEvaluation(ctx, u.ID, into.ID)
		if err != nil || len(fields) != 1 || fields[0].ID != deletion.ID {
			t.Fatalf("late request restored a review: %+v %v", fields, err)
		}
		late.Revision++
		if err := s.WriteEvaluation(ctx, u.ID, into.ID, late); !errors.Is(err, store.ErrIDMismatch) {
			t.Fatalf("changed retry revision: %v", err)
		}
		late.Revision--
		late.WriterID = "another-installation"
		if err := s.WriteEvaluation(ctx, u.ID, into.ID, late); !errors.Is(err, store.ErrIDMismatch) {
			t.Fatalf("changed retry writer: %v", err)
		}
		late.ID = "another-writer-edit"
		late.Revision = 1
		if err := s.WriteEvaluation(ctx, u.ID, into.ID, late); err != nil {
			t.Fatal(err)
		}
		fields, err = s.ReadEvaluation(ctx, u.ID, into.ID)
		if err != nil || len(fields) != 1 || fields[0].ID != late.ID {
			t.Fatalf("other writer did not win in receipt order: %+v %v", fields, err)
		}
		late.ID = "independent-rating"
		late.WriterID = deletion.WriterID
		late.Field = "rating"
		late.Value = "9"
		if err := s.WriteEvaluation(ctx, u.ID, into.ID, late); err != nil {
			t.Fatal(err)
		}
		fields, err = s.ReadEvaluation(ctx, u.ID, into.ID)
		if err != nil || len(fields) != 2 || fields[0].Value != "9" {
			t.Fatalf("revision ordering crossed fields: %+v %v", fields, err)
		}
	})

	for _, scenario := range []struct {
		name              string
		newerInSource     bool
		otherWriter       bool
		interveningWriter bool
	}{
		{name: "newer destination revision"},
		{name: "newer source revision", newerInSource: true},
		{name: "different writers use receipt order", otherWriter: true},
		{name: "obsolete source behind another writer", interveningWriter: true},
		{name: "obsolete destination behind another writer", newerInSource: true, interveningWriter: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			from := MkWork(t, s, u, scenario.name+"-from", scenario.name+"-from-edition")
			into := MkWork(t, s, u, scenario.name+"-into", scenario.name+"-into-edition")
			newerWork, olderWork := into, from
			if scenario.newerInSource {
				newerWork, olderWork = from, into
			}
			deletion := store.EvaluationWrite{ID: scenario.name + "-deletion", WriterID: "merge-writer", Revision: 20, Field: "review", Value: "null"}
			late := store.EvaluationWrite{ID: scenario.name + "-late", WriterID: deletion.WriterID, Revision: 10, Field: "review", Value: `"Obsolete review"`}
			if scenario.otherWriter {
				late.WriterID = "other-merge-writer"
			}
			if err := s.WriteEvaluation(ctx, u.ID, newerWork.ID, deletion); err != nil {
				t.Fatal(err)
			}
			var intervening *store.EvaluationWrite
			if scenario.interveningWriter {
				intervening = &store.EvaluationWrite{ID: scenario.name + "-current", WriterID: "intervening-writer", Revision: 1, Field: "review", Value: `"Current review"`}
				if err := s.WriteEvaluation(ctx, u.ID, newerWork.ID, *intervening); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.WriteEvaluation(ctx, u.ID, olderWork.ID, late); err != nil {
				t.Fatal(err)
			}
			if err := s.MergeWorks(ctx, u.ID, from.ID, into.ID); err != nil {
				t.Fatal(err)
			}
			want := deletion
			if intervening != nil {
				want = *intervening
			}
			if scenario.otherWriter {
				want = late
			}
			for _, replay := range []store.EvaluationWrite{deletion, late} {
				if err := s.WriteEvaluation(ctx, u.ID, into.ID, replay); err != nil {
					t.Fatal(err)
				}
			}
			fields, err := s.ReadEvaluation(ctx, u.ID, into.ID)
			if err != nil || len(fields) != 1 || fields[0].ID != want.ID || fields[0].Value != want.Value {
				t.Fatalf("merged field: %+v %v; want %+v", fields, err, want)
			}
		})
	}

	t.Run("superseded heads require a fresh choice before merge", func(t *testing.T) {
		from := MkWork(t, s, u, "cyclic-from", "cyclic-from-edition")
		into := MkWork(t, s, u, "cyclic-into", "cyclic-into-edition")
		writes := []struct {
			work string
			item store.EvaluationWrite
		}{
			{from.ID, store.EvaluationWrite{ID: "cyclic-a20", WriterID: "cyclic-a", Revision: 20, Field: "review", Value: "null"}},
			{into.ID, store.EvaluationWrite{ID: "cyclic-b20", WriterID: "cyclic-b", Revision: 20, Field: "review", Value: `"Current review"`}},
			{into.ID, store.EvaluationWrite{ID: "cyclic-a10", WriterID: "cyclic-a", Revision: 10, Field: "review", Value: `"Obsolete A"`}},
			{from.ID, store.EvaluationWrite{ID: "cyclic-b10", WriterID: "cyclic-b", Revision: 10, Field: "review", Value: `"Obsolete B"`}},
		}
		for _, write := range writes {
			if err := s.WriteEvaluation(ctx, u.ID, write.work, write.item); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.MergeWorks(ctx, u.ID, from.ID, into.ID); !errors.Is(err, store.ErrEvaluationMergeConflict) {
			t.Fatalf("merge with no eligible head: %v", err)
		}
		for _, work := range []store.Work{from, into} {
			if _, err := s.WorkByID(ctx, u.ID, work.ID); err != nil {
				t.Fatalf("conflict removed a work: %v", err)
			}
			edition, err := s.EditionBySHA(ctx, u.ID, work.ID+"-edition")
			if err != nil || edition.WorkID != work.ID {
				t.Fatalf("conflict moved an edition: %+v %v", edition, err)
			}
		}
		for _, write := range writes[2:] {
			fields, err := s.ReadEvaluation(ctx, u.ID, write.work)
			if err != nil || len(fields) != 1 || fields[0].ID != write.item.ID {
				t.Fatalf("conflict changed a head: %+v %v", fields, err)
			}
		}
		fresh := store.EvaluationWrite{ID: "cyclic-fresh", WriterID: "cyclic-a", Revision: 21, Field: "review", Value: "null"}
		if err := s.WriteEvaluation(ctx, u.ID, into.ID, fresh); err != nil {
			t.Fatal(err)
		}
		if err := s.MergeWorks(ctx, u.ID, from.ID, into.ID); err != nil {
			t.Fatalf("merge after a fresh choice: %v", err)
		}
		fields, err := s.ReadEvaluation(ctx, u.ID, into.ID)
		if err != nil || len(fields) != 1 || fields[0].ID != fresh.ID || fields[0].Value != "null" {
			t.Fatalf("fresh choice lost: %+v %v", fields, err)
		}
	})
}
