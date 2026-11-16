package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
)

func receive(t *testing.T, ch <-chan string, what string) string {
	t.Helper()
	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatalf("channel closed while waiting for %s", what)
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return ""
	}
}

func TestPostgresReplicaBroadcast_EveryReplicaReceivesEveryMessage(t *testing.T) {
	db, dsn := postgresForSignal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	replicaA := persistence.NewPostgresReplicaBroadcast(db, dsn, "pr_follower_test_broadcast")
	replicaB := persistence.NewPostgresReplicaBroadcast(db, dsn, "pr_follower_test_broadcast")
	onA := replicaA.Subscribe(ctx)
	onB := replicaB.Subscribe(ctx)

	// A subscription is only live once its LISTEN ran; send until B hears one, then check both.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := replicaA.Broadcast(ctx, "1:op-1"); err != nil {
			t.Fatalf("Broadcast() error: %v", err)
		}
		select {
		case msg := <-onB:
			if msg != "1:op-1" {
				t.Fatalf("B got %q", msg)
			}
			if got := receive(t, onA, "A's own message"); got != "1:op-1" {
				t.Fatalf("A got %q", got)
			}
			return
		case <-time.After(100 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("B never received a broadcast")
			}
		}
	}
}

func TestPostgresReplicaBroadcast_SubscriptionClosesWithItsContext(t *testing.T) {
	db, dsn := postgresForSignal(t)
	ctx, cancel := context.WithCancel(context.Background())

	ch := persistence.NewPostgresReplicaBroadcast(db, dsn, "pr_follower_test_broadcast").Subscribe(ctx)
	cancel()
	select {
	case _, ok := <-ch:
		for ok {
			_, ok = <-ch
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription must close with its context")
	}
}
