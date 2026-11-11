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
	m := syncjob.NewManager(context.Background(), s)

	st, started := m.Start(context.Background(), 1)
	if !started || st.State != syncjob.StateRunning {
		t.Fatalf("Start() = %+v, %v; want a running sync that was started", st, started)
	}
	<-s.started
	waitFor(t, "progress", func() bool { return m.Status(context.Background(), 1).Progress.Current == "acme/web" })
	if p := m.Status(context.Background(), 1).Progress; p.Total != 3 || p.Done != 1 {
		t.Fatalf("progress = %+v", p)
	}

	close(s.release)
	waitFor(t, "the sync to finish", func() bool { return m.Status(context.Background(), 1).State == syncjob.StateDone })
	done := m.Status(context.Background(), 1)
	if done.Result == nil || done.Result.Merged != 1 || done.FinishedAt.IsZero() || done.Err != nil {
		t.Fatalf("finished status = %+v", done)
	}
}

func TestStart_OneSyncPerUserAtATime(t *testing.T) {
	t.Parallel()
	s := newScripted()
	m := syncjob.NewManager(context.Background(), s)
	ctx := context.Background()

	if _, started := m.Start(ctx, 1); !started {
		t.Fatal("the first start begins a sync")
	}
	<-s.started
	for range 3 {
		st, started := m.Start(ctx, 1) // a second click, another tab, a retry
		if started || st.State != syncjob.StateRunning {
			t.Fatalf("a sync is already running: %+v, started=%v", st, started)
		}
	}
	if s.calls.Load() != 1 {
		t.Fatalf("the sync must run once, got %d", s.calls.Load())
	}

	// Another user's sync is independent.
	if _, started := m.Start(ctx, 2); !started {
		t.Fatal("users do not share a sync")
	}
	close(s.release)
	waitFor(t, "both to finish", func() bool {
		return m.Status(ctx, 1).State == syncjob.StateDone && m.Status(ctx, 2).State == syncjob.StateDone
	})

	// After it finished, starting again runs a new one.
	s2 := newScripted()
	close(s2.release)
	m2 := syncjob.NewManager(context.Background(), s2)
	m2.Start(ctx, 1)
	waitFor(t, "first", func() bool { return m2.Status(ctx, 1).State == syncjob.StateDone })
	if _, started := m2.Start(ctx, 1); !started {
		t.Fatal("a finished sync does not block the next one")
	}
}

func TestCancel_StopsTheSyncAndSaysSo(t *testing.T) {
	t.Parallel()
	s := newScripted()
	m := syncjob.NewManager(context.Background(), s)
	ctx := context.Background()
	m.Start(ctx, 1)
	<-s.started

	if !m.Cancel(ctx, 1) {
		t.Fatal("a running sync can be canceled")
	}
	waitFor(t, "cancellation", func() bool { return m.Status(ctx, 1).State == syncjob.StateCanceled })
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sawCtx) != 1 || !errors.Is(s.sawCtx[0], context.Canceled) {
		t.Fatalf("the sync itself must see the cancellation: %v", s.sawCtx)
	}
	if m.Cancel(ctx, 1) || m.Cancel(ctx, 99) {
		t.Fatal("nothing running means nothing to cancel")
	}
}

func TestFailureAndPanicAreReportedNotCrashes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	failing := newScripted()
	failing.err = entity.ErrGitHubTokenExpired
	close(failing.release)
	m := syncjob.NewManager(ctx, failing)
	m.Start(ctx, 1)
	waitFor(t, "failure", func() bool { return m.Status(ctx, 1).State == syncjob.StateFailed })
	if st := m.Status(ctx, 1); !errors.Is(st.Err, entity.ErrGitHubTokenExpired) || st.Result != nil {
		t.Fatalf("a failed sync carries its error: %+v", st)
	}

	crashing := newScripted()
	crashing.panics = true
	close(crashing.release)
	m2 := syncjob.NewManager(ctx, crashing)
	m2.Start(ctx, 1)
	waitFor(t, "panic to be caught", func() bool { return m2.Status(ctx, 1).State == syncjob.StateFailed })
	if _, started := m2.Start(ctx, 1); !started {
		t.Fatal("a crashed sync must not wedge the next one")
	}
}

func TestShutdownCancelsTheRunningSync(t *testing.T) {
	t.Parallel()
	s := newScripted()
	base, shutdown := context.WithCancel(context.Background())
	m := syncjob.NewManager(base, s)
	m.Start(context.Background(), 1)
	<-s.started

	shutdown()
	waitFor(t, "the sync to stop with the server", func() bool { return m.Status(context.Background(), 1).State == syncjob.StateCanceled })
}

func TestStatusOfAUserWhoNeverSyncedIsIdle_AndACanceledRequestStartsNothing(t *testing.T) {
	t.Parallel()
	s := newScripted()
	m := syncjob.NewManager(context.Background(), s)
	if st := m.Status(context.Background(), 7); st.State != syncjob.StateIdle {
		t.Fatalf("expected idle, got %+v", st)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, started := m.Start(canceled, 7); started || s.calls.Load() != 0 {
		t.Fatal("a canceled request starts nothing")
	}
}
