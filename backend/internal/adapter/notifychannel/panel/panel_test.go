package panel_test

import (
	"context"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/adapter/notifychannel/panel"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

type streams struct{ sent []entity.Notification }

func (s *streams) SendNotifications(ns []entity.Notification) { s.sent = append(s.sent, ns...) }

func TestPanel_DeliverSendsToTheOpenStreams(t *testing.T) {
	t.Parallel()
	s := &streams{}
	ch := panel.New(s)
	if ch.Name() != "panel" {
		t.Fatalf("Name() = %q", ch.Name())
	}
	if err := ch.Deliver(context.Background(), []entity.Notification{{ID: 1, UserID: 7}, {ID: 2, UserID: 8}}); err != nil {
		t.Fatal(err)
	}
	if len(s.sent) != 2 {
		t.Fatalf("sent %+v", s.sent)
	}
}

func TestPanel_RedeliverShowsEachNotificationOnce(t *testing.T) {
	t.Parallel()
	s := &streams{}
	ch := panel.New(s)
	ctx := context.Background()
	_ = ch.Deliver(ctx, []entity.Notification{{ID: 1, UserID: 7}})
	_ = ch.Deliver(ctx, []entity.Notification{{ID: 1, UserID: 7}, {ID: 2, UserID: 7}})
	if len(s.sent) != 2 || s.sent[1].ID != 2 {
		t.Fatalf("a notification handed over twice pops up once: %+v", s.sent)
	}
}

func TestPanel_DeliverHonoursCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &streams{}
	if err := panel.New(s).Deliver(ctx, []entity.Notification{{ID: 1}}); err == nil || len(s.sent) != 0 {
		t.Fatalf("a cancelled delivery sends nothing: %v %+v", err, s.sent)
	}
}
