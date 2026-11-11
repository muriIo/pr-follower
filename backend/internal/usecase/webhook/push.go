package webhook

import (
	"context"
	"errors"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/domain/service"
)

// PushEventPayload is the part of GitHub's `push` webhook PR Follower uses.
type PushEventPayload struct {
	GitHubRepoID int64
	// Ref is the pushed ref, e.g. refs/heads/main.
	Ref           string
	DefaultBranch string
	// Deleted is a branch or tag deletion.
	Deleted bool
	// ChangedPaths are the files the push's commits added, modified or removed.
	ChangedPaths []string
	// Truncated means GitHub listed fewer commits than the push holds (it lists at most 20), so
	// ChangedPaths may be incomplete.
	Truncated bool
}

// PushIngestUseCase keeps the cached CODEOWNERS current: a push to the default branch that may
// have changed it makes the rules be read again.
type PushIngestUseCase struct {
	repoRepo  port.RepositoryRepository
	refresher port.CodeownersRefresher
}

func NewPushIngestUseCase(repoRepo port.RepositoryRepository, refresher port.CodeownersRefresher) *PushIngestUseCase {
	return &PushIngestUseCase{repoRepo: repoRepo, refresher: refresher}
}

// IngestPushEvent refreshes the repository's CODEOWNERS when the push is to its default branch and
// touches a CODEOWNERS path (or may, when GitHub truncated the commit list). Anything else is
// ignored. A refresh that fails fails the delivery, so it shows as failed on GitHub and can be
// redelivered: refreshing converges (ADR-0011).
func (uc *PushIngestUseCase) IngestPushEvent(ctx context.Context, p PushEventPayload) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	repo, err := uc.repoRepo.GetByGitHubRepoID(ctx, p.GitHubRepoID)
	if errors.Is(err, entity.ErrNotFound) {
		return nil // not a repository anyone follows
	}
	if err != nil {
		return err
	}

	defaultBranch := p.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = repo.DefaultBranch
	}
	if p.Deleted || defaultBranch == "" || p.Ref != "refs/heads/"+defaultBranch {
		return nil
	}
	if !p.Truncated && !touchesCodeowners(p.ChangedPaths) {
		return nil
	}
	return uc.refresher.RefreshCodeowners(ctx, *repo)
}

func touchesCodeowners(paths []string) bool {
	for _, path := range paths {
		if service.IsCodeownersPath(path) {
			return true
		}
	}
	return false
}
