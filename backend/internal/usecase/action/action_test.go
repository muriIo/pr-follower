package action_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/action"
)

type fakePRRepo struct {
	prs    map[uint64]*entity.PullRequest
	states map[uint64]*entity.UserPRState
}

func (f *fakePRRepo) Upsert(_ context.Context, pr *entity.PullRequest) error {
	f.prs[pr.ID] = pr
	return nil
}
func (f *fakePRRepo) GetByID(_ context.Context, id uint64) (*entity.PullRequest, error) {
	if p, ok := f.prs[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) GetByRepoAndNumber(_ context.Context, _ uint64, _ int) (*entity.PullRequest, error) {
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) ListOpenByRepoIDs(_ context.Context, _ []uint64) ([]entity.PullRequest, error) {
	return nil, nil
}
func (f *fakePRRepo) SaveAnalysis(_ context.Context, _ *entity.DependabotAnalysis) error { return nil }
func (f *fakePRRepo) GetAnalysisByPRAndSHA(_ context.Context, _ uint64, _ string) (*entity.DependabotAnalysis, error) {
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) UpsertUserPRState(_ context.Context, s *entity.UserPRState) error {
	f.states[s.PullRequestID] = s
	return nil
}
func (f *fakePRRepo) GetUserPRState(_ context.Context, prID, _ uint64) (*entity.UserPRState, error) {
	if s, ok := f.states[prID]; ok {
		return s, nil
	}
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) ListUserPRStates(_ context.Context, _ uint64, _ []uint64) (map[uint64]entity.UserPRState, error) {
	return nil, nil
}

type fakeRepoRepo struct{}

func (f *fakeRepoRepo) Upsert(_ context.Context, _ *entity.Repository) error { return nil }
func (f *fakeRepoRepo) GetByID(_ context.Context, id uint64) (*entity.Repository, error) {
	return &entity.Repository{ID: id, Owner: "arvore", Name: "hive-web", FullName: "arvore/hive-web"}, nil
}
func (f *fakeRepoRepo) GetByGitHubRepoID(_ context.Context, _ int64) (*entity.Repository, error) {
	return nil, entity.ErrNotFound
}
func (f *fakeRepoRepo) ListFollowedByUser(_ context.Context, _ uint64) ([]entity.Repository, error) {
	return nil, nil
}
func (f *fakeRepoRepo) SetUserFollow(_ context.Context, _, _ uint64, _ bool) error { return nil }

type fakeGHGateway struct {
	approvedPRs []int
	mergedPRs   []int
	comments    []string
	onMerge     func(prNumber int)
}

func (f *fakeGHGateway) GetFileContent(_ context.Context, _ entity.Repository, _, _ string) ([]byte, error) {
	return nil, nil
}
func (f *fakeGHGateway) ListPRChangedFiles(_ context.Context, _ entity.Repository, _ int) ([]entity.ChangedFile, error) {
	return nil, nil
}
func (f *fakeGHGateway) StreamRepoTarball(_ context.Context, _ entity.Repository, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (f *fakeGHGateway) ApprovePullRequest(ctx context.Context, _ entity.Repository, prNumber int, _ string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	f.approvedPRs = append(f.approvedPRs, prNumber)
	return nil
}
func (f *fakeGHGateway) MergePullRequest(ctx context.Context, _ entity.Repository, prNumber int, _ string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	f.mergedPRs = append(f.mergedPRs, prNumber)
	if f.onMerge != nil {
		f.onMerge(prNumber)
	}
	return nil
}
func (f *fakeGHGateway) CreatePRComment(ctx context.Context, _ entity.Repository, _ int, comment string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	f.comments = append(f.comments, comment)
	return nil
}

type fakePub struct{ events []entity.ActivityEvent }

func (f *fakePub) Publish(_ context.Context, ev entity.ActivityEvent) error {
	f.events = append(f.events, ev)
	return nil
}

type fakeUsers struct{}

func (fakeUsers) Upsert(context.Context, *entity.User) error { return nil }
func (fakeUsers) GetByID(_ context.Context, id uint64) (*entity.User, error) {
	if id == 1 {
		return &entity.User{ID: 1, Login: "octocat"}, nil
	}
	return nil, entity.ErrNotFound
}
func (fakeUsers) GetByGitHubID(context.Context, int64) (*entity.User, error) {
	return nil, entity.ErrNotFound
}

func TestExecutePRActionUseCase_ApproveMergeAndBatchCancel(t *testing.T) {
	t.Parallel()

	prRepo := &fakePRRepo{
		prs: map[uint64]*entity.PullRequest{
			1: {ID: 1, RepositoryID: 10, Number: 11, State: entity.PRStateOpen, HeadSHA: "sha-1"},
			2: {ID: 2, RepositoryID: 10, Number: 12, State: entity.PRStateOpen, HeadSHA: "sha-2"},
			3: {ID: 3, RepositoryID: 10, Number: 13, State: entity.PRStateOpen, HeadSHA: "sha-3"},
		},
		states: make(map[uint64]*entity.UserPRState),
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	gh := &fakeGHGateway{
		onMerge: func(prNumber int) {
			if prNumber == 12 {
				cancel(entity.ErrOperationCanceled)
			}
		},
	}

	uc := action.NewUseCase(prRepo, &fakeRepoRepo{}, fakeUsers{}, gh, &fakePub{})

	if err := uc.ApprovePR(context.Background(), 1, 1, "LGTM"); err != nil {
		t.Fatalf("ApprovePR() error: %v", err)
	}
	if prRepo.states[1].TurnState != entity.TurnStateApprovedByMe {
		t.Errorf("expected APPROVED_BY_ME, got %q", prRepo.states[1].TurnState)
	}

	if err := uc.RunDependabotCommand(context.Background(), 1, 1, "rebase"); err != nil {
		t.Fatalf("RunDependabotCommand() error: %v", err)
	}
	if len(gh.comments) != 1 || gh.comments[0] != "@dependabot rebase" {
		t.Errorf("unexpected dependabot comment: %v", gh.comments)
	}

	// Batch merge PRs 2 and 3, canceling after PR 2 merges
	mergedIDs, err := uc.BatchApproveAndMerge(ctx, 1, []uint64{2, 3}, "squash")
	if !errors.Is(err, entity.ErrOperationCanceled) {
		t.Fatalf("expected ErrOperationCanceled on batch cancel, got %v", err)
	}
	if len(mergedIDs) != 1 || mergedIDs[0] != 2 {
		t.Fatalf("expected only PR 2 to complete before cancellation, got %v", mergedIDs)
	}
}

func TestActions_PublishWhoActed(t *testing.T) {
	t.Parallel()
	prRepo := &fakePRRepo{
		prs: map[uint64]*entity.PullRequest{
			1: {ID: 1, RepositoryID: 10, Number: 11, State: entity.PRStateOpen, HeadSHA: "sha-1"},
			2: {ID: 2, RepositoryID: 10, Number: 12, State: entity.PRStateOpen, HeadSHA: "sha-2"},
		},
		states: make(map[uint64]*entity.UserPRState),
	}
	pub := &fakePub{}
	uc := action.NewUseCase(prRepo, &fakeRepoRepo{}, fakeUsers{}, &fakeGHGateway{}, pub)
	ctx := context.Background()

	_ = uc.ApprovePR(ctx, 1, 1, "LGTM")
	_ = uc.MergePR(ctx, 1, 1, "squash")
	_ = uc.RunDependabotCommand(ctx, 1, 2, "rebase")

	if len(pub.events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(pub.events))
	}
	for _, ev := range pub.events {
		if ev.ActorLogin != "octocat" {
			t.Errorf("%s: expected actor octocat, got %q", ev.Type, ev.ActorLogin)
		}
	}

	// An unknown user still acts; the event just has no actor.
	pub.events = nil
	_ = uc.ApprovePR(ctx, 99, 2, "LGTM")
	if len(pub.events) != 1 || pub.events[0].ActorLogin != "" {
		t.Fatalf("unknown user: expected an event without actor, got %+v", pub.events)
	}
}
