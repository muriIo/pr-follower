package dependabot_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/dependabot"
)

// scriptedAnalyzer records every analysis and lets a test hold or fail it.
type scriptedAnalyzer struct {
	mu      sync.Mutex
	calls   []uint64
	running atomic.Int32
	maxRun  atomic.Int32
	started chan uint64
	release chan struct{} // when set, an analysis waits for it (or for its context)
	fail    map[uint64]error
	seenErr []error
}

func newScripted() *scriptedAnalyzer {
	return &scriptedAnalyzer{started: make(chan uint64, 64), fail: map[uint64]error{}}
}

func (s *scriptedAnalyzer) AnalyzePR(ctx context.Context, prID uint64) (*entity.DependabotAnalysis, error) {
	now := s.running.Add(1)
	defer s.running.Add(-1)
	for {
		max := s.maxRun.Load()
		if now <= max || s.maxRun.CompareAndSwap(max, now) {
			break
		}
	}
	s.mu.Lock()
	s.calls = append(s.calls, prID)
	s.mu.Unlock()
	s.started <- prID
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			s.mu.Lock()
			s.seenErr = append(s.seenErr, context.Cause(ctx))
			s.mu.Unlock()
			return nil, context.Cause(ctx)
		}
	}
	if err := s.fail[prID]; err != nil {
		return nil, err
	}
	return &entity.DependabotAnalysis{PullRequestID: prID}, nil
}

func (s *scriptedAnalyzer) called() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uint64(nil), s.calls...)
}

func startRunner(t *testing.T, r *dependabot.Runner) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not return after its context was canceled")
		}
	}
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

func TestRunner_AnalysesScheduledPRsInTheBackground(t *testing.T) {
	t.Parallel()
	a := newScripted()
	r := dependabot.NewRunner(a, dependabot.RunnerConfig{Workers: 2})
	defer startRunner(t, r)()

	r.ScheduleAnalysis(context.Background(), 7)
	r.ScheduleAnalysis(context.Background(), 8)
	waitFor(t, "both analyses", func() bool { return len(a.called()) == 2 })
}

func TestRunner_ScheduleNeverBlocksAndDropsWhatDoesNotFit(t *testing.T) {
	t.Parallel()
	a := newScripted()
	r := dependabot.NewRunner(a, dependabot.RunnerConfig{Workers: 1, QueueSize: 2})

	done := make(chan struct{})
	go func() {
		for id := uint64(1); id <= 5; id++ { // nobody is consuming yet
			r.ScheduleAnalysis(context.Background(), id)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ScheduleAnalysis blocked a webhook on a full queue")
	}

	defer startRunner(t, r)()
	waitFor(t, "the two queued analyses", func() bool { return len(a.called()) == 2 })

	// A dropped PR is not stuck as "scheduled": it can be scheduled again later.
	r.ScheduleAnalysis(context.Background(), 5)
	waitFor(t, "the dropped PR to run once rescheduled", func() bool { return len(a.called()) == 3 })
}

func TestRunner_AnAlreadyScheduledPRIsNotQueuedTwice(t *testing.T) {
	t.Parallel()
	a := newScripted()
	r := dependabot.NewRunner(a, dependabot.RunnerConfig{Workers: 1})
	for range 4 {
		r.ScheduleAnalysis(context.Background(), 7) // four webhooks for the same PR before a worker is free
	}
	defer startRunner(t, r)()
	waitFor(t, "one analysis", func() bool { return len(a.called()) == 1 })
	time.Sleep(20 * time.Millisecond)
	if got := a.called(); len(got) != 1 {
		t.Fatalf("expected a single analysis, got %v", got)
	}
}

func TestRunner_ACommitPushedDuringAnAnalysisIsAnalysedAfterwards(t *testing.T) {
	t.Parallel()
	a := newScripted()
	a.release = make(chan struct{})
	r := dependabot.NewRunner(a, dependabot.RunnerConfig{Workers: 1})
	defer startRunner(t, r)()

	r.ScheduleAnalysis(context.Background(), 7)
	<-a.started // the first analysis read the PR as it was
	r.ScheduleAnalysis(context.Background(), 7)
	a.release <- struct{}{}

	select {
	case <-a.started: // the PR changed meanwhile: a second run sees the new head
	case <-time.After(2 * time.Second):
		t.Fatal("the PR scheduled during its own analysis was lost")
	}
	a.release <- struct{}{}
	waitFor(t, "the PR to be idle", func() bool { return a.running.Load() == 0 })
	if got := a.called(); len(got) != 2 {
		t.Fatalf("expected exactly two analyses, got %v", got)
	}
}

func TestRunner_RunsAtMostTheConfiguredNumberAtOnce(t *testing.T) {
	t.Parallel()
	a := newScripted()
	a.release = make(chan struct{})
	r := dependabot.NewRunner(a, dependabot.RunnerConfig{Workers: 2})
	defer startRunner(t, r)()

	for id := uint64(1); id <= 6; id++ {
		r.ScheduleAnalysis(context.Background(), id)
	}
	waitFor(t, "two analyses in flight", func() bool { return a.running.Load() == 2 })
	time.Sleep(20 * time.Millisecond)
	if a.running.Load() != 2 {
		t.Fatalf("expected exactly 2 in flight, got %d", a.running.Load())
	}
	for range 6 {
		a.release <- struct{}{}
	}
	waitFor(t, "all six to finish", func() bool { return len(a.called()) == 6 && a.running.Load() == 0 })
	if a.maxRun.Load() > 2 {
		t.Fatalf("concurrency exceeded the bound: %d", a.maxRun.Load())
	}
}

func TestRunner_AFailureOrATimeoutDoesNotStopTheWorker(t *testing.T) {
	t.Parallel()
	a := newScripted()
	a.fail[1] = errors.New("github is down")
	var mu sync.Mutex
	failed := map[uint64]error{}
	r := dependabot.NewRunner(a, dependabot.RunnerConfig{Workers: 1, JobTimeout: 30 * time.Millisecond, OnError: func(id uint64, err error) {
		mu.Lock()
		failed[id] = err
		mu.Unlock()
	}})
	defer startRunner(t, r)()

	r.ScheduleAnalysis(context.Background(), 1)
	waitFor(t, "the failure to be reported", func() bool { mu.Lock(); defer mu.Unlock(); return failed[1] != nil })

	a.release = make(chan struct{}) // PR 2 hangs until its timeout
	r.ScheduleAnalysis(context.Background(), 2)
	waitFor(t, "the timeout to be reported", func() bool { mu.Lock(); defer mu.Unlock(); return failed[2] != nil })
	if !errors.Is(failed[2], context.DeadlineExceeded) {
		t.Fatalf("expected a deadline error for the hung analysis, got %v", failed[2])
	}

	a.release = nil
	r.ScheduleAnalysis(context.Background(), 3)
	waitFor(t, "the next PR to be analysed", func() bool { return len(a.called()) == 3 })
}

func TestRunner_ShutdownCancelsTheAnalysisInFlightAndRunReturns(t *testing.T) {
	t.Parallel()
	a := newScripted()
	a.release = make(chan struct{})
	r := dependabot.NewRunner(a, dependabot.RunnerConfig{Workers: 1})
	stop := startRunner(t, r)

	r.ScheduleAnalysis(context.Background(), 7)
	<-a.started
	stop() // fails the test itself if Run does not return

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.seenErr) != 1 || !errors.Is(a.seenErr[0], context.Canceled) {
		t.Fatalf("the analysis in flight must see the shutdown: %v", a.seenErr)
	}
}

func TestRunner_IgnoresARequestThatIsAlreadyCanceled(t *testing.T) {
	t.Parallel()
	a := newScripted()
	r := dependabot.NewRunner(a, dependabot.RunnerConfig{Workers: 1})
	defer startRunner(t, r)()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	r.ScheduleAnalysis(canceled, 7)
	time.Sleep(20 * time.Millisecond)
	if got := a.called(); len(got) != 0 {
		t.Fatalf("a canceled request schedules nothing, got %v", got)
	}
}
