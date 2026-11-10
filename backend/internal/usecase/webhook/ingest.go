package webhook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

type PullRequestEventPayload struct {
	Action       string
	GitHubRepoID int64
	GitHubPRID   int64
	Number       int
	Title        string
	AuthorLogin  string
	HTMLURL      string
	IsDraft      bool
	State        entity.PRState
	HeadSHA      string
	BaseRef      string
	// MergeableState is GitHub's mergeable_state from the payload (clean, dirty, blocked, ...).
	MergeableState string
	// Merged is pull_request.merged: GitHub reports a merge as action "closed" with merged=true.
	Merged bool
	// SenderLogin is who triggered the event (who merged, pushed, edited); the PR author may differ.
	SenderLogin string
}

type IngestUseCase struct {
	repoRepo port.RepositoryRepository
	prRepo   port.PullRequestRepository
	checks   port.CheckRunStore
	pub      port.EventPublisher
}

func NewIngestUseCase(
	repoRepo port.RepositoryRepository,
	prRepo port.PullRequestRepository,
	checks port.CheckRunStore,
	pub port.EventPublisher,
) *IngestUseCase {
	return &IngestUseCase{
		repoRepo: repoRepo,
		prRepo:   prRepo,
		checks:   checks,
		pub:      pub,
	}
}

func (uc *IngestUseCase) IngestPullRequestEvent(ctx context.Context, p PullRequestEventPayload) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}

	repo, err := uc.repoRepo.GetByGitHubRepoID(ctx, p.GitHubRepoID)
	if err != nil {
		return err
	}

	isDependabot := entity.IsDependabotAuthor(p.AuthorLogin)

	pr := &entity.PullRequest{
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		GitHubPRID:     p.GitHubPRID,
		Number:         p.Number,
		Title:          p.Title,
		AuthorLogin:    p.AuthorLogin,
		HTMLURL:        p.HTMLURL,
		IsDependabot:   isDependabot,
		IsDraft:        p.IsDraft,
		State:          stateOf(p),
		MergeableState: entity.MergeableStateFromGitHub(p.MergeableState),
		CIStatus:       entity.CIStatusNone,
		HeadSHA:        p.HeadSHA,
		BaseRef:        p.BaseRef,
		UpdatedAt:      time.Now().UTC(),
	}

	// ADR-0011: a redelivered webhook converges to the same state and publishes nothing.
	existing, err := uc.prRepo.GetByRepoAndNumber(ctx, repo.ID, p.Number)
	if err != nil && !errors.Is(err, entity.ErrNotFound) {
		return err
	}
	sameCommit := err == nil && existing.HeadSHA == pr.HeadSHA
	if err == nil {
		pr.CIStatus = existing.CIStatus
		if sameWebhookState(existing, pr) {
			return nil
		}
	}
	if !sameCommit {
		// New PR or new commit: CI comes from the check runs already reported for it, if any.
		runs, runErr := uc.checks.ListCheckRuns(ctx, repo.ID, pr.HeadSHA)
		if runErr != nil {
			return runErr
		}
		pr.CIStatus = entity.DeriveCIStatus(runs)
	}

	if err := uc.prRepo.Upsert(ctx, pr); err != nil {
		return err
	}

	return uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("wh-pr-%d-%d", p.Number, time.Now().UnixNano()),
		Type:           "pr." + actionOf(p),
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		PullRequestID:  pr.ID,
		PRNumber:       pr.Number,
		ActorLogin:     actorOf(p),
		Params:         map[string]string{"title": pr.Title},
		OccurredAt:     time.Now().UTC(),
	})
}

// sameWebhookState reports whether a pull_request webhook carries nothing new for a stored PR.
func sameWebhookState(stored, incoming *entity.PullRequest) bool {
	return stored.GitHubPRID == incoming.GitHubPRID &&
		stored.Title == incoming.Title &&
		stored.State == incoming.State &&
		stored.IsDraft == incoming.IsDraft &&
		stored.HeadSHA == incoming.HeadSHA &&
		stored.BaseRef == incoming.BaseRef &&
		stored.AuthorLogin == incoming.AuthorLogin &&
		stored.HTMLURL == incoming.HTMLURL &&
		stored.MergeableState == incoming.MergeableState
}

func actorOf(p PullRequestEventPayload) string {
	if p.SenderLogin != "" {
		return p.SenderLogin
	}
	return p.AuthorLogin
}

func stateOf(p PullRequestEventPayload) entity.PRState {
	if p.Merged {
		return entity.PRStateMerged
	}
	return p.State
}

func actionOf(p PullRequestEventPayload) string {
	if p.Action == "closed" && p.Merged {
		return "merged"
	}
	return p.Action
}
