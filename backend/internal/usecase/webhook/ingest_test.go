package webhook_test

import (
	"context"
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
