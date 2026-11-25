// Package notify decides which notifications exist and serves them (ADR-0017): the notifier turns
// activity into notifications, the center lists them and keeps each user's settings, and the
// dispatcher hands them to the delivery channels.
package notify

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

// NotifierLogPosition names the notifier's place in the activity log.
const NotifierLogPosition = "notifier"

// NotifierLockID is the advisory lock that keeps the notifier on one replica at a time.
const NotifierLockID int64 = 4097083627

const (
	activityBatch = 200
	// baselineBatch bounds how many new follows one pass records.
	baselineBatch = 50
)

// RepositoryReader reads a repository by id.
type RepositoryReader interface {
	GetByID(ctx context.Context, id uint64) (*entity.Repository, error)
}

// PullRequestReader reads what the turn of a PR depends on.
type PullRequestReader interface {
	GetByID(ctx context.Context, id uint64) (*entity.PullRequest, error)
	ListOpenByRepoIDs(ctx context.Context, repoIDs []uint64) ([]entity.PullRequest, error)
	GetAnalysisByPRAndSHA(ctx context.Context, prID uint64, headSHA string) (*entity.DependabotAnalysis, error)
	ListUserPRStates(ctx context.Context, userID uint64, prIDs []uint64) (map[uint64]entity.UserPRState, error)
}

// UserReader reads a user by id.
type UserReader interface {
	GetByID(ctx context.Context, id uint64) (*entity.User, error)
}

// ChangedFilesReader reads the stored changed files of PRs.
type ChangedFilesReader interface {
	ListChangedFiles(ctx context.Context, prIDs []uint64) (map[uint64]entity.PRChangedFiles, error)
}

// ReviewReader reads the reviews GitHub recorded for PRs.
type ReviewReader interface {
	ListReviews(ctx context.Context, prIDs []uint64) (map[uint64][]entity.PullRequestReview, error)
}

// NotifierDeps are what the notifier reads and writes.
type NotifierDeps struct {
	Log       port.ActivityLog
	Memory    port.NotifierMemory
	Store     port.NotificationStore
	Repos     RepositoryReader
	Followers port.RepositoryFollowers
	PRs       PullRequestReader
	Users     UserReader
	Files     ChangedFilesReader
	Reviews   ReviewReader
	// Wake (optional) tells the delivery channels that notifications were created.
	Wake func(ctx context.Context)
}

// Notifier reads the activity log in order and creates the notifications each activity calls for.
// It runs on one replica at a time (NotifierLockID); every step is safe to repeat (ADR-0011).
type Notifier struct {
	d NotifierDeps
}

func NewNotifier(d NotifierDeps) *Notifier {
	return &Notifier{d: d}
}

// Pass records the follows seen for the first time, then handles the activity appended since the
// last pass. The first pass ever starts at the newest activity: the log's past notifies nobody.
func (n *Notifier) Pass(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if err := n.d.Memory.ForgetUnfollowed(ctx); err != nil {
		return fmt.Errorf("forget unfollowed repositories: %w", err)
	}
	if err := n.recordNewFollows(ctx); err != nil {
		return err
	}
	pos, ok, err := n.d.Memory.LogPosition(ctx, NotifierLogPosition)
	if err != nil {
		return fmt.Errorf("read the notifier position: %w", err)
	}
	if !ok {
		latest, err := n.d.Log.LatestSeq(ctx)
		if err != nil {
			return fmt.Errorf("read the activity log position: %w", err)
		}
		return n.d.Memory.SaveLogPosition(ctx, NotifierLogPosition, latest)
	}
	for {
		batch, err := n.d.Log.ListSince(ctx, pos, activityBatch)
		if err != nil {
			return fmt.Errorf("read the activity log: %w", err)
		}
		for _, logged := range batch {
			if err := ctx.Err(); err != nil {
				return context.Cause(ctx)
			}
			created, err := n.handle(ctx, logged.Event)
			if err != nil {
				return err
			}
			if len(created) > 0 && n.d.Wake != nil {
				n.d.Wake(ctx)
			}
			pos = logged.Seq
			if err := n.d.Memory.SaveLogPosition(ctx, NotifierLogPosition, pos); err != nil {
				return fmt.Errorf("save the notifier position: %w", err)
			}
		}
		if len(batch) < activityBatch {
			return nil
		}
	}
}

// recordNewFollows remembers, without notifying, where each PR of a newly seen follow stands.
func (n *Notifier) recordNewFollows(ctx context.Context) error {
	follows, err := n.d.Memory.ListFollowsWithoutBaseline(ctx, baselineBatch)
	if err != nil {
		return fmt.Errorf("list new follows: %w", err)
	}
	for _, f := range follows {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		user, err := n.d.Users.GetByID(ctx, f.UserID)
		if err != nil {
			return fmt.Errorf("read user %d: %w", f.UserID, err)
		}
		repo, err := n.d.Repos.GetByID(ctx, f.RepositoryID)
		if errors.Is(err, entity.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read repository %d: %w", f.RepositoryID, err)
		}
		prs, err := n.d.PRs.ListOpenByRepoIDs(ctx, []uint64{repo.ID})
		if err != nil {
			return err
		}
		facts, err := n.readFacts(ctx, *user, prs)
		if err != nil {
			return err
		}
		for _, pr := range prs {
			watch, _ := n.observe(ctx, *user, *repo, pr, facts, nil)
			if err := n.d.Memory.SaveNotificationWatch(ctx, watch); err != nil {
				return err
			}
		}
		if err := n.d.Memory.SaveNotificationBaseline(ctx, f.UserID, f.RepositoryID, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

// handle works out what one activity changed for every follower of its repository.
func (n *Notifier) handle(ctx context.Context, ev entity.ActivityEvent) ([]entity.Notification, error) {
	if ev.PullRequestID == 0 {
		return nil, nil
	}
	pr, err := n.d.PRs.GetByID(ctx, ev.PullRequestID)
	if errors.Is(err, entity.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read PR %d: %w", ev.PullRequestID, err)
	}
	repo, err := n.d.Repos.GetByID(ctx, pr.RepositoryID)
	if errors.Is(err, entity.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read repository %d: %w", pr.RepositoryID, err)
	}
	followers, err := n.d.Followers.ListFollowerIDs(ctx, repo.ID)
	if err != nil {
		return nil, err
	}

	var decided []entity.Notification
	for _, userID := range followers {
		seen, err := n.d.Memory.HasNotificationBaseline(ctx, userID, repo.ID)
		if err != nil {
			return nil, err
		}
		if !seen {
			continue // recorded silently on the next pass
		}
		user, err := n.d.Users.GetByID(ctx, userID)
		if errors.Is(err, entity.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		facts, err := n.readFacts(ctx, *user, []entity.PullRequest{*pr})
		if err != nil {
			return nil, err
		}
		before, err := n.d.Memory.GetNotificationWatch(ctx, userID, pr.ID)
		if err != nil {
			return nil, err
		}
		after, involved := n.observe(ctx, *user, *repo, *pr, facts, before)
		decided = append(decided, service.DecideNotifications(service.NotificationChange{
			Event: ev, Recipient: *user, PR: *pr, Before: before, After: after, Involved: involved,
		})...)
		if err := n.d.Memory.SaveNotificationWatch(ctx, after); err != nil {
			return nil, err
		}
	}
	if len(decided) == 0 {
		return nil, nil
	}
	created, err := n.d.Store.CreateNotifications(ctx, decided)
	if err != nil {
		return nil, fmt.Errorf("store notifications: %w", err)
	}
	if len(created) > 0 {
		slog.Info("notifications created", "count", len(created), "activity", ev.Type, "pr", pr.ID)
	}
	return created, nil
}

// prFacts are what one user's turn on some PRs is derived from.
type prFacts struct {
	files   map[uint64]entity.PRChangedFiles
	reviews map[uint64][]entity.PullRequestReview
	states  map[uint64]entity.UserPRState
}

func (n *Notifier) readFacts(ctx context.Context, user entity.User, prs []entity.PullRequest) (prFacts, error) {
	ids := make([]uint64, 0, len(prs))
	for _, pr := range prs {
		ids = append(ids, pr.ID)
	}
	var f prFacts
	var err error
	if f.files, err = n.d.Files.ListChangedFiles(ctx, ids); err != nil {
		return f, err
	}
	if f.reviews, err = n.d.Reviews.ListReviews(ctx, ids); err != nil {
		return f, err
	}
	if f.states, err = n.d.PRs.ListUserPRStates(ctx, user.ID, ids); err != nil {
		return f, err
	}
	return f, nil
}

// observe is what the user's position on the PR is now, as the panel would show it, and whether
// they acted on it. A turn that cannot be told yet (changed files not read for the head commit)
// keeps what was seen before.
func (n *Notifier) observe(ctx context.Context, user entity.User, repo entity.Repository, pr entity.PullRequest, f prFacts, before *entity.NotificationWatch) (entity.NotificationWatch, bool) {
	reviews := f.reviews[pr.ID]
	facts := service.DeriveReviewFacts(reviews, pr, user.Login)
	w := entity.NotificationWatch{UserID: user.ID, PullRequestID: pr.ID, RepositoryID: repo.ID, HeadSHA: pr.HeadSHA}
	if facts.ApprovedByMeSHA != "" && facts.ApprovedByMeSHA == pr.HeadSHA {
		w.ApprovedSHA = pr.HeadSHA
	}
	if pr.IsDependabot {
		if a, err := n.d.PRs.GetAnalysisByPRAndSHA(ctx, pr.ID, pr.HeadSHA); err == nil && a != nil {
			w.Risk = a.RiskVerdict
		}
	}
	w.Turn = turnOf(user, repo, pr, f, facts)
	if w.Turn == "" && before != nil {
		w.Turn = before.Turn
	}

	involved := pr.ClaimedByUserID != nil && *pr.ClaimedByUserID == user.ID
	for _, r := range reviews {
		involved = involved || strings.EqualFold(r.ReviewerLogin, user.Login)
	}
	return w, involved
}

// turnOf derives the turn the way the workspace does; "" when it cannot be told yet.
func turnOf(user entity.User, repo entity.Repository, pr entity.PullRequest, f prFacts, facts service.ReviewFacts) entity.TurnState {
	if pr.State == entity.PRStateMerged || pr.State == entity.PRStateClosed {
		return entity.TurnStateMergedOrClosed
	}
	if pr.IsDependabot {
		return entity.TurnStateHiddenGate // Dependabot PRs are triaged in the radar, never as a turn
	}
	state := f.states[pr.ID]
	if state.IsSnoozedFor(pr.HeadSHA, pr.CIStatus) {
		return entity.TurnStateSnoozed
	}
	if repo.CodeownersStatus != entity.CodeownersPresent {
		return entity.TurnStateHiddenGate
	}
	files, known := f.files[pr.ID]
	if !known || !files.IsCurrentFor(pr) {
		return ""
	}
	owned := service.EvaluateCodeownersForUser(repo.CachedCodeownersRules, files.Files, user).OwnedFiles
	return service.ComputeTurnState(service.TurnStateInput{
		PR: pr, OwnedFiles: owned, ExistingState: state,
		ApprovedByMeSHA:       facts.ApprovedByMeSHA,
		TeammateApprovedAtSHA: facts.TeammateApprovedAtSHA,
		UserActedAtSHA:        facts.UserActedAtSHA,
	})
}
