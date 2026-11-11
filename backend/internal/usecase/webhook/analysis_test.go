package webhook_test

import (
	"context"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

type recordingScheduler struct{ ids []uint64 }

func (r *recordingScheduler) ScheduleAnalysis(_ context.Context, prID uint64) {
	r.ids = append(r.ids, prID)
}

func dependabotPayload(action, sha string, mutate ...func(*webhook.PullRequestEventPayload)) webhook.PullRequestEventPayload {
	p := webhook.PullRequestEventPayload{
		Action: action, GitHubRepoID: 9001, GitHubPRID: 5005, Number: 88,
		Title: "chore(deps): bump rxjs from 7.8.0 to 7.8.1", AuthorLogin: "dependabot[bot]",
		State: entity.PRStateOpen, HeadSHA: sha, BaseRef: "main",
	}
	for _, m := range mutate {
		m(&p)
	}
	return p
}

func TestIngestPullRequestEvent_SchedulesTheAnalysisOfAnOpenDependabotPR(t *testing.T) {
	t.Parallel()
	scheduler := &recordingScheduler{}
	repos := &fakeRepoRepo{repo: &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "arvore/hive-web"}}
	uc := webhook.NewIngestUseCase(repos, &fakePRRepo{}, newFakeChecks(), &fakePub{}, nil, webhook.WithAnalysisScheduler(scheduler))
	ctx := context.Background()

	if err := uc.IngestPullRequestEvent(ctx, dependabotPayload("opened", "sha-1")); err != nil {
		t.Fatal(err)
	}
	if len(scheduler.ids) != 1 || scheduler.ids[0] != 777 {
		t.Fatalf("expected the PR to be scheduled once: %v", scheduler.ids)
	}

	// Redelivery changes nothing, so it schedules nothing (ADR-0011).
	if err := uc.IngestPullRequestEvent(ctx, dependabotPayload("opened", "sha-1")); err != nil || len(scheduler.ids) != 1 {
		t.Fatalf("a redelivery must not schedule again: err=%v ids=%v", err, scheduler.ids)
	}

	// A new commit does: it needs a new analysis.
	if err := uc.IngestPullRequestEvent(ctx, dependabotPayload("synchronize", "sha-2")); err != nil || len(scheduler.ids) != 2 {
		t.Fatalf("a new commit is scheduled: err=%v ids=%v", err, scheduler.ids)
	}
}

func TestIngestPullRequestEvent_SchedulesNothingForWhatIsNotAnalysable(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*webhook.PullRequestEventPayload){
		"a human PR": func(p *webhook.PullRequestEventPayload) {
			p.AuthorLogin = "bia"
			p.Title = "feat: bump rxjs from 7.8.0 to 7.8.1"
		},
		"a closed PR": func(p *webhook.PullRequestEventPayload) { p.State = entity.PRStateClosed; p.Action = "closed" },
		"a merged PR": func(p *webhook.PullRequestEventPayload) {
			p.Merged = true
			p.State = entity.PRStateClosed
			p.Action = "closed"
		},
	} {
		scheduler := &recordingScheduler{}
		repos := &fakeRepoRepo{repo: &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "arvore/hive-web"}}
		uc := webhook.NewIngestUseCase(repos, &fakePRRepo{}, newFakeChecks(), &fakePub{}, nil, webhook.WithAnalysisScheduler(scheduler))
		if err := uc.IngestPullRequestEvent(context.Background(), dependabotPayload("opened", "sha-1", mutate)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(scheduler.ids) != 0 {
			t.Errorf("%s: nothing to analyse, got %v", name, scheduler.ids)
		}
	}
}

func TestIngestPullRequestEvent_WorksWithoutAScheduler(t *testing.T) {
	t.Parallel()
	repos := &fakeRepoRepo{repo: &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "arvore/hive-web"}}
	uc := webhook.NewIngestUseCase(repos, &fakePRRepo{}, newFakeChecks(), &fakePub{}, nil)
	if err := uc.IngestPullRequestEvent(context.Background(), dependabotPayload("opened", "sha-1")); err != nil {
		t.Fatalf("the app credentials are optional: %v", err)
	}
}
