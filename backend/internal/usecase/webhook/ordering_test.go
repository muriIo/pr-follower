package webhook_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

// fakeVersions keeps, per PR, the newest GitHub updated_at applied: it only moves forward.
type fakeVersions struct {
	mu   sync.Mutex
	byPR map[uint64]time.Time
}

func (f *fakeVersions) AdvancePullRequestVersion(_ context.Context, prID uint64, updatedAt time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if current, ok := f.byPR[prID]; ok && updatedAt.Before(current) {
		return false, nil
	}
	f.byPR[prID] = updatedAt
	return true, nil
}

// ADR-0011: GitHub may deliver webhooks out of order; an older delivery never overwrites newer state.
func TestIngestPullRequestEvent_AnOlderDeliveryChangesNothing(t *testing.T) {
	t.Parallel()
	prRepo := &fakePRRepo{}
	repoRepo := &fakeRepoRepo{repo: &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "acme/web"}}
	pub := &fakePub{}
	versions := &fakeVersions{byPR: map[uint64]time.Time{}}
	uc := webhook.NewIngestUseCase(repoRepo, prRepo, newFakeChecks(), pub, nil, webhook.WithDeliveryOrdering(versions))
	ctx := context.Background()
	t0 := time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC)

	opened := webhook.PullRequestEventPayload{
		Action: "opened", GitHubRepoID: 9001, GitHubPRID: 5005, Number: 88, Title: "feat: x",
		AuthorLogin: "bia", State: entity.PRStateOpen, HeadSHA: "sha-1", BaseRef: "main", UpdatedAt: t0,
	}
	merged := opened
	merged.Action, merged.State, merged.Merged, merged.UpdatedAt = "closed", entity.PRStateClosed, true, t0.Add(2*time.Minute)
	pushed := opened
	pushed.Action, pushed.HeadSHA, pushed.UpdatedAt = "synchronize", "sha-2", t0.Add(time.Minute) // sent before the merge, arrives after it

	for _, p := range []webhook.PullRequestEventPayload{opened, merged, pushed} {
		if err := uc.IngestPullRequestEvent(ctx, p); err != nil {
			t.Fatalf("%s: %v", p.Action, err)
		}
	}

	if prRepo.upserted.State != entity.PRStateMerged || prRepo.upserted.HeadSHA != "sha-1" {
		t.Fatalf("the late synchronize reopened or moved the merged PR: state=%s head=%s", prRepo.upserted.State, prRepo.upserted.HeadSHA)
	}
	if len(pub.events) != 2 {
		t.Fatalf("the late delivery must publish nothing, got %d events", len(pub.events))
	}

	// Two events in the same second are both applied: only an older one is dropped.
	edited := merged
	edited.Action, edited.Title = "edited", "feat: x (renamed)"
	if err := uc.IngestPullRequestEvent(ctx, edited); err != nil || prRepo.upserted.Title != "feat: x (renamed)" {
		t.Fatalf("same-second delivery: err=%v title=%q", err, prRepo.upserted.Title)
	}
}

func TestIngestPullRequestEvent_RedeliveryOfTheNewestIsStillIdempotent(t *testing.T) {
	t.Parallel()
	prRepo := &fakePRRepo{}
	pub := &fakePub{}
	uc := webhook.NewIngestUseCase(&fakeRepoRepo{repo: &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "acme/web"}},
		prRepo, newFakeChecks(), pub, nil, webhook.WithDeliveryOrdering(&fakeVersions{byPR: map[uint64]time.Time{}}))
	p := webhook.PullRequestEventPayload{Action: "opened", GitHubRepoID: 9001, GitHubPRID: 5005, Number: 88, Title: "t",
		AuthorLogin: "bia", State: entity.PRStateOpen, HeadSHA: "sha-1", UpdatedAt: time.Unix(1_700_000_000, 0)}
	_ = uc.IngestPullRequestEvent(context.Background(), p)
	_ = uc.IngestPullRequestEvent(context.Background(), p)
	if len(pub.events) != 1 {
		t.Fatalf("a redelivery publishes nothing, got %d", len(pub.events))
	}
}
