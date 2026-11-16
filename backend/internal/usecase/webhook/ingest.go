package webhook

import (
	"context"
	"errors"
	"fmt"
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
	// MergeableState is GitHub's mergeable_state from the payload (clean, dirty, blocked, ...).
	MergeableState string
	// Merged is pull_request.merged: GitHub reports a merge as action "closed" with merged=true.
	Merged bool
	// SenderLogin is who triggered the event (who merged, pushed, edited); the PR author may differ.
	SenderLogin string
	// UpdatedAt is pull_request.updated_at: GitHub's version of the PR this delivery describes (zero
	// when the payload has none, which skips the ordering check).
	UpdatedAt time.Time
}

type IngestUseCase struct {
	repoRepo port.RepositoryRepository
	prRepo   port.PullRequestRepository
	checks   port.CheckRunStore
	pub      port.EventPublisher
	files    port.ChangedFilesSyncer
	analysis port.AnalysisScheduler
	versions port.PullRequestVersionStore
}

// IngestOption adds an optional collaborator to IngestUseCase.
type IngestOption func(*IngestUseCase)

// WithAnalysisScheduler has every change to an open Dependabot PR schedule its analysis.
func WithAnalysisScheduler(s port.AnalysisScheduler) IngestOption {
	return func(uc *IngestUseCase) { uc.analysis = s }
}

// WithDeliveryOrdering drops a pull_request delivery older than the newest one applied to the PR
// (ADR-0011: GitHub may deliver webhooks out of order).
func WithDeliveryOrdering(versions port.PullRequestVersionStore) IngestOption {
	return func(uc *IngestUseCase) { uc.versions = versions }
}

// NewIngestUseCase wires webhook ingestion. files (optional) reads a PR's changed files with a
// follower's token, so each follower's turn can be derived as soon as they reload.
func NewIngestUseCase(
	repoRepo port.RepositoryRepository,
	prRepo port.PullRequestRepository,
	checks port.CheckRunStore,
	pub port.EventPublisher,
	files port.ChangedFilesSyncer,
	opts ...IngestOption,
) *IngestUseCase {
	uc := &IngestUseCase{repoRepo: repoRepo, prRepo: prRepo, checks: checks, pub: pub, files: files}
	for _, opt := range opts {
		opt(uc)
	}
	return uc
}

func (uc *IngestUseCase) IngestPullRequestEvent(ctx context.Context, p PullRequestEventPayload) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}

	repo, err := uc.repoRepo.GetByGitHubRepoID(ctx, p.GitHubRepoID)
	if err != nil {
		return err
	}

	isDependabot := entity.IsDependabotAuthor(p.AuthorLogin)

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
		State:          stateOf(p),
		MergeableState: entity.MergeableStateFromGitHub(p.MergeableState),
		CIStatus:       entity.CIStatusNone,
		HeadSHA:        p.HeadSHA,
		BaseRef:        p.BaseRef,
		UpdatedAt:      time.Now().UTC(),
	}

	// ADR-0011: a redelivered webhook converges to the same state and publishes nothing.
	existing, err := uc.prRepo.GetByRepoAndNumber(ctx, repo.ID, p.Number)
	if err != nil && !errors.Is(err, entity.ErrNotFound) {
		return err
	}
	found := err == nil
	if found {
		applied, versionErr := uc.advanceVersion(ctx, existing.ID, p.UpdatedAt)
		if versionErr != nil {
			return versionErr
		}
		if !applied {
			return nil // an older delivery that arrived late: what is stored is newer
		}
	}
	sameCommit := err == nil && existing.HeadSHA == pr.HeadSHA
	if err == nil {
		pr.CIStatus = existing.CIStatus
		if sameWebhookState(existing, pr) {
			return nil
		}
	}
	if !sameCommit {
		// New PR or new commit: CI comes from the check runs already reported for it, if any.
		runs, runErr := uc.checks.ListCheckRuns(ctx, repo.ID, pr.HeadSHA)
		if runErr != nil {
			return runErr
		}
		pr.CIStatus = entity.DeriveCIStatus(runs)
	}

	if err := uc.prRepo.Upsert(ctx, pr); err != nil {
		return err
	}
	if !found {
		if _, err := uc.advanceVersion(ctx, pr.ID, p.UpdatedAt); err != nil {
			return err
		}
	}
	if err := uc.syncChangedFiles(ctx, repo, pr); err != nil {
		return err
	}
	if uc.analysis != nil && pr.IsDependabot && pr.State == entity.PRStateOpen {
		uc.analysis.ScheduleAnalysis(ctx, pr.ID) // in the background: the webhook is answered at once
	}

	return uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("wh-pr-%d-%d", p.Number, time.Now().UnixNano()),
		Type:           "pr." + actionOf(p),
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		PullRequestID:  pr.ID,
		PRNumber:       pr.Number,
		ActorLogin:     actorOf(p),
		Params:         map[string]string{"title": pr.Title},
		OccurredAt:     time.Now().UTC(),
	})
}

// advanceVersion records the delivery's GitHub version for the PR and reports whether it is not older
// than what was applied before. Without a store, or a payload without a version, everything applies.
func (uc *IngestUseCase) advanceVersion(ctx context.Context, prID uint64, updatedAt time.Time) (bool, error) {
	if uc.versions == nil || updatedAt.IsZero() {
		return true, nil
	}
	return uc.versions.AdvancePullRequestVersion(ctx, prID, updatedAt)
}

// syncChangedFiles reads the files of an open, human PR when the repository has CODEOWNERS:
// only then can they give someone a turn. A failed read never loses the webhook (GitHub does not
// redeliver on its own); the files are read later, when a follower loads the workspace. Only
// cancellation is returned.
func (uc *IngestUseCase) syncChangedFiles(ctx context.Context, repo *entity.Repository, pr *entity.PullRequest) error {
	if uc.files == nil || pr.State != entity.PRStateOpen || pr.IsDependabot || repo.CodeownersStatus != entity.CodeownersPresent {
		return nil
	}
	_ = uc.files.SyncAsFollower(ctx, *repo, *pr)
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return nil
}

// sameWebhookState reports whether a pull_request webhook carries nothing new for a stored PR.
func sameWebhookState(stored, incoming *entity.PullRequest) bool {
	return stored.GitHubPRID == incoming.GitHubPRID &&
		stored.Title == incoming.Title &&
		stored.State == incoming.State &&
		stored.IsDraft == incoming.IsDraft &&
		stored.HeadSHA == incoming.HeadSHA &&
		stored.BaseRef == incoming.BaseRef &&
		stored.AuthorLogin == incoming.AuthorLogin &&
		stored.HTMLURL == incoming.HTMLURL &&
		stored.MergeableState == incoming.MergeableState
}

func actorOf(p PullRequestEventPayload) string {
	if p.SenderLogin != "" {
		return p.SenderLogin
	}
	return p.AuthorLogin
}

func stateOf(p PullRequestEventPayload) entity.PRState {
	if p.Merged {
		return entity.PRStateMerged
	}
	return p.State
}

func actionOf(p PullRequestEventPayload) string {
	if p.Action == "closed" && p.Merged {
		return "merged"
	}
	return p.Action
}
