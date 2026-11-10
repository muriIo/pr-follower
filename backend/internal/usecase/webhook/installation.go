package webhook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// InstallationEventPayload is the part of GitHub's `installation` webhook PR Follower uses.
type InstallationEventPayload struct {
	Action         string // created | deleted | suspend | unsuspend | new_permissions_accepted
	InstallationID int64
	AccountLogin   string
	RepositoryIDs  []int64 // repositories granted on "created"
}

// InstallationRepositoriesEventPayload is the part of GitHub's `installation_repositories` webhook used.
type InstallationRepositoriesEventPayload struct {
	Action         string // added | removed
	InstallationID int64
	AddedIDs       []int64
	RemovedIDs     []int64
}

const (
	eventAccessLost      = "repository.access_lost"
	eventAccessSuspended = "repository.access_suspended"
	eventAccessRestored  = "repository.access_restored"
)

// InstallationUseCase keeps followed repositories' access in sync with GitHub App
// installations (ADR-0010), so Seats can flag a repository the app no longer reaches.
// Redelivered webhooks are idempotent: events go out only when access actually changes.
type InstallationUseCase struct {
	repos port.RepositoryAccessStore
	pub   port.EventPublisher
	now   func() time.Time
}

func NewInstallationUseCase(repos port.RepositoryAccessStore, pub port.EventPublisher) *InstallationUseCase {
	return &InstallationUseCase{repos: repos, pub: pub, now: time.Now}
}

func (uc *InstallationUseCase) IngestInstallationEvent(ctx context.Context, p InstallationEventPayload) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	switch p.Action {
	case "deleted":
		return uc.setInstallationAccess(ctx, p.InstallationID, entity.RepositoryAccessRemoved)
	case "suspend":
		return uc.setInstallationAccess(ctx, p.InstallationID, entity.RepositoryAccessSuspended)
	case "unsuspend":
		return uc.setInstallationAccess(ctx, p.InstallationID, entity.RepositoryAccessActive)
	case "created":
		return uc.setRepositoriesAccess(ctx, p.RepositoryIDs, entity.RepositoryAccessActive, p.InstallationID, false)
	default:
		return nil
	}
}

func (uc *InstallationUseCase) IngestInstallationRepositoriesEvent(ctx context.Context, p InstallationRepositoriesEventPayload) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	switch p.Action {
	case "removed":
		return uc.setRepositoriesAccess(ctx, p.RemovedIDs, entity.RepositoryAccessRemoved, p.InstallationID, true)
	case "added":
		return uc.setRepositoriesAccess(ctx, p.AddedIDs, entity.RepositoryAccessActive, p.InstallationID, false)
	default:
		return nil
	}
}

func (uc *InstallationUseCase) setInstallationAccess(ctx context.Context, installationID int64, access entity.RepositoryAccess) error {
	repos, err := uc.repos.ListByInstallation(ctx, installationID)
	if err != nil {
		return err
	}
	for i := range repos {
		if err := uc.apply(ctx, &repos[i], access, 0); err != nil {
			return err
		}
	}
	return nil
}

// setRepositoriesAccess updates repositories PR Follower knows about; unknown ids are ignored.
// With sameInstallationOnly, a repository attached to another installation is left alone.
func (uc *InstallationUseCase) setRepositoriesAccess(ctx context.Context, githubRepoIDs []int64, access entity.RepositoryAccess, installationID int64, sameInstallationOnly bool) error {
	for _, id := range githubRepoIDs {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		repo, err := uc.repos.GetByGitHubRepoID(ctx, id)
		if errors.Is(err, entity.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if sameInstallationOnly && repo.InstallationID != installationID {
			continue
		}
		target := int64(0)
		if access == entity.RepositoryAccessActive {
			target = installationID
		}
		if err := uc.apply(ctx, repo, access, target); err != nil {
			return err
		}
	}
	return nil
}

func (uc *InstallationUseCase) apply(ctx context.Context, repo *entity.Repository, access entity.RepositoryAccess, installationID int64) error {
	hadAccess := repo.HasAccess()
	if !repo.ApplyAccess(access, installationID) {
		return nil
	}
	if err := uc.repos.SaveAccess(ctx, repo); err != nil {
		return err
	}

	var eventType, summary string
	switch {
	case access == entity.RepositoryAccessRemoved && hadAccess:
		eventType, summary = eventAccessLost, "Lost access to "+repo.FullName+": the GitHub App was uninstalled or the repository was removed from it"
	case access == entity.RepositoryAccessSuspended && hadAccess:
		eventType, summary = eventAccessSuspended, "Access to "+repo.FullName+" is suspended: the GitHub App installation was suspended"
	case access == entity.RepositoryAccessActive && !hadAccess:
		eventType, summary = eventAccessRestored, "Access to "+repo.FullName+" restored"
	default:
		return nil // installation moved without an access change
	}
	return uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("access-%d-%d", repo.ID, uc.now().UnixNano()),
		Type:           eventType,
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		Summary:        summary,
		OccurredAt:     uc.now().UTC(),
	})
}
