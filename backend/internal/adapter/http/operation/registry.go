// Package operation lets a user cancel a long request they started (ADR-0007), whichever API replica
// runs it: the cancel request may land on another replica, which then asks every replica.
package operation

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// maxOperationIDLength keeps a cancel message well under Postgres' NOTIFY payload limit. The SPA
// sends UUIDs; a longer id is not tracked, so that request simply cannot be cancelled.
const maxOperationIDLength = 128

// Outcome is what a cancel request achieved.
type Outcome int

const (
	// NotFound: no replica can be asked, and this one does not run the operation.
	NotFound Outcome = iota
	// Canceled: this replica ran the operation and cancelled it.
	Canceled
	// Requested: this replica does not run it; every replica was asked to cancel it.
	Requested
)

// Registry holds the cancel functions of the operations running on this replica, keyed by user and
// operation id (a user can only cancel their own operations).
//
// replica-local: the cancel functions of requests this replica is serving; a cancel that lands elsewhere reaches it through the replica broadcast.
type Registry struct {
	mu      sync.Mutex
	cancels map[string]context.CancelCauseFunc
	bus     port.ReplicaBroadcast
}

func NewRegistry() *Registry {
	return &Registry{cancels: make(map[string]context.CancelCauseFunc)}
}

// Connect makes cancels reach the other replicas through bus, and applies their cancels here, until
// ctx ends. Without it the registry serves one replica only.
func (r *Registry) Connect(ctx context.Context, bus port.ReplicaBroadcast) {
	messages := bus.Subscribe(ctx)
	r.mu.Lock()
	r.bus = bus
	r.mu.Unlock()
	go func() {
		for key := range messages {
			r.cancelLocal(key)
		}
	}()
}

func key(userID uint64, operationID string) (string, bool) {
	id := strings.TrimSpace(operationID)
	if id == "" || len(id) > maxOperationIDLength {
		return "", false
	}
	return strconv.FormatUint(userID, 10) + ":" + id, true
}

// Begin returns a context the user can cancel by operationID, and the function to call when the
// operation ends.
func (r *Registry) Begin(parent context.Context, userID uint64, operationID string) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	k, ok := key(userID, operationID)
	if !ok {
		return ctx, func() { cancel(nil) }
	}

	r.mu.Lock()
	r.cancels[k] = cancel
	r.mu.Unlock()

	release := func() {
		r.mu.Lock()
		delete(r.cancels, k)
		r.mu.Unlock()
		cancel(nil)
	}
	return ctx, release
}

// Cancel stops the user's operation: here when this replica runs it, else by asking every replica.
func (r *Registry) Cancel(ctx context.Context, userID uint64, operationID string) (Outcome, error) {
	k, ok := key(userID, operationID)
	if !ok {
		return NotFound, nil
	}
	if r.cancelLocal(k) {
		return Canceled, nil
	}
	r.mu.Lock()
	bus := r.bus
	r.mu.Unlock()
	if bus == nil {
		return NotFound, nil
	}
	if err := bus.Broadcast(ctx, k); err != nil {
		return NotFound, err
	}
	return Requested, nil
}

func (r *Registry) cancelLocal(k string) bool {
	r.mu.Lock()
	cancel, ok := r.cancels[k]
	if ok {
		delete(r.cancels, k)
	}
	r.mu.Unlock()
	if ok {
		cancel(entity.ErrOperationCanceled)
	}
	return ok
}

// IsActive reports whether the user's operation runs on this replica.
func (r *Registry) IsActive(userID uint64, operationID string) bool {
	k, ok := key(userID, operationID)
	if !ok {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok = r.cancels[k]
	return ok
}
