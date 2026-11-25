// Package panel is the delivery channel to the open panel (ADR-0017): each notification goes to
// the live streams of its own user on this replica, which show it in the notification center and,
// when it may pop up, as a browser notification.
package panel

import (
	"context"
	"sync"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// remembered bounds how many delivered ids are kept to drop a repeat.
const remembered = 1024

// Streams sends notifications to the live streams of their users.
type Streams interface {
	SendNotifications(notifications []entity.Notification)
}

// Channel is the panel's port.NotificationChannel.
//
// replica-local: the ids this replica already sent to its own streams, to drop a repeat; the notifications are in Postgres.
type Channel struct {
	streams Streams

	mu    sync.Mutex
	seen  map[uint64]struct{}
	order []uint64
}

var _ port.NotificationChannel = (*Channel)(nil)

func New(streams Streams) *Channel {
	return &Channel{streams: streams, seen: map[uint64]struct{}{}}
}

func (c *Channel) Name() string { return "panel" }

// Deliver sends each notification once, however often it is handed over (ADR-0011).
func (c *Channel) Deliver(ctx context.Context, notifications []entity.Notification) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	c.mu.Lock()
	fresh := make([]entity.Notification, 0, len(notifications))
	for _, n := range notifications {
		if _, done := c.seen[n.ID]; done {
			continue
		}
		c.seen[n.ID] = struct{}{}
		c.order = append(c.order, n.ID)
		fresh = append(fresh, n)
	}
	for len(c.order) > remembered {
		delete(c.seen, c.order[0])
		c.order = c.order[1:]
	}
	c.mu.Unlock()
	if len(fresh) > 0 {
		c.streams.SendNotifications(fresh)
	}
	return nil
}
