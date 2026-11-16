package dependabot

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

const (
	defaultWorkers    = 2
	defaultQueueSize  = 256
	defaultJobTimeout = 2 * time.Minute
)

// RunnerConfig bounds the background analysis. Zero values take the defaults.
type RunnerConfig struct {
	// Workers is how many analyses run at once; each may download a repository tarball.
	Workers int
	// QueueSize is how many PRs may wait; more are dropped and can be scheduled again.
	QueueSize int
	// JobTimeout stops an analysis that hangs, so it cannot hold a worker forever.
	JobTimeout time.Duration
	// OnError hears about every failed analysis (logged when nil).
	OnError func(prID uint64, err error)
}

type job struct {
	running bool
	rerun   bool
}

// Runner analyses Dependabot PRs in the background so a webhook is answered at once. It keeps one
// pending job per PR, re-runs a PR that changed while it was being analysed, and stops with its
// context (ADR-0007). Run it once, in its own goroutine.
//
// replica-local: the analyses queued and running on this replica; one lost with the replica is scheduled again by the next webhook, sync or click.
type Runner struct {
	analyzer port.PullRequestAnalyzer
	cfg      RunnerConfig
	queue    chan uint64

	mu   sync.Mutex
	jobs map[uint64]*job
}

func NewRunner(analyzer port.PullRequestAnalyzer, cfg RunnerConfig) *Runner {
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkers
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = defaultJobTimeout
	}
	if cfg.OnError == nil {
		cfg.OnError = func(prID uint64, err error) {
			slog.Warn("dependabot analysis failed", "pr_id", prID, "err", err)
		}
	}
	return &Runner{analyzer: analyzer, cfg: cfg, queue: make(chan uint64, cfg.QueueSize), jobs: map[uint64]*job{}}
}

// ScheduleAnalysis queues a PR without blocking. A PR already waiting is not queued twice; one
// being analysed right now is analysed again afterwards (it may have a new head commit). The
// request's own context only decides whether to schedule: the work outlives the request.
func (r *Runner) ScheduleAnalysis(ctx context.Context, prID uint64) {
	if ctx.Err() != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if j, ok := r.jobs[prID]; ok {
		if j.running {
			j.rerun = true
		}
		return
	}
	select {
	case r.queue <- prID:
		r.jobs[prID] = &job{}
	default:
		slog.Warn("dependabot analysis queue full, dropping", "pr_id", prID)
	}
}

// Run processes the queue until ctx is canceled, then returns once the analyses in flight have
// seen the cancellation.
func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for range r.cfg.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case prID := <-r.queue:
					r.process(ctx, prID)
				}
			}
		}()
	}
	wg.Wait()
}

func (r *Runner) process(ctx context.Context, prID uint64) {
	r.mu.Lock()
	r.jobs[prID].running = true
	r.mu.Unlock()

	jobCtx, cancel := context.WithTimeout(ctx, r.cfg.JobTimeout)
	_, err := r.analyzer.AnalyzePR(jobCtx, prID)
	cancel()
	if err != nil && ctx.Err() == nil {
		r.cfg.OnError(prID, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	j := r.jobs[prID]
	if j.rerun && ctx.Err() == nil {
		j.rerun, j.running = false, false
		select {
		case r.queue <- prID:
			return
		default:
		}
	}
	delete(r.jobs, prID)
}
