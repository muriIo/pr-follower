package operation

import (
	"context"
	"strings"
	"sync"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

type Registry struct {
	mu      sync.RWMutex
	cancels map[string]context.CancelCauseFunc
}

func NewRegistry() *Registry {
	return &Registry{
		cancels: make(map[string]context.CancelCauseFunc),
	}
}

func (r *Registry) Begin(parent context.Context, operationID string) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	cleanID := strings.TrimSpace(operationID)
	if cleanID == "" {
		return ctx, func() { cancel(nil) }
	}

	r.mu.Lock()
	r.cancels[cleanID] = cancel
	r.mu.Unlock()

	release := func() {
		r.mu.Lock()
		delete(r.cancels, cleanID)
		r.mu.Unlock()
		cancel(nil)
	}
	return ctx, release
}

func (r *Registry) Cancel(operationID string) bool {
	cleanID := strings.TrimSpace(operationID)
	if cleanID == "" {
		return false
	}

	r.mu.Lock()
	cancel, ok := r.cancels[cleanID]
	if ok {
		delete(r.cancels, cleanID)
	}
	r.mu.Unlock()

	if !ok {
		return false
	}
	cancel(entity.ErrOperationCanceled)
	return true
}

func (r *Registry) IsActive(operationID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.cancels[strings.TrimSpace(operationID)]
	return ok
}
