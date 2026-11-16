package triage

import (
	"context"
	"fmt"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/domain/service"
)

// MaxOwnershipSyncsPerLoad bounds how many PRs one workspace load reads from GitHub to learn
// their changed files; the rest stay pending until the next load.
const MaxOwnershipSyncsPerLoad = 10

type InboxItem struct {
	PullRequest entity.PullRequest `json:"pull_request"`
	UserState   entity.UserPRState `json:"user_state"`
}

type DependabotItem struct {
	PullRequest entity.PullRequest         `json:"pull_request"`
	Analysis    *entity.DependabotAnalysis `json:"analysis,omitempty"`
	// AnalysisFailure is why the head commit's analysis failed; absent once it is analysed.
	AnalysisFailure *entity.AnalysisFailure `json:"analysis_failure,omitempty"`
	// ApprovedByMe: the reader has a standing approval of the head commit (from the review GitHub
	// recorded, so a new commit makes it stale). ApprovedByLogin: a teammate who approved the head.
	ApprovedByMe    bool   `json:"approved_by_me"`
	ApprovedByLogin string `json:"approved_by_login,omitempty"`
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
	// OwnershipPendingPRIDs are PRs whose changed files are not known for their head commit yet,
	// so nobody can say whether they touch the user's files. They are never shown as a turn.
	OwnershipPendingPRIDs []uint64 `json:"ownership_pending_pr_ids"`
	// Reviews says where each reviewer stands, by PR id, for the PRs somebody reviewed. It shows in
	// the drawer whoever owns the files; the turn itself is derived separately.
	Reviews map[uint64][]entity.ReviewDecision `json:"reviews"`
	// Checks lists each open PR's current checks (the latest run of each check on its head commit),
	// by PR id, for the PRs whose commit reported any: what GitHub's checks panel shows.
	Checks map[uint64][]entity.CheckRun `json:"checks"`
}

type UseCase struct {
	userRepo port.UserRepository
	repoRepo port.RepositoryRepository
	prRepo   port.PullRequestRepository
	pub      port.EventPublisher
	files    port.ChangedFilesStore
	syncer   port.ChangedFilesSyncer
	reviews  port.ReviewStore
	checks   port.CheckRunBatchReader
	failures port.AnalysisFailureStore
}

// Option adds an optional collaborator to the use case.
type Option func(*UseCase)

// WithAnalysisFailures lets the snapshot say why a Dependabot PR's analysis failed.
func WithAnalysisFailures(store port.AnalysisFailureStore) Option {
	return func(uc *UseCase) { uc.failures = store }
}

// WithCheckRuns lets the snapshot carry each PR's checks.
func WithCheckRuns(reader port.CheckRunBatchReader) Option {
	return func(uc *UseCase) { uc.checks = reader }
}

// NewUseCase wires the triage use case. files holds each PR's changed files and syncer reads the
// missing ones from GitHub with the reader's token; without them every PR stays "not evaluated".
// reviews (optional) holds what reviewers decided, so an approved PR stops being the user's move.
func NewUseCase(
	userRepo port.UserRepository,
	repoRepo port.RepositoryRepository,
	prRepo port.PullRequestRepository,
	pub port.EventPublisher,
	files port.ChangedFilesStore,
	syncer port.ChangedFilesSyncer,
	reviews port.ReviewStore,
	opts ...Option,
) *UseCase {
	uc := &UseCase{userRepo: userRepo, repoRepo: repoRepo, prRepo: prRepo, pub: pub, files: files, syncer: syncer, reviews: reviews}
	for _, opt := range opts {
		opt(uc)
	}
	return uc
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

	changed := map[uint64]entity.PRChangedFiles{}
	if uc.files != nil {
		if changed, err = uc.files.ListChangedFiles(ctx, prIDs); err != nil {
			return nil, err
		}
	}
	reviewsByPR := map[uint64][]entity.PullRequestReview{}
	if uc.reviews != nil {
		if reviewsByPR, err = uc.reviews.ListReviews(ctx, prIDs); err != nil {
			return nil, err
		}
	}
	var reader entity.User
	if user, err := uc.userRepo.GetByID(ctx, userID); err != nil {
		return nil, err
	} else if user != nil {
		reader = *user
	}
	syncBudget := MaxOwnershipSyncsPerLoad

	snapshot := &WorkspaceSnapshot{
		InboxWaitingOnMe:      []InboxItem{},
		InboxDimmed:           []InboxItem{},
		DependabotPRs:         []DependabotItem{},
		Seats:                 make([]RepoSeat, 0, len(repos)),
		OwnershipPendingPRIDs: []uint64{},
		Reviews:               map[uint64][]entity.ReviewDecision{},
		Checks:                map[uint64][]entity.CheckRun{},
	}
	if err := uc.collectChecks(ctx, snapshot, repoIDs, prs); err != nil {
		return nil, err
	}

	for _, pr := range prs {
		if decisions := service.LatestReviewDecisions(reviewsByPR[pr.ID], pr); len(decisions) > 0 {
			snapshot.Reviews[pr.ID] = decisions
		}
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
			var failure *entity.AnalysisFailure
			if analysis == nil && uc.failures != nil {
				failure, _ = uc.failures.GetAnalysisFailure(ctx, pr.ID, pr.HeadSHA)
			}
			facts := service.DeriveReviewFacts(reviewsByPR[pr.ID], pr, reader.Login)
			snapshot.DependabotPRs = append(snapshot.DependabotPRs, DependabotItem{
				PullRequest:     pr,
				Analysis:        analysis,
				AnalysisFailure: failure,
				ApprovedByMe:    facts.ApprovedByMeSHA != "" && facts.ApprovedByMeSHA == pr.HeadSHA,
				ApprovedByLogin: facts.TeammateApprovedBy,
			})
			continue
		}

		// The turn is derived here from facts (the PR, its changed files, CODEOWNERS, what the
		// user did), never from a stored state that could be stale or was never evaluated.
		st := userStates[pr.ID]
		st.PullRequestID, st.UserID = pr.ID, userID

		// Snoozing is the user's own choice and holds whoever owns the files. An approval is not
		// stored: it is derived below from the review GitHub recorded, per head commit.
		if st.IsSnoozedFor(pr.HeadSHA, pr.CIStatus) {
			st.TurnState = entity.TurnStateSnoozed
			snapshot.InboxDimmed = append(snapshot.InboxDimmed, InboxItem{PullRequest: pr, UserState: st})
			continue
		}
		if seat == nil || seat.Repository.CodeownersStatus != entity.CodeownersPresent {
			continue // no CODEOWNERS (or not checked): no review is assigned to anyone
		}

		files, known := changed[pr.ID]
		known = known && files.IsCurrentFor(pr)
		if !known && uc.files != nil && uc.syncer != nil && syncBudget > 0 {
			syncBudget--
			syncErr := uc.syncer.SyncAsUser(ctx, userID, seat.Repository, pr)
			if err := ctx.Err(); err != nil {
				return nil, context.Cause(ctx)
			}
			if syncErr == nil {
				fresh, err := uc.files.ListChangedFiles(ctx, []uint64{pr.ID})
				if err != nil {
					return nil, err
				}
				files = fresh[pr.ID]
				known = files.IsCurrentFor(pr)
			}
		}
		if !known {
			snapshot.OwnershipPendingPRIDs = append(snapshot.OwnershipPendingPRIDs, pr.ID)
			continue
		}

		owned := service.EvaluateCodeownersForUser(seat.Repository.CachedCodeownersRules, files.Files, reader).OwnedFiles
		st.OwnedFilesChanged = owned
		facts := service.DeriveReviewFacts(reviewsByPR[pr.ID], pr, reader.Login)
		st.TurnState = service.ComputeTurnState(service.TurnStateInput{
			PR: pr, OwnedFiles: owned, ExistingState: st,
			ApprovedByMeSHA:       facts.ApprovedByMeSHA,
			TeammateApprovedAtSHA: facts.TeammateApprovedAtSHA,
			UserActedAtSHA:        facts.UserActedAtSHA,
		})
		if st.TurnState == entity.TurnStateTeammateApproved {
			st.ApprovedByLogin = facts.TeammateApprovedBy
		}
		item := InboxItem{PullRequest: pr, UserState: st}
		switch {
		case st.TurnState == entity.TurnStateWaitingOnMe:
			snapshot.InboxWaitingOnMe = append(snapshot.InboxWaitingOnMe, item)
			seat.WaitingOnMeCount++
		case len(owned) > 0:
			snapshot.InboxDimmed = append(snapshot.InboxDimmed, item) // mine, but the gate is closed
		}
	}

	for _, repo := range repos {
		if seat, ok := seatMap[repo.ID]; ok {
			snapshot.Seats = append(snapshot.Seats, *seat)
		}
	}

	return snapshot, nil
}

// collectChecks reads the checks of every open PR in one query and keeps each PR's own: the
// latest run of each check on its head commit.
func (uc *UseCase) collectChecks(ctx context.Context, snapshot *WorkspaceSnapshot, repoIDs []uint64, prs []entity.PullRequest) error {
	if uc.checks == nil || len(prs) == 0 {
		return nil
	}
	shas := make([]string, 0, len(prs))
	for _, pr := range prs {
		if pr.HeadSHA != "" {
			shas = append(shas, pr.HeadSHA)
		}
	}
	runs, err := uc.checks.ListCheckRunsForCommits(ctx, repoIDs, shas)
	if err != nil {
		return err
	}
	type commit struct {
		repoID uint64
		sha    string
	}
	byCommit := map[commit][]entity.CheckRun{}
	for _, run := range runs {
		key := commit{run.RepositoryID, run.HeadSHA}
		byCommit[key] = append(byCommit[key], run)
	}
	for _, pr := range prs {
		if latest := entity.LatestCheckRuns(byCommit[commit{pr.RepositoryID, pr.HeadSHA}]); len(latest) > 0 {
			snapshot.Checks[pr.ID] = latest
		}
	}
	return nil
}

func (uc *UseCase) SnoozePR(ctx context.Context, userID, prID uint64, condition entity.SnoozeCondition) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	pr, err := uc.prRepo.GetByID(ctx, prID)
	if err != nil {
		return err
	}

	// Waiting for CI to pass makes sense only while it can still pass: a PR whose CI already passed
	// has nothing to wait for, and one with no CI would stay silenced forever.
	if condition == entity.SnoozeConditionCIGreen && pr.CIStatus != entity.CIStatusPending && pr.CIStatus != entity.CIStatusFailure {
		return fmt.Errorf("%w: CI is %q, so there is nothing to wait for", entity.ErrConflict, pr.CIStatus)
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
		Params:         map[string]string{"condition": string(condition)},
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
		OccurredAt:     time.Now().UTC(),
	})
}
