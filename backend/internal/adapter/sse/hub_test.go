package sse_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/sse"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func TestHub_PublishAndSubscribeWithContextCleanup(t *testing.T) {
	t.Parallel()

	hub := sse.NewHub(10)
	ctx, cancel := context.WithCancel(context.Background())

	ch := hub.Subscribe(ctx)
	if hub.SubscriberCount() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", hub.SubscriberCount())
	}

	ev := entity.ActivityEvent{
		ID:         "ev-1",
		Type:       "pr.opened",
		PRNumber:   42,
		ActorLogin: "murilo",
		Summary:    "Opened PR #42",
		OccurredAt: time.Now().UTC(),
	}

	if err := hub.Publish(context.Background(), ev); err != nil {
		t.Fatalf("Publish() error: %v", err)
	}

	select {
	case got := <-ch:
		if got.ID != "ev-1" || got.PRNumber != 42 {
			t.Fatalf("unexpected event: %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SSE event")
	}

	recent := hub.RecentEvents()
	if len(recent) != 1 || recent[0].ID != "ev-1" {
		t.Fatalf("expected 1 recent event, got %#v", recent)
	}

	cancel()
	deadline := time.Now().Add(time.Second)
	for hub.SubscriberCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if hub.SubscriberCount() != 0 {
		t.Fatalf("expected 0 subscribers after context cancel, got %d", hub.SubscriberCount())
	}
}

func TestHub_ServeHTTPStreamsEventsUntilContextCancel(t *testing.T) {
	t.Parallel()

	hub := sse.NewHub(10)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		hub.ServeHTTP(rec, req)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for hub.SubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	_ = hub.Publish(context.Background(), entity.ActivityEvent{
		ID:      "ev-99",
		Type:    "pr.approved",
		Summary: "Approved PR #99",
	})

	time.Sleep(15 * time.Millisecond)
	cancel()
	<-done

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: pr.approved") || !strings.Contains(body, `"id":"ev-99"`) {
		t.Errorf("expected SSE frame in response body, got:\n%s", body)
	}
}
