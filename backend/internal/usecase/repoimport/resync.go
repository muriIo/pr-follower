package repoimport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

// Resync brings every repository the user follows in line with GitHub's current word, for whatever
// changed while no webhook arrived (the API was down, a delivery failed). For each repository it
// refreshes the open PRs it lists (title, head commit, checks, reviews, files), stores the ones it
// did not know, and closes those GitHub no longer has open: it asks GitHub what became of each
// before closing it, so a PR missing only from a capped listing stays open, and announces each
// close so other users' open drawers follow. It converges (ADR-0011). One repository failing is
// reported and does not stop the others; the user's own GitHub access failing, or a cancellation,
// ends it.
func (uc *UseCase) Resync(ctx context.Context, userID uint64) (entity.SyncResult, error) {
	return uc.ResyncReporting(ctx, userID, nil)
}

// ResyncReporting is Resync that tells report where it is before each repository and when it is done.
// Repositories are visited in name order, one after another (GitHub's rate limit is the user's, so
// parallel reads would only reach it sooner). When the limit is reached the rest are skipped, and the
// result says when it resets.
func (uc *UseCase) ResyncReporting(ctx context.Context, userID uint64, report func(entity.SyncProgress)) (entity.SyncResult, error) {
	var res entity.SyncResult
	if report == nil {
		report = func(entity.SyncProgress) {}
	}
	if err := ctx.Err(); err != nil {
		return res, context.Cause(ctx)
	}
	token, err := uc.tokens.AccessToken(ctx, userID)
	if err != nil {
		return res, err
	}
	followed, err := uc.repos.ListFollowedByUser(ctx, userID)
	if err != nil {
		return res, err
	}

	sort.Slice(followed, func(i, j int) bool { return followed[i].FullName < followed[j].FullName })

	for i := range followed {
		repo := &followed[i]
		if err := ctx.Err(); err != nil {
			return res, context.Cause(ctx)
		}
		report(entity.SyncProgress{Total: len(followed), Done: i, Current: repo.FullName})
		res.Repositories++
		target := entity.InstallationRepository{
			GitHubRepoID: repo.GitHubRepoID, InstallationID: repo.InstallationID,
			Owner: repo.Owner, Name: repo.Name, FullName: repo.FullName, DefaultBranch: repo.DefaultBranch,
		}
		stats, err := uc.backfillOpenPullRequests(ctx, token, repo, target, true)
		var closed, merged int
		if err == nil {
			closed, merged, err = uc.closeMissingPullRequests(ctx, token, repo, stats.open)
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return res, context.Cause(ctx)
			}
			if errors.Is(err, entity.ErrUnauthorized) {
				return res, err // the user's own access is gone: nothing else will work either
			}
			res.Failed = append(res.Failed, entity.SyncFailure{Repository: repo.FullName, Err: err})
			var limited *entity.RateLimitedError
			if errors.As(err, &limited) {
				res.RateLimitedUntil = limited.ResetAt
				res.Skipped = len(followed) - i - 1
				break
			}
			continue
		}
		res.Added += stats.added
		res.Updated += stats.updated
		res.Closed += closed
		res.Merged += merged
	}
	report(entity.SyncProgress{Total: len(followed), Done: len(followed)})
	return res, nil
}

// closeMissingPullRequests closes the stored open PRs that GitHub did not list, after asking GitHub
// what became of each: only one that is really not open is closed (merged, or closed without
// merging), and each is announced.
func (uc *UseCase) closeMissingPullRequests(ctx context.Context, token string, repo *entity.Repository, listed map[int]bool) (closed, merged int, err error) {
	stored, err := uc.prs.ListOpenByRepoIDs(ctx, []uint64{repo.ID})
	if err != nil {
		return 0, 0, err
	}
	for i := range stored {
		pr := stored[i]
		if listed[pr.Number] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return closed, merged, context.Cause(ctx)
		}
		live, err := uc.github.GetPullRequest(ctx, token, repo.Owner, repo.Name, pr.Number)
		switch {
		case errors.Is(err, entity.ErrNotFound): // gone altogether: it is no longer open
			live = entity.RemotePullRequestState{}
		case err != nil:
			return closed, merged, err
		case live.Open:
			continue // still open on GitHub: only the listing missed it
		}

		eventType := "pr.closed"
		pr.State = entity.PRStateClosed
		if live.Merged {
			eventType, pr.State = "pr.merged", entity.PRStateMerged
		}
		pr.UpdatedAt = uc.nowFunc().UTC()
		if err := uc.prs.Upsert(ctx, &pr); err != nil {
			return closed, merged, err
		}
		if live.Merged {
			merged++
		} else {
			closed++
		}
		_ = uc.events.Publish(ctx, entity.ActivityEvent{
			ID:             fmt.Sprintf("resync-%d-%s-%d", pr.ID, pr.State, time.Now().UnixNano()),
			Type:           eventType,
			RepositoryID:   repo.ID,
			RepositoryName: repo.FullName,
			PullRequestID:  pr.ID,
			PRNumber:       pr.Number,
			OccurredAt:     uc.nowFunc().UTC(),
		})
	}
	return closed, merged, nil
}
