package action

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

type UseCase struct {
	prRepo    port.PullRequestRepository
	repoRepo  port.RepositoryRepository
	ghGateway port.GitHubGateway
	pub       port.EventPublisher
}

func NewUseCase(
	prRepo port.PullRequestRepository,
	repoRepo port.RepositoryRepository,
	ghGateway port.GitHubGateway,
	pub port.EventPublisher,
) *UseCase {
	return &UseCase{
		prRepo:    prRepo,
		repoRepo:  repoRepo,
		ghGateway: ghGateway,
		pub:       pub,
	}
}

func (uc *UseCase) ApprovePR(ctx context.Context, userID, prID uint64, body string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	pr, err := uc.prRepo.GetByID(ctx, prID)
	if err != nil {
		return err
	}
	repo, err := uc.repoRepo.GetByID(ctx, pr.RepositoryID)
	if err != nil {
		return err
	}

	if err := uc.ghGateway.ApprovePullRequest(ctx, *repo, pr.Number, body); err != nil {
		return err
	}

	st, err := uc.prRepo.GetUserPRState(ctx, prID, userID)
	if err != nil || st == nil {
		st = &entity.UserPRState{
			PullRequestID: prID,
			UserID:        userID,
		}
	}
	st.TurnState = entity.TurnStateApprovedByMe
	st.UpdatedAt = time.Now().UTC()

	if err := uc.prRepo.UpsertUserPRState(ctx, st); err != nil {
		return err
	}

	return uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("approve-%d-%d", prID, time.Now().UnixNano()),
		Type:           "pr.approved",
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		PullRequestID:  pr.ID,
		PRNumber:       pr.Number,
		Summary:        fmt.Sprintf("Approved PR #%d", pr.Number),
		OccurredAt:     time.Now().UTC(),
	})
}

func (uc *UseCase) MergePR(ctx context.Context, userID, prID uint64, method string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	pr, err := uc.prRepo.GetByID(ctx, prID)
	if err != nil {
		return err
	}
	repo, err := uc.repoRepo.GetByID(ctx, pr.RepositoryID)
	if err != nil {
		return err
	}

	if method == "" {
		method = "squash"
	}
	if err := uc.ghGateway.MergePullRequest(ctx, *repo, pr.Number, method); err != nil {
		return err
	}

	pr.State = entity.PRStateMerged
	pr.UpdatedAt = time.Now().UTC()
	if err := uc.prRepo.Upsert(ctx, pr); err != nil {
		return err
	}

	st, err := uc.prRepo.GetUserPRState(ctx, prID, userID)
	if err != nil || st == nil {
		st = &entity.UserPRState{PullRequestID: prID, UserID: userID}
	}
	st.TurnState = entity.TurnStateMergedOrClosed
	st.UpdatedAt = time.Now().UTC()
	_ = uc.prRepo.UpsertUserPRState(ctx, st)

	return uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("merge-%d-%d", prID, time.Now().UnixNano()),
		Type:           "pr.merged",
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		PullRequestID:  pr.ID,
		PRNumber:       pr.Number,
		Summary:        fmt.Sprintf("Merged PR #%d (%s)", pr.Number, method),
		OccurredAt:     time.Now().UTC(),
	})
}

func (uc *UseCase) BatchApproveAndMerge(ctx context.Context, userID uint64, prIDs []uint64, method string) ([]uint64, error) {
	completed := make([]uint64, 0, len(prIDs))
	for _, prID := range prIDs {
		if err := ctx.Err(); err != nil {
			return completed, context.Cause(ctx)
		}
		if err := uc.ApprovePR(ctx, userID, prID, "Approved via PR Follower"); err != nil {
			return completed, err
		}
		if err := uc.MergePR(ctx, userID, prID, method); err != nil {
			return completed, err
		}
		completed = append(completed, prID)
	}
	return completed, nil
}

func (uc *UseCase) RunDependabotCommand(ctx context.Context, _ uint64, prID uint64, command string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	cleanCmd := strings.TrimSpace(strings.TrimPrefix(command, "@dependabot"))
	if cleanCmd == "" {
		return fmt.Errorf("%w: empty dependabot command", entity.ErrInvalidInput)
	}

	pr, err := uc.prRepo.GetByID(ctx, prID)
	if err != nil {
		return err
	}
	repo, err := uc.repoRepo.GetByID(ctx, pr.RepositoryID)
	if err != nil {
		return err
	}

	body := "@dependabot " + cleanCmd
	if err := uc.ghGateway.CreatePRComment(ctx, *repo, pr.Number, body); err != nil {
		return err
	}

	return uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("depcmd-%d-%d", prID, time.Now().UnixNano()),
		Type:           "dependabot.command",
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		PullRequestID:  pr.ID,
		PRNumber:       pr.Number,
		Summary:        fmt.Sprintf("Triggered '%s' on PR #%d", body, pr.Number),
		OccurredAt:     time.Now().UTC(),
	})
}
