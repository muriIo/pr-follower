package notify_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/notify"
)

type dispatchStore struct {
	centerStore
}

func (s *dispatchStore) ListNotificationsSince(_ context.Context, after uint64, limit int) ([]entity.Notification, error) {
	var out []entity.Notification
	for _, n := range s.created {
		if n.ID > after && len(out) < limit {
			out = append(out, n)
		}
	}
	return out, nil
}
func (s *dispatchStore) LatestNotificationID(context.Context) (uint64, error) {
	return uint64(len(s.created)), nil
}

type recordingChannel struct {
	got []entity.Notification
	err error
}

func (c *recordingChannel) Name() string { return "test" }
func (c *recordingChannel) Deliver(_ context.Context, ns []entity.Notification) error {
	if c.err != nil {
		return c.err
	}
	c.got = append(c.got, ns...)
	return nil
}

func add(store *dispatchStore, user uint64, kind entity.NotificationKind, pr uint64) {
	_, _ = store.CreateNotifications(context.Background(), []entity.Notification{{UserID: user, Kind: kind, Reason: entity.NotificationReasonNewCommit, PullRequestID: pr}})
}

func TestDispatcher_HandsOverWhatIsNewWithItsPopUpFlag(t *testing.T) {
	t.Parallel()
	store := &dispatchStore{}
	add(store, alice, entity.NotificationKindTurn, 1) // before the start: already in the center, never re-sent
	ch := &recordingChannel{}
	d := notify.NewDispatcher(store, ch)
	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	_ = store.SaveNotificationSettings(ctx, bob, entity.NotificationSettings{PopupsOff: []entity.NotificationKind{entity.NotificationKindRisk}}, store.pruned)
	add(store, alice, entity.NotificationKindRisk, 2)
	add(store, bob, entity.NotificationKindRisk, 2)

	if err := d.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ch.got) != 2 || ch.got[0].ID != 2 || !ch.got[0].PopUp || ch.got[1].PopUp {
		t.Fatalf("each notification goes once, popping up as its owner chose: %+v", ch.got)
	}
}

func TestDispatcher_RedeliverHandsEachNotificationOverOnce(t *testing.T) {
	t.Parallel()
	store := &dispatchStore{}
	ch := &recordingChannel{}
	d := notify.NewDispatcher(store, ch)
	ctx := context.Background()
	_ = d.Start(ctx)
	add(store, alice, entity.NotificationKindTurn, 1)
	_ = d.Pass(ctx)
	_ = d.Pass(ctx) // the tail is read again, so a notification that commits late is not missed
	if len(ch.got) != 1 {
		t.Fatalf("each notification is handed over once: %+v", ch.got)
	}

	restarted := notify.NewDispatcher(store, ch)
	_ = restarted.Start(ctx)
	_ = restarted.Pass(ctx)
	if len(ch.got) != 1 {
		t.Fatalf("a replica that starts does not re-send the newest notifications: %+v", ch.got)
	}
}

func TestDispatcher_AFailedDeliveryIsRetried(t *testing.T) {
	t.Parallel()
	store := &dispatchStore{}
	ch := &recordingChannel{err: errors.New("down")}
	d := notify.NewDispatcher(store, ch)
	ctx := context.Background()
	_ = d.Start(ctx)
	add(store, alice, entity.NotificationKindTurn, 1)
	if err := d.Pass(ctx); err == nil {
		t.Fatal("the channel's failure is reported")
	}
	ch.err = nil
	_ = d.Pass(ctx)
	if len(ch.got) != 1 || ch.got[0].ID != 1 {
		t.Fatalf("delivered on the next pass: %+v", ch.got)
	}
}
