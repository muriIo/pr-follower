// Package syncjob runs the "sync with GitHub" in the background, one per user. A sync of many
// repositories takes minutes (hundreds of GitHub calls, one after another), far longer than an HTTP
// request should be held open, so the request only starts it and the user reads its progress.
package syncjob

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// State is where a user's sync stands.
type State string

const (
	StateIdle     State = "idle" // never started since the server started
	StateRunning  State = "running"
	StateDone     State = "done"
	StateFailed   State = "failed"
	StateCanceled State = "canceled"
)

// maxDuration stops a sync that never ends; a normal one takes minutes.
const maxDuration = 30 * time.Minute

// Status is a snapshot of a user's latest sync.
type Status struct {
	State      State
	Progress   entity.SyncProgress
	Result     *entity.SyncResult // set when done
	Err        error              // set when failed (the user's own GitHub access gone, ...)
	StartedAt  time.Time
	FinishedAt time.Time
}

type job struct {
	status Status
	cancel context.CancelFunc
}

// Manager keeps each user's latest sync. It lives in memory, like the live-event hub: restarting the
// server forgets finished syncs and stops running ones, and one API replica holds them all.
type Manager struct {
	base   context.Context // the server's lifetime: shutting down stops every sync
	syncer port.WorkspaceSyncer
	now    func() time.Time

	mu   sync.Mutex
	jobs map[uint64]*job
}

func NewManager(base context.Context, syncer port.WorkspaceSyncer) *Manager {
	return &Manager{base: base, syncer: syncer, now: time.Now, jobs: map[uint64]*job{}}
}

// Start begins a sync for the user unless one is already running, in which case that one is
// returned and started is false (a second click or another tab joins it instead of repeating it).
// The request's context only decides whether to start: the sync outlives the request.
func (m *Manager) Start(ctx context.Context, userID uint64) (status Status, started bool) {
	if ctx.Err() != nil {
		return m.Status(ctx, userID), false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.jobs[userID]; ok && j.status.State == StateRunning {
		return j.status, false
	}
	jobCtx, cancel := context.WithTimeout(m.base, maxDuration)
	j := &job{cancel: cancel, status: Status{State: StateRunning, StartedAt: m.now()}}
	m.jobs[userID] = j
	go m.run(jobCtx, userID, j)
	return j.status, true
}

func (m *Manager) run(ctx context.Context, userID uint64, j *job) {
	defer j.cancel()
	var result entity.SyncResult
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("sync crashed: %v", r)
			}
		}()
		result, err = m.syncer.ResyncReporting(ctx, userID, func(p entity.SyncProgress) {
			m.mu.Lock()
			j.status.Progress = p
			m.mu.Unlock()
		})
	}()

	m.mu.Lock()
	defer m.mu.Unlock()
	j.status.FinishedAt = m.now()
	switch {
	case ctx.Err() != nil: // canceled by the user, by shutdown, or by the time limit
		j.status.State = StateCanceled
	case err != nil:
		j.status.State, j.status.Err = StateFailed, err
	default:
		j.status.State, j.status.Result = StateDone, &result
	}
}

// Status returns the user's latest sync, or idle when they never ran one.
func (m *Manager) Status(_ context.Context, userID uint64) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.jobs[userID]; ok {
		return j.status
	}
	return Status{State: StateIdle}
}

// Cancel stops the user's running sync and reports whether there was one.
func (m *Manager) Cancel(_ context.Context, userID uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[userID]
	if !ok || j.status.State != StateRunning {
		return false
	}
	j.cancel()
	return true
}
