package persistence_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/database"
)

func openInMemoryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Connect(context.Background(), database.Options{
		DSN:          "sqlite://:memory:",
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("failed to open and migrate sqlite in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSqlcRepositories_FullLifecycleAndMappings(t *testing.T) {
	t.Parallel()

	db := openInMemoryTestDB(t)
	ctx := context.Background()

	userRepo := persistence.NewUserRepository(db)
	repoRepo := persistence.NewRepositoryRepository(db)
	prRepo := persistence.NewPullRequestRepository(db)

	// 1. User Upsert & Fetch
	user := &entity.User{
		GitHubID:  1001,
		Login:     "murilo",
		Name:      "Murilo",
		AvatarURL: "https://avatars.githubusercontent.com/u/1001",
		Teams:     []string{"@arvore/frontend", "@arvore/platform"},
	}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("userRepo.Upsert() error: %v", err)
	}
	if user.ID == 0 {
		t.Fatal("expected user.ID to be populated")
	}

	gotUserByID, err := userRepo.GetByID(ctx, user.ID)
	if err != nil || gotUserByID.Login != "murilo" {
		t.Fatalf("userRepo.GetByID() = %#v, err = %v", gotUserByID, err)
	}

	gotUser, err := userRepo.GetByGitHubID(ctx, 1001)
	if err != nil || gotUser.Login != "murilo" || len(gotUser.Teams) != 2 {
		t.Fatalf("GetByGitHubID() = %#v, err = %v", gotUser, err)
	}

	if _, err := userRepo.GetByID(ctx, 99999); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing user, got %v", err)
	}

	// 2. Repository Upsert, Follow & Fetch
	repo := &entity.Repository{
		GitHubRepoID:  5001,
		Owner:         "arvore",
		Name:          "hive-web",
		FullName:      "arvore/hive-web",
		DefaultBranch: "main",
		CachedCodeownersRules: []entity.CodeownerRule{
			{Pattern: "/src/app/", Owners: []string{"@murilo"}},
		},
	}
	if err := repoRepo.Upsert(ctx, repo); err != nil {
		t.Fatalf("repoRepo.Upsert() error: %v", err)
	}

	if gotRepo, err := repoRepo.GetByID(ctx, repo.ID); err != nil || gotRepo.FullName != "arvore/hive-web" {
		t.Fatalf("repoRepo.GetByID() = %#v, err = %v", gotRepo, err)
	}
	if gotRepoGH, err := repoRepo.GetByGitHubRepoID(ctx, 5001); err != nil || gotRepoGH.Name != "hive-web" {
		t.Fatalf("repoRepo.GetByGitHubRepoID() = %#v, err = %v", gotRepoGH, err)
	}

	if err := repoRepo.SetUserFollow(ctx, user.ID, repo.ID, true); err != nil {
		t.Fatalf("SetUserFollow(true) error: %v", err)
	}

	followed, err := repoRepo.ListFollowedByUser(ctx, user.ID)
	if err != nil || len(followed) != 1 || followed[0].FullName != "arvore/hive-web" {
		t.Fatalf("ListFollowedByUser() = %#v, err = %v", followed, err)
	}

	// 3. PullRequest Upsert, Analysis & UserPRState
	pr := &entity.PullRequest{
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		GitHubPRID:     90001,
		Number:         42,
		Title:          "chore(deps): bump vitest from 2.1.0 to 2.1.1",
		AuthorLogin:    "dependabot[bot]",
		HTMLURL:        "https://github.com/arvore/hive-web/pull/42",
		IsDependabot:   true,
		State:          entity.PRStateOpen,
		MergeableState: entity.MergeableStateClean,
		CIStatus:       entity.CIStatusSuccess,
		HeadSHA:        "sha-42",
		BaseRef:        "main",
	}
	if err := prRepo.Upsert(ctx, pr); err != nil {
		t.Fatalf("prRepo.Upsert() error: %v", err)
	}

	if gotPR, err := prRepo.GetByID(ctx, pr.ID); err != nil || gotPR.Number != 42 {
		t.Fatalf("prRepo.GetByID() = %#v, err = %v", gotPR, err)
	}
	if gotPRByNum, err := prRepo.GetByRepoAndNumber(ctx, repo.ID, 42); err != nil || gotPRByNum.ID != pr.ID {
		t.Fatalf("prRepo.GetByRepoAndNumber() = %#v, err = %v", gotPRByNum, err)
	}

	openPRs, err := prRepo.ListOpenByRepoIDs(ctx, []uint64{repo.ID})
	if err != nil || len(openPRs) != 1 || openPRs[0].Number != 42 {
		t.Fatalf("ListOpenByRepoIDs() = %#v, err = %v", openPRs, err)
	}

	analysis := &entity.DependabotAnalysis{
		PullRequestID:     pr.ID,
		HeadSHA:           "sha-42",
		Ecosystem:         "npm",
		PackageName:       "vitest",
		FromVersion:       "2.1.0",
		ToVersion:         "2.1.1",
		SemverBumpType:    entity.SemverBumpPatch,
		IsDirectDep:       true,
		DepScope:          entity.DependencyScopeDev,
		RiskVerdict:       entity.RiskVerdictLow,
		RiskScore:         12,
		VerdictSummaryKey: "dependabot.verdict.low.dev_safe",
		DependencyPathGraph: []entity.DependencyNode{
			{Name: "hive-web", Version: "root", Scope: entity.DependencyScopeProd},
			{Name: "vitest", Version: "2.1.1", Scope: entity.DependencyScopeDev},
		},
	}
	if err := prRepo.SaveAnalysis(ctx, analysis); err != nil {
		t.Fatalf("SaveAnalysis() error: %v", err)
	}

	gotAnalysis, err := prRepo.GetAnalysisByPRAndSHA(ctx, pr.ID, "sha-42")
	if err != nil || gotAnalysis.PackageName != "vitest" || len(gotAnalysis.DependencyPathGraph) != 2 {
		t.Fatalf("GetAnalysisByPRAndSHA() = %#v, err = %v", gotAnalysis, err)
	}

	state := &entity.UserPRState{
		PullRequestID:         pr.ID,
		UserID:                user.ID,
		TurnState:             entity.TurnStateSnoozed,
		SnoozedUntilCondition: entity.SnoozeConditionNextCommit,
		SnoozedAtSHA:          "sha-42",
		OwnedFilesChanged: []entity.ChangedFile{
			{Path: "src/app/app.ts", Additions: 4},
		},
	}
	if err := prRepo.UpsertUserPRState(ctx, state); err != nil {
		t.Fatalf("UpsertUserPRState() error: %v", err)
	}

	if gotState, err := prRepo.GetUserPRState(ctx, pr.ID, user.ID); err != nil || gotState.TurnState != entity.TurnStateSnoozed {
		t.Fatalf("GetUserPRState() = %#v, err = %v", gotState, err)
	}

	stateMap, err := prRepo.ListUserPRStates(ctx, user.ID, []uint64{pr.ID})
	if err != nil || stateMap[pr.ID].TurnState != entity.TurnStateSnoozed || len(stateMap[pr.ID].OwnedFilesChanged) != 1 {
		t.Fatalf("ListUserPRStates() = %#v, err = %v", stateMap, err)
	}

	if err := repoRepo.SetUserFollow(ctx, user.ID, repo.ID, false); err != nil {
		t.Fatalf("SetUserFollow(false) error: %v", err)
	}
}
