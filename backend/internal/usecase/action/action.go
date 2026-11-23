// Package action performs the user's actions on GitHub (approve, merge, command Dependabot) as
// that user, with their own token. Before writing it checks what GitHub says now, and after an
// answer that never came it asks GitHub again instead of guessing (ADR-0011).
package action

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/domain/service"
)

// Deps are the collaborators of the use case.
type Deps struct {
	PRs    port.PullRequestRepository
	Repos  port.RepositoryRepository
	Users  port.UserRepository
	Tokens port.UserTokenSource
	GitHub port.GitHubGateway
	// State reads a PR's live state and reviews with the user's token.
	State port.PullRequestStateReader
	// Reviews keeps the review GitHub recorded, so "approved by me" is derived from it.
	Reviews port.ReviewStore
	Pub     port.EventPublisher
}

type UseCase struct {
	prRepo    port.PullRequestRepository
	repoRepo  port.RepositoryRepository
	users     port.UserRepository
	tokens    port.UserTokenSource
	ghGateway port.GitHubGateway
	state     port.PullRequestStateReader
	reviews   port.ReviewStore
	pub       port.EventPublisher
}

func NewUseCase(d Deps) *UseCase {
	return &UseCase{
		prRepo: d.PRs, repoRepo: d.Repos, users: d.Users, tokens: d.Tokens,
		ghGateway: d.GitHub, state: d.State, reviews: d.Reviews, pub: d.Pub,
	}
}

// target is a PR an action is about, with the user acting and their token.
type target struct {
	user  *entity.User
	pr    *entity.PullRequest
	repo  *entity.Repository
	token string
}

// resolve loads the PR, its repository and the acting user, and refuses to approve the user's
// own PR before anything reaches GitHub. The token is only fetched after those checks.
func (uc *UseCase) resolve(ctx context.Context, userID, prID uint64) (*target, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	pr, err := uc.prRepo.GetByID(ctx, prID)
	if err != nil {
		return nil, err
	}
	repo, err := uc.repoRepo.GetByID(ctx, pr.RepositoryID)
	if err != nil {
		return nil, err
	}
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &target{user: user, pr: pr, repo: repo}, nil
}

func (uc *UseCase) authenticate(ctx context.Context, t *target) error {
	token, err := uc.tokens.AccessToken(ctx, t.user.ID)
	if err != nil {
		return err
	}
	t.token = token
	return nil
}

func (uc *UseCase) live(ctx context.Context, t *target) (entity.RemotePullRequestState, error) {
	return uc.state.GetPullRequest(ctx, t.token, t.repo.Owner, t.repo.Name, t.pr.Number)
}

func sameLogin(a, b string) bool { return a != "" && strings.EqualFold(a, b) }

// ApprovePR approves the PR as the user. Nobody approves their own PR; a closed PR cannot be
// approved; an approval GitHub already has on the head commit is not repeated.
func (uc *UseCase) ApprovePR(ctx context.Context, userID, prID uint64, body string) error {
	t, err := uc.resolve(ctx, userID, prID)
	if err != nil {
		return err
	}
	if sameLogin(t.pr.AuthorLogin, t.user.Login) {
		return entity.ErrOwnPullRequest
	}
	if err := uc.authenticate(ctx, t); err != nil {
		return err
	}
	live, err := uc.live(ctx, t)
	if err != nil {
		return err
	}
	return uc.approve(ctx, t, live, body)
}

func (uc *UseCase) approve(ctx context.Context, t *target, live entity.RemotePullRequestState, body string) error {
	if sameLogin(live.AuthorLogin, t.user.Login) {
		return entity.ErrOwnPullRequest
	}
	if !live.Open {
		return fmt.Errorf("%w: the pull request is not open", entity.ErrConflict)
	}
	existing, err := uc.myApprovalOnHead(ctx, t, live)
	if err != nil {
		return err
	}
	if existing != nil { // converge: GitHub already has it, nothing to write or announce
		if err := uc.recordReview(ctx, t, *existing); err != nil {
			return err
		}
		return uc.keepMergeState(ctx, t, live)
	}
	if err := uc.requireChecksFinished(ctx, t, live); err != nil {
		return err
	}

	review, err := uc.ghGateway.ApprovePullRequest(ctx, t.token, *t.repo, t.pr.Number, body)
	if err != nil {
		if !errors.Is(err, entity.ErrOutcomeUnknown) {
			return err
		}
		// No answer: the approval may exist. Ask GitHub; never write a second time.
		confirmed, checkErr := uc.myApprovalOnHead(ctx, t, live)
		if checkErr != nil || confirmed == nil {
			return err
		}
		review = *confirmed
	}
	if review.GitHubReviewID != 0 {
		if err := uc.recordReview(ctx, t, review); err != nil {
			return err
		}
	}
	// The live state was read before the approval: GitHub recomputes it now.
	if err := uc.storeMergeState(ctx, t, t.pr.MergeableState.AfterReviewOrCheckChange()); err != nil {
		return err
	}
	return uc.publish(ctx, t, "approve", "pr.approved", nil)
}

// keepMergeState stores the merge state GitHub just reported for the stored head commit. An
// unknown state (GitHub is still computing) or one of another commit says nothing about ours.
func (uc *UseCase) keepMergeState(ctx context.Context, t *target, live entity.RemotePullRequestState) error {
	if live.MergeableState == entity.MergeableStateUnknown || live.HeadSHA != t.pr.HeadSHA {
		return nil
	}
	return uc.storeMergeState(ctx, t, live.MergeableState)
}

// learnWhyMergeWasRefused reads the PR again after GitHub refused to merge it and stores the merge
// state GitHub has now, so the panel shows the blocker instead of offering the same merge again.
// It is best effort: a failed read leaves the stored state, and the refusal is reported either way.
func (uc *UseCase) learnWhyMergeWasRefused(ctx context.Context, t *target) {
	after, err := uc.live(ctx, t)
	if err != nil {
		return
	}
	if err := uc.keepMergeState(ctx, t, after); err != nil && ctx.Err() == nil {
		slog.Warn("storing the merge state after a refused merge failed", "pr", t.pr.ID, "err", err)
	}
}

// storeMergeState writes the PR's merge state when it changed.
func (uc *UseCase) storeMergeState(ctx context.Context, t *target, state entity.MergeableState) error {
	if state == t.pr.MergeableState {
		return nil
	}
	next := *t.pr
	next.MergeableState = state
	if err := uc.prRepo.Upsert(ctx, &next); err != nil {
		return err
	}
	*t.pr = next
	return nil
}

// requireChecksFinished refuses to approve or merge while any check or commit status of the head
// commit is still running: those are blockers. It asks GitHub now (check runs and legacy statuses,
// the latest run of each check) instead of trusting stored CI, and never lets an action through on
// checks it could not read. A failed check is not a blocker here; only an unfinished one is.
func (uc *UseCase) requireChecksFinished(ctx context.Context, t *target, live entity.RemotePullRequestState) error {
	runs, err := uc.state.ListCommitCheckRuns(ctx, t.token, t.repo.Owner, t.repo.Name, live.HeadSHA)
	if err != nil {
		return err
	}
	statuses, err := uc.state.ListCommitStatuses(ctx, t.token, t.repo.Owner, t.repo.Name, live.HeadSHA)
	if err != nil {
		return err
	}
	for _, s := range statuses {
		runs = append(runs, s.AsCheckRun())
	}
	if entity.DeriveCIStatus(runs) == entity.CIStatusPending {
		return entity.ErrChecksRunning
	}
	return nil
}

// myApprovalOnHead returns the user's standing approval of the PR's head commit, as GitHub
// has it now: the latest decision counts, and an approval of an older commit does not.
func (uc *UseCase) myApprovalOnHead(ctx context.Context, t *target, live entity.RemotePullRequestState) (*entity.PullRequestReview, error) {
	reviews, err := uc.state.ListPullRequestReviews(ctx, t.token, t.repo.Owner, t.repo.Name, t.pr.Number)
	if err != nil {
		return nil, err
	}
	pr := entity.PullRequest{AuthorLogin: live.AuthorLogin, HeadSHA: live.HeadSHA}
	if service.DeriveReviewFacts(reviews, pr, t.user.Login).ApprovedByMeSHA != live.HeadSHA {
		return nil, nil
	}
	var found *entity.PullRequestReview
	for i, r := range reviews {
		if r.State == entity.ReviewApproved && r.CommitSHA == live.HeadSHA && sameLogin(r.ReviewerLogin, t.user.Login) &&
			(found == nil || r.GitHubReviewID > found.GitHubReviewID) {
			found = &reviews[i]
		}
	}
	return found, nil
}

func (uc *UseCase) recordReview(ctx context.Context, t *target, review entity.PullRequestReview) error {
	if uc.reviews == nil {
		return nil
	}
	review.PullRequestID = t.pr.ID
	_, err := uc.reviews.UpsertReview(ctx, review)
	return err
}

// MergePR merges the PR as the user. A PR GitHub already merged converges to success; one closed
// without merging cannot be merged.
func (uc *UseCase) MergePR(ctx context.Context, userID, prID uint64, method string) error {
	t, err := uc.resolve(ctx, userID, prID)
	if err != nil {
		return err
	}
	if err := uc.authenticate(ctx, t); err != nil {
		return err
	}
	live, err := uc.live(ctx, t)
	if err != nil {
		return err
	}
	return uc.merge(ctx, t, live, method)
}

func (uc *UseCase) merge(ctx context.Context, t *target, live entity.RemotePullRequestState, method string) error {
	if live.Merged {
		return nil // converge: GitHub's webhook records it; nothing to write or announce
	}
	if !live.Open {
		return fmt.Errorf("%w: the pull request is closed", entity.ErrConflict)
	}
	// The stored merge state is only a hint: GitHub's word replaces it, and GitHub decides the merge.
	if err := uc.keepMergeState(ctx, t, live); err != nil {
		return err
	}
	if err := uc.requireChecksFinished(ctx, t, live); err != nil {
		return err
	}
	if method == "" {
		method = "squash"
	}
	if err := uc.ghGateway.MergePullRequest(ctx, t.token, *t.repo, t.pr.Number, method); err != nil {
		if errors.Is(err, entity.ErrConflict) {
			uc.learnWhyMergeWasRefused(ctx, t)
			return err // GitHub's refusal, with its reason, is what the user is told
		}
		if !errors.Is(err, entity.ErrOutcomeUnknown) {
			return err
		}
		after, checkErr := uc.live(ctx, t)
		if checkErr != nil || !after.Merged {
			return err
		}
	}

	t.pr.State = entity.PRStateMerged
	t.pr.UpdatedAt = time.Now().UTC()
	if err := uc.prRepo.Upsert(ctx, t.pr); err != nil {
		return err
	}
	return uc.publish(ctx, t, "merge", "pr.merged", map[string]string{"method": method})
}

// BatchApproveAndMerge approves and merges each PR in turn, stopping at the first failure or
// cancellation. A PR of the user's own is refused before anything is written, because they could
// not approve it; a PR GitHub already merged counts as done.
func (uc *UseCase) BatchApproveAndMerge(ctx context.Context, userID uint64, prIDs []uint64, method string) ([]uint64, error) {
	completed := make([]uint64, 0, len(prIDs))
	for _, prID := range prIDs {
		if err := ctx.Err(); err != nil {
			return completed, context.Cause(ctx)
		}
		t, err := uc.resolve(ctx, userID, prID)
		if err != nil {
			return completed, err
		}
		if sameLogin(t.pr.AuthorLogin, t.user.Login) {
			return completed, entity.ErrOwnPullRequest
		}
	}
	for _, prID := range prIDs {
		if err := ctx.Err(); err != nil {
			return completed, context.Cause(ctx)
		}
		t, err := uc.resolve(ctx, userID, prID)
		if err != nil {
			return completed, err
		}
		if err := uc.authenticate(ctx, t); err != nil {
			return completed, err
		}
		live, err := uc.live(ctx, t)
		if err != nil {
			return completed, err
		}
		if !live.Merged {
			if err := uc.approve(ctx, t, live, "Approved via PR Follower"); err != nil {
				return completed, err
			}
			// The merge state was read before the approval, which GitHub now recomputes.
			live.MergeableState = entity.MergeableStateUnknown
			if err := uc.merge(ctx, t, live, method); err != nil {
				return completed, err
			}
		}
		completed = append(completed, prID)
	}
	return completed, nil
}

// RunDependabotCommand comments "@dependabot <command>" as the user. An unconfirmed comment is
// reported as such and not announced.
func (uc *UseCase) RunDependabotCommand(ctx context.Context, userID uint64, prID uint64, command string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	cleanCmd := strings.TrimSpace(strings.TrimPrefix(command, "@dependabot"))
	if cleanCmd == "" {
		return fmt.Errorf("%w: empty dependabot command", entity.ErrInvalidInput)
	}
	t, err := uc.resolve(ctx, userID, prID)
	if err != nil {
		return err
	}
	if err := uc.authenticate(ctx, t); err != nil {
		return err
	}

	body := "@dependabot " + cleanCmd
	if err := uc.ghGateway.CreatePRComment(ctx, t.token, *t.repo, t.pr.Number, body); err != nil {
		return err
	}
	return uc.publish(ctx, t, "depcmd", "dependabot.command", map[string]string{"command": body})
}

func (uc *UseCase) publish(ctx context.Context, t *target, idPrefix, eventType string, params map[string]string) error {
	return uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("%s-%d-%d", idPrefix, t.pr.ID, time.Now().UnixNano()),
		Type:           eventType,
		RepositoryID:   t.repo.ID,
		RepositoryName: t.repo.FullName,
		PullRequestID:  t.pr.ID,
		PRNumber:       t.pr.Number,
		ActorLogin:     t.user.Login,
		Params:         params,
		OccurredAt:     time.Now().UTC(),
	})
}
