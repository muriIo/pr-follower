package dependabot_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/usecase/dependabot"
)

type fakePRRepo struct {
	pr     *entity.PullRequest
	saved  *entity.DependabotAnalysis
	cached *entity.DependabotAnalysis
}

func (f *fakePRRepo) Upsert(_ context.Context, _ *entity.PullRequest) error { return nil }
func (f *fakePRRepo) GetByID(_ context.Context, _ uint64) (*entity.PullRequest, error) {
	return f.pr, nil
}
func (f *fakePRRepo) GetByRepoAndNumber(_ context.Context, _ uint64, _ int) (*entity.PullRequest, error) {
	return f.pr, nil
}
func (f *fakePRRepo) ListOpenByRepoIDs(_ context.Context, _ []uint64) ([]entity.PullRequest, error) {
	return nil, nil
}
func (f *fakePRRepo) SaveAnalysis(_ context.Context, a *entity.DependabotAnalysis) error {
	f.saved = a
	return nil
}
func (f *fakePRRepo) GetAnalysisByPRAndSHA(_ context.Context, _ uint64, sha string) (*entity.DependabotAnalysis, error) {
	if f.cached != nil && f.cached.HeadSHA == sha {
		return f.cached, nil
	}
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) UpsertUserPRState(_ context.Context, _ *entity.UserPRState) error { return nil }
func (f *fakePRRepo) GetUserPRState(_ context.Context, _, _ uint64) (*entity.UserPRState, error) {
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) ListUserPRStates(_ context.Context, _ uint64, _ []uint64) (map[uint64]entity.UserPRState, error) {
	return nil, nil
}

type fakeRepoRepo struct {
	repo *entity.Repository
}

func (f *fakeRepoRepo) Upsert(_ context.Context, _ *entity.Repository) error { return nil }
func (f *fakeRepoRepo) GetByID(_ context.Context, _ uint64) (*entity.Repository, error) {
	return f.repo, nil
}
func (f *fakeRepoRepo) GetByGitHubRepoID(_ context.Context, _ int64) (*entity.Repository, error) {
	return f.repo, nil
}
func (f *fakeRepoRepo) ListFollowedByUser(_ context.Context, _ uint64) ([]entity.Repository, error) {
	return nil, nil
}
func (f *fakeRepoRepo) SetUserFollow(_ context.Context, _, _ uint64, _ bool) error { return nil }

type fakeGHGateway struct{}

func (f *fakeGHGateway) GetFileContent(_ context.Context, _ entity.Repository, _, filePath string) ([]byte, error) {
	if filePath == "package.json" {
		return []byte(`{"dependencies":{"axios":"^2.0.0"}}`), nil
	}
	return []byte(`{"lockfileVersion":3}`), nil
}
func (f *fakeGHGateway) ListPRChangedFiles(_ context.Context, _ entity.Repository, _ int) ([]entity.ChangedFile, error) {
	return []entity.ChangedFile{{Path: "package-lock.json"}}, nil
}
func (f *fakeGHGateway) StreamRepoTarball(_ context.Context, _ entity.Repository, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader([]byte("tarball"))), nil
}
func (f *fakeGHGateway) ApprovePullRequest(_ context.Context, _ entity.Repository, _ int, _ string) error {
	return nil
}
func (f *fakeGHGateway) MergePullRequest(_ context.Context, _ entity.Repository, _ int, _ string) error {
	return nil
}
func (f *fakeGHGateway) CreatePRComment(_ context.Context, _ entity.Repository, _ int, _ string) error {
	return nil
}

type fakeAnalyzer struct{}

func (f *fakeAnalyzer) Ecosystem() string { return "npm" }
func (f *fakeAnalyzer) CanAnalyze(lockfile string) bool {
	return lockfile == "package-lock.json"
}
func (f *fakeAnalyzer) AnalyzeLockfile(_ context.Context, in port.DependencyAnalysisInput) (*entity.DependabotAnalysis, error) {
	return &entity.DependabotAnalysis{
		PullRequestID:  in.PullRequest.ID,
		HeadSHA:        in.PullRequest.HeadSHA,
		Ecosystem:      "npm",
		PackageName:    in.PackageName,
		FromVersion:    in.FromVersion,
		ToVersion:      in.ToVersion,
		SemverBumpType: entity.SemverBumpMajor,
		IsDirectDep:    true,
		DepScope:       entity.DependencyScopeProd,
		DependencyPathGraph: []entity.DependencyNode{
			{Name: in.Repository.Name, Version: "root", Scope: entity.DependencyScopeProd},
			{Name: in.PackageName, Version: in.ToVersion, Scope: entity.DependencyScopeProd},
		},
	}, nil
}

type fakeScanner struct{}

func (f *fakeScanner) ScanTarball(ctx context.Context, _ io.Reader, _ []string) ([]entity.BlastRadiusUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	return []entity.BlastRadiusUsage{
		{
			FilePath:   "src/api/client.ts",
			LineNumber: 3,
			ImportStmt: "import axios from 'axios';",
		},
	}, nil
}

type fakePub struct{}

func (f *fakePub) Publish(_ context.Context, _ entity.ActivityEvent) error { return nil }

func TestAnalyzeDependabotPR_ComputesFullVerdictAndRespectsCancellation(t *testing.T) {
	t.Parallel()

	prRepo := &fakePRRepo{
		pr: &entity.PullRequest{
			ID:           55,
			RepositoryID: 10,
			Number:       19,
			Title:        "chore(deps): bump axios from 1.7.2 to 2.0.0",
			IsDependabot: true,
			State:        entity.PRStateOpen,
			CIStatus:     entity.CIStatusSuccess,
			HeadSHA:      "sha-axios-2",
		},
	}
	repoRepo := &fakeRepoRepo{
		repo: &entity.Repository{ID: 10, Owner: "arvore", Name: "api", FullName: "arvore/api"},
	}

	uc := dependabot.NewAnalyzeUseCase(
		prRepo,
		repoRepo,
		&fakeGHGateway{},
		[]port.DependencyAnalyzer{&fakeAnalyzer{}},
		&fakeScanner{},
		&fakePub{},
	)

	res, err := uc.AnalyzePR(context.Background(), 55)
	if err != nil {
		t.Fatalf("AnalyzePR() unexpected error: %v", err)
	}

	if res.PackageName != "axios" || res.FromVersion != "1.7.2" || res.ToVersion != "2.0.0" {
		t.Fatalf("unexpected parsed versions: %#v", res)
	}
	if res.RiskVerdict != entity.RiskVerdictHigh {
		t.Errorf("expected HIGH risk verdict for direct prod major bump with imports, got %q", res.RiskVerdict)
	}
	if len(res.BlastRadiusUsages) != 1 {
		t.Errorf("expected 1 blast radius usage, got %d", len(res.BlastRadiusUsages))
	}

	// Test cooperative cancellation
	canceledCtx, cancel := context.WithCancelCause(context.Background())
	cancel(entity.ErrOperationCanceled)
	_, err = uc.AnalyzePR(canceledCtx, 55)
	if !errors.Is(err, entity.ErrOperationCanceled) {
		t.Fatalf("expected ErrOperationCanceled, got %v", err)
	}
}
