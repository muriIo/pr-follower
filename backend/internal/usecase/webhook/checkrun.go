package webhook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

// CheckRunEventPayload is the part of GitHub's `check_run` webhook PR Follower uses.
type CheckRunEventPayload struct {
	CheckRunID   int64
	GitHubRepoID int64
	HeadSHA      string
	Name         string
	Status       string // queued | in_progress | completed
	Conclusion   string
}

var ciEventTypes = map[entity.CIStatus]string{
	entity.CIStatusPending: "ci.running",
	entity.CIStatusSuccess: "ci.passed",
	entity.CIStatusFailure: "ci.failed",
}

// IngestCheckRunEvent records a CI check run and re-derives the CI of every open PR whose head
// is that commit. A repository without CI never sends one, so its PRs stay NONE (ADR-0011:
// idempotent, never moves a run backwards, publishes only when a PR's CI changes).
func (uc *IngestUseCase) IngestCheckRunEvent(ctx context.Context, p CheckRunEventPayload) error {
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

	incoming := entity.CheckRun{ID: p.CheckRunID, RepositoryID: repo.ID, HeadSHA: p.HeadSHA, Name: p.Name, Status: p.Status, Conclusion: p.Conclusion}
	stored, err := uc.checks.GetCheckRun(ctx, p.CheckRunID)
	switch {
	case err == nil:
		if *stored == incoming || !incoming.Supersedes(*stored) {
			return nil
		}
	case !errors.Is(err, entity.ErrNotFound):
		return err
	}
	if err := uc.checks.UpsertCheckRun(ctx, incoming); err != nil {
		return err
	}

	runs, err := uc.checks.ListCheckRuns(ctx, repo.ID, p.HeadSHA)
	if err != nil {
		return err
	}
	ci := entity.DeriveCIStatus(runs)
	prs, err := uc.checks.ListOpenByHeadSHA(ctx, repo.ID, p.HeadSHA)
	if err != nil {
		return err
	}
	for i := range prs {
		pr := &prs[i]
		if pr.CIStatus == ci {
			continue
		}
		pr.CIStatus = ci
		pr.UpdatedAt = time.Now().UTC()
		if err := uc.prRepo.Upsert(ctx, pr); err != nil {
			return err
		}
		eventType, ok := ciEventTypes[ci]
		if !ok {
			continue
		}
		if err := uc.pub.Publish(ctx, entity.ActivityEvent{
			ID:             fmt.Sprintf("ci-%d-%s-%d", pr.ID, ci, time.Now().UnixNano()),
			Type:           eventType,
			RepositoryID:   repo.ID,
			RepositoryName: repo.FullName,
			PullRequestID:  pr.ID,
			PRNumber:       pr.Number,
			Summary:        fmt.Sprintf("%s #%d: CI %s", repo.FullName, pr.Number, ciSummary[ci]),
			OccurredAt:     time.Now().UTC(),
		}); err != nil {
			return err
		}
	}
	return nil
}

var ciSummary = map[entity.CIStatus]string{
	entity.CIStatusPending: "running",
	entity.CIStatusSuccess: "passed",
	entity.CIStatusFailure: "failed",
}
