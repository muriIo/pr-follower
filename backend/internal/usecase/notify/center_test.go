package notify_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/notify"
)

type centerStore struct {
	fakeStore
	unread   int
	readIDs  []uint64
	readAll  bool
	settings map[uint64]entity.NotificationSettings
	pruned   time.Time
	listed   struct{ user, before uint64 }
}

func (s *centerStore) ListNotifications(_ context.Context, userID, before uint64, limit int) ([]entity.Notification, error) {
	s.listed.user, s.listed.before = userID, before
	var out []entity.Notification
	for i := len(s.created) - 1; i >= 0 && len(out) < limit; i-- {
		n := s.created[i]
		if n.UserID == userID && (before == 0 || n.ID < before) {
			out = append(out, n)
		}
	}
	return out, nil
}
func (s *centerStore) CountUnreadNotifications(context.Context, uint64) (int, error) {
	return s.unread, nil
}
func (s *centerStore) MarkNotificationsRead(_ context.Context, _ uint64, ids []uint64, _ time.Time) (int64, error) {
	s.readIDs, s.readAll = ids, len(ids) == 0
	return 1, nil
}
func (s *centerStore) GetNotificationSettings(_ context.Context, userID uint64) (entity.NotificationSettings, error) {
	return s.settings[userID], nil
}
func (s *centerStore) SaveNotificationSettings(_ context.Context, userID uint64, st entity.NotificationSettings, _ time.Time) error {
	if s.settings == nil {
		s.settings = map[uint64]entity.NotificationSettings{}
	}
	s.settings[userID] = st
	return nil
}
func (s *centerStore) PruneNotifications(_ context.Context, before time.Time) (int64, error) {
	s.pruned = before
	return 0, nil
}

func seeded(t *testing.T, store *centerStore, n int) {
	t.Helper()
	for i := range n {
		_, _ = store.CreateNotifications(context.Background(), []entity.Notification{{UserID: alice, Kind: entity.NotificationKindTurn, Reason: entity.NotificationReasonNewCommit, PullRequestID: uint64(i + 1)}})
	}
}

func TestCenter_ListsAPageWithTheUnreadCount(t *testing.T) {
	t.Parallel()
	store := &centerStore{unread: 3}
	seeded(t, store, 3)
	center := notify.NewCenter(store, nil)

	page, err := center.List(context.Background(), alice, 0, 2)
	if err != nil || len(page.Notifications) != 2 || page.Notifications[0].ID != 3 || page.Unread != 3 || page.NextBefore != 2 {
		t.Fatalf("List() = %+v, %v", page, err)
	}
	last, _ := center.List(context.Background(), alice, page.NextBefore, 2)
	if len(last.Notifications) != 1 || last.NextBefore != 0 {
		t.Fatalf("the last page has no next cursor: %+v", last)
	}
	if page, _ := center.List(context.Background(), alice, 0, 0); len(page.Notifications) != 3 {
		t.Fatalf("a missing limit takes the default page: %+v", page)
	}
}

func TestCenter_MarksReadOnlyWhatItIsAskedTo(t *testing.T) {
	t.Parallel()
	store := &centerStore{}
	center := notify.NewCenter(store, nil)
	ctx := context.Background()

	if err := center.MarkRead(ctx, alice, []uint64{4, 5}, false); err != nil || len(store.readIDs) != 2 || store.readAll {
		t.Fatalf("MarkRead(ids) = %v, %+v", err, store)
	}
	if err := center.MarkRead(ctx, alice, nil, true); err != nil || !store.readAll {
		t.Fatalf("MarkRead(all) = %v", err)
	}
	if err := center.MarkRead(ctx, alice, nil, false); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("nothing to mark is a bad request, got %v", err)
	}
	if err := center.MarkRead(ctx, alice, []uint64{1}, true); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("ids and all together are ambiguous, got %v", err)
	}
}

func TestCenter_SettingsAndChannels(t *testing.T) {
	t.Parallel()
	store := &centerStore{}
	center := notify.NewCenter(store, []notify.ChannelInfo{{Name: "panel"}})
	ctx := context.Background()

	saved, err := center.SaveSettings(ctx, alice, entity.NotificationSettings{Paused: true, PopupsOff: []entity.NotificationKind{entity.NotificationKindRisk}})
	if err != nil || !saved.Paused || len(saved.PopupsOff) != 1 {
		t.Fatalf("SaveSettings() = %+v, %v", saved, err)
	}
	if got, _ := center.Settings(ctx, alice); !got.Paused {
		t.Fatalf("Settings() = %+v", got)
	}
	if _, err := center.SaveSettings(ctx, alice, entity.NotificationSettings{PopupsOff: []entity.NotificationKind{"email"}}); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("an unknown kind is refused, got %v", err)
	}
	if opts, err := center.Options(ctx); err != nil || len(opts.Channels) != 1 || opts.Channels[0].Name != "panel" || len(opts.Kinds) != len(entity.NotificationKinds) {
		t.Fatalf("Options() = %+v, %v", opts, err)
	}

	if err := center.Prune(ctx); err != nil || time.Since(store.pruned) < notify.Retention-time.Minute {
		t.Fatalf("notifications are kept %s, pruned before %s", notify.Retention, store.pruned)
	}
}

func TestCenter_StopsOnCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	center := notify.NewCenter(&centerStore{}, nil)
	if _, err := center.List(ctx, alice, 0, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("List: %v", err)
	}
	if err := center.MarkRead(ctx, alice, nil, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("MarkRead: %v", err)
	}
}
