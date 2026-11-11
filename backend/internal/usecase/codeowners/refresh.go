// Package codeowners keeps a repository's cached CODEOWNERS rules current. The rules feed every
// user's turn, so a stale copy hands reviews to the wrong people; this re-reads the file when a
// push to the default branch may have changed it.
package codeowners

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/domain/service"
)

// ErrNoReader means neither the GitHub App nor any follower could read the file right now, so the
// cached rules were kept. A push that cannot be refreshed fails visibly instead of leaving stale
// rules unnoticed (the delivery can be redelivered, and refreshing converges).
var ErrNoReader = errors.New("nobody can read the repository's CODEOWNERS")

// errTokenUnusable: this credential cannot read the repository; try the next one.
var errTokenUnusable = errors.New("token cannot read the repository")

type UseCase struct {
	repos     port.RepositoryRepository
	files     port.RepositoryFileReader
	tokens    port.UserTokenSource
	followers port.RepositoryFollowers
	app       port.InstallationTokenSource
	pub       port.EventPublisher
}

// NewUseCase wires the refresh. app (optional) is tried first, so the refresh does not depend on
// a follower being signed in; followers' tokens are the fallback.
func NewUseCase(
	repos port.RepositoryRepository,
	files port.RepositoryFileReader,
	tokens port.UserTokenSource,
	followers port.RepositoryFollowers,
	app port.InstallationTokenSource,
	pub port.EventPublisher,
) *UseCase {
	return &UseCase{repos: repos, files: files, tokens: tokens, followers: followers, app: app, pub: pub}
}

// RefreshCodeowners reads the CODEOWNERS now in force and stores it when it differs from the
// cached copy, announcing the change so open panels reload. Nothing changed means nothing is
// stored or announced (ADR-0011). A read that cannot tell (GitHub failing, no usable token)
// never becomes "no CODEOWNERS": the cache is kept and the error returned.
func (uc *UseCase) RefreshCodeowners(ctx context.Context, repo entity.Repository) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	rules, status, err := uc.read(ctx, repo)
	if err != nil {
		return err
	}
	if status == repo.CodeownersStatus && sameRules(rules, repo.CachedCodeownersRules) {
		return nil
	}

	repo.CachedCodeownersRules, repo.CodeownersStatus = rules, status
	if err := uc.repos.Upsert(ctx, &repo); err != nil {
		return err
	}
	return uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("codeowners-%d-%d", repo.ID, time.Now().UnixNano()),
		Type:           "repository.codeowners_updated",
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		Params:         map[string]string{"status": string(status), "rules": strconv.Itoa(len(rules))},
		OccurredAt:     time.Now().UTC(),
	})
}

// read tries the app's installation token, then each follower's token, until one can read.
func (uc *UseCase) read(ctx context.Context, repo entity.Repository) ([]entity.CodeownerRule, entity.CodeownersStatus, error) {
	try := func(token string) ([]entity.CodeownerRule, entity.CodeownersStatus, error) {
		return uc.load(ctx, token, repo)
	}

	if uc.app != nil && repo.InstallationID != 0 {
		if token, err := uc.app.InstallationToken(ctx, repo.InstallationID); err == nil {
			rules, status, err := try(token)
			if !errors.Is(err, errTokenUnusable) {
				return rules, status, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, "", context.Cause(ctx)
		}
	}

	followers, err := uc.followers.ListFollowerIDs(ctx, repo.ID)
	if err != nil {
		return nil, "", err
	}
	for _, userID := range followers {
		if err := ctx.Err(); err != nil {
			return nil, "", context.Cause(ctx)
		}
		token, err := uc.tokens.AccessToken(ctx, userID)
		if err != nil {
			continue // signed out, or the refresh token expired
		}
		rules, status, err := try(token)
		if !errors.Is(err, errTokenUnusable) {
			return rules, status, err
		}
	}
	return nil, "", fmt.Errorf("%w: %s", ErrNoReader, repo.FullName)
}

// load reads the file with one token. A file missing at every location is a repository without
// CODEOWNERS; a refused token is unusable; any other failure means "could not tell".
func (uc *UseCase) load(ctx context.Context, token string, repo entity.Repository) ([]entity.CodeownerRule, entity.CodeownersStatus, error) {
	for _, path := range service.CodeownersPaths {
		content, err := uc.files.GetRepositoryFile(ctx, token, repo.Owner, repo.Name, path)
		switch {
		case err == nil:
			return service.ParseCodeowners(string(content)), entity.CodeownersPresent, nil
		case errors.Is(err, entity.ErrNotFound):
			continue
		case errors.Is(err, entity.ErrUnauthorized), errors.Is(err, entity.ErrForbidden):
			return nil, "", errTokenUnusable
		default:
			return nil, "", err
		}
	}
	return nil, entity.CodeownersAbsent, nil
}

func sameRules(a, b []entity.CodeownerRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Pattern != b[i].Pattern || len(a[i].Owners) != len(b[i].Owners) {
			return false
		}
		for j := range a[i].Owners {
			if a[i].Owners[j] != b[i].Owners[j] {
				return false
			}
		}
	}
	return true
}
