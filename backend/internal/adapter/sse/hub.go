package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

const (
	// streamEventName is the single SSE event name for every activity; the activity type
	// travels in the JSON payload. Browsers only deliver named events that have a listener,
	// so per-type names silently dropped every type the SPA did not list.
	streamEventName  = "activity"
	defaultHeartbeat = 25 * time.Second
	reconnectHintMs  = 3000
)

// AllowFunc decides whether one subscriber may see an event (e.g. only repositories the
// signed-in user follows).
type AllowFunc func(ctx context.Context, event entity.ActivityEvent) bool

type Hub struct {
	mu          sync.RWMutex
	subscribers map[chan entity.ActivityEvent]struct{}
	history     []entity.ActivityEvent
	maxHistory  int
	heartbeat   time.Duration
}

func NewHub(maxHistory int) *Hub {
	if maxHistory <= 0 {
		maxHistory = 100
	}
	return &Hub{
		subscribers: make(map[chan entity.ActivityEvent]struct{}),
		history:     make([]entity.ActivityEvent, 0, maxHistory),
		maxHistory:  maxHistory,
		heartbeat:   defaultHeartbeat,
	}
}

// SetHeartbeat sets how often an idle stream sends a comment so proxies and load balancers
// keep the connection open.
func (h *Hub) SetHeartbeat(d time.Duration) {
	if d > 0 {
		h.heartbeat = d
	}
}

func (h *Hub) Subscribe(ctx context.Context) <-chan entity.ActivityEvent {
	ch := make(chan entity.ActivityEvent, 32)

	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()

	go func() {
		<-ctx.Done()
		h.mu.Lock()
		if _, ok := h.subscribers[ch]; ok {
			delete(h.subscribers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}()

	return ch
}

func (h *Hub) Publish(ctx context.Context, event entity.ActivityEvent) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}

	h.mu.Lock()
	if len(h.history) >= h.maxHistory {
		h.history = h.history[1:]
	}
	h.history = append(h.history, event)

	for ch := range h.subscribers {
		select {
		case ch <- event:
		default:
			// Non-blocking send so a slow subscriber never stalls the hub
		}
	}
	h.mu.Unlock()

	return nil
}

func (h *Hub) RecentEvents() []entity.ActivityEvent {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]entity.ActivityEvent, len(h.history))
	copy(out, h.history)
	return out
}

func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}

// Stream serves one Server-Sent Events connection, delivering only events allow accepts.
// The subscription is registered before anything is written, so nothing published once the
// client sees the stream open is lost.
func (h *Hub) Stream(w http.ResponseWriter, r *http.Request, allow AllowFunc) {
	ctx := r.Context()
	events := h.Subscribe(ctx)

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	_, _ = fmt.Fprintf(w, "retry: %d\n: connected\n\n", reconnectHintMs)
	_ = rc.Flush()

	heartbeat := time.NewTicker(h.heartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			if !allow(ctx, ev) {
				continue
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", ev.ID, streamEventName, payload); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}
