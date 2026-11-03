package mirror

import (
	"context"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// kosyncPeer is KOReader's sync protocol as a Protocol: the three
// calls stock kosync defines, joined on the document fingerprint.
//
// It is the original mirror and remains the default one. The reasons
// are in ADR-0047 and they have not changed: these three routes are
// KOReader's contract rather than any one server's, so they are the
// part a peer can be relied on to implement and the part that does not
// move when the peer does.
type kosyncPeer struct {
	client *Client
	cfg    config.MirrorConfig
}

// KosyncProtocol wraps a kosync client as a Protocol.
func KosyncProtocol(c *Client, cfg config.MirrorConfig) Protocol {
	return &kosyncPeer{client: c, cfg: cfg}
}

func (k *kosyncPeer) Name() string { return config.ProtocolKosync }

func (k *kosyncPeer) Identity() string {
	return peerIdentity(config.ProtocolKosync, k.cfg.BaseURL, k.cfg.RemoteUser)
}

func (k *kosyncPeer) Authorize(ctx context.Context) error { return k.client.Authorize(ctx) }

func (k *kosyncPeer) Close(context.Context) {}

// Pull maps a kosync position onto a Place. Both of the judgements a
// Place carries are made here, because both are facts about kosync: an
// echo is a reply stamped with the device id this server writes under,
// or one whose content is what was last written here — a peer reached
// through a route of its own may restamp the write and drop the device
// with it — and a reset is the synthetic start-of-book position a peer
// answers with while a reading reset is outstanding.
func (k *kosyncPeer) Pull(
	ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor,
) (Place, error) {
	p, err := k.client.Pull(ctx, c.Document)
	if err != nil {
		return Place{}, err
	}
	device := p.DeviceID
	if device == "" {
		device = p.Device
	}
	place := Place{
		Percentage: p.Percentage,
		Foreign:    p.Progress,
		Device:     device,
		At:         p.Time(),
		Reset:      p.IsReset(),
	}
	place.Mark = placeMark(place)
	place.Echo = p.DeviceID == k.client.DeviceID() ||
		(cur.PushedMark != "" && place.Mark == cur.PushedMark)
	if !place.Echo {
		// The mark says what the peer holds because of us. It plainly
		// holds something else now, so the mark is spent: keeping it
		// would call a reader's return to that spot our own writing,
		// months later, and refuse to follow them back to it.
		cur.PushedMark = ""
	}
	return place, nil
}

// Push sends a position out. A CFI is dropped on the floor: kosync has
// nowhere to put one, and the fraction is what crosses. The client
// fills in an empty progress string with the percentage, which is what
// this server already answers its own non-CRe clients with.
func (k *kosyncPeer) Push(
	ctx context.Context, c store.MirrorCandidate, cur *store.MirrorCursor, p Place,
) error {
	if err := k.client.Push(ctx, Position{
		Document:   c.Document,
		Progress:   p.Foreign,
		Percentage: p.Percentage,
		Timestamp:  p.At.UTC().Unix(),
	}); err != nil {
		return err
	}
	// What crossed, so the same content coming back can be told from
	// news even when the peer's reply carries no device of ours.
	cur.PushedMark = placeMark(Place{Percentage: p.Percentage, Foreign: p.Foreign})
	return nil
}
