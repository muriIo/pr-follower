package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
)

func TestPostgresReplicaBroadcast_EveryReplicaReceivesEveryMessage(t *testing.T) {
	db, dsn := postgresForSignal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	replicaA := persistence.NewPostgresReplicaBroadcast(db, dsn, "pr_follower_test_broadcast")
	replicaB := persistence.NewPostgresReplicaBroadcast(db, dsn, "pr_follower_test_broadcast")
	onA := replicaA.Subscribe(ctx)
	onB := replicaB.Subscribe(ctx)

	// Each subscription is live only once its own LISTEN ran, and the two start independently: keep
	// sending until both have heard one (the sender included), so neither can miss its only chance.
	heardA, heardB := false, false
	deadline := time.Now().Add(10 * time.Second)
	for !heardA || !heardB {
		if err := replicaA.Broadcast(ctx, "1:op-1"); err != nil {
			t.Fatalf("Broadcast() error: %v", err)
		}
		wait := time.After(100 * time.Millisecond)
	drain:
		for {
			select {
			case msg := <-onA:
				if msg != "1:op-1" {
					t.Fatalf("A got %q", msg)
				}
				heardA = true
			case msg := <-onB:
				if msg != "1:op-1" {
					t.Fatalf("B got %q", msg)
				}
				heardB = true
			case <-wait:
				break drain
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("after 10 s: A heard a broadcast = %v, B = %v", heardA, heardB)
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
