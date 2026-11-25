package notify

import (
	"context"
	"fmt"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

const (
	dispatchBatch = 200
	// dispatchOverlap is how many ids behind the newest one handed over are read again on every pass:
	// an id is taken when a row is inserted, not when it commits, so a lower one can show up later.
	dispatchOverlap = 64
)

// Dispatcher hands new notifications to one delivery channel, each with whether its owner lets it pop
// up. Its position is this replica's own: it serves the channel's connections on this replica.
//
// replica-local: where this replica has handed notifications to its own channel; the notifications themselves are in Postgres.
type Dispatcher struct {
	store     port.NotificationStore
	channel   port.NotificationChannel
	last      uint64
	delivered map[uint64]struct{}
}

func NewDispatcher(store port.NotificationStore, channel port.NotificationChannel) *Dispatcher {
	return &Dispatcher{store: store, channel: channel, delivered: map[uint64]struct{}{}}
}

// Start hands over only notifications created from now on: older ones are in the center already.
func (d *Dispatcher) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	last, err := d.store.LatestNotificationID(ctx)
	if err != nil {
		return fmt.Errorf("read the newest notification: %w", err)
	}
	d.last = last
	tail, err := d.store.ListNotificationsSince(ctx, d.from(), dispatchBatch)
	if err != nil {
		return fmt.Errorf("read the newest notifications: %w", err)
	}
	for _, n := range tail {
		if n.ID <= last {
			d.delivered[n.ID] = struct{}{}
		}
	}
	return nil
}

func (d *Dispatcher) from() uint64 {
	if d.last > dispatchOverlap {
		return d.last - dispatchOverlap
	}
	return 0
}

// Pass hands over the notifications created since the last pass, each once. A channel that fails
// gets them again on the next pass.
func (d *Dispatcher) Pass(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		batch, err := d.store.ListNotificationsSince(ctx, d.from(), dispatchBatch)
		if err != nil {
			return fmt.Errorf("read notifications: %w", err)
		}
		fresh := make([]entity.Notification, 0, len(batch))
		settings := map[uint64]entity.NotificationSettings{}
		for _, n := range batch {
			if _, done := d.delivered[n.ID]; done {
				continue
			}
			s, ok := settings[n.UserID]
			if !ok {
				if s, err = d.store.GetNotificationSettings(ctx, n.UserID); err != nil {
					return err
				}
				settings[n.UserID] = s
			}
			n.PopUp = s.PopsUp(n.Kind)
			fresh = append(fresh, n)
		}
		if len(fresh) > 0 {
			if err := d.channel.Deliver(ctx, fresh); err != nil {
				return fmt.Errorf("deliver to %s: %w", d.channel.Name(), err)
			}
		}
		for _, n := range fresh {
			d.delivered[n.ID] = struct{}{}
			d.last = max(d.last, n.ID)
		}
		for id := range d.delivered {
			if id+dispatchOverlap < d.last {
				delete(d.delivered, id)
			}
		}
		if len(fresh) == 0 || len(batch) < dispatchBatch {
			return nil
		}
	}
}
