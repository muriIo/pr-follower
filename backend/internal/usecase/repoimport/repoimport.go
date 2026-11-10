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
	"github.com/murilo/pr-follower/backend/internal/domain/service"
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
	users   port.UserRepository
	events  port.EventPublisher
	nowFunc func() time.Time
}

func NewUseCase(tokens port.UserTokenSource, github port.GitHubUserAPI, repos port.RepositoryRepository, users port.UserRepository, events port.EventPublisher) *UseCase {
	return &UseCase{tokens: tokens, github: github, repos: repos, users: users, events: events, nowFunc: time.Now}
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
	codeownersStatus := entity.CodeownersUnknown
	if existing, err := uc.repos.GetByGitHubRepoID(ctx, githubRepoID); err == nil {
		cachedRules = existing.CachedCodeownersRules
		codeownersStatus = existing.CodeownersStatus
	} else if !errors.Is(err, entity.ErrNotFound) {
		return nil, err
	}
	if rules, status, ok := uc.loadCodeowners(ctx, token, *target); ok {
		cachedRules, codeownersStatus = rules, status
	}

	repo := target.ToRepository(cachedRules)
	repo.CodeownersStatus = codeownersStatus
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
		ActorLogin:     uc.actorLogin(ctx, userID),
		OccurredAt:     uc.nowFunc().UTC(),
	})
	return repo, nil
}

// codeownersPaths is where GitHub looks for CODEOWNERS, in its order of precedence.
var codeownersPaths = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

// loadCodeowners reads the repository's CODEOWNERS with the user's token. ok is false when
// GitHub could not answer, so the caller keeps what it knew instead of guessing "absent".
func (uc *UseCase) loadCodeowners(ctx context.Context, token string, repo entity.InstallationRepository) ([]entity.CodeownerRule, entity.CodeownersStatus, bool) {
	for _, path := range codeownersPaths {
		content, err := uc.github.GetRepositoryFile(ctx, token, repo.Owner, repo.Name, path)
		if err == nil {
			return service.ParseCodeowners(string(content)), entity.CodeownersPresent, true
		}
		if !errors.Is(err, entity.ErrNotFound) {
			return nil, entity.CodeownersUnknown, false
		}
	}
	return nil, entity.CodeownersAbsent, true
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

// actorLogin names who followed, for the "my activity" timeline. An unknown user leaves it empty.
func (uc *UseCase) actorLogin(ctx context.Context, userID uint64) string {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return ""
	}
	return user.Login
}
