package resilience_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

func TestRetryWithBackoff_TransientSuccessWithFullJitter(t *testing.T) {
	t.Parallel()

	var recordedSleeps []time.Duration
	cfg := resilience.BackoffConfig{
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    1 * time.Second,
		MaxAttempts: 4,
		RandFloat64: func() float64 { return 0.5 }, // Deterministic 50% jitter
		SleepFunc: func(_ context.Context, d time.Duration) error {
			recordedSleeps = append(recordedSleeps, d)
			return nil
		},
	}

	attempts := 0
	val, err := resilience.Retry(context.Background(), cfg, func(_ context.Context) (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("transient 503 error")
		}
		return "ok", nil
	})

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if val != "ok" {
		t.Fatalf("expected 'ok', got %q", val)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	// Attempt 0 cap = 100ms * 0.5 = 50ms; Attempt 1 cap = 200ms * 0.5 = 100ms
	if len(recordedSleeps) != 2 || recordedSleeps[0] != 50*time.Millisecond || recordedSleeps[1] != 100*time.Millisecond {
		t.Fatalf("unexpected jittered sleeps: %v", recordedSleeps)
	}
}

func TestRetryWithBackoff_PermanentErrorStopsImmediately(t *testing.T) {
	t.Parallel()

	cfg := resilience.BackoffConfig{
		BaseDelay:   50 * time.Millisecond,
		MaxDelay:    500 * time.Millisecond,
		MaxAttempts: 5,
		SleepFunc: func(_ context.Context, _ time.Duration) error {
			t.Fatal("should not sleep on permanent error")
			return nil
		},
	}

	attempts := 0
	_, err := resilience.Retry(context.Background(), cfg, func(_ context.Context) (int, error) {
		attempts++
		return 0, resilience.Permanent(errors.New("404 not found"))
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt on permanent error, got %d", attempts)
	}
}

func TestRetryWithBackoff_HonorsRetryAfterHeaderDelay(t *testing.T) {
	t.Parallel()

	var slept time.Duration
	cfg := resilience.BackoffConfig{
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    100 * time.Millisecond,
		MaxAttempts: 3,
		SleepFunc: func(_ context.Context, d time.Duration) error {
			slept = d
			return nil
		},
	}

	attempts := 0
	_, err := resilience.Retry(context.Background(), cfg, func(_ context.Context) (bool, error) {
		attempts++
		if attempts == 1 {
			return false, resilience.RetryAfter(errors.New("429 rate limited"), 2*time.Second)
		}
		return true, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slept != 2*time.Second {
		t.Fatalf("expected Retry-After delay of 2s, got %v", slept)
	}
}

func TestRetryWithBackoff_ContextCancellationAbortsImmediately(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancelCause(context.Background())
	cfg := resilience.BackoffConfig{
		BaseDelay:   10 * time.Second,
		MaxDelay:    30 * time.Second,
		MaxAttempts: 5,
	}

	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel(entity.ErrOperationCanceled)
	}()

	start := time.Now()
	_, err := resilience.Retry(ctx, cfg, func(_ context.Context) (string, error) {
		return "", errors.New("transient error")
	})
	elapsed := time.Since(start)

	if !errors.Is(err, entity.ErrOperationCanceled) {
		t.Fatalf("expected ErrOperationCanceled, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected immediate cancellation, took %v", elapsed)
	}
}

func TestSingleFlightGroup_DeduplicatesConcurrentCalls(t *testing.T) {
	t.Parallel()

	var group resilience.SingleFlightGroup[string]
	var execCount atomic.Int32

	const workers = 8
	errCh := make(chan error, workers)

	for range workers {
		go func() {
			val, err := group.Do(context.Background(), "repo:1:sha:abc", func(_ context.Context) (string, error) {
				execCount.Add(1)
				time.Sleep(30 * time.Millisecond)
				return "computed-tree", nil
			})
			if err != nil {
				errCh <- err
				return
			}
			if val != "computed-tree" {
				errCh <- errors.New("unexpected value")
				return
			}
			errCh <- nil
		}()
	}

	for range workers {
		if err := <-errCh; err != nil {
			t.Fatalf("worker failed: %v", err)
		}
	}

	if got := execCount.Load(); got != 1 {
		t.Fatalf("expected underlying function to execute once, got %d", got)
	}
}
