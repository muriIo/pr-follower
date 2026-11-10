package webhook_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

// fakeChecks is a CheckRunStore over in-memory runs and PRs.
type fakeChecks struct {
	mu                          sync.Mutex
	runs                        map[int64]entity.CheckRun
	prs                         []*entity.PullRequest
	err                         error
	upsertErr, listErr, openErr error
}

func newFakeChecks(prs ...*entity.PullRequest) *fakeChecks {
	return &fakeChecks{runs: map[int64]entity.CheckRun{}, prs: prs}
}

func (f *fakeChecks) UpsertCheckRun(_ context.Context, run entity.CheckRun) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.runs[run.ID] = run
	return nil
}

func (f *fakeChecks) GetCheckRun(_ context.Context, id int64) (*entity.CheckRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	r, ok := f.runs[id]
	if !ok {
		return nil, entity.ErrNotFound
	}
	return &r, nil
}

func (f *fakeChecks) ListCheckRuns(_ context.Context, repoID uint64, head string) ([]entity.CheckRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []entity.CheckRun
	for _, r := range f.runs {
		if r.RepositoryID == repoID && r.HeadSHA == head {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeChecks) ListOpenByHeadSHA(_ context.Context, repoID uint64, head string) ([]entity.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.openErr != nil {
		return nil, f.openErr
	}
	var out []entity.PullRequest
	for _, p := range f.prs {
		if p.RepositoryID == repoID && p.HeadSHA == head && p.State == entity.PRStateOpen {
			out = append(out, *p)
		}
	}
	return out, nil
}

// savingPRRepo records PR upserts back into the fakeChecks PR list.
type savingPRRepo struct {
	fakePRRepo
	checks    *fakeChecks
	upsertErr error
}

func (s *savingPRRepo) Upsert(_ context.Context, pr *entity.PullRequest) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.checks.mu.Lock()
	defer s.checks.mu.Unlock()
	for i, p := range s.checks.prs {
		if p.GitHubPRID == pr.GitHubPRID {
			cp := *pr
			s.checks.prs[i] = &cp
			return nil
		}
	}
	cp := *pr
	s.checks.prs = append(s.checks.prs, &cp)
	return nil
}

func checkRunSetup() (*webhook.IngestUseCase, *fakeChecks, *fakePub) {
	repo := &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "acme/web"}
	pr := &entity.PullRequest{ID: 1, RepositoryID: 10, GitHubPRID: 501, Number: 7, State: entity.PRStateOpen, HeadSHA: "sha-1", CIStatus: entity.CIStatusNone}
	checks := newFakeChecks(pr)
	pub := &fakePub{}
	uc := webhook.NewIngestUseCase(&fakeRepoRepo{repo: repo}, &savingPRRepo{checks: checks}, checks, pub)
	return uc, checks, pub
}

func ciOf(checks *fakeChecks) entity.CIStatus {
	checks.mu.Lock()
	defer checks.mu.Unlock()
	return checks.prs[0].CIStatus
}

func run(id int64, status, conclusion string) webhook.CheckRunEventPayload {
	return webhook.CheckRunEventPayload{CheckRunID: id, GitHubRepoID: 9001, HeadSHA: "sha-1", Name: "test", Status: status, Conclusion: conclusion}
}

func TestIngestCheckRunEvent_DerivesPRCIFromAllRuns(t *testing.T) {
	t.Parallel()
	uc, checks, pub := checkRunSetup()
	ctx := context.Background()

	_ = uc.IngestCheckRunEvent(ctx, run(1, "queued", ""))
	if got := ciOf(checks); got != entity.CIStatusPending {
		t.Fatalf("queued run: CI = %q, want PENDING", got)
	}
	_ = uc.IngestCheckRunEvent(ctx, run(2, "in_progress", ""))
	_ = uc.IngestCheckRunEvent(ctx, run(1, "completed", "success"))
	if got := ciOf(checks); got != entity.CIStatusPending {
		t.Fatalf("one run still going: CI = %q, want PENDING", got)
	}
	_ = uc.IngestCheckRunEvent(ctx, run(2, "completed", "failure"))
	if got := ciOf(checks); got != entity.CIStatusFailure {
		t.Fatalf("a failed run: CI = %q, want FAILURE", got)
	}
	types := []string{}
	for _, e := range pub.events {
		types = append(types, e.Type)
	}
	if len(types) != 2 || types[0] != "ci.running" || types[1] != "ci.failed" {
		t.Fatalf("events only on CI changes, got %v", types)
	}
}

func TestIngestCheckRunEvent_RedeliveryIsIdempotent(t *testing.T) {
	t.Parallel()
	uc, checks, pub := checkRunSetup()
	ctx := context.Background()

	done := run(1, "completed", "success")
	for range 2 {
		if err := uc.IngestCheckRunEvent(ctx, done); err != nil {
			t.Fatalf("delivery: %v", err)
		}
	}
	// A late "in_progress" delivery must not move the run (or the PR) backwards.
	_ = uc.IngestCheckRunEvent(ctx, run(1, "in_progress", ""))
	if got := ciOf(checks); got != entity.CIStatusSuccess {
		t.Fatalf("CI = %q, want SUCCESS", got)
	}
	if len(pub.events) != 1 || pub.events[0].Type != "ci.passed" {
		t.Fatalf("expected a single ci.passed event, got %+v", pub.events)
	}
}

func TestIngestCheckRunEvent_IgnoresUnknownRepositoriesAndOtherCommits(t *testing.T) {
	t.Parallel()
	uc, checks, pub := checkRunSetup()
	ctx := context.Background()

	other := run(5, "completed", "failure")
	other.HeadSHA = "sha-old"
	if err := uc.IngestCheckRunEvent(ctx, other); err != nil {
		t.Fatalf("other commit: %v", err)
	}
	if got := ciOf(checks); got != entity.CIStatusNone || len(pub.events) != 0 {
		t.Fatalf("a run on another commit must not change the PR: CI=%q events=%d", got, len(pub.events))
	}

	unknownRepoUC := webhook.NewIngestUseCase(&fakeRepoRepo{err: entity.ErrNotFound}, &fakePRRepo{}, newFakeChecks(), &fakePub{})
	if err := unknownRepoUC.IngestCheckRunEvent(ctx, run(9, "completed", "success")); err != nil {
		t.Fatalf("a repository nobody follows is ignored, got %v", err)
	}
}

func TestIngestCheckRunEvent_PropagatesFailuresAndCancellation(t *testing.T) {
	t.Parallel()
	boom := errors.New("db down")
	uc, checks, _ := checkRunSetup()
	checks.err = boom
	if err := uc.IngestCheckRunEvent(context.Background(), run(1, "queued", "")); !errors.Is(err, boom) {
		t.Fatalf("store failure: got %v", err)
	}

	uc, _, _ = checkRunSetup()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := uc.IngestCheckRunEvent(ctx, run(1, "queued", "")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: got %v", err)
	}
}

func TestIngestPullRequestEvent_UsesGitHubStateNotDefaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "acme/web"}
	checks := newFakeChecks()
	prs := &savingPRRepo{checks: checks}
	uc := webhook.NewIngestUseCase(&fakeRepoRepo{repo: repo}, prs, checks, &fakePub{})

	// No CI ever reported: the PR is NONE, not "running".
	payload := webhook.PullRequestEventPayload{Action: "opened", GitHubRepoID: 9001, GitHubPRID: 600, Number: 11,
		Title: "chore: README", State: entity.PRStateOpen, HeadSHA: "h1", MergeableState: "blocked"}
	if err := uc.IngestPullRequestEvent(ctx, payload); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	got := checks.prs[0]
	if got.CIStatus != entity.CIStatusNone || got.MergeableState != entity.MergeableStateBlocked {
		t.Fatalf("expected NONE / BLOCKED from GitHub, got %q / %q", got.CIStatus, got.MergeableState)
	}

	// Check runs reported before the PR webhook are picked up by a new head.
	_ = checks.UpsertCheckRun(ctx, entity.CheckRun{ID: 1, RepositoryID: 10, HeadSHA: "h2", Status: "completed", Conclusion: "success"})
	pushed := payload
	pushed.Action, pushed.HeadSHA, pushed.MergeableState = "synchronize", "h2", "clean"
	_ = uc.IngestPullRequestEvent(ctx, pushed)
	got = checks.prs[0]
	if got.CIStatus != entity.CIStatusSuccess || got.MergeableState != entity.MergeableStateClean {
		t.Fatalf("expected SUCCESS / CLEAN, got %q / %q", got.CIStatus, got.MergeableState)
	}
}

type failingPub struct{ err error }

func (f failingPub) Publish(context.Context, entity.ActivityEvent) error { return f.err }

func TestIngestCheckRunEvent_StaleRunsReturnThePRToNoCI(t *testing.T) {
	t.Parallel()
	uc, checks, pub := checkRunSetup()
	ctx := context.Background()
	_ = uc.IngestCheckRunEvent(ctx, run(1, "in_progress", ""))
	_ = uc.IngestCheckRunEvent(ctx, run(1, "completed", "stale"))
	if got := ciOf(checks); got != entity.CIStatusNone {
		t.Fatalf("stale run: CI = %q, want NONE", got)
	}
	if len(pub.events) != 1 {
		t.Fatalf("NONE is not announced; got %+v", pub.events)
	}
}

func TestIngestCheckRunEvent_EachStoreFailureIsReturned(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	ctx := context.Background()
	repo := &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "acme/web"}
	build := func(mut func(*fakeChecks, *savingPRRepo), repoErr error, pub interface {
		Publish(context.Context, entity.ActivityEvent) error
	}) *webhook.IngestUseCase {
		pr := &entity.PullRequest{ID: 1, RepositoryID: 10, GitHubPRID: 501, Number: 7, State: entity.PRStateOpen, HeadSHA: "sha-1", CIStatus: entity.CIStatusNone}
		checks := newFakeChecks(pr)
		prs := &savingPRRepo{checks: checks}
		mut(checks, prs)
		return webhook.NewIngestUseCase(&fakeRepoRepo{repo: repo, err: repoErr}, prs, checks, pub)
	}
	cases := map[string]*webhook.IngestUseCase{
		"repository lookup": build(func(*fakeChecks, *savingPRRepo) {}, boom, &fakePub{}),
		"store run":         build(func(c *fakeChecks, _ *savingPRRepo) { c.upsertErr = boom }, nil, &fakePub{}),
		"list runs":         build(func(c *fakeChecks, _ *savingPRRepo) { c.listErr = boom }, nil, &fakePub{}),
		"find PRs":          build(func(c *fakeChecks, _ *savingPRRepo) { c.openErr = boom }, nil, &fakePub{}),
		"save PR":           build(func(_ *fakeChecks, p *savingPRRepo) { p.upsertErr = boom }, nil, &fakePub{}),
		"publish":           build(func(*fakeChecks, *savingPRRepo) {}, nil, failingPub{err: boom}),
	}
	for name, uc := range cases {
		if err := uc.IngestCheckRunEvent(ctx, run(1, "queued", "")); !errors.Is(err, boom) {
			t.Errorf("%s failure: got %v", name, err)
		}
	}

	prFailure := build(func(c *fakeChecks, _ *savingPRRepo) { c.listErr = boom }, nil, &fakePub{})
	if err := prFailure.IngestPullRequestEvent(ctx, webhook.PullRequestEventPayload{GitHubRepoID: 9001, GitHubPRID: 9, Number: 9, HeadSHA: "x"}); !errors.Is(err, boom) {
		t.Errorf("PR ingest check-run lookup failure: got %v", err)
	}
	repoFailure := build(func(*fakeChecks, *savingPRRepo) {}, boom, &fakePub{})
	if err := repoFailure.IngestPullRequestEvent(ctx, webhook.PullRequestEventPayload{GitHubRepoID: 9001, Number: 9}); !errors.Is(err, boom) {
		t.Errorf("PR ingest repository lookup failure: got %v", err)
	}
}
