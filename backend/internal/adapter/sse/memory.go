package sse

import (
	"context"
	"sync"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// memoryLogCapacity bounds a MemoryLog; the database-backed log prunes by age instead.
const memoryLogCapacity = 1000

// MemoryLog is an in-process port.ActivityLog for tests and for running without a database. It is
// not shared between processes, so it cannot back more than one replica.
//
// replica-local: the activity log for a single process (tests, SQLite); a server on Postgres uses the shared log.
type MemoryLog struct {
	mu     sync.Mutex
	events []port.LoggedActivity
	next   int64
}

var _ port.ActivityLog = (*MemoryLog)(nil)

func NewMemoryLog() *MemoryLog { return &MemoryLog{} }

func (l *MemoryLog) Append(ctx context.Context, ev entity.ActivityEvent) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, context.Cause(ctx)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	l.events = append(l.events, port.LoggedActivity{Seq: l.next, Event: ev})
	if len(l.events) > memoryLogCapacity {
		l.events = l.events[len(l.events)-memoryLogCapacity:]
	}
	return l.next, nil
}

func (l *MemoryLog) ListSince(ctx context.Context, afterSeq int64, limit int) ([]port.LoggedActivity, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]port.LoggedActivity, 0)
	for _, e := range l.events {
		if e.Seq > afterSeq {
			out = append(out, e)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (l *MemoryLog) LatestSeq(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, context.Cause(ctx)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.next, nil
}

func (l *MemoryLog) Recent(ctx context.Context, repositoryIDs []uint64, limit int) ([]entity.ActivityEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	wanted := make(map[uint64]bool, len(repositoryIDs))
	for _, id := range repositoryIDs {
		wanted[id] = true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]entity.ActivityEvent, 0)
	for i := len(l.events) - 1; i >= 0 && len(out) < limit; i-- {
		if wanted[l.events[i].Event.RepositoryID] {
			out = append(out, l.events[i].Event)
		}
	}
	return out, nil
}

func (l *MemoryLog) Prune(ctx context.Context, before time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, context.Cause(ctx)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.events[:0]
	var removed int64
	for _, e := range l.events {
		if !e.Event.OccurredAt.IsZero() && e.Event.OccurredAt.Before(before) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	l.events = kept
	return removed, nil
}

// LocalSignal is an in-process port.ActivitySignal: it wakes the listeners of this process only.
//
// replica-local: wake-ups within a single process (tests, SQLite); a server on Postgres uses LISTEN/NOTIFY.
type LocalSignal struct {
	mu        sync.Mutex
	listeners map[chan struct{}]struct{}
}

var _ port.ActivitySignal = (*LocalSignal)(nil)

func NewLocalSignal() *LocalSignal {
	return &LocalSignal{listeners: make(map[chan struct{}]struct{})}
}

func (s *LocalSignal) Notify(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.listeners {
		select {
		case ch <- struct{}{}:
		default: // a wake-up is already pending; they coalesce
		}
	}
	return nil
}

func (s *LocalSignal) Listen(ctx context.Context) <-chan struct{} {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.listeners[ch] = struct{}{}
	s.mu.Unlock()
	ch <- struct{}{} // like a (re)connection: read whatever is already there
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		delete(s.listeners, ch)
		close(ch)
		s.mu.Unlock()
	}()
	return ch
}
