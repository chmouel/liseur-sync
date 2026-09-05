package storetest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/infer"
	"github.com/chmouel/liseur-sync/internal/store"
)

type changeNotice struct {
	userID string
	topics []store.Topic
}

type notificationProbe struct {
	notices []changeNotice
	read    func(context.Context)
}

func (p *notificationProbe) Notify(userID string, topics ...store.Topic) {
	p.notices = append(p.notices, changeNotice{userID, slices.Clone(topics)})
	if p.read != nil {
		// Read through the store, not the write transaction: an early
		// notification sees stale data (Postgres) or times out (SQLite).
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		p.read(ctx)
	}
}

func (p *notificationProbe) check(t *testing.T, userID string, topic store.Topic) {
	t.Helper()
	defer func() { p.notices = nil }()
	if topic == "" {
		if len(p.notices) != 0 {
			t.Fatalf("no-op write notified: %+v", p.notices)
		}
		return
	}
	if len(p.notices) != 1 || p.notices[0].userID != userID ||
		!slices.Equal(p.notices[0].topics, []store.Topic{topic}) {
		t.Fatalf("want one %s notice for %s, got %+v", topic, userID, p.notices)
	}
}

func notificationFixture(t *testing.T, open OpenFunc) (store.Store, store.User, store.Work, *notificationProbe) {
	t.Helper()
	s := open(t)
	other := MkUser(t, s, "alice")
	MkWork(t, s, other, "w1", "sha1")
	user := MkUser(t, s, "bob")
	work := MkWork(t, s, user, "w1", "sha1")
	hook, ok := s.(store.ChangeNotifying)
	if !ok {
		t.Fatal("backend does not implement ChangeNotifying")
	}
	probe := &notificationProbe{}
	hook.SetChangeNotifier(probe)
	t.Cleanup(func() { hook.SetChangeNotifier(nil) })
	return s, user, work, probe
}

func testNotifications(t *testing.T, open OpenFunc) {
	for _, kosync := range []bool{false, true} {
		name := "NativeOps"
		if kosync {
			name = "KosyncOps"
		}
		t.Run(name, func(t *testing.T) { testOpNotifications(t, open, kosync) })
	}
	t.Run("Annotations", func(t *testing.T) { testAnnotationNotifications(t, open) })
	t.Run("NativeSessions", func(t *testing.T) { testSessionNotifications(t, open) })
	t.Run("InferredSessions", func(t *testing.T) { testInferredNotifications(t, open, false) })
	t.Run("ExistingInferredSession", func(t *testing.T) { testInferredNotifications(t, open, true) })
	for _, alias := range []bool{false, true} {
		name := "Koplugin"
		if alias {
			name = "KopluginByAlias"
		}
		t.Run(name, func(t *testing.T) { testKopluginNotifications(t, open, alias) })
	}
}

func testOpNotifications(t *testing.T, open OpenFunc, kosync bool) {
	s, user, work, probe := notificationFixture(t, open)
	ctx := context.Background()
	op := store.Op{
		OpID: "op1", WorkID: work.ID, ClientTS: time.Now(),
		Progression: 0.2, Origin: store.OriginNative,
	}
	if kosync {
		op.Origin = store.OriginKosync
	}
	appendOp := func(op store.Op) (store.OpResult, error) {
		if kosync {
			return s.AppendKosyncOp(ctx, user.ID, "md5-w1", "device", op)
		}
		results, err := s.AppendOps(ctx, user.ID, "device", []store.Op{op})
		if err != nil {
			return store.OpResult{}, err
		}
		return results[0], nil
	}
	probe.read = func(ctx context.Context) {
		page, err := s.Changes(ctx, user.ID, 0, 10)
		if err != nil || len(page.Ops) != 1 || page.Ops[0].OpID != op.OpID ||
			page.Ops[0].Progression != 0.2 {
			t.Errorf("notification preceded op commit: %+v, %v", page, err)
		}
	}
	for _, status := range []string{"applied", "duplicate", "conflict"} {
		if status == "conflict" {
			op.Progression = 0.7
		}
		result, err := appendOp(op)
		if err != nil || result.Status != status {
			t.Fatalf("want %s, got %+v, %v", status, result, err)
		}
		topic := store.Topic("")
		if status == "applied" {
			topic = store.TopicPositions
		}
		probe.check(t, user.ID, topic)
	}
	probe.read = nil
	if kosync {
		bad := op
		bad.OpID, bad.EditionSHA = "bad", Ptr("unknown-edition")
		if _, err := s.AppendKosyncOp(ctx, user.ID, "new-alias", "device", bad); err == nil {
			t.Fatal("expected foreign-key failure")
		}
		if _, err := s.WorkIDByAlias(ctx, user.ID, "partial-md5", "new-alias"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("failed kosync write left its alias: %v", err)
		}
	} else {
		valid, invalid := op, op
		valid.OpID, invalid.OpID, invalid.WorkID = "rolled-back", "unknown", "no-work"
		if _, err := s.AppendOps(ctx, user.ID, "device", []store.Op{valid, invalid}); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("expected unknown work: %v", err)
		}
	}
	probe.check(t, user.ID, "")
	page, err := s.Changes(ctx, user.ID, 0, 10)
	if err != nil || len(page.Ops) != 1 {
		t.Fatalf("failed write was not rolled back: %+v, %v", page, err)
	}
	if !kosync {
		badEdition := op
		badEdition.OpID, badEdition.EditionSHA = "bad-edition", Ptr("unknown-edition")
		if _, err := s.AppendOps(ctx, user.ID, "device", []store.Op{badEdition}); err == nil {
			t.Fatal("expected deferred foreign-key failure at commit")
		}
		probe.check(t, user.ID, "")
		fresh := op
		fresh.OpID = "op2"
		probe.read = func(ctx context.Context) {
			page, err := s.Changes(ctx, user.ID, 0, 10)
			if err != nil || len(page.Ops) != 2 {
				t.Errorf("mixed batch not committed: %+v, %v", page, err)
			}
		}
		original := op
		original.Progression = 0.2
		results, err := s.AppendOps(ctx, user.ID, "device", []store.Op{original, fresh})
		if err != nil || len(results) != 2 || results[0].Status != "duplicate" || results[1].Status != "applied" {
			t.Fatalf("mixed batch: %+v, %v", results, err)
		}
		probe.check(t, user.ID, store.TopicPositions)
		if _, err := s.AppendOps(ctx, user.ID, "device", nil); err != nil {
			t.Fatal(err)
		}
		probe.check(t, user.ID, "")
	}
	probe.read = nil
	if _, err := s.AppendOps(ctx, "u-alice", "device", []store.Op{op}); err != nil {
		t.Fatal(err)
	}
	probe.check(t, "u-alice", store.TopicPositions)
}

func testAnnotationNotifications(t *testing.T, open OpenFunc) {
	s, user, work, probe := notificationFixture(t, open)
	ctx := context.Background()
	item := annWrite("a1", work.ID, 0)
	wantRev := int64(1)
	wantDeleted := false
	probe.read = func(ctx context.Context) {
		page, err := s.AnnotationChanges(ctx, user.ID, 0, 10)
		if err != nil || len(page.Annotations) != 1 || page.Annotations[0].Rev != wantRev ||
			page.Annotations[0].Deleted() != wantDeleted {
			t.Errorf("notification preceded annotation commit: %+v, %v", page, err)
		}
	}
	for _, status := range []string{"applied", "duplicate"} {
		if r := pushOne(t, s, user.ID, "device", item); r.Status != status {
			t.Fatalf("create/replay: %+v", r)
		}
		topic := store.Topic("")
		if status == "applied" {
			topic = store.TopicAnnotations
		}
		probe.check(t, user.ID, topic)
	}
	item.BaseRev, item.Body, wantRev = 1, "edited", 2
	if r := pushOne(t, s, user.ID, "device", item); r.Status != "applied" {
		t.Fatalf("edit: %+v", r)
	}
	probe.check(t, user.ID, store.TopicAnnotations)
	if r := pushOne(t, s, user.ID, "device", item); r.Status != "duplicate" {
		t.Fatalf("edit replay: %+v", r)
	}
	probe.check(t, user.ID, "")
	item.Body = "stale edit"
	if r := pushOne(t, s, user.ID, "device", item); r.Status != "conflict" {
		t.Fatalf("stale edit: %+v", r)
	}
	probe.check(t, user.ID, "")
	if r := pushOne(t, s, user.ID, "device", annWrite("unknown", "no-work", 0)); r.Status != "invalid" {
		t.Fatalf("unknown work: %+v", r)
	}
	probe.check(t, user.ID, "")
	if r, err := s.DeleteAnnotation(ctx, user.ID, item.ID, 1); err != nil || r.Status != "conflict" {
		t.Fatalf("stale delete: %+v, %v", r, err)
	}
	probe.check(t, user.ID, "")
	wantRev, wantDeleted = 3, true
	if r, err := s.DeleteAnnotation(ctx, user.ID, item.ID, 2); err != nil || r.Status != "applied" {
		t.Fatalf("delete: %+v, %v", r, err)
	}
	probe.check(t, user.ID, store.TopicAnnotations)
	if r, err := s.DeleteAnnotation(ctx, user.ID, item.ID, 2); err != nil || r.Status != "duplicate" {
		t.Fatalf("delete replay: %+v, %v", r, err)
	}
	probe.check(t, user.ID, "")
	if _, err := s.DeleteAnnotation(ctx, user.ID, "missing", 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing delete: %v", err)
	}
	probe.check(t, user.ID, "")
	if _, err := s.PushAnnotations(ctx, user.ID, "device", nil, 2000); err != nil {
		t.Fatal(err)
	}
	probe.check(t, user.ID, "")
	probe.read = nil
	fresh, invalid := annWrite("a2", work.ID, 0), annWrite("bad-kind", work.ID, 0)
	// Bypass handler validation to fail SQL after an earlier item wrote.
	invalid.Kind = "unsupported"
	if _, err := s.PushAnnotations(ctx, user.ID, "device", []store.AnnotationWrite{fresh, invalid}, 2000); err == nil {
		t.Fatal("expected annotation constraint failure")
	}
	probe.check(t, user.ID, "")
	page, err := s.AnnotationChanges(ctx, user.ID, 0, 10)
	if err != nil || len(page.Annotations) != 1 || page.HighWater != 3 {
		t.Fatalf("failed annotation batch was not rolled back: %+v, %v", page, err)
	}
	probe.read = func(ctx context.Context) {
		page, err := s.AnnotationChanges(ctx, user.ID, 3, 10)
		if err != nil || len(page.Annotations) != 1 || page.Annotations[0].ID != fresh.ID {
			t.Errorf("notification preceded mixed annotation commit: %+v, %v", page, err)
		}
	}
	invalid = annWrite("unknown", "no-work", 0)
	results, err := s.PushAnnotations(ctx, user.ID, "device", []store.AnnotationWrite{invalid, fresh, fresh}, 2000)
	if err != nil || len(results) != 3 || results[0].Status != "invalid" ||
		results[1].Status != "applied" || results[2].Status != "duplicate" {
		t.Fatalf("mixed annotation batch: %+v, %v", results, err)
	}
	probe.check(t, user.ID, store.TopicAnnotations)
}

func notificationSession(workID string) store.Session {
	start := time.Unix(1754800000, 0)
	return store.Session{
		SessionID: "session1", WorkID: workID, DeviceID: "device",
		StartedAt: start, EndedAt: start.Add(time.Minute),
		StartProg: 0.1, EndProg: 0.2, Origin: store.OriginNative,
	}
}

func testSessionNotifications(t *testing.T, open OpenFunc) {
	s, user, work, probe := notificationFixture(t, open)
	ctx := context.Background()
	session := notificationSession(work.ID)
	wantCount := 1
	probe.read = func(ctx context.Context) {
		sessions, err := s.SessionsForWork(ctx, user.ID, work.ID, 10)
		if err != nil || len(sessions) != wantCount {
			t.Errorf("notification preceded session commit: %+v, %v", sessions, err)
		}
	}
	for _, topic := range []store.Topic{store.TopicInsights, ""} {
		if err := s.AppendSessions(ctx, user.ID, []store.Session{session}); err != nil {
			t.Fatal(err)
		}
		probe.check(t, user.ID, topic)
	}
	fresh := session
	fresh.SessionID = "session2"
	for _, unknown := range []bool{false, true} {
		bad := session
		bad.EndProg = 0.9
		wantErr := store.ErrIDMismatch
		if unknown {
			bad.SessionID, bad.WorkID = "unknown", "no-work"
			wantErr = store.ErrNotFound
		}
		if err := s.AppendSessions(ctx, user.ID, []store.Session{fresh, bad}); !errors.Is(err, wantErr) {
			t.Fatalf("rejected batch: %v", err)
		}
		probe.check(t, user.ID, "")
		sessions, err := s.SessionsForWork(ctx, user.ID, work.ID, 10)
		if err != nil || len(sessions) != 1 {
			t.Fatalf("failed session batch was not rolled back: %+v, %v", sessions, err)
		}
	}
	wantCount = 2
	badEdition := fresh
	badEdition.EditionSHA = Ptr("unknown-edition")
	if err := s.AppendSessions(ctx, user.ID, []store.Session{badEdition}); err == nil {
		t.Fatal("expected deferred foreign-key failure at commit")
	}
	probe.check(t, user.ID, "")
	if err := s.AppendSessions(ctx, user.ID, []store.Session{session, fresh}); err != nil {
		t.Fatal(err)
	}
	probe.check(t, user.ID, store.TopicInsights)
	if err := s.AppendSessions(ctx, user.ID, nil); err != nil {
		t.Fatal(err)
	}
	probe.check(t, user.ID, "")
}

func testInferredNotifications(t *testing.T, open OpenFunc, existing bool) {
	s, user, work, probe := notificationFixture(t, open)
	ctx := context.Background()
	for i, id := range []string{"op1", "op2"} {
		op := store.Op{
			OpID: id, ClientTS: time.Now(), Progression: float64(i) / 10,
			Origin: store.OriginKosync, OriginAlias: Ptr("partial-md5:md5-w1"),
		}
		if _, err := s.AppendKosyncOp(ctx, user.ID, "md5-w1", "device", op); err != nil {
			t.Fatal(err)
		}
		probe.check(t, user.ID, store.TopicPositions)
	}
	ops, err := s.PendingInferenceOps(ctx, user.ID)
	if err != nil || len(ops) != 2 {
		t.Fatalf("pending ops: %+v, %v", ops, err)
	}
	group := store.InferredSessionGroup{Session: infer.Materialize(user.ID, ops), Ops: ops}
	if existing {
		if err := s.AppendSessions(ctx, user.ID, []store.Session{group.Session}); err != nil {
			t.Fatal(err)
		}
		probe.check(t, user.ID, store.TopicInsights)
	}
	probe.read = func(ctx context.Context) {
		sessions, err := s.SessionsForWork(ctx, user.ID, work.ID, 10)
		if err != nil || len(sessions) != 1 || sessions[0].SessionID != group.Session.SessionID {
			t.Errorf("notification preceded inferred session commit: %+v, %v", sessions, err)
		}
		pending, err := s.PendingInferenceOps(ctx, user.ID)
		if err != nil || len(pending) != 0 {
			t.Errorf("notification preceded inference stamps: %+v, %v", pending, err)
		}
	}
	staleOps := slices.Clone(ops)
	staleOps[0].Seq++
	stale := store.InferredSessionGroup{Session: group.Session, Ops: staleOps}
	if err := s.AppendInferredSession(ctx, user.ID, stale); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale inference: %v", err)
	}
	probe.check(t, user.ID, "")
	if err := s.AppendInferredSession(ctx, user.ID, group); err != nil {
		t.Fatal(err)
	}
	topic := store.TopicInsights
	if existing {
		topic = ""
	}
	probe.check(t, user.ID, topic)
	probe.read(ctx)
	if err := s.AppendInferredSession(ctx, user.ID, group); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("inference replay: %v", err)
	}
	probe.check(t, user.ID, "")
}

func testKopluginNotifications(t *testing.T, open OpenFunc, byAlias bool) {
	s, user, work, probe := notificationFixture(t, open)
	ctx := context.Background()
	session := notificationSession(work.ID)
	session.Origin, session.SourceKey = store.OriginKoplugin, Ptr("device|md5-w1|42|1754800000")
	upsert := func(ses store.Session) (string, error) {
		if byAlias {
			return s.UpsertKopluginSessionByAlias(ctx, user.ID, "md5-w1", ses)
		}
		return s.UpsertKopluginSession(ctx, user.ID, ses)
	}
	wantCount := 1
	probe.read = func(ctx context.Context) {
		sessions, err := s.SessionsForWork(ctx, user.ID, work.ID, 10)
		if err != nil || len(sessions) != wantCount {
			t.Errorf("notification preceded koplugin commit: %+v, %v", sessions, err)
		}
		current, err := s.CurrentSessionsForWork(ctx, user.ID, work.ID, 10)
		wantID := "session1"
		if wantCount == 2 {
			wantID = "session2"
		}
		if err != nil || len(current) != 1 || current[0].SessionID != wantID {
			t.Errorf("notification preceded koplugin supersession commit: %+v, %v", current, err)
		}
	}
	for _, status := range []string{"inserted", "duplicate", "superseded", "duplicate"} {
		switch status {
		case "superseded":
			session.SessionID, session.EndProg, wantCount = "session2", 0.3, 2
		case "duplicate":
			// Replay the original revision, including after supersession.
			session.SessionID, session.EndProg = "session1", 0.2
		}
		got, err := upsert(session)
		if err != nil || got != status {
			t.Fatalf("want %s, got %s, %v", status, got, err)
		}
		topic := store.TopicInsights
		if status == "duplicate" {
			topic = ""
		}
		probe.check(t, user.ID, topic)
	}
	bad := session
	bad.SessionID, bad.EditionSHA = "bad-session", Ptr("unknown-edition")
	if _, err := upsert(bad); err == nil {
		t.Fatal("expected foreign-key failure")
	}
	probe.check(t, user.ID, "")
	sessions, err := s.SessionsForWork(ctx, user.ID, work.ID, 10)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("failed koplugin write changed sessions: %+v, %v", sessions, err)
	}
}
