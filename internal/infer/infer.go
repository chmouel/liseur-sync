// Package infer groups a position-only device's op log into inferred
// reading sessions (design §6.3). Groups are computed from ops ordered
// by server seq with gaps measured on server received_at; a group is
// "closed" (materializable) only when its last op is older than the
// lateness window, so a session is never split by materialization.
package infer

import (
	"time"

	"github.com/google/uuid"

	"github.com/chmouel/liseur-sync/internal/store"
)

var sessionNS = uuid.MustParse("7e0a3b10-5f2e-4c3a-9b6d-2f6f6c1a0002")

// SessionID derives the deterministic id for an inferred group:
// UUIDv5(ns, user|work|device|first op_id).
func SessionID(userID, workID, deviceID, firstOpID string) string {
	return uuid.NewSHA1(sessionNS, []byte(userID+"|"+workID+"|"+deviceID+"|"+firstOpID)).String()
}

// Group splits ops (already ordered by seq per the caller contract)
// into sessions: same (work, device), consecutive gaps below gapDur on
// received_at. Returns groups in order; the caller decides which are
// closed.
func Group(ops []store.Op, gapDur time.Duration) [][]store.Op {
	var groups [][]store.Op
	for _, o := range ops {
		if n := len(groups); n > 0 {
			last := groups[n-1][len(groups[n-1])-1]
			if last.WorkID == o.WorkID && last.DeviceID == o.DeviceID &&
				o.ReceivedAt.Sub(last.ReceivedAt) < gapDur {
				groups[n-1] = append(groups[n-1], o)
				continue
			}
		}
		groups = append(groups, []store.Op{o})
	}
	return groups
}

// Materialize converts one closed group into an inferred session.
// start/end progression come from the first/last op; the end timestamp
// is the last op's received_at (there is no measured end for
// position-only devices).
func Materialize(userID string, group []store.Op) store.Session {
	first, last := group[0], group[len(group)-1]
	return store.Session{
		SessionID: SessionID(userID, last.WorkID, last.DeviceID, first.OpID),
		WorkID:    last.WorkID,
		DeviceID:  last.DeviceID,
		StartedAt: first.ReceivedAt,
		EndedAt:   last.ReceivedAt,
		StartProg: first.Progression,
		EndProg:   last.Progression,
		Origin:    store.OriginInferred,
	}
}

// ClosedGroups returns the groups whose last op is older than the
// lateness window — exactly the materializable set.
func ClosedGroups(ops []store.Op, gapDur time.Duration, lateBefore time.Time) [][]store.Op {
	var out [][]store.Op
	for _, g := range Group(ops, gapDur) {
		if g[len(g)-1].ReceivedAt.Before(lateBefore) {
			out = append(out, g)
		}
	}
	return out
}
