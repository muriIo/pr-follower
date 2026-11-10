// Package repoimport lets a signed-in user follow repositories reachable through the GitHub App,
// the Vercel-style import flow of ADR-0010. GitHub is asked, with the user's own token, which
// installations and repositories they can reach; nothing the browser sends is trusted.
package repoimport

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// ImportableRepository is a repository the user can import, with their follow state.
type ImportableRepository struct {
	entity.InstallationRepository
	Followed     bool   `json:"followed"`
	RepositoryID uint64 `json:"repository_id,omitempty"`
}

type UseCase struct {
	tokens  port.UserTokenSource
	github  port.GitHubUserAPI
	repos   port.RepositoryRepository
	events  port.EventPublisher
	nowFunc func() time.Time
}

func NewUseCase(tokens port.UserTokenSource, github port.GitHubUserAPI, repos port.RepositoryRepository, events port.EventPublisher) *UseCase {
	return &UseCase{tokens: tokens, github: github, repos: repos, events: events, nowFunc: time.Now}
}

// ListAccounts returns the installations (user and organization accounts) the user can reach.
func (uc *UseCase) ListAccounts(ctx context.Context, userID uint64) ([]entity.Installation, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	token, err := uc.tokens.AccessToken(ctx, userID)
	if err != nil {
		return nil, err
	}
	return uc.github.ListInstallations(ctx, token)
}

// ListRepositories returns the repositories of one installation that the user can reach,
// flagged with whether the user already follows them.
func (uc *UseCase) ListRepositories(ctx context.Context, userID uint64, installationID int64) ([]ImportableRepository, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	token, err := uc.tokens.AccessToken(ctx, userID)
	if err != nil {
		return nil, err
	}
	available, err := uc.github.ListInstallationRepositories(ctx, token, installationID)
	if err != nil {
		return nil, err
	}
	followed, err := uc.repos.ListFollowedByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	followedByGitHubID := make(map[int64]uint64, len(followed))
	for _, r := range followed {
		followedByGitHubID[r.GitHubRepoID] = r.ID
	}

	out := make([]ImportableRepository, 0, len(available))
	for _, r := range available {
		repoID, ok := followedByGitHubID[r.GitHubRepoID]
		out = append(out, ImportableRepository{InstallationRepository: r, Followed: ok, RepositoryID: repoID})
	}
	return out, nil
}

// Import follows a repository after confirming, through GitHub, that the user reaches it via
// the given installation. Unknown installations or repositories yield entity.ErrNotFound.
func (uc *UseCase) Import(ctx context.Context, userID uint64, installationID, githubRepoID int64) (*entity.Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if installationID <= 0 || githubRepoID <= 0 {
		return nil, entity.ErrInvalidInput
	}
	token, err := uc.tokens.AccessToken(ctx, userID)
	if err != nil {
		return nil, err
	}
	available, err := uc.github.ListInstallationRepositories(ctx, token, installationID)
	if err != nil {
		return nil, err
	}

	var target *entity.InstallationRepository
	for i := range available {
		if available[i].GitHubRepoID == githubRepoID {
			target = &available[i]
			break
		}
	}
	if target == nil {
		return nil, entity.ErrNotFound
	}

	var cachedRules []entity.CodeownerRule
	if existing, err := uc.repos.GetByGitHubRepoID(ctx, githubRepoID); err == nil {
		cachedRules = existing.CachedCodeownersRules
	} else if !errors.Is(err, entity.ErrNotFound) {
		return nil, err
	}

	repo := target.ToRepository(cachedRules)
	if err := uc.repos.Upsert(ctx, repo); err != nil {
		return nil, err
	}
	if err := uc.repos.SetUserFollow(ctx, userID, repo.ID, true); err != nil {
		return nil, err
	}
	_ = uc.events.Publish(ctx, entity.ActivityEvent{
		ID:             "follow-" + strconv.FormatUint(repo.ID, 10),
		Type:           "repository.followed",
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		Summary:        "Followed repository " + repo.FullName,
		OccurredAt:     uc.nowFunc().UTC(),
	})
	return repo, nil
}

// Unfollow stops the user from following a repository. The repository row stays, since other
// users may follow it.
func (uc *UseCase) Unfollow(ctx context.Context, userID, repositoryID uint64) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if repositoryID == 0 {
		return entity.ErrInvalidInput
	}
	return uc.repos.SetUserFollow(ctx, userID, repositoryID, false)
}
