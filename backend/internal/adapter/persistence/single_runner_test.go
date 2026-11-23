package persistence_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
)

func TestSingleRunner_WithoutPostgresRunsEveryTime(t *testing.T) {
	t.Parallel()
	runner := persistence.NewSingleRunner(nil)
	ran := 0
	for range 2 {
		ok, err := runner.TryRun(context.Background(), 7, func(context.Context) error { ran++; return nil })
		if !ok || err != nil {
			t.Fatalf("TryRun() = %v, %v", ok, err)
		}
	}
	boom := errors.New("boom")
	if _, err := runner.TryRun(context.Background(), 7, func(context.Context) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("the work's error comes back, got %v", err)
	}
	if ran != 2 {
		t.Fatalf("ran %d times, want 2", ran)
	}
}

func TestSingleRunner_OneReplicaAtATime(t *testing.T) {
	t.Parallel()
	db, _ := postgresForSignal(t)
	a, b := persistence.NewSingleRunner(db), persistence.NewSingleRunner(db)
	ctx := context.Background()

	ok, err := a.TryRun(ctx, 4097083627, func(ctx context.Context) error {
		inner, err := b.TryRun(ctx, 4097083627, func(context.Context) error {
			t.Error("the second runner must not run while the first holds the lock")
			return nil
		})
		if inner || err != nil {
			t.Errorf("the second runner: %v, %v", inner, err)
		}
		return nil
	})
	if !ok || err != nil {
		t.Fatalf("the first runner: %v, %v", ok, err)
	}
	if ok, _ := b.TryRun(ctx, 4097083627, func(context.Context) error { return nil }); !ok {
		t.Fatal("the lock is released when the work returns")
	}
}
