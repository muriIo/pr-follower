package webhook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// ReviewEventPayload is the part of GitHub's `pull_request_review` webhook PR Follower uses.
type ReviewEventPayload struct {
	Action        string // submitted | edited | dismissed
	GitHubRepoID  int64
	PRNumber      int
	ReviewID      int64
	ReviewerLogin string
	State         string // approved | changes_requested | commented | dismissed
	CommitSHA     string
	SubmittedAt   time.Time
}

var reviewEventTypes = map[entity.ReviewState]string{
	entity.ReviewApproved:         "pr.approved",
	entity.ReviewChangesRequested: "pr.changes_requested",
	entity.ReviewCommented:        "pr.reviewed",
	entity.ReviewDismissed:        "pr.review_dismissed",
}

// ReviewIngestUseCase records reviews so the turn engine can tell who approved what.
type ReviewIngestUseCase struct {
	repoRepo port.RepositoryRepository
	prRepo   port.PullRequestRepository
	reviews  port.ReviewStore
	pub      port.EventPublisher
}

func NewReviewIngestUseCase(
	repoRepo port.RepositoryRepository,
	prRepo port.PullRequestRepository,
	reviews port.ReviewStore,
	pub port.EventPublisher,
) *ReviewIngestUseCase {
	return &ReviewIngestUseCase{repoRepo: repoRepo, prRepo: prRepo, reviews: reviews, pub: pub}
}

// IngestReviewEvent stores a review by GitHub's review id (ADR-0011): a redelivery changes
// nothing and publishes nothing, and a dismissal is never undone by a late "submitted". A review
// of a repository nobody follows, of a PR not stored yet, or still a draft is ignored.
func (uc *ReviewIngestUseCase) IngestReviewEvent(ctx context.Context, p ReviewEventPayload) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	state := entity.ReviewStateFromGitHub(p.State)
	if state == entity.ReviewUnknown {
		return nil
	}
	repo, err := uc.repoRepo.GetByGitHubRepoID(ctx, p.GitHubRepoID)
	if errors.Is(err, entity.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	pr, err := uc.prRepo.GetByRepoAndNumber(ctx, repo.ID, p.PRNumber)
	if errors.Is(err, entity.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	changed, err := uc.reviews.UpsertReview(ctx, entity.PullRequestReview{
		GitHubReviewID: p.ReviewID,
		PullRequestID:  pr.ID,
		ReviewerLogin:  p.ReviewerLogin,
		State:          state,
		CommitSHA:      p.CommitSHA,
		SubmittedAt:    p.SubmittedAt,
	})
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if next := pr.MergeableState.AfterReviewOrCheckChange(); next != pr.MergeableState {
		updated := *pr
		updated.MergeableState = next
		if err := uc.prRepo.Upsert(ctx, &updated); err != nil {
			return err
		}
	}
	return uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("wh-review-%d-%d", p.ReviewID, time.Now().UnixNano()),
		Type:           reviewEventTypes[state],
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		PullRequestID:  pr.ID,
		PRNumber:       pr.Number,
		ActorLogin:     p.ReviewerLogin,
		OccurredAt:     time.Now().UTC(),
	})
}
