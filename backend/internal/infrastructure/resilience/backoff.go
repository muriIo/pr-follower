package resilience

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

type permanentError struct {
	err error
}

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

type retryAfterError struct {
	err   error
	delay time.Duration
}

func (e retryAfterError) Error() string { return e.err.Error() }
func (e retryAfterError) Unwrap() error { return e.err }

func RetryAfter(err error, delay time.Duration) error {
	if err == nil {
		return nil
	}
	return retryAfterError{err: err, delay: delay}
}

type BackoffConfig struct {
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	MaxAttempts int
	RandFloat64 func() float64
	SleepFunc   func(ctx context.Context, d time.Duration) error
}

func DefaultBackoffConfig() BackoffConfig {
	return BackoffConfig{
		BaseDelay:   150 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		MaxAttempts: 4,
	}
}

func Retry[T any](ctx context.Context, cfg BackoffConfig, fn func(ctx context.Context) (T, error)) (T, error) {
	var zero T
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	baseDelay := cfg.BaseDelay
	if baseDelay <= 0 {
		baseDelay = 100 * time.Millisecond
	}
	maxDelay := cfg.MaxDelay
	if maxDelay <= 0 {
		maxDelay = 5 * time.Second
	}
	randFn := cfg.RandFloat64
	if randFn == nil {
		randFn = rand.Float64
	}
	sleepFn := cfg.SleepFunc
	if sleepFn == nil {
		sleepFn = contextAwareSleep
	}

	var lastErr error
	for attempt := range maxAttempts {
		if err := ctx.Err(); err != nil {
			return zero, context.Cause(ctx)
		}

		res, err := fn(ctx)
		if err == nil {
			return res, nil
		}
		lastErr = err

		var permErr permanentError
		if errors.As(err, &permErr) {
			return zero, permErr.err
		}

		if attempt == maxAttempts-1 {
			break
		}

		delay := computeFullJitterDelay(attempt, baseDelay, maxDelay, randFn)
		var raErr retryAfterError
		if errors.As(err, &raErr) && raErr.delay > 0 {
			delay = raErr.delay
		}

		if sleepErr := sleepFn(ctx, delay); sleepErr != nil {
			return zero, sleepErr
		}
	}

	return zero, lastErr
}

func computeFullJitterDelay(attempt int, base, maxDelay time.Duration, randFn func() float64) time.Duration {
	shift := min(attempt, 20)
	exp := base * time.Duration(1<<shift)
	if exp > maxDelay || exp <= 0 {
		exp = maxDelay
	}
	factor := randFn()
	if factor < 0 {
		factor = 0
	} else if factor > 1 {
		factor = 1
	}
	return time.Duration(float64(exp) * factor)
}

func contextAwareSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

type inflightCall[T any] struct {
	done chan struct{}
	val  T
	err  error
}

type SingleFlightGroup[T any] struct {
	mu    sync.Mutex
	calls map[string]*inflightCall[T]
}

func (g *SingleFlightGroup[T]) Do(ctx context.Context, key string, fn func(ctx context.Context) (T, error)) (T, error) {
	var zero T
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*inflightCall[T])
	}
	if existing, ok := g.calls[key]; ok {
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return zero, context.Cause(ctx)
		case <-existing.done:
			return existing.val, existing.err
		}
	}

	call := &inflightCall[T]{done: make(chan struct{})}
	g.calls[key] = call
	g.mu.Unlock()

	call.val, call.err = fn(ctx)
	close(call.done)

	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()

	return call.val, call.err
}
