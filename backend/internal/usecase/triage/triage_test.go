package triage_test

import (
	"context"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/triage"
	"time"
)

type fakeUserRepo struct {
	user *entity.User
}

func (f *fakeUserRepo) Upsert(_ context.Context, u *entity.User) error {
	f.user = u
	return nil
}
func (f *fakeUserRepo) GetByID(_ context.Context, _ uint64) (*entity.User, error) {
	return f.user, nil
}
func (f *fakeUserRepo) GetByGitHubID(_ context.Context, _ int64) (*entity.User, error) {
	return f.user, nil
}

type fakeRepoRepo struct {
	repos []entity.Repository
}

func (f *fakeRepoRepo) Upsert(_ context.Context, _ *entity.Repository) error { return nil }
func (f *fakeRepoRepo) GetByID(_ context.Context, id uint64) (*entity.Repository, error) {
	for _, r := range f.repos {
		if r.ID == id {
			return &r, nil
		}
	}
	return nil, entity.ErrNotFound
}
func (f *fakeRepoRepo) GetByGitHubRepoID(_ context.Context, _ int64) (*entity.Repository, error) {
	return nil, entity.ErrNotFound
}
func (f *fakeRepoRepo) ListFollowedByUser(_ context.Context, _ uint64) ([]entity.Repository, error) {
	return f.repos, nil
}
func (f *fakeRepoRepo) SetUserFollow(_ context.Context, _, _ uint64, _ bool) error { return nil }

type fakePRRepo struct {
	prs      []entity.PullRequest
	states   map[uint64]entity.UserPRState
	analyses map[uint64]*entity.DependabotAnalysis
}

func (f *fakePRRepo) Upsert(_ context.Context, pr *entity.PullRequest) error {
	for i := range f.prs {
		if f.prs[i].ID == pr.ID {
			f.prs[i] = *pr
			return nil
		}
	}
	f.prs = append(f.prs, *pr)
	return nil
}
func (f *fakePRRepo) GetByID(_ context.Context, id uint64) (*entity.PullRequest, error) {
	for _, p := range f.prs {
		if p.ID == id {
			cp := p
			return &cp, nil
		}
	}
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) GetByRepoAndNumber(_ context.Context, _ uint64, _ int) (*entity.PullRequest, error) {
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) ListOpenByRepoIDs(_ context.Context, _ []uint64) ([]entity.PullRequest, error) {
	return f.prs, nil
}
func (f *fakePRRepo) SaveAnalysis(_ context.Context, a *entity.DependabotAnalysis) error {
	f.analyses[a.PullRequestID] = a
	return nil
}
func (f *fakePRRepo) GetAnalysisByPRAndSHA(_ context.Context, prID uint64, _ string) (*entity.DependabotAnalysis, error) {
	if a, ok := f.analyses[prID]; ok {
		return a, nil
	}
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) UpsertUserPRState(_ context.Context, s *entity.UserPRState) error {
	f.states[s.PullRequestID] = *s
	return nil
}
func (f *fakePRRepo) GetUserPRState(_ context.Context, prID, _ uint64) (*entity.UserPRState, error) {
	if s, ok := f.states[prID]; ok {
		return &s, nil
	}
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) ListUserPRStates(_ context.Context, _ uint64, _ []uint64) (map[uint64]entity.UserPRState, error) {
	return f.states, nil
}

type fakePublisher struct {
	events []entity.ActivityEvent
}

func (f *fakePublisher) Publish(_ context.Context, ev entity.ActivityEvent) error {
	f.events = append(f.events, ev)
	return nil
}

func TestTriageUseCase_BuildWorkspaceSnapshotAndSnoozeClaim(t *testing.T) {
	t.Parallel()

	userRepo := &fakeUserRepo{
		user: &entity.User{ID: 1, Login: "murilo", Teams: []string{"@arvore/frontend"}},
	}
	repoRepo := &fakeRepoRepo{
		repos: []entity.Repository{
			{
				ID:               10,
				Owner:            "arvore",
				Name:             "hive-web",
				FullName:         "arvore/hive-web",
				CodeownersStatus: entity.CodeownersPresent,
				CachedCodeownersRules: []entity.CodeownerRule{
					{Pattern: "/src/app/", Owners: []string{"@murilo", "@arvore/frontend"}},
				},
			},
		},
	}
	prRepo := &fakePRRepo{
		prs: []entity.PullRequest{
			{
				ID:             100,
				RepositoryID:   10,
				RepositoryName: "arvore/hive-web",
				Number:         42,
				Title:          "feat: add seats grid",
				AuthorLogin:    "alice",
				IsDependabot:   false,
				State:          entity.PRStateOpen,
				CIStatus:       entity.CIStatusSuccess,
				MergeableState: entity.MergeableStateClean,
				HeadSHA:        "sha-42",
			},
			{
				ID:             101,
				RepositoryID:   10,
				RepositoryName: "arvore/hive-web",
				Number:         43,
				Title:          "chore(deps): bump vitest from 2.1.0 to 2.1.1",
				AuthorLogin:    "dependabot[bot]",
				IsDependabot:   true,
				State:          entity.PRStateOpen,
				CIStatus:       entity.CIStatusSuccess,
				MergeableState: entity.MergeableStateClean,
				HeadSHA:        "sha-43",
			},
		},
		states: map[uint64]entity.UserPRState{
			100: {
				PullRequestID: 100,
				UserID:        1,
				TurnState:     entity.TurnStateWaitingOnMe,
				OwnedFilesChanged: []entity.ChangedFile{
					{Path: "src/app/seats.component.ts", Additions: 18},
				},
			},
		},
		analyses: map[uint64]*entity.DependabotAnalysis{
			101: {
				PullRequestID:     101,
				HeadSHA:           "sha-43",
				PackageName:       "vitest",
				FromVersion:       "2.1.0",
				ToVersion:         "2.1.1",
				SemverBumpType:    entity.SemverBumpPatch,
				DepScope:          entity.DependencyScopeDev,
				RiskVerdict:       entity.RiskVerdictLow,
				RiskScore:         12,
				VerdictSummaryKey: "dependabot.verdict.low.dev_safe",
			},
		},
	}
	pub := &fakePublisher{}

	files := &fakeFiles{byPR: map[uint64]entity.PRChangedFiles{
		100: {PullRequestID: 100, HeadSHA: "sha-42", Files: []entity.ChangedFile{{Path: "src/app/seats.component.ts", Additions: 18}}},
	}}
	uc := triage.NewUseCase(userRepo, repoRepo, prRepo, pub, files, &fakeSyncer{store: files}, nil)

	snapshot, err := uc.GetWorkspaceSnapshot(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetWorkspaceSnapshot() unexpected error: %v", err)
	}

	if len(snapshot.Seats) != 1 {
		t.Fatalf("expected 1 repo seat, got %d", len(snapshot.Seats))
	}
	if snapshot.Seats[0].OpenPRCount != 2 || snapshot.Seats[0].DependabotPRCount != 1 {
		t.Errorf("unexpected seat counts: %#v", snapshot.Seats[0])
	}
	if len(snapshot.InboxWaitingOnMe) != 1 {
		t.Fatalf("expected 1 PR waiting on me, got %d", len(snapshot.InboxWaitingOnMe))
	}
	if len(snapshot.DependabotPRs) != 1 || snapshot.DependabotPRs[0].Analysis.RiskVerdict != entity.RiskVerdictLow {
		t.Fatalf("expected 1 low-risk dependabot PR, got %#v", snapshot.DependabotPRs)
	}

	// Snooze PR 100 until next commit
	if err := uc.SnoozePR(context.Background(), 1, 100, entity.SnoozeConditionNextCommit); err != nil {
		t.Fatalf("SnoozePR() error: %v", err)
	}
	if prRepo.states[100].TurnState != entity.TurnStateSnoozed {
		t.Errorf("expected PR 100 turn state SNOOZED, got %q", prRepo.states[100].TurnState)
	}

	// Claim PR 101 for user 1
	if err := uc.ClaimPR(context.Background(), 1, 101); err != nil {
		t.Fatalf("ClaimPR() error: %v", err)
	}
	if len(pub.events) != 2 {
		t.Errorf("expected 2 published SSE events, got %d", len(pub.events))
	}
}

type fakeFailures map[uint64]entity.AnalysisFailure

func (f fakeFailures) RecordAnalysisFailure(context.Context, entity.AnalysisFailure) error {
	return nil
}
func (f fakeFailures) GetAnalysisFailure(_ context.Context, prID uint64, headSHA string) (*entity.AnalysisFailure, error) {
	if failure, ok := f[prID]; ok && failure.HeadSHA == headSHA {
		return &failure, nil
	}
	return nil, entity.ErrNotFound
}

func TestTriageUseCase_ADependabotPRWhoseAnalysisFailedSaysWhy(t *testing.T) {
	t.Parallel()
	bump := func(id uint64, sha string) entity.PullRequest {
		return entity.PullRequest{ID: id, RepositoryID: 10, RepositoryName: "arvore/hive-web", Number: int(id),
			Title: "chore(deps): bump vitest from 2.1.0 to 2.1.1", AuthorLogin: "dependabot[bot]", IsDependabot: true,
			State: entity.PRStateOpen, CIStatus: entity.CIStatusSuccess, MergeableState: entity.MergeableStateClean, HeadSHA: sha}
	}
	prRepo := &fakePRRepo{
		prs:    []entity.PullRequest{bump(201, "sha-new"), bump(202, "sha-202"), bump(203, "sha-203")},
		states: map[uint64]entity.UserPRState{},
		analyses: map[uint64]*entity.DependabotAnalysis{
			202: {PullRequestID: 202, HeadSHA: "sha-202", RiskVerdict: entity.RiskVerdictLow},
		},
	}
	failedAt := time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC)
	failures := fakeFailures{
		201: {PullRequestID: 201, HeadSHA: "sha-old", ErrorKind: "forbidden"}, // an older commit: stale
		202: {PullRequestID: 202, HeadSHA: "sha-202", ErrorKind: "timeout"},   // analysed since: superseded
		203: {PullRequestID: 203, HeadSHA: "sha-203", ErrorKind: "forbidden", Message: "forbidden by github", FailedAt: failedAt},
	}
	userRepo := &fakeUserRepo{user: &entity.User{ID: 1, Login: "murilo"}}
	repoRepo := &fakeRepoRepo{repos: []entity.Repository{{ID: 10, Owner: "arvore", Name: "hive-web", FullName: "arvore/hive-web"}}}
	files := &fakeFiles{byPR: map[uint64]entity.PRChangedFiles{}}
	uc := triage.NewUseCase(userRepo, repoRepo, prRepo, &fakePublisher{}, files, &fakeSyncer{store: files}, nil,
		triage.WithAnalysisFailures(failures))

	snapshot, err := uc.GetWorkspaceSnapshot(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetWorkspaceSnapshot() error: %v", err)
	}
	byID := map[uint64]triage.DependabotItem{}
	for _, item := range snapshot.DependabotPRs {
		byID[item.PullRequest.ID] = item
	}
	if f := byID[201].AnalysisFailure; f != nil {
		t.Errorf("a failure of an older commit says nothing about the new one: %+v", f)
	}
	if f := byID[202].AnalysisFailure; f != nil || byID[202].Analysis == nil {
		t.Errorf("an analysis of the commit supersedes its failure: %+v", byID[202])
	}
	if f := byID[203].AnalysisFailure; f == nil || f.ErrorKind != "forbidden" || !f.FailedAt.Equal(failedAt) {
		t.Errorf("the head commit's failure must be reported: %+v", f)
	}
}
