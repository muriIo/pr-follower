package webhook_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

type fakeReviews struct {
	byID map[int64]entity.PullRequestReview
	err  error
}

func newFakeReviews() *fakeReviews { return &fakeReviews{byID: map[int64]entity.PullRequestReview{}} }

func (f *fakeReviews) UpsertReview(_ context.Context, r entity.PullRequestReview) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if stored, ok := f.byID[r.GitHubReviewID]; ok && (!r.Supersedes(stored) || stored.State == r.State) {
		return false, nil
	}
	f.byID[r.GitHubReviewID] = r
	return true, nil
}
func (f *fakeReviews) ListReviews(_ context.Context, _ []uint64) (map[uint64][]entity.PullRequestReview, error) {
	return nil, nil
}

func reviewPayload(id int64, state string) webhook.ReviewEventPayload {
	return webhook.ReviewEventPayload{
		Action: "submitted", GitHubRepoID: 9001, PRNumber: 88, ReviewID: id,
		ReviewerLogin: "bob", State: state, CommitSHA: "sha-88",
		SubmittedAt: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC),
	}
}

func newReviewIngest(t *testing.T) (*webhook.ReviewIngestUseCase, *fakeReviews, *fakePub) {
	t.Helper()
	reviews, pub := newFakeReviews(), &fakePub{}
	prs := &fakePRRepo{upserted: &entity.PullRequest{ID: 777, RepositoryID: 10, RepositoryName: "arvore/hive-web", Number: 88}}
	repos := &fakeRepoRepo{repo: &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "arvore/hive-web"}}
	return webhook.NewReviewIngestUseCase(repos, prs, reviews, pub), reviews, pub
}

func TestIngestReviewEvent_StoresTheReviewAndPublishesIt(t *testing.T) {
	t.Parallel()
	uc, reviews, pub := newReviewIngest(t)

	if err := uc.IngestReviewEvent(context.Background(), reviewPayload(5, "approved")); err != nil {
		t.Fatalf("IngestReviewEvent() error: %v", err)
	}
	got := reviews.byID[5]
	if got.PullRequestID != 777 || got.ReviewerLogin != "bob" || got.State != entity.ReviewApproved || got.CommitSHA != "sha-88" {
		t.Fatalf("unexpected stored review: %+v", got)
	}
	if len(pub.events) != 1 || pub.events[0].Type != "pr.approved" || pub.events[0].ActorLogin != "bob" || pub.events[0].PRNumber != 88 {
		t.Fatalf("unexpected events: %+v", pub.events)
	}
}

func TestIngestReviewEvent_EventTypePerState(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"approved":          "pr.approved",
		"changes_requested": "pr.changes_requested",
		"commented":         "pr.reviewed",
		"dismissed":         "pr.review_dismissed",
	}
	for state, want := range cases {
		uc, _, pub := newReviewIngest(t)
		if err := uc.IngestReviewEvent(context.Background(), reviewPayload(5, state)); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if len(pub.events) != 1 || pub.events[0].Type != want {
			t.Errorf("%s: expected %s, got %+v", state, want, pub.events)
		}
	}
}

func TestIngestReviewEvent_RedeliverChangesNothingAndPublishesNothing(t *testing.T) {
	t.Parallel()
	uc, reviews, pub := newReviewIngest(t)
	ctx := context.Background()

	for range 3 {
		if err := uc.IngestReviewEvent(ctx, reviewPayload(5, "approved")); err != nil {
			t.Fatal(err)
		}
	}
	if len(reviews.byID) != 1 || len(pub.events) != 1 {
		t.Fatalf("redelivery must converge: %d reviews, %d events", len(reviews.byID), len(pub.events))
	}

	dismissal := reviewPayload(5, "dismissed")
	dismissal.Action = "dismissed"
	_ = uc.IngestReviewEvent(ctx, dismissal)
	_ = uc.IngestReviewEvent(ctx, reviewPayload(5, "approved")) // late 'submitted' after the dismissal
	if reviews.byID[5].State != entity.ReviewDismissed || len(pub.events) != 2 {
		t.Fatalf("a dismissal is final: %+v, %d events", reviews.byID[5], len(pub.events))
	}
}

func TestIngestReviewEvent_IgnoresWhatItCannotUse(t *testing.T) {
	t.Parallel()
	uc, reviews, pub := newReviewIngest(t)
	ctx := context.Background()

	if err := uc.IngestReviewEvent(ctx, reviewPayload(5, "pending")); err != nil {
		t.Fatalf("a draft review is ignored, not an error: %v", err)
	}
	if len(reviews.byID) != 0 || len(pub.events) != 0 {
		t.Fatal("a draft review decides nothing")
	}

	unknownRepo := webhook.NewReviewIngestUseCase(&fakeRepoRepo{err: entity.ErrNotFound}, &fakePRRepo{}, reviews, pub)
	if err := unknownRepo.IngestReviewEvent(ctx, reviewPayload(5, "approved")); err != nil {
		t.Fatalf("a repository nobody follows is ignored: %v", err)
	}
	unknownPR := webhook.NewReviewIngestUseCase(&fakeRepoRepo{repo: &entity.Repository{ID: 10}}, &fakePRRepo{}, reviews, pub)
	if err := unknownPR.IngestReviewEvent(ctx, reviewPayload(5, "approved")); err != nil {
		t.Fatalf("a PR not stored yet is ignored: %v", err)
	}
	if len(reviews.byID) != 0 {
		t.Fatal("nothing may be stored for an unknown repository or PR")
	}
}

func TestIngestReviewEvent_PropagatesFailuresAndCancellation(t *testing.T) {
	t.Parallel()
	uc, reviews, _ := newReviewIngest(t)

	boom := errors.New("db down")
	reviews.err = boom
	if err := uc.IngestReviewEvent(context.Background(), reviewPayload(5, "approved")); !errors.Is(err, boom) {
		t.Fatalf("expected the store error, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := uc.IngestReviewEvent(ctx, reviewPayload(6, "approved")); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
