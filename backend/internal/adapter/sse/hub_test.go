package sse_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/sse"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

func TestHub_PublishAndSubscribeWithContextCleanup(t *testing.T) {
	t.Parallel()

	hub := sse.NewInMemoryHub(t.Context())
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
		Params:     map[string]string{"title": "feat: x"},
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

	recent, err := hub.Recent(context.Background(), []uint64{0}, 10)
	if err != nil || len(recent) != 1 || recent[0].ID != "ev-1" {
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
	hub := sse.NewInMemoryHub(t.Context())

	body, header := streamOnce(t, hub, allowAll, func() {
		for _, typ := range []string{"pr.synchronize", "pr.opened", "dependabot.analyzed"} {
			_ = hub.Publish(context.Background(), entity.ActivityEvent{ID: "ev-" + typ, Type: typ, RepositoryID: 1})
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
	hub := sse.NewInMemoryHub(t.Context())
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
	hub := sse.NewInMemoryHub(t.Context())
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
	hub := sse.NewInMemoryHub(t.Context())
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

// receive waits for one event or fails.
func receive(t *testing.T, ch <-chan entity.ActivityEvent) entity.ActivityEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
		return entity.ActivityEvent{}
	}
}

func expectNone(t *testing.T, ch <-chan entity.ActivityEvent) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event %q", ev.ID)
	case <-time.After(60 * time.Millisecond):
	}
}

func startedHub(t *testing.T, log port.ActivityLog, signal port.ActivitySignal, poll time.Duration) *sse.Hub {
	t.Helper()
	hub := sse.NewHub(log, signal)
	hub.SetPollInterval(poll)
	if err := hub.Start(t.Context()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	return hub
}

func TestHub_AnotherReplicaDeliversWhatThisOnePublished(t *testing.T) {
	t.Parallel()
	// Two replicas share the database (the log) but not memory (each has its own signal), so the
	// second one only learns of the event by polling the log.
	log := sse.NewMemoryLog()
	replicaA := startedHub(t, log, sse.NewLocalSignal(), time.Hour)
	replicaB := startedHub(t, log, sse.NewLocalSignal(), 10*time.Millisecond)

	onB := replicaB.Subscribe(t.Context())
	if err := replicaA.Publish(context.Background(), entity.ActivityEvent{ID: "from-a", Type: "pr.opened", RepositoryID: 1}); err != nil {
		t.Fatalf("Publish() error: %v", err)
	}

	if got := receive(t, onB); got.ID != "from-a" {
		t.Fatalf("replica B got %q, want from-a", got.ID)
	}
	recent, _ := replicaB.Recent(context.Background(), []uint64{1}, 10)
	if len(recent) != 1 || recent[0].ID != "from-a" {
		t.Fatalf("the timeline on replica B must include it, got %#v", recent)
	}
}

func TestHub_SignalDeliversWithoutWaitingForThePoll(t *testing.T) {
	t.Parallel()
	log := sse.NewMemoryLog()
	signal := sse.NewLocalSignal()
	replicaA := startedHub(t, log, signal, time.Hour)
	replicaB := startedHub(t, log, signal, time.Hour) // one-hour poll: only the signal can wake it

	onB := replicaB.Subscribe(t.Context())
	_ = replicaA.Publish(context.Background(), entity.ActivityEvent{ID: "wake", Type: "pr.opened", RepositoryID: 1})

	if got := receive(t, onB); got.ID != "wake" {
		t.Fatalf("got %q, want wake", got.ID)
	}
}

func TestHub_DoesNotReplayHistoryToNewSubscribers(t *testing.T) {
	t.Parallel()
	log := sse.NewMemoryLog()
	_, _ = log.Append(context.Background(), entity.ActivityEvent{ID: "before-start", RepositoryID: 1})

	hub := startedHub(t, log, sse.NewLocalSignal(), 10*time.Millisecond)
	ch := hub.Subscribe(t.Context())

	expectNone(t, ch)
	recent, _ := hub.Recent(context.Background(), []uint64{1}, 10)
	if len(recent) != 1 {
		t.Fatalf("history stays available to the timeline, got %#v", recent)
	}
}

// lateLog hides one position until reveal is called, like a transaction that took its sequence
// number first but committed after a later one.
type lateLog struct {
	*sse.MemoryLog
	mu       sync.Mutex
	hidden   int64
	revealed bool
}

func (l *lateLog) ListSince(ctx context.Context, after int64, limit int) ([]port.LoggedActivity, error) {
	all, err := l.MemoryLog.ListSince(ctx, after, limit)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.revealed {
		return all, err
	}
	out := all[:0:0]
	for _, e := range all {
		if e.Seq != l.hidden {
			out = append(out, e)
		}
	}
	return out, err
}

func (l *lateLog) reveal() { l.mu.Lock(); l.revealed = true; l.mu.Unlock() }

func TestHub_DeliversAnEventThatCommittedLateExactlyOnce(t *testing.T) {
	t.Parallel()
	log := &lateLog{MemoryLog: sse.NewMemoryLog(), hidden: 2}
	hub := startedHub(t, log, sse.NewLocalSignal(), 5*time.Millisecond)
	ch := hub.Subscribe(t.Context())

	for _, id := range []string{"one", "late", "three"} {
		_ = hub.Publish(context.Background(), entity.ActivityEvent{ID: id, RepositoryID: 1})
	}
	if a, b := receive(t, ch), receive(t, ch); a.ID != "one" || b.ID != "three" {
		t.Fatalf("expected one then three first, got %q, %q", a.ID, b.ID)
	}

	log.reveal()
	if got := receive(t, ch); got.ID != "late" {
		t.Fatalf("expected the late event, got %q", got.ID)
	}
	expectNone(t, ch) // and nothing is delivered twice
}

type failingLog struct{ *sse.MemoryLog }

func (failingLog) Append(context.Context, entity.ActivityEvent) (int64, error) {
	return 0, errors.New("database down")
}

func TestHub_PublishFailsWhenTheEventCannotBeRecorded(t *testing.T) {
	t.Parallel()
	hub := startedHub(t, failingLog{sse.NewMemoryLog()}, sse.NewLocalSignal(), 10*time.Millisecond)
	ch := hub.Subscribe(t.Context())

	if err := hub.Publish(context.Background(), entity.ActivityEvent{ID: "x", RepositoryID: 1}); err == nil {
		t.Fatal("Publish() must report that the event was not recorded")
	}
	expectNone(t, ch)
}

func TestHub_PublishStillSucceedsWhenOnlyTheSignalFails(t *testing.T) {
	t.Parallel()
	log := sse.NewMemoryLog()
	hub := startedHub(t, log, brokenSignal{}, 10*time.Millisecond)
	ch := hub.Subscribe(t.Context())

	if err := hub.Publish(context.Background(), entity.ActivityEvent{ID: "x", RepositoryID: 1}); err != nil {
		t.Fatalf("a failed wake-up must not fail the publish (readers poll): %v", err)
	}
	if got := receive(t, ch); got.ID != "x" {
		t.Fatalf("the poll must still deliver it, got %q", got.ID)
	}
}

type brokenSignal struct{}

func (brokenSignal) Notify(context.Context) error { return errors.New("notify failed") }
func (brokenSignal) Listen(ctx context.Context) <-chan struct{} {
	ch := make(chan struct{})
	go func() { <-ctx.Done(); close(ch) }()
	return ch
}

func TestHub_PublishHonoursACancelledContext(t *testing.T) {
	t.Parallel()
	hub := sse.NewInMemoryHub(t.Context())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := hub.Publish(ctx, entity.ActivityEvent{ID: "x", RepositoryID: 1}); err == nil {
		t.Fatal("Publish() must fail on a cancelled context")
	}
}

type pruneLog struct {
	*sse.MemoryLog
	cutoffs chan time.Time
}

func (l pruneLog) Prune(_ context.Context, before time.Time) (int64, error) {
	l.cutoffs <- before
	return 0, nil
}

func TestHub_PrunesActivityOlderThanTheRetention(t *testing.T) {
	t.Parallel()
	log := pruneLog{MemoryLog: sse.NewMemoryLog(), cutoffs: make(chan time.Time, 4)}
	hub := sse.NewHub(log, sse.NewLocalSignal())
	hub.SetRetention(48*time.Hour, 5*time.Millisecond)
	if err := hub.Start(t.Context()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	select {
	case cutoff := <-log.cutoffs:
		age := time.Since(cutoff)
		if age < 47*time.Hour || age > 49*time.Hour {
			t.Fatalf("cutoff should be about 48h ago, was %v ago", age)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the log was never pruned")
	}
}

func TestMemoryLog_KeepsOnlyTheNewestEvents(t *testing.T) {
	t.Parallel()
	log := sse.NewMemoryLog()
	for i := 0; i < 1100; i++ {
		_, _ = log.Append(context.Background(), entity.ActivityEvent{ID: strconv.Itoa(i), RepositoryID: 1})
	}
	all, _ := log.ListSince(context.Background(), 0, 5000)
	if len(all) != 1000 || all[0].Event.ID != "100" {
		t.Fatalf("expected the newest 1000 (from id 100), got %d from %q", len(all), all[0].Event.ID)
	}
}
