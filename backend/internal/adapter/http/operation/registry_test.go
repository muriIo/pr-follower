package operation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/http/operation"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func TestRegistry_BeginAndCancelOperation(t *testing.T) {
	t.Parallel()

	reg := operation.NewRegistry()
	ctx, release := reg.Begin(context.Background(), 1, "op-123")
	defer release()

	if !reg.IsActive(1, "op-123") {
		t.Fatal("expected op-123 to be active")
	}

	outcome, err := reg.Cancel(context.Background(), 1, "op-123")
	if err != nil || outcome != operation.Canceled {
		t.Fatalf("Cancel(op-123) = %v, %v; want Canceled", outcome, err)
	}

	if err := context.Cause(ctx); !errors.Is(err, entity.ErrOperationCanceled) {
		t.Fatalf("expected context cause ErrOperationCanceled, got %v", err)
	}

	if reg.IsActive(1, "op-123") {
		t.Fatal("expected op-123 to no longer be active after cancel")
	}

	if outcome, _ := reg.Cancel(context.Background(), 1, "non-existent"); outcome != operation.NotFound {
		t.Fatalf("a single replica that does not know the operation says so, got %v", outcome)
	}
}

func TestRegistry_OnlyTheUserWhoStartedAnOperationCancelsIt(t *testing.T) {
	t.Parallel()

	reg := operation.NewRegistry()
	ctx, release := reg.Begin(context.Background(), 1, "op-1")
	defer release()

	if outcome, _ := reg.Cancel(context.Background(), 2, "op-1"); outcome != operation.NotFound {
		t.Fatalf("another user's cancel = %v, want NotFound", outcome)
	}
	if ctx.Err() != nil {
		t.Fatal("another user must not cancel the operation")
	}
}

// bus is an in-process stand-in for Postgres NOTIFY: every subscriber gets every message.
type bus struct {
	mu   sync.Mutex
	subs []chan string
}

func (b *bus) Broadcast(_ context.Context, message string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		s <- message
	}
	return nil
}

func (b *bus) Subscribe(ctx context.Context) <-chan string {
	ch := make(chan string, 16)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

func TestRegistry_CancelReachesTheReplicaRunningTheOperation(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	shared := &bus{}
	replicaA, replicaB := operation.NewRegistry(), operation.NewRegistry()
	replicaA.Connect(ctx, shared)
	replicaB.Connect(ctx, shared)

	opCtx, release := replicaA.Begin(context.Background(), 1, "op-1")
	defer release()
	otherUser, releaseOther := replicaA.Begin(context.Background(), 2, "op-1")
	defer releaseOther()

	// The cancel lands on B, which does not run it: B asks every replica.
	outcome, err := replicaB.Cancel(context.Background(), 1, "op-1")
	if err != nil || outcome != operation.Requested {
		t.Fatalf("Cancel on another replica = %v, %v; want Requested", outcome, err)
	}

	select {
	case <-opCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the replica running the operation must cancel it")
	}
	if err := context.Cause(opCtx); !errors.Is(err, entity.ErrOperationCanceled) {
		t.Fatalf("cause = %v, want ErrOperationCanceled", err)
	}
	if otherUser.Err() != nil {
		t.Fatal("the same operation id of another user must keep running")
	}
}

func TestRegistry_RejectsOperationIDsTooLongToBroadcast(t *testing.T) {
	t.Parallel()

	reg := operation.NewRegistry()
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'a'
	}
	_, release := reg.Begin(context.Background(), 1, string(long))
	defer release()

	if reg.IsActive(1, string(long)) {
		t.Fatal("an id longer than the limit is not tracked (the request runs, uncancellable)")
	}
}
