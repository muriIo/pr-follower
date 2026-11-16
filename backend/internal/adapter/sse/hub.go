package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

const (
	// streamEventName is the single SSE event name for every activity; the activity type
	// travels in the JSON payload. Browsers only deliver named events that have a listener,
	// so per-type names silently dropped every type the SPA did not list.
	streamEventName  = "activity"
	heartbeatFrame   = "event: heartbeat\ndata: {}\n\n"
	defaultHeartbeat = 25 * time.Second
	reconnectHintMs  = 3000
)

// AllowFunc decides whether one subscriber may see an event (e.g. only repositories the
// signed-in user follows).
type AllowFunc func(ctx context.Context, event entity.ActivityEvent) bool

const (
	defaultPollInterval = 5 * time.Second
	defaultRetention    = 7 * 24 * time.Hour
	defaultPruneEvery   = time.Hour
	// batchSize bounds one read of the log.
	batchSize = 200
	// overlap is how many positions behind the newest one are read again on every pass. A sequence
	// number is taken when a row is inserted, not when it commits, so a lower number can become
	// visible after a higher one was already delivered; re-reading the tail picks it up.
	overlap = 64
)

// Hub fans activity out to the SSE subscribers of this replica. The ActivityLog is the single source
// of truth: Publish appends to it and wakes every replica through the ActivitySignal, and each replica
// (this one too) delivers what it reads back from the log, in order, to its own subscribers. Because
// delivery comes only from the log, a restart loses nothing and any number of replicas agree.
type Hub struct {
	log    port.ActivityLog
	signal port.ActivitySignal

	mu          sync.RWMutex
	subscribers map[chan entity.ActivityEvent]struct{}
	heartbeat   time.Duration
	poll        time.Duration
	retention   time.Duration
	pruneEvery  time.Duration

	// pump state, owned by the pump goroutine once Start has returned
	last      int64
	delivered map[int64]struct{}
}

func NewHub(log port.ActivityLog, signal port.ActivitySignal) *Hub {
	return &Hub{
		log:         log,
		signal:      signal,
		subscribers: make(map[chan entity.ActivityEvent]struct{}),
		heartbeat:   defaultHeartbeat,
		poll:        defaultPollInterval,
		retention:   defaultRetention,
		pruneEvery:  defaultPruneEvery,
		delivered:   make(map[int64]struct{}),
	}
}

// NewInMemoryHub is a started hub over in-process storage: for tests and for running without a
// database. It serves one process only.
func NewInMemoryHub(ctx context.Context) *Hub {
	h := NewHub(NewMemoryLog(), NewLocalSignal())
	h.poll = 20 * time.Millisecond
	_ = h.Start(ctx) // the in-memory log cannot fail to read
	return h
}

// SetHeartbeat sets how often an idle stream sends a heartbeat event. It keeps proxies and load
// balancers from closing the connection, and lets the client tell a live stream from a dead one
// (EventSource never shows the page a comment, so the heartbeat is an event). The SPA reconnects
// a stream silent for 60s, so keep this well under that.
func (h *Hub) SetHeartbeat(d time.Duration) {
	if d > 0 {
		h.heartbeat = d
	}
}

// SetPollInterval sets how often the log is read even when no wake-up arrived (the safety net for a
// lost signal). Call before Start.
func (h *Hub) SetPollInterval(d time.Duration) {
	if d > 0 {
		h.poll = d
	}
}

// SetRetention sets how long activity is kept and how often older activity is deleted. Call before Start.
func (h *Hub) SetRetention(keep, every time.Duration) {
	if keep > 0 && every > 0 {
		h.retention, h.pruneEvery = keep, every
	}
}

// Start begins delivering activity appended from now on, until ctx ends. History is not replayed to
// the stream (the timeline reads it with Recent). It returns once the starting position is known, so
// anything published after it returns is delivered.
func (h *Hub) Start(ctx context.Context) error {
	last, err := h.log.LatestSeq(ctx)
	if err != nil {
		return fmt.Errorf("read the activity log position: %w", err)
	}
	h.last = last
	tail, err := h.log.ListSince(ctx, max(0, last-overlap), batchSize)
	if err != nil {
		return fmt.Errorf("read the activity log tail: %w", err)
	}
	for _, e := range tail {
		if e.Seq <= last {
			h.delivered[e.Seq] = struct{}{}
		}
	}
	wake := h.signal.Listen(ctx)
	go h.pump(ctx, wake)
	return nil
}

func (h *Hub) pump(ctx context.Context, wake <-chan struct{}) {
	poll := time.NewTicker(h.poll)
	defer poll.Stop()
	prune := time.NewTicker(h.pruneEvery)
	defer prune.Stop()
	h.pruneOld(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-wake:
			if !ok {
				return
			}
			h.drain(ctx)
		case <-poll.C:
			h.drain(ctx)
		case <-prune.C:
			h.pruneOld(ctx)
		}
	}
}

// drain delivers every event not delivered yet, oldest first.
func (h *Hub) drain(ctx context.Context) {
	for {
		batch, err := h.log.ListSince(ctx, max(0, h.last-overlap), batchSize)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("reading the activity log failed", "err", err)
			}
			return
		}
		fresh := 0
		for _, e := range batch {
			if _, seen := h.delivered[e.Seq]; seen {
				continue
			}
			h.delivered[e.Seq] = struct{}{}
			h.last = max(h.last, e.Seq)
			h.deliver(e.Event)
			fresh++
		}
		for seq := range h.delivered {
			if seq < h.last-overlap {
				delete(h.delivered, seq)
			}
		}
		if fresh == 0 || len(batch) < batchSize {
			return
		}
	}
}

func (h *Hub) pruneOld(ctx context.Context) {
	if _, err := h.log.Prune(ctx, time.Now().Add(-h.retention)); err != nil && ctx.Err() == nil {
		slog.Warn("pruning old activity failed", "err", err)
	}
}

func (h *Hub) deliver(event entity.ActivityEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers {
		select {
		case ch <- event:
		default:
			// Non-blocking send so a slow subscriber never stalls the hub
		}
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

// Publish records the event in the shared log and wakes every replica to deliver it. The event is
// not delivered here directly: it reaches this replica's subscribers like everyone else's.
func (h *Hub) Publish(ctx context.Context, event entity.ActivityEvent) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if _, err := h.log.Append(ctx, event); err != nil {
		return fmt.Errorf("record activity: %w", err)
	}
	if err := h.signal.Notify(ctx); err != nil {
		// The event is safe in the log; replicas poll it, only a little later.
		slog.Warn("waking the replicas failed", "err", err)
	}
	return nil
}

// Recent returns the newest activity of the given repositories, newest first.
func (h *Hub) Recent(ctx context.Context, repositoryIDs []uint64, limit int) ([]entity.ActivityEvent, error) {
	return h.log.Recent(ctx, repositoryIDs, limit)
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
			if _, err := fmt.Fprint(w, heartbeatFrame); err != nil {
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
