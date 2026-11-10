package webhook_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

type fakePRRepo struct {
	upserted *entity.PullRequest
}

func (f *fakePRRepo) Upsert(_ context.Context, pr *entity.PullRequest) error {
	pr.ID = 777
	f.upserted = pr
	return nil
}
func (f *fakePRRepo) GetByID(_ context.Context, _ uint64) (*entity.PullRequest, error) {
	return f.upserted, nil
}
func (f *fakePRRepo) GetByRepoAndNumber(_ context.Context, _ uint64, _ int) (*entity.PullRequest, error) {
	if f.upserted != nil {
		return f.upserted, nil
	}
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) ListOpenByRepoIDs(_ context.Context, _ []uint64) ([]entity.PullRequest, error) {
	return nil, nil
}
func (f *fakePRRepo) SaveAnalysis(_ context.Context, _ *entity.DependabotAnalysis) error { return nil }
func (f *fakePRRepo) GetAnalysisByPRAndSHA(_ context.Context, _ uint64, _ string) (*entity.DependabotAnalysis, error) {
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

type fakePub struct {
	events []entity.ActivityEvent
}

func (f *fakePub) Publish(_ context.Context, ev entity.ActivityEvent) error {
	f.events = append(f.events, ev)
	return nil
}

func TestIngestWebhookUseCase_HandlesPullRequestEventAndPublishesSSE(t *testing.T) {
	t.Parallel()

	prRepo := &fakePRRepo{}
	repoRepo := &fakeRepoRepo{
		repo: &entity.Repository{
			ID:           10,
			GitHubRepoID: 9001,
			FullName:     "arvore/hive-web",
		},
	}
	pub := &fakePub{}

	uc := webhook.NewIngestUseCase(repoRepo, prRepo, pub)

	err := uc.IngestPullRequestEvent(context.Background(), webhook.PullRequestEventPayload{
		Action:       "opened",
		GitHubRepoID: 9001,
		GitHubPRID:   5005,
		Number:       88,
		Title:        "chore(deps): bump rxjs from 7.8.0 to 7.8.1",
		AuthorLogin:  "dependabot[bot]",
		HTMLURL:      "https://github.com/arvore/hive-web/pull/88",
		IsDraft:      false,
		State:        entity.PRStateOpen,
		HeadSHA:      "sha-88",
		BaseRef:      "main",
	})
	if err != nil {
		t.Fatalf("IngestPullRequestEvent() error: %v", err)
	}

	if prRepo.upserted == nil || !prRepo.upserted.IsDependabot {
		t.Fatalf("expected dependabot PR to be upserted, got %#v", prRepo.upserted)
	}
	if len(pub.events) != 1 || pub.events[0].PRNumber != 88 {
		t.Fatalf("expected 1 published SSE event for PR #88, got %#v", pub.events)
	}
}

func TestIngestPullRequestEvent_RedeliveryIsIdempotent(t *testing.T) {
	t.Parallel()
	prRepo := &fakePRRepo{}
	repoRepo := &fakeRepoRepo{repo: &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "arvore/hive-web"}}
	pub := &fakePub{}
	uc := webhook.NewIngestUseCase(repoRepo, prRepo, pub)
	ctx := context.Background()

	payload := webhook.PullRequestEventPayload{
		Action: "opened", GitHubRepoID: 9001, GitHubPRID: 5005, Number: 88, Title: "feat: x",
		AuthorLogin: "bianca", State: entity.PRStateOpen, HeadSHA: "sha-1", BaseRef: "main",
	}
	if err := uc.IngestPullRequestEvent(ctx, payload); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	// CI finished in the meantime; a redelivery must neither reset it nor publish again.
	prRepo.upserted.CIStatus = entity.CIStatusSuccess
	if err := uc.IngestPullRequestEvent(ctx, payload); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if len(pub.events) != 1 {
		t.Fatalf("redelivery must not publish again, got %d events", len(pub.events))
	}
	if prRepo.upserted.CIStatus != entity.CIStatusSuccess {
		t.Fatalf("redelivery must not reset CI, got %q", prRepo.upserted.CIStatus)
	}

	// A real change (edited title, same head) is stored and announced, keeping CI.
	edited := payload
	edited.Action, edited.Title = "edited", "feat: x (renamed)"
	_ = uc.IngestPullRequestEvent(ctx, edited)
	if len(pub.events) != 2 || prRepo.upserted.Title != "feat: x (renamed)" || prRepo.upserted.CIStatus != entity.CIStatusSuccess {
		t.Fatalf("edit: events=%d title=%q ci=%q", len(pub.events), prRepo.upserted.Title, prRepo.upserted.CIStatus)
	}

	// A new commit resets CI to pending.
	pushed := edited
	pushed.Action, pushed.HeadSHA = "synchronize", "sha-2"
	_ = uc.IngestPullRequestEvent(ctx, pushed)
	if len(pub.events) != 3 || prRepo.upserted.CIStatus != entity.CIStatusPending {
		t.Fatalf("new commit: events=%d ci=%q", len(pub.events), prRepo.upserted.CIStatus)
	}
}

type failingPRRepo struct {
	fakePRRepo
	getErr, upsertErr error
}

func (f *failingPRRepo) GetByRepoAndNumber(ctx context.Context, repoID uint64, number int) (*entity.PullRequest, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.fakePRRepo.GetByRepoAndNumber(ctx, repoID, number)
}

func (f *failingPRRepo) Upsert(ctx context.Context, pr *entity.PullRequest) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	return f.fakePRRepo.Upsert(ctx, pr)
}

func TestIngestPullRequestEvent_PropagatesFailures(t *testing.T) {
	t.Parallel()
	boom := errors.New("db down")
	repoRepo := &fakeRepoRepo{repo: &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "arvore/hive-web"}}
	payload := webhook.PullRequestEventPayload{Action: "opened", GitHubRepoID: 9001, GitHubPRID: 1, Number: 1, HeadSHA: "a"}

	if err := webhook.NewIngestUseCase(repoRepo, &failingPRRepo{getErr: boom}, &fakePub{}).IngestPullRequestEvent(context.Background(), payload); !errors.Is(err, boom) {
		t.Errorf("lookup failure: got %v", err)
	}
	if err := webhook.NewIngestUseCase(repoRepo, &failingPRRepo{upsertErr: boom}, &fakePub{}).IngestPullRequestEvent(context.Background(), payload); !errors.Is(err, boom) {
		t.Errorf("upsert failure: got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := webhook.NewIngestUseCase(repoRepo, &fakePRRepo{}, &fakePub{}).IngestPullRequestEvent(ctx, payload); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: got %v", err)
	}
}
