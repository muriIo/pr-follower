package dependabot

import (
	"context"
	"sync"
)

type flightCall[T any] struct {
	done chan struct{}
	val  T
	err  error
}

// flightGroup lets concurrent callers for one key share a single run of fn. A waiting caller
// gives up when its own context ends, without cancelling the run it waits for.
//
// replica-local: joins analyses of one PR running in this process; two replicas may analyse the same commit at once, and the result converges (stored per commit).
type flightGroup[T any] struct {
	mu    sync.Mutex
	calls map[string]*flightCall[T]
}

func (g *flightGroup[T]) do(ctx context.Context, key string, fn func(ctx context.Context) (T, error)) (T, error) {
	var zero T
	g.mu.Lock()
	if g.calls == nil {
		g.calls = map[string]*flightCall[T]{}
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
	call := &flightCall[T]{done: make(chan struct{})}
	g.calls[key] = call
	g.mu.Unlock()

	call.val, call.err = fn(ctx)
	close(call.done)

	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()
	return call.val, call.err
}
