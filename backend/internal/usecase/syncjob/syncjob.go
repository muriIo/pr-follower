// Package syncjob runs the "sync with GitHub" in the background, one per user. A sync of many
// repositories takes minutes (hundreds of GitHub calls, one after another), far longer than an HTTP
// request should be held open, so the request only starts it and the user reads its progress.
//
// The job lives in the database, not in the replica running it: whichever API replica a start, a
// progress poll or a cancel lands on sees the same sync. The replica running it reports its progress
// and that it is alive at every heartbeat, and picks up a cancel asked on another replica there. A
// sync whose replica stopped reporting (a deploy, a crash) reads as interrupted and can run again.
package syncjob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// State is where a user's sync stands.
type State = entity.SyncJobState

const (
	StateIdle     = entity.SyncJobIdle // never started
	StateRunning  = entity.SyncJobRunning
	StateDone     = entity.SyncJobDone
	StateFailed   = entity.SyncJobFailed
	StateCanceled = entity.SyncJobCanceled
)

const (
	// maxDuration stops a sync that never ends; a normal one takes minutes.
	maxDuration = 30 * time.Minute
	// defaultHeartbeat is how often the running replica reports; the panel polls once a second.
	defaultHeartbeat = time.Second
	// defaultStaleAfter is how long without a heartbeat before a sync counts as interrupted.
	defaultStaleAfter = 15 * time.Second
	// finishTimeout bounds recording the end of a run, which happens even during shutdown.
	finishTimeout = 5 * time.Second
)

// Status is a snapshot of a user's latest sync.
type Status struct {
	State      State
	Progress   entity.SyncProgress
	Result     *entity.SyncResult // set when done
	Err        error              // set when failed (the user's own GitHub access gone, interrupted, ...)
	StartedAt  time.Time
	FinishedAt time.Time
}

// run is a sync this replica is running.
//
// replica-local: a sync this replica executes; its state is in the store.
type run struct {
	cancel       context.CancelFunc
	mu           sync.Mutex
	progress     entity.SyncProgress
	userCanceled bool
}

// Manager starts, reports and cancels users' syncs. Their state is in the store; it keeps only the
// cancel handles of the runs this replica executes.
//
// replica-local: only the cancel handles of the runs this replica executes; every sync's state is in the store.
type Manager struct {
	base   context.Context // the server's lifetime: shutting down stops (interrupts) every run here
	syncer port.WorkspaceSyncer
	store  port.SyncJobStore
	now    func() time.Time

	heartbeat  time.Duration
	staleAfter time.Duration

	mu   sync.Mutex
	runs map[uint64]*run
}

// Option adjusts a Manager.
type Option func(*Manager)

// WithTiming sets how often a running sync reports and how long without a report makes it count as
// interrupted. staleAfter must be longer than heartbeat; otherwise the defaults stay.
func WithTiming(heartbeat, staleAfter time.Duration) Option {
	return func(m *Manager) {
		if heartbeat > 0 && staleAfter > heartbeat {
			m.heartbeat, m.staleAfter = heartbeat, staleAfter
		}
	}
}

func NewManager(base context.Context, syncer port.WorkspaceSyncer, store port.SyncJobStore, opts ...Option) *Manager {
	m := &Manager{
		base: base, syncer: syncer, store: store, now: time.Now,
		heartbeat: defaultHeartbeat, staleAfter: defaultStaleAfter,
		runs: map[uint64]*run{},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *Manager) staleBefore(now time.Time) time.Time { return now.Add(-m.staleAfter) }

// Start begins a sync for the user unless one is already running on any replica, in which case that
// one is returned and started is false (a second click or another tab joins it instead of repeating
// it). The request's context only decides whether to start: the sync outlives the request.
func (m *Manager) Start(ctx context.Context, userID uint64) (status Status, started bool, err error) {
	if ctx.Err() != nil {
		status, err = m.Status(context.WithoutCancel(ctx), userID)
		return status, false, err
	}
	runID, err := newRunID()
	if err != nil {
		return Status{}, false, err
	}
	now := m.now().UTC()
	claimed, err := m.store.ClaimSyncJob(ctx, userID, runID, now, m.staleBefore(now))
	if err != nil {
		return Status{}, false, err
	}
	if !claimed {
		status, err = m.Status(ctx, userID)
		return status, false, err
	}

	runCtx, cancel := context.WithTimeout(m.base, maxDuration)
	r := &run{cancel: cancel}
	m.mu.Lock()
	m.runs[userID] = r
	m.mu.Unlock()
	go m.execute(runCtx, userID, runID, now, r)
	return Status{State: StateRunning, StartedAt: now}, true, nil
}

func (m *Manager) execute(ctx context.Context, userID uint64, runID string, startedAt time.Time, r *run) {
	defer r.cancel()
	defer func() {
		m.mu.Lock()
		if m.runs[userID] == r {
			delete(m.runs, userID)
		}
		m.mu.Unlock()
	}()

	var result entity.SyncResult
	var syncErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil {
				syncErr = fmt.Errorf("sync crashed: %v", p)
			}
		}()
		result, syncErr = m.syncer.ResyncReporting(ctx, userID, func(p entity.SyncProgress) {
			r.mu.Lock()
			r.progress = p
			r.mu.Unlock()
		})
	}()

	lost := m.reportUntilDone(ctx, userID, runID, r, done)
	if lost {
		return // another run took over: it owns the job now
	}

	r.mu.Lock()
	job := entity.SyncJob{
		UserID: userID, RunID: runID, Progress: r.progress,
		StartedAt: startedAt, FinishedAt: m.now().UTC(),
	}
	userCanceled := r.userCanceled
	r.mu.Unlock()
	job.HeartbeatAt = job.FinishedAt

	switch {
	case userCanceled:
		job.State = StateCanceled
	case ctx.Err() != nil: // the server is stopping, or the time limit: nobody asked for it
		job.State, job.Err = StateFailed, entity.ErrSyncInterrupted
	case syncErr != nil:
		job.State, job.Err = StateFailed, syncErr
	default:
		job.State, job.Result = StateDone, &result
	}

	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	if err := m.store.FinishSyncJob(finishCtx, job); err != nil {
		slog.Error("failed to record the end of a sync", "user_id", userID, "state", job.State, "err", err)
	}
}

// reportUntilDone heartbeats until the sync ends, stopping it when a cancel was asked on any replica.
// It reports whether the run lost its claim to a newer one.
func (m *Manager) reportUntilDone(ctx context.Context, userID uint64, runID string, r *run, done <-chan struct{}) (lost bool) {
	ticker := time.NewTicker(m.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return lost
		case <-ticker.C:
			if lost || ctx.Err() != nil {
				continue
			}
			r.mu.Lock()
			progress := r.progress
			r.mu.Unlock()
			cancelRequested, err := m.store.HeartbeatSyncJob(ctx, userID, runID, progress, m.now().UTC())
			switch {
			case errors.Is(err, entity.ErrNotFound):
				lost = true
				r.cancel()
			case err != nil:
				slog.Warn("sync heartbeat failed", "user_id", userID, "err", err) // the next one may pass
			case cancelRequested:
				r.mu.Lock()
				r.userCanceled = true
				r.mu.Unlock()
				r.cancel()
			}
		}
	}
}

// Status returns the user's latest sync, or idle when they never ran one.
func (m *Manager) Status(ctx context.Context, userID uint64) (Status, error) {
	job, err := m.store.GetSyncJob(ctx, userID)
	if errors.Is(err, entity.ErrNotFound) {
		return Status{State: StateIdle}, nil
	}
	if err != nil {
		return Status{}, err
	}
	st := Status{
		State: job.State, Progress: job.Progress, Result: job.Result, Err: job.Err,
		StartedAt: job.StartedAt, FinishedAt: job.FinishedAt,
	}
	if job.State == StateRunning && job.HeartbeatAt.Before(m.staleBefore(m.now())) {
		st.State, st.Err, st.FinishedAt = StateFailed, entity.ErrSyncInterrupted, job.HeartbeatAt
	}
	return st, nil
}

// Cancel stops the user's running sync, on this replica at once or on another at its next
// heartbeat, and reports whether there was one.
func (m *Manager) Cancel(ctx context.Context, userID uint64) (bool, error) {
	m.mu.Lock()
	r, local := m.runs[userID]
	m.mu.Unlock()
	if local {
		r.mu.Lock()
		r.userCanceled = true
		r.mu.Unlock()
		r.cancel()
		return true, nil
	}
	return m.store.RequestSyncJobCancel(ctx, userID, m.staleBefore(m.now().UTC()))
}

func newRunID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate a sync run id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
