package triage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/triage"
)

// fakeCheckReader returns every run it holds, like the store's superset read: the use case must
// pick each PR's own runs (its repository and head commit) out of it.
type fakeCheckReader struct {
	runs  []entity.CheckRun
	err   error
	calls int
}

func (f *fakeCheckReader) ListCheckRunsForCommits(context.Context, []uint64, []string) ([]entity.CheckRun, error) {
	f.calls++
	return f.runs, f.err
}

func withChecks(f turnFixture, reader *fakeCheckReader) *triage.UseCase {
	return triage.NewUseCase(&fakeUserRepo{user: &entity.User{ID: 1, Login: "murilo"}}, f.repos, f.prs, &fakePublisher{}, nil, nil, nil, triage.WithCheckRuns(reader))
}

func TestSnapshot_CarriesTheCurrentChecksOfEachPR(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(
		openPR(1, func(p *entity.PullRequest) { p.HeadSHA = "sha-a" }),
		openPR(2, func(p *entity.PullRequest) { p.HeadSHA = "sha-b" }),
		openPR(3, func(p *entity.PullRequest) { p.HeadSHA = "sha-c" }),
	)
	reader := &fakeCheckReader{runs: []entity.CheckRun{
		{ID: 1, RepositoryID: 10, HeadSHA: "sha-a", Name: "build", Status: "completed", Conclusion: "failure"},
		{ID: 5, RepositoryID: 10, HeadSHA: "sha-a", Name: "build", Status: "completed", Conclusion: "success", Title: "ok"}, // a re-run of build
		{ID: 2, RepositoryID: 10, HeadSHA: "sha-a", Name: "test", Status: "in_progress"},
		{ID: 3, RepositoryID: 10, HeadSHA: "sha-b", Name: "build", Status: "completed", Conclusion: "success"},
		{ID: 4, RepositoryID: 99, HeadSHA: "sha-a", Name: "other-repo", Status: "completed", Conclusion: "success"}, // same commit id, another repository
	}}
	uc := withChecks(f, reader)

	snap, err := uc.GetWorkspaceSnapshot(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetWorkspaceSnapshot() error: %v", err)
	}

	names := func(id uint64) []string {
		out := []string{}
		for _, r := range snap.Checks[id] {
			out = append(out, r.Name+":"+r.Status+":"+r.Conclusion)
		}
		return out
	}
	if got := names(1); len(got) != 2 || got[0] != "build:completed:success" || got[1] != "test:in_progress:" {
		t.Errorf("PR 1 shows the latest run of each check, and none of another repository: %v", got)
	}
	if got := names(2); len(got) != 1 || got[0] != "build:completed:success" {
		t.Errorf("PR 2: %v", got)
	}
	if _, ok := snap.Checks[3]; ok {
		t.Errorf("a PR whose commit reported nothing has no entry: %+v", snap.Checks)
	}
	if reader.calls != 1 {
		t.Errorf("the checks of every PR are read in one query, got %d", reader.calls)
	}
}

func TestSnapshot_ChecksFailureIsAnErrorAndNoReaderMeansNone(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1))
	boom := errors.New("db down")
	if _, err := withChecks(f, &fakeCheckReader{err: boom}).GetWorkspaceSnapshot(context.Background(), 1); !errors.Is(err, boom) {
		t.Fatalf("expected the store error (never show CI without its checks), got %v", err)
	}

	snap, err := f.uc.GetWorkspaceSnapshot(context.Background(), 1)
	if err != nil || snap.Checks == nil || len(snap.Checks) != 0 {
		t.Fatalf("without a check reader there are none, as an empty map and never null: %+v, %v", snap, err)
	}
}
