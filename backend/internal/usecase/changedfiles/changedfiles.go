// Package changedfiles keeps, for each pull request, the files it changes at its head commit.
// That list is the one fact CODEOWNERS ownership needs from GitHub; it is the same for every
// user, so it is read once per commit and the turn is derived from it when the workspace is read.
package changedfiles

import (
	"context"
	"errors"
	"fmt"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// ErrNoReader means no follower of the repository has a token GitHub accepts right now. The
// files stay unknown until a signed-in follower loads the workspace.
var ErrNoReader = errors.New("no follower can read the pull request files")

type UseCase struct {
	tokens    port.UserTokenSource
	github    port.PullRequestFilesReader
	store     port.ChangedFilesStore
	followers port.RepositoryFollowers
}

func NewUseCase(tokens port.UserTokenSource, github port.PullRequestFilesReader, store port.ChangedFilesStore, followers port.RepositoryFollowers) *UseCase {
	return &UseCase{tokens: tokens, github: github, store: store, followers: followers}
}

// SyncWithToken stores the files the PR changes at its head commit, read with the given user
// token. It converges (ADR-0011): a commit already stored is neither read nor written again.
func (uc *UseCase) SyncWithToken(ctx context.Context, accessToken string, repo entity.Repository, pr entity.PullRequest) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	current, err := uc.isCurrent(ctx, pr)
	if err != nil || current {
		return err
	}
	files, err := uc.github.ListPullRequestFiles(ctx, accessToken, repo.Owner, repo.Name, pr.Number)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return uc.store.SaveChangedFiles(ctx, entity.PRChangedFiles{PullRequestID: pr.ID, HeadSHA: pr.HeadSHA, Files: files})
}

// SyncAsUser is SyncWithToken with that user's own token.
func (uc *UseCase) SyncAsUser(ctx context.Context, userID uint64, repo entity.Repository, pr entity.PullRequest) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	current, err := uc.isCurrent(ctx, pr)
	if err != nil || current {
		return err
	}
	token, err := uc.tokens.AccessToken(ctx, userID)
	if err != nil {
		return err
	}
	return uc.SyncWithToken(ctx, token, repo, pr)
}

// SyncAsFollower reads the files with the token of a user who follows the repository, for work
// that has no user behind it (webhooks). A follower whose token is missing or rejected is
// skipped; any other failure stops the attempt. ErrNoReader when nobody could read.
func (uc *UseCase) SyncAsFollower(ctx context.Context, repo entity.Repository, pr entity.PullRequest) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	current, err := uc.isCurrent(ctx, pr)
	if err != nil || current {
		return err
	}
	followers, err := uc.followers.ListFollowerIDs(ctx, repo.ID)
	if err != nil {
		return err
	}
	for _, userID := range followers {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		token, err := uc.tokens.AccessToken(ctx, userID)
		if err != nil {
			continue // signed out, or the refresh token expired
		}
		err = uc.SyncWithToken(ctx, token, repo, pr)
		if err == nil {
			return nil
		}
		if !errors.Is(err, entity.ErrUnauthorized) {
			return err
		}
	}
	return fmt.Errorf("%w: %s#%d", ErrNoReader, repo.FullName, pr.Number)
}

// isCurrent reports whether the PR's files are already stored for its head commit.
func (uc *UseCase) isCurrent(ctx context.Context, pr entity.PullRequest) (bool, error) {
	if pr.ID == 0 || pr.HeadSHA == "" {
		return false, fmt.Errorf("%w: a stored pull request with a head commit is required", entity.ErrInvalidInput)
	}
	stored, err := uc.store.ListChangedFiles(ctx, []uint64{pr.ID})
	if err != nil {
		return false, err
	}
	return stored[pr.ID].IsCurrentFor(pr), nil
}
