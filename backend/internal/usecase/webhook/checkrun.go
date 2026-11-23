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
	// Title is the check's short result, DetailsURL where it reports in full.
	Title      string
	DetailsURL string
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

	incoming := entity.CheckRun{ID: p.CheckRunID, RepositoryID: repo.ID, HeadSHA: p.HeadSHA, Name: p.Name, Status: p.Status, Conclusion: p.Conclusion, Title: p.Title, DetailsURL: p.DetailsURL}
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

	return uc.syncCI(ctx, repo, p.HeadSHA)
}

// syncCI re-derives the CI of every open PR whose head is the commit, from all its checks (check
// runs and commit statuses), and announces the PRs whose CI changed. It converges (ADR-0011).
func (uc *IngestUseCase) syncCI(ctx context.Context, repo *entity.Repository, sha string) error {
	runs, err := uc.checks.ListCheckRuns(ctx, repo.ID, sha)
	if err != nil {
		return err
	}
	ci := entity.DeriveCIStatus(runs)
	prs, err := uc.checks.ListOpenByHeadSHA(ctx, repo.ID, sha)
	if err != nil {
		return err
	}
	for i := range prs {
		pr := &prs[i]
		if pr.CIStatus == ci {
			continue
		}
		pr.CIStatus = ci
		// Required checks are part of branch protection: GitHub recomputes the merge state.
		pr.MergeableState = pr.MergeableState.AfterReviewOrCheckChange()
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
			OccurredAt:     time.Now().UTC(),
		}); err != nil {
			return err
		}
	}
	return nil
}

// StatusEventPayload is the part of GitHub's `status` webhook (a legacy commit status) PR Follower uses.
type StatusEventPayload struct {
	StatusID     int64
	GitHubRepoID int64
	SHA          string
	Context      string
	State        string // pending | success | failure | error
	Description  string
	TargetURL    string
}

// IngestStatusEvent records a commit status and re-derives the CI of the open PRs at that commit.
// Statuses are how Jenkins, Vercel and other CI report; GitHub lists them beside check runs, so a
// pending one keeps a PR from looking green. A redelivered status changes nothing (ADR-0011).
func (uc *IngestUseCase) IngestStatusEvent(ctx context.Context, p StatusEventPayload) error {
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

	incoming := entity.CommitStatus{
		ID: p.StatusID, RepositoryID: repo.ID, HeadSHA: p.SHA, Context: p.Context,
		State: p.State, Description: p.Description, TargetURL: p.TargetURL,
	}
	stored, err := uc.checks.GetCommitStatus(ctx, p.StatusID)
	switch {
	case err == nil:
		if *stored == incoming {
			return nil
		}
	case !errors.Is(err, entity.ErrNotFound):
		return err
	}
	if err := uc.checks.UpsertCommitStatus(ctx, incoming); err != nil {
		return err
	}
	return uc.syncCI(ctx, repo, p.SHA)
}
