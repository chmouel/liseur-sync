package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/chmouel/liseur-sync/internal/oauth"
	"github.com/chmouel/liseur-sync/internal/store"
)

type parameterLimitedReadings struct{ store.Store }

func (s parameterLimitedReadings) ListWorks(ctx context.Context, userID string) ([]store.WorkSummary, error) {
	works, err := s.Store.ListWorks(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range 1200 {
		works = append(works, store.WorkSummary{Work: store.Work{ID: fmt.Sprintf("extra-%04d", i), UserID: userID}})
	}
	return works, nil
}

func (s parameterLimitedReadings) WorkReadings(ctx context.Context, userID string, ids []string) (map[string]store.WorkReading, error) {
	if len(ids) > 1000 {
		return nil, fmt.Errorf("too many SQL parameters")
	}
	return s.Store.WorkReadings(ctx, userID, ids)
}

func TestMCPReadingListsWithManyWorks(t *testing.T) {
	f := newFolderFixture(t)
	bookID, sha := f.writeBook(t, "moby.epub", chapterEPUB(t))
	_, resolved := f.resolveBook(t, bookID, f.token)
	resp, body := f.postRaw(t, "/v1/ops", f.token, `{"ops":[{
		"op_id":"position","work_id":"`+resolved.WorkID+`","edition_sha":"`+sha+`",
		"client_ts":"2024-01-01T12:00:00Z","progression":0.4}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("push: %d %s", resp.StatusCode, body)
	}
	f.srv.St = parameterLimitedReadings{f.st}
	f.enableMCP(t, nil)
	access := f.assistantToken(t, f.user.ID, oauth.ScopeReading)
	for _, tool := range []string{"get_reading_status", "list_currently_reading"} {
		out := f.mustCall(t, access, tool, map[string]any{"limit": 1})
		works := out["works"].([]any)
		if len(works) != 1 || works[0].(map[string]any)["work_id"] != resolved.WorkID {
			t.Errorf("%s: %v", tool, out)
		}
	}
}
