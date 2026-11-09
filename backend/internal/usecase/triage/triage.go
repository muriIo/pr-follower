package triage

import (
	"context"
	"fmt"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

type InboxItem struct {
	PullRequest entity.PullRequest `json:"pull_request"`
	UserState   entity.UserPRState `json:"user_state"`
}

type DependabotItem struct {
	PullRequest entity.PullRequest         `json:"pull_request"`
	Analysis    *entity.DependabotAnalysis `json:"analysis,omitempty"`
}

type RepoSeat struct {
	Repository        entity.Repository    `json:"repository"`
	OpenPRCount       int                  `json:"open_pr_count"`
	WaitingOnMeCount  int                  `json:"waiting_on_me_count"`
	DependabotPRCount int                  `json:"dependabot_pr_count"`
	FailingCICount    int                  `json:"failing_ci_count"`
	PullRequests      []entity.PullRequest `json:"pull_requests"`
}

type WorkspaceSnapshot struct {
	InboxWaitingOnMe []InboxItem      `json:"inbox_waiting_on_me"`
	InboxDimmed      []InboxItem      `json:"inbox_dimmed"`
	DependabotPRs    []DependabotItem `json:"dependabot_prs"`
	Seats            []RepoSeat       `json:"seats"`
}

type UseCase struct {
	userRepo port.UserRepository
	repoRepo port.RepositoryRepository
	prRepo   port.PullRequestRepository
	pub      port.EventPublisher
}

func NewUseCase(
	userRepo port.UserRepository,
	repoRepo port.RepositoryRepository,
	prRepo port.PullRequestRepository,
	pub port.EventPublisher,
) *UseCase {
	return &UseCase{
		userRepo: userRepo,
		repoRepo: repoRepo,
		prRepo:   prRepo,
		pub:      pub,
	}
}

func (uc *UseCase) GetWorkspaceSnapshot(ctx context.Context, userID uint64) (*WorkspaceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}

	repos, err := uc.repoRepo.ListFollowedByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	repoIDs := make([]uint64, 0, len(repos))
	seatMap := make(map[uint64]*RepoSeat, len(repos))
	for _, repo := range repos {
		repoIDs = append(repoIDs, repo.ID)
		seatMap[repo.ID] = &RepoSeat{
			Repository:   repo,
			PullRequests: []entity.PullRequest{},
		}
	}

	prs, err := uc.prRepo.ListOpenByRepoIDs(ctx, repoIDs)
	if err != nil {
		return nil, err
	}

	prIDs := make([]uint64, 0, len(prs))
	for _, pr := range prs {
		prIDs = append(prIDs, pr.ID)
	}

	userStates, err := uc.prRepo.ListUserPRStates(ctx, userID, prIDs)
	if err != nil {
		return nil, err
	}

	snapshot := &WorkspaceSnapshot{
		InboxWaitingOnMe: []InboxItem{},
		InboxDimmed:      []InboxItem{},
		DependabotPRs:    []DependabotItem{},
		Seats:            make([]RepoSeat, 0, len(repos)),
	}

	for _, pr := range prs {
		seat := seatMap[pr.RepositoryID]
		if seat != nil {
			seat.OpenPRCount++
			seat.PullRequests = append(seat.PullRequests, pr)
			if pr.CIStatus == entity.CIStatusFailure {
				seat.FailingCICount++
			}
		}

		if pr.IsDependabot {
			if seat != nil {
				seat.DependabotPRCount++
			}
			analysis, _ := uc.prRepo.GetAnalysisByPRAndSHA(ctx, pr.ID, pr.HeadSHA)
			snapshot.DependabotPRs = append(snapshot.DependabotPRs, DependabotItem{
				PullRequest: pr,
				Analysis:    analysis,
			})
			continue
		}

		st, ok := userStates[pr.ID]
		if !ok {
			st = entity.UserPRState{
				PullRequestID: pr.ID,
				UserID:        userID,
				TurnState:     entity.TurnStateWaitingOnMe,
			}
		}

		item := InboxItem{PullRequest: pr, UserState: st}
		if st.TurnState == entity.TurnStateWaitingOnMe && pr.IsReadyForReviewGate() && !st.IsSnoozedFor(pr.HeadSHA, pr.CIStatus) {
			snapshot.InboxWaitingOnMe = append(snapshot.InboxWaitingOnMe, item)
			if seat != nil {
				seat.WaitingOnMeCount++
			}
		} else {
			snapshot.InboxDimmed = append(snapshot.InboxDimmed, item)
		}
	}

	for _, repo := range repos {
		if seat, ok := seatMap[repo.ID]; ok {
			snapshot.Seats = append(snapshot.Seats, *seat)
		}
	}

	return snapshot, nil
}

func (uc *UseCase) SnoozePR(ctx context.Context, userID, prID uint64, condition entity.SnoozeCondition) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	pr, err := uc.prRepo.GetByID(ctx, prID)
	if err != nil {
		return err
	}

	st, err := uc.prRepo.GetUserPRState(ctx, prID, userID)
	if err != nil {
		st = &entity.UserPRState{
			PullRequestID: prID,
			UserID:        userID,
		}
	}

	st.SnoozedUntilCondition = condition
	st.SnoozedAtSHA = pr.HeadSHA
	st.TurnState = entity.TurnStateSnoozed
	st.UpdatedAt = time.Now().UTC()

	if err := uc.prRepo.UpsertUserPRState(ctx, st); err != nil {
		return err
	}

	return uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("snooze-%d-%d", prID, time.Now().UnixNano()),
		Type:           "pr.snoozed",
		RepositoryID:   pr.RepositoryID,
		RepositoryName: pr.RepositoryName,
		PullRequestID:  pr.ID,
		PRNumber:       pr.Number,
		Summary:        fmt.Sprintf("PR #%d snoozed (%s)", pr.Number, condition),
		OccurredAt:     time.Now().UTC(),
	})
}

func (uc *UseCase) ClaimPR(ctx context.Context, userID, prID uint64) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	user, err := uc.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	pr, err := uc.prRepo.GetByID(ctx, prID)
	if err != nil {
		return err
	}

	uid := user.ID
	pr.ClaimedByUserID = &uid
	pr.ClaimedByLogin = user.Login
	pr.UpdatedAt = time.Now().UTC()

	if err := uc.prRepo.Upsert(ctx, pr); err != nil {
		return err
	}

	return uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("claim-%d-%d", prID, time.Now().UnixNano()),
		Type:           "pr.claimed",
		RepositoryID:   pr.RepositoryID,
		RepositoryName: pr.RepositoryName,
		PullRequestID:  pr.ID,
		PRNumber:       pr.Number,
		ActorLogin:     user.Login,
		Summary:        fmt.Sprintf("@%s claimed PR #%d", user.Login, pr.Number),
		OccurredAt:     time.Now().UTC(),
	})
}
