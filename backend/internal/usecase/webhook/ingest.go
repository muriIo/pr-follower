package webhook

import (
	"context"
	"fmt"
	"strings"
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
}

type IngestUseCase struct {
	repoRepo port.RepositoryRepository
	prRepo   port.PullRequestRepository
	pub      port.EventPublisher
}

func NewIngestUseCase(
	repoRepo port.RepositoryRepository,
	prRepo port.PullRequestRepository,
	pub port.EventPublisher,
) *IngestUseCase {
	return &IngestUseCase{
		repoRepo: repoRepo,
		prRepo:   prRepo,
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

	isDependabot := strings.Contains(strings.ToLower(p.AuthorLogin), "dependabot")

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
		State:          p.State,
		MergeableState: entity.MergeableStateClean,
		CIStatus:       entity.CIStatusPending,
		HeadSHA:        p.HeadSHA,
		BaseRef:        p.BaseRef,
		UpdatedAt:      time.Now().UTC(),
	}

	if err := uc.prRepo.Upsert(ctx, pr); err != nil {
		return err
	}

	return uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("wh-pr-%d-%d", p.Number, time.Now().UnixNano()),
		Type:           "pr." + p.Action,
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		PullRequestID:  pr.ID,
		PRNumber:       pr.Number,
		ActorLogin:     p.AuthorLogin,
		Summary:        fmt.Sprintf("%s #%d (%s): %s", repo.FullName, pr.Number, p.Action, pr.Title),
		OccurredAt:     time.Now().UTC(),
	})
}
