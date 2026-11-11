package dependabot

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFlightGroup_ConcurrentCallersShareOneRunAndItsError(t *testing.T) {
	t.Parallel()
	var g flightGroup[int]
	var runs, entered atomic.Int32
	release := make(chan struct{})
	boom := errors.New("boom")

	var wg sync.WaitGroup
	results := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entered.Add(1)
			_, results[i] = g.do(context.Background(), "k", func(context.Context) (int, error) {
				runs.Add(1)
				<-release
				return 0, boom
			})
		}()
	}
	for runs.Load() == 0 || entered.Load() < int32(len(results)) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let the late callers reach the group and wait on the leader
	close(release)
	wg.Wait()

	if runs.Load() != 1 {
		t.Fatalf("expected one run, got %d", runs.Load())
	}
	for _, err := range results {
		if !errors.Is(err, boom) {
			t.Fatalf("every caller gets the shared error, got %v", err)
		}
	}
}

func TestFlightGroup_AWaiterThatGivesUpDoesNotCancelTheRun(t *testing.T) {
	t.Parallel()
	var g flightGroup[int]
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan int, 1)
	go func() {
		v, _ := g.do(context.Background(), "k", func(context.Context) (int, error) {
			close(started)
			<-release
			return 42, nil
		})
		done <- v
	}()
	<-started

	waiter, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.do(waiter, "k", func(context.Context) (int, error) { return 0, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("a waiter leaves with its own cancellation, got %v", err)
	}
	close(release)
	if v := <-done; v != 42 {
		t.Fatalf("the run was cancelled with the waiter: %d", v)
	}
}
