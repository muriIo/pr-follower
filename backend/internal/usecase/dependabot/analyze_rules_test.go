package dependabot_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/usecase/dependabot"
)

type recordingPub struct {
	mu     sync.Mutex
	events []entity.ActivityEvent
}

func (r *recordingPub) Publish(_ context.Context, ev entity.ActivityEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return nil
}

// countingGateway counts the reads an analysis makes and can hold the tarball read open.
type countingGateway struct {
	fakeGHGateway
	listed   atomic.Int32
	tarballs atomic.Int32
	hold     chan struct{}
}

func (c *countingGateway) ListPRChangedFiles(ctx context.Context, repo entity.Repository, n int) ([]entity.ChangedFile, error) {
	c.listed.Add(1)
	return c.fakeGHGateway.ListPRChangedFiles(ctx, repo, n)
}

func (c *countingGateway) StreamRepoTarball(_ context.Context, _ entity.Repository, _ string) (io.ReadCloser, error) {
	c.tarballs.Add(1)
	if c.hold != nil {
		<-c.hold
	}
	return io.NopCloser(bytes.NewReader([]byte("tarball"))), nil
}

// devPatchAnalyzer reports a harmless dev-dependency patch bump.
type devPatchAnalyzer struct{ fakeAnalyzer }

func (devPatchAnalyzer) AnalyzeLockfile(_ context.Context, in port.DependencyAnalysisInput) (*entity.DependabotAnalysis, error) {
	return &entity.DependabotAnalysis{
		PullRequestID: in.PullRequest.ID, HeadSHA: in.PullRequest.HeadSHA, Ecosystem: "npm", PackageName: in.PackageName,
		FromVersion: in.FromVersion, ToVersion: in.ToVersion, SemverBumpType: entity.SemverBumpPatch,
		IsDirectDep: true, DepScope: entity.DependencyScopeDev,
	}, nil
}

func dependabotPR(mutate ...func(*entity.PullRequest)) *entity.PullRequest {
	pr := &entity.PullRequest{ID: 55, RepositoryID: 10, Number: 19, Title: "chore(deps-dev): bump vitest from 2.1.0 to 2.1.1",
		IsDependabot: true, State: entity.PRStateOpen, CIStatus: entity.CIStatusSuccess, HeadSHA: "sha-1"}
	for _, m := range mutate {
		m(pr)
	}
	return pr
}

func newAnalyze(pr *entity.PullRequest, gh port.GitHubGateway, analyzer port.DependencyAnalyzer, pub port.EventPublisher) (*dependabot.AnalyzeUseCase, *fakePRRepo) {
	prs := &fakePRRepo{pr: pr}
	repos := &fakeRepoRepo{repo: &entity.Repository{ID: 10, Owner: "acme", Name: "web", FullName: "acme/web", InstallationID: 5}}
	return dependabot.NewAnalyzeUseCase(prs, repos, gh, []port.DependencyAnalyzer{analyzer}, &fakeScanner{}, pub), prs
}

func TestAnalyzePR_OnlyAnalysesWhatItCanReadHonestly(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		pr   *entity.PullRequest
		want error
	}{
		"a grouped update names no single package": {dependabotPR(func(p *entity.PullRequest) { p.Title = "chore(deps): bump the npm_and_yarn group with 3 updates" }), entity.ErrNotAnalysable},
		"several packages in one title":            {dependabotPR(func(p *entity.PullRequest) { p.Title = "build(deps): bump tinypool and vitest" }), entity.ErrNotAnalysable},
		"a human PR is not analysed":               {dependabotPR(func(p *entity.PullRequest) { p.IsDependabot = false }), entity.ErrInvalidInput},
		"a closed PR is not analysed":              {dependabotPR(func(p *entity.PullRequest) { p.State = entity.PRStateClosed }), entity.ErrConflict},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gh, pub := &countingGateway{}, &recordingPub{}
			uc, prs := newAnalyze(tc.pr, gh, &fakeAnalyzer{}, pub)
			if _, err := uc.AnalyzePR(context.Background(), 55); !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
			if gh.listed.Load() != 0 || prs.saved != nil || len(pub.events) != 0 {
				t.Fatalf("nothing may be read, stored or announced: reads=%d saved=%v events=%d", gh.listed.Load(), prs.saved, len(pub.events))
			}
		})
	}
}

func TestAnalyzePR_VerdictDoesNotBakeInTheCIOfTheMoment(t *testing.T) {
	t.Parallel()
	// The analysis is kept for the commit and CI keeps changing: failing CI is judged live (it
	// blocks the batch merge), not frozen into the stored verdict.
	for _, ci := range []entity.CIStatus{entity.CIStatusFailure, entity.CIStatusNone, entity.CIStatusPending} {
		uc, _ := newAnalyze(dependabotPR(func(p *entity.PullRequest) { p.CIStatus = ci }), &countingGateway{}, &devPatchAnalyzer{}, &recordingPub{})
		got, err := uc.AnalyzePR(context.Background(), 55)
		if err != nil || got.RiskVerdict != entity.RiskVerdictLow {
			t.Fatalf("CI %q: a dev patch bump is low risk whatever CI said when it was read: %+v, %v", ci, got, err)
		}
	}
}

func TestAnalyzePR_ConcurrentCallsShareOneAnalysisAndOneEvent(t *testing.T) {
	t.Parallel()
	gh := &countingGateway{hold: make(chan struct{})}
	pub := &recordingPub{}
	uc, _ := newAnalyze(dependabotPR(), gh, &devPatchAnalyzer{}, pub)

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := uc.AnalyzePR(context.Background(), 55); err != nil {
				t.Errorf("AnalyzePR() error: %v", err)
			}
		}()
	}
	for gh.tarballs.Load() == 0 { // the leader is inside the scan; the others wait for it
	}
	close(gh.hold)
	wg.Wait()

	if gh.tarballs.Load() != 1 || len(pub.events) != 1 {
		t.Fatalf("expected one scan and one event, got %d scans and %d events", gh.tarballs.Load(), len(pub.events))
	}
}

func TestAnalyzePR_AlreadyAnalysedCommitIsNotReadOrAnnouncedAgain(t *testing.T) {
	t.Parallel()
	gh, pub := &countingGateway{}, &recordingPub{}
	uc, prs := newAnalyze(dependabotPR(), gh, &devPatchAnalyzer{}, pub)
	first, err := uc.AnalyzePR(context.Background(), 55)
	if err != nil {
		t.Fatal(err)
	}
	prs.cached = first

	if _, err := uc.AnalyzePR(context.Background(), 55); err != nil || gh.listed.Load() != 1 || len(pub.events) != 1 {
		t.Fatalf("a redelivered webhook converges: err=%v reads=%d events=%d", err, gh.listed.Load(), len(pub.events))
	}
}

// followRepo knows which repositories a user follows.
type followRepo struct {
	fakeRepoRepo
	followed []entity.Repository
}

func (f *followRepo) ListFollowedByUser(context.Context, uint64) ([]entity.Repository, error) {
	return f.followed, nil
}

func TestAnalyzePRForUser_OnlyFollowersOfTheRepositoryMayAskForIt(t *testing.T) {
	t.Parallel()
	repo := entity.Repository{ID: 10, Owner: "acme", Name: "web", FullName: "acme/web"}
	build := func(followed ...entity.Repository) *dependabot.AnalyzeUseCase {
		return dependabot.NewAnalyzeUseCase(&fakePRRepo{pr: dependabotPR()},
			&followRepo{fakeRepoRepo: fakeRepoRepo{repo: &repo}, followed: followed}, &countingGateway{},
			[]port.DependencyAnalyzer{&devPatchAnalyzer{}}, &fakeScanner{}, &recordingPub{})
	}

	if _, err := build().AnalyzePRForUser(context.Background(), 1, 55); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("someone who does not follow the repository must not reach its analysis: %v", err)
	}
	got, err := build(repo).AnalyzePRForUser(context.Background(), 1, 55)
	if err != nil || got.PackageName != "vitest" {
		t.Fatalf("a follower gets the analysis: %+v, %v", got, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := build(repo).AnalyzePRForUser(canceled, 1, 55); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestAnalyzePR_AnEcosystemNoAnalyzerReadsIsNotAnalysableEither(t *testing.T) {
	t.Parallel()
	uc, prs := newAnalyze(dependabotPR(), &countingGateway{}, &noLockfileAnalyzer{}, &recordingPub{})
	if _, err := uc.AnalyzePR(context.Background(), 55); !errors.Is(err, entity.ErrNotAnalysable) {
		t.Fatalf("a lockfile nobody can read is a PR we cannot analyse, not a bad request: %v", err)
	}
	if prs.saved != nil {
		t.Fatal("nothing may be stored")
	}
}

// noLockfileAnalyzer reads no lockfile the PR changes.
type noLockfileAnalyzer struct{ devPatchAnalyzer }

func (noLockfileAnalyzer) CanAnalyze(string) bool { return false }
