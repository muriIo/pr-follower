package syncjob_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/syncjob"
)

// scriptedSyncer is a sync that waits for the test to let it go, reporting progress on the way.
type scriptedSyncer struct {
	calls   atomic.Int32
	started chan uint64
	release chan struct{}
	result  entity.SyncResult
	err     error
	panics  bool
	mu      sync.Mutex
	sawCtx  []error
}

func newScripted() *scriptedSyncer {
	return &scriptedSyncer{started: make(chan uint64, 8), release: make(chan struct{})}
}

func (s *scriptedSyncer) ResyncReporting(ctx context.Context, userID uint64, report func(entity.SyncProgress)) (entity.SyncResult, error) {
	s.calls.Add(1)
	report(entity.SyncProgress{Total: 3, Done: 1, Current: "acme/web"})
	s.started <- userID
	select {
	case <-s.release:
	case <-ctx.Done():
		s.mu.Lock()
		s.sawCtx = append(s.sawCtx, context.Cause(ctx))
		s.mu.Unlock()
		return entity.SyncResult{}, context.Cause(ctx)
	}
	if s.panics {
		panic("boom")
	}
	return s.result, s.err
}

// memoryStore is the port.SyncJobStore in memory, with the semantics of the Postgres one: what two
// managers sharing it see is what two replicas sharing a database see.
type memoryStore struct {
	mu   sync.Mutex
	jobs map[uint64]entity.SyncJob
}

func newStore() *memoryStore { return &memoryStore{jobs: map[uint64]entity.SyncJob{}} }

func (s *memoryStore) ClaimSyncJob(_ context.Context, userID uint64, runID string, now, staleBefore time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[userID]; ok && j.State == entity.SyncJobRunning && !j.HeartbeatAt.Before(staleBefore) {
		return false, nil
	}
	s.jobs[userID] = entity.SyncJob{UserID: userID, RunID: runID, State: entity.SyncJobRunning, StartedAt: now, HeartbeatAt: now}
	return true, nil
}

func (s *memoryStore) GetSyncJob(_ context.Context, userID uint64) (entity.SyncJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[userID]
	if !ok {
		return entity.SyncJob{}, entity.ErrNotFound
	}
	return j, nil
}

func (s *memoryStore) HeartbeatSyncJob(_ context.Context, userID uint64, runID string, p entity.SyncProgress, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[userID]
	if !ok || j.RunID != runID || j.State != entity.SyncJobRunning {
		return false, entity.ErrNotFound
	}
	j.Progress, j.HeartbeatAt = p, now
	s.jobs[userID] = j
	return j.CancelRequested, nil
}

func (s *memoryStore) FinishSyncJob(_ context.Context, job entity.SyncJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[job.UserID]; ok && j.RunID == job.RunID {
		s.jobs[job.UserID] = job
	}
	return nil
}

func (s *memoryStore) RequestSyncJobCancel(_ context.Context, userID uint64, staleBefore time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[userID]
	if !ok || j.State != entity.SyncJobRunning || j.HeartbeatAt.Before(staleBefore) {
		return false, nil
	}
	j.CancelRequested = true
	s.jobs[userID] = j
	return true, nil
}

func (s *memoryStore) put(j entity.SyncJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.UserID] = j
}

// replica is a manager with test timings: a heartbeat every 5ms, stale after 100ms.
func replica(base context.Context, s *scriptedSyncer, store *memoryStore) *syncjob.Manager {
	return syncjob.NewManager(base, s, store, syncjob.WithTiming(5*time.Millisecond, 100*time.Millisecond))
}

func status(t *testing.T, m *syncjob.Manager, userID uint64) syncjob.Status {
	t.Helper()
	st, err := m.Status(context.Background(), userID)
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	return st
}

func start(t *testing.T, m *syncjob.Manager, userID uint64) (syncjob.Status, bool) {
	t.Helper()
	st, started, err := m.Start(context.Background(), userID)
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	return st, started
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStart_ReturnsAtOnceWhileTheSyncRunsInTheBackground(t *testing.T) {
	t.Parallel()
	s := newScripted()
	s.result = entity.SyncResult{Repositories: 3, Merged: 1}
	m := replica(context.Background(), s, newStore())

	st, started := start(t, m, 1)
	if !started || st.State != syncjob.StateRunning {
		t.Fatalf("Start() = %+v, %v; want a running sync that was started", st, started)
	}
	<-s.started
	waitFor(t, "progress", func() bool { return status(t, m, 1).Progress.Current == "acme/web" })
	if p := status(t, m, 1).Progress; p.Total != 3 || p.Done != 1 {
		t.Fatalf("progress = %+v", p)
	}

	close(s.release)
	waitFor(t, "the sync to finish", func() bool { return status(t, m, 1).State == syncjob.StateDone })
	done := status(t, m, 1)
	if done.Result == nil || done.Result.Merged != 1 || done.FinishedAt.IsZero() || done.Err != nil {
		t.Fatalf("finished status = %+v", done)
	}
}

func TestStart_OneSyncPerUserAtATime(t *testing.T) {
	t.Parallel()
	s := newScripted()
	m := replica(context.Background(), s, newStore())

	if _, started := start(t, m, 1); !started {
		t.Fatal("the first start begins a sync")
	}
	<-s.started
	for range 3 {
		st, started := start(t, m, 1) // a second click, another tab, a retry
		if started || st.State != syncjob.StateRunning {
			t.Fatalf("a sync is already running: %+v, started=%v", st, started)
		}
	}
	if s.calls.Load() != 1 {
		t.Fatalf("the sync must run once, got %d", s.calls.Load())
	}
	if _, started := start(t, m, 2); !started {
		t.Fatal("users do not share a sync")
	}
	close(s.release)
	waitFor(t, "both to finish", func() bool {
		return status(t, m, 1).State == syncjob.StateDone && status(t, m, 2).State == syncjob.StateDone
	})
	if _, started := start(t, m, 1); !started {
		t.Fatal("a finished sync does not block the next one")
	}
}

func TestTwoReplicas_ShareOneSyncItsProgressAndItsCancel(t *testing.T) {
	t.Parallel()
	s := newScripted()
	store := newStore()
	replicaA := replica(context.Background(), s, store)
	replicaB := replica(context.Background(), s, store)

	if _, started := start(t, replicaA, 1); !started {
		t.Fatal("A starts the sync")
	}
	<-s.started
	if _, started := start(t, replicaB, 1); started {
		t.Fatal("a start on B joins the sync running on A")
	}
	waitFor(t, "B to see A's progress", func() bool { return status(t, replicaB, 1).Progress.Current == "acme/web" })

	canceled, err := replicaB.Cancel(context.Background(), 1)
	if err != nil || !canceled {
		t.Fatalf("Cancel on B = %v, %v; want the sync on A asked to stop", canceled, err)
	}
	waitFor(t, "A to stop", func() bool { return status(t, replicaB, 1).State == syncjob.StateCanceled })
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sawCtx) != 1 || !errors.Is(s.sawCtx[0], context.Canceled) {
		t.Fatalf("the sync itself must see the cancellation: %v", s.sawCtx)
	}
	if s.calls.Load() != 1 {
		t.Fatalf("the sync must run once across replicas, got %d", s.calls.Load())
	}
}

func TestASyncWhoseReplicaStoppedReportingIsInterrupted_AndCanRunAgain(t *testing.T) {
	t.Parallel()
	s := newScripted()
	close(s.release)
	store := newStore()
	gone := time.Now().Add(-time.Minute)
	store.put(entity.SyncJob{UserID: 1, RunID: "dead-replica", State: entity.SyncJobRunning, StartedAt: gone, HeartbeatAt: gone})
	m := replica(context.Background(), s, store)

	st := status(t, m, 1)
	if st.State != syncjob.StateFailed || !errors.Is(st.Err, entity.ErrSyncInterrupted) {
		t.Fatalf("a sync nobody reports on is interrupted, got %+v", st)
	}
	if canceled, _ := m.Cancel(context.Background(), 1); canceled {
		t.Fatal("an interrupted sync is not running: nothing to cancel")
	}
	if _, started := start(t, m, 1); !started {
		t.Fatal("an interrupted sync must not wedge the next one")
	}
	waitFor(t, "the new run to finish", func() bool { return status(t, m, 1).State == syncjob.StateDone })
}

func TestARunThatLostItsClaimStopsAndOverwritesNothing(t *testing.T) {
	t.Parallel()
	s := newScripted()
	store := newStore()
	m := replica(context.Background(), s, store)
	start(t, m, 1)
	<-s.started

	// Another replica took over (this one looked dead to it): this run must stop and leave it alone.
	now := time.Now()
	store.put(entity.SyncJob{UserID: 1, RunID: "newer", State: entity.SyncJobRunning, StartedAt: now, HeartbeatAt: now})
	waitFor(t, "the old run to stop", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.sawCtx) == 1
	})
	time.Sleep(20 * time.Millisecond)
	if j, _ := store.GetSyncJob(context.Background(), 1); j.RunID != "newer" || j.State != entity.SyncJobRunning {
		t.Fatalf("the newer run was overwritten: %+v", j)
	}
}

func TestCancel_StopsTheSyncAndSaysSo(t *testing.T) {
	t.Parallel()
	s := newScripted()
	m := replica(context.Background(), s, newStore())
	start(t, m, 1)
	<-s.started

	if canceled, err := m.Cancel(context.Background(), 1); err != nil || !canceled {
		t.Fatalf("a running sync can be canceled: %v, %v", canceled, err)
	}
	waitFor(t, "cancellation", func() bool { return status(t, m, 1).State == syncjob.StateCanceled })
	if canceled, _ := m.Cancel(context.Background(), 1); canceled {
		t.Fatal("nothing running means nothing to cancel")
	}
	if canceled, _ := m.Cancel(context.Background(), 99); canceled {
		t.Fatal("a user who never synced has nothing to cancel")
	}
}

func TestFailureAndPanicAreReportedNotCrashes(t *testing.T) {
	t.Parallel()
	failing := newScripted()
	failing.err = entity.ErrGitHubTokenExpired
	close(failing.release)
	m := replica(context.Background(), failing, newStore())
	start(t, m, 1)
	waitFor(t, "failure", func() bool { return status(t, m, 1).State == syncjob.StateFailed })
	if st := status(t, m, 1); !errors.Is(st.Err, entity.ErrGitHubTokenExpired) || st.Result != nil {
		t.Fatalf("a failed sync carries its error: %+v", st)
	}

	crashing := newScripted()
	crashing.panics = true
	close(crashing.release)
	m2 := replica(context.Background(), crashing, newStore())
	start(t, m2, 1)
	waitFor(t, "panic to be caught", func() bool { return status(t, m2, 1).State == syncjob.StateFailed })
	if _, started := start(t, m2, 1); !started {
		t.Fatal("a crashed sync must not wedge the next one")
	}
}

func TestShutdownInterruptsTheRunningSync(t *testing.T) {
	t.Parallel()
	s := newScripted()
	base, shutdown := context.WithCancel(context.Background())
	m := replica(base, s, newStore())
	start(t, m, 1)
	<-s.started

	shutdown() // a deploy: the user did not cancel anything
	waitFor(t, "the sync to stop with the server", func() bool { return status(t, m, 1).State == syncjob.StateFailed })
	if st := status(t, m, 1); !errors.Is(st.Err, entity.ErrSyncInterrupted) {
		t.Fatalf("a sync stopped by the server is interrupted, got %+v", st)
	}
}

func TestStatusOfAUserWhoNeverSyncedIsIdle_AndACanceledRequestStartsNothing(t *testing.T) {
	t.Parallel()
	s := newScripted()
	m := replica(context.Background(), s, newStore())
	if st := status(t, m, 7); st.State != syncjob.StateIdle {
		t.Fatalf("expected idle, got %+v", st)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, started, _ := m.Start(canceled, 7); started || s.calls.Load() != 0 {
		t.Fatal("a canceled request starts nothing")
	}
}
