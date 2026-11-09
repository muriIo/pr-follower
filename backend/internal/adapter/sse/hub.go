package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

type Hub struct {
	mu          sync.RWMutex
	subscribers map[chan entity.ActivityEvent]struct{}
	history     []entity.ActivityEvent
	maxHistory  int
}

func NewHub(maxHistory int) *Hub {
	if maxHistory <= 0 {
		maxHistory = 100
	}
	return &Hub{
		subscribers: make(map[chan entity.ActivityEvent]struct{}),
		history:     make([]entity.ActivityEvent, 0, maxHistory),
		maxHistory:  maxHistory,
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

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	_ = rc.Flush()

	ctx := r.Context()
	events := h.Subscribe(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			evType := ev.Type
			if evType == "" {
				evType = "message"
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evType, payload)
			_ = rc.Flush()
		}
	}
}
