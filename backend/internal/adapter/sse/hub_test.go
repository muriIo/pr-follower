package sse_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// streamOnce runs Stream until publish has run and the context is canceled, then returns the body.
func streamOnce(t *testing.T, hub *sse.Hub, allow sse.AllowFunc, publish func()) (string, http.Header) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		hub.Stream(rec, req, allow)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for hub.SubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	publish()
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
	return rec.Body.String(), rec.Header()
}

func allowAll(context.Context, entity.ActivityEvent) bool { return true }

func TestHub_StreamSendsEveryTypeAsOneActivityEvent(t *testing.T) {
	t.Parallel()
	hub := sse.NewHub(10)

	body, header := streamOnce(t, hub, allowAll, func() {
		for _, typ := range []string{"pr.synchronize", "pr.opened", "dependabot.analyzed"} {
			_ = hub.Publish(context.Background(), entity.ActivityEvent{ID: "ev-" + typ, Type: typ, RepositoryID: 1, Summary: typ})
		}
	})

	if ct := header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream, got %q", ct)
	}
	if !strings.HasPrefix(body, "retry: ") {
		t.Errorf("expected a reconnect hint first, got:\n%s", body)
	}
	if strings.Count(body, "event: activity\n") != 3 {
		t.Errorf("expected 3 frames named \"activity\" whatever their type, got:\n%s", body)
	}
	for _, typ := range []string{"pr.synchronize", "pr.opened", "dependabot.analyzed"} {
		if !strings.Contains(body, "id: ev-"+typ+"\n") || !strings.Contains(body, `"type":"`+typ+`"`) {
			t.Errorf("missing id/type for %s in:\n%s", typ, body)
		}
	}
}

func TestHub_StreamDeliversOnlyAllowedEvents(t *testing.T) {
	t.Parallel()
	hub := sse.NewHub(10)
	onlyRepo1 := func(_ context.Context, ev entity.ActivityEvent) bool { return ev.RepositoryID == 1 }

	body, _ := streamOnce(t, hub, onlyRepo1, func() {
		_ = hub.Publish(context.Background(), entity.ActivityEvent{ID: "mine", Type: "pr.opened", RepositoryID: 1})
		_ = hub.Publish(context.Background(), entity.ActivityEvent{ID: "someone-elses", Type: "pr.opened", RepositoryID: 2})
	})

	if !strings.Contains(body, "id: mine") || strings.Contains(body, "someone-elses") {
		t.Fatalf("expected only the allowed event, got:\n%s", body)
	}
}

func TestHub_StreamSendsHeartbeats(t *testing.T) {
	t.Parallel()
	hub := sse.NewHub(10)
	hub.SetHeartbeat(5 * time.Millisecond)

	body, _ := streamOnce(t, hub, allowAll, func() {})
	if !strings.Contains(body, ": ping\n\n") {
		t.Fatalf("expected heartbeat comments to keep proxies from closing the stream, got:\n%s", body)
	}
}

// firstWriteWriter reports when the stream writes its first bytes (the client sees it open).
type firstWriteWriter struct {
	*httptest.ResponseRecorder
	once  sync.Once
	ready chan struct{}
}

func (w *firstWriteWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(b)
	w.once.Do(func() { close(w.ready) })
	return n, err
}

func TestHub_StreamSubscribesBeforeTheClientSeesItOpen(t *testing.T) {
	t.Parallel()
	hub := sse.NewHub(10)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream", nil).WithContext(ctx)
	w := &firstWriteWriter{ResponseRecorder: httptest.NewRecorder(), ready: make(chan struct{})}

	done := make(chan struct{})
	go func() {
		hub.Stream(w, req, allowAll)
		close(done)
	}()
	<-w.ready // publish the instant the stream opens
	_ = hub.Publish(context.Background(), entity.ActivityEvent{ID: "right-away", Type: "pr.opened", RepositoryID: 1})
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done

	if !strings.Contains(w.Body.String(), "id: right-away") {
		t.Fatalf("an event published as soon as the stream opened was lost:\n%s", w.Body.String())
	}
}
