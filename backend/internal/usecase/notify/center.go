package notify

import (
	"context"
	"fmt"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// Retention is how long notifications are kept.
const Retention = 30 * 24 * time.Hour

const (
	defaultPage = 50
	maxPage     = 100
)

// ChannelInfo describes a delivery channel this server offers, for the panel to show.
type ChannelInfo struct {
	Name string `json:"name"`
	// PublicKey is what a browser needs to subscribe (Web Push's VAPID key), "" when nothing.
	PublicKey string `json:"public_key,omitempty"`
}

// Page is one page of a user's notifications, newest first.
type Page struct {
	Notifications []entity.Notification `json:"notifications"`
	Unread        int                   `json:"unread"`
	// NextBefore is the cursor of the next (older) page, 0 when this is the last.
	NextBefore uint64 `json:"next_before,omitempty"`
}

// Center serves the notification center: a user's notifications, read state and settings.
type Center struct {
	store    port.NotificationStore
	channels []ChannelInfo
	now      func() time.Time
}

func NewCenter(store port.NotificationStore, channels []ChannelInfo) *Center {
	return &Center{store: store, channels: channels, now: time.Now}
}

// List returns the user's notifications older than before (0: the newest).
func (c *Center) List(ctx context.Context, userID, before uint64, limit int) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, context.Cause(ctx)
	}
	if limit <= 0 {
		limit = defaultPage
	}
	limit = min(limit, maxPage)
	ns, err := c.store.ListNotifications(ctx, userID, before, limit+1)
	if err != nil {
		return Page{}, err
	}
	unread, err := c.store.CountUnreadNotifications(ctx, userID)
	if err != nil {
		return Page{}, err
	}
	page := Page{Notifications: ns, Unread: unread}
	if len(ns) > limit {
		page.Notifications = ns[:limit]
		page.NextBefore = ns[limit-1].ID
	}
	if page.Notifications == nil {
		page.Notifications = []entity.Notification{}
	}
	return page, nil
}

// MarkRead marks the given notifications of the user read, or all of them.
func (c *Center) MarkRead(ctx context.Context, userID uint64, ids []uint64, all bool) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if all == (len(ids) > 0) {
		return fmt.Errorf("%w: give either the notification ids or all", entity.ErrInvalidInput)
	}
	_, err := c.store.MarkNotificationsRead(ctx, userID, ids, c.now().UTC())
	return err
}

func (c *Center) Settings(ctx context.Context, userID uint64) (entity.NotificationSettings, error) {
	if err := ctx.Err(); err != nil {
		return entity.NotificationSettings{}, context.Cause(ctx)
	}
	return c.store.GetNotificationSettings(ctx, userID)
}

func (c *Center) SaveSettings(ctx context.Context, userID uint64, settings entity.NotificationSettings) (entity.NotificationSettings, error) {
	if err := ctx.Err(); err != nil {
		return entity.NotificationSettings{}, context.Cause(ctx)
	}
	if err := settings.Validate(); err != nil {
		return entity.NotificationSettings{}, err
	}
	if settings.PopupsOff == nil {
		settings.PopupsOff = []entity.NotificationKind{}
	}
	if err := c.store.SaveNotificationSettings(ctx, userID, settings, c.now().UTC()); err != nil {
		return entity.NotificationSettings{}, err
	}
	return settings, nil
}

// Options are what this server offers for notifications.
type Options struct {
	// Channels are the delivery channels this server offers.
	Channels []ChannelInfo `json:"channels"`
	// Kinds are the notification kinds a user can switch on and off.
	Kinds []entity.NotificationKind `json:"kinds"`
}

// Options lists the delivery channels and the notification kinds.
func (c *Center) Options(ctx context.Context) (Options, error) {
	if err := ctx.Err(); err != nil {
		return Options{}, context.Cause(ctx)
	}
	channels := c.channels
	if channels == nil {
		channels = []ChannelInfo{}
	}
	return Options{Channels: channels, Kinds: entity.NotificationKinds}, nil
}

// Prune deletes notifications older than Retention.
func (c *Center) Prune(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	_, err := c.store.PruneNotifications(ctx, c.now().Add(-Retention))
	return err
}
