package persistence_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/crypto"
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

func testCipher(t *testing.T) *crypto.AESGCMCipher {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error: %v", err)
	}
	c, err := crypto.NewAESGCMCipher(key)
	if err != nil {
		t.Fatalf("NewAESGCMCipher() error: %v", err)
	}
	return c
}

func TestSqlcRepositories_FullLifecycleAndMappings(t *testing.T) {
	t.Parallel()

	db := openInMemoryTestDB(t)
	ctx := context.Background()

	userRepo := persistence.NewUserRepository(db, testCipher(t))
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

func TestUserRepository_EncryptsGitHubTokensAtRest(t *testing.T) {
	t.Parallel()
	db := openInMemoryTestDB(t)
	ctx := context.Background()
	repo := persistence.NewUserRepository(db, testCipher(t))

	accessExp := time.Unix(1_800_000_000, 0).UTC()
	refreshExp := time.Unix(1_810_000_000, 0).UTC()
	user := &entity.User{
		GitHubID: 42,
		Login:    "octo",
		GitHubToken: entity.OAuthToken{
			AccessToken:           "ghu_access",
			RefreshToken:          "ghr_refresh",
			AccessTokenExpiresAt:  accessExp,
			RefreshTokenExpiresAt: refreshExp,
		},
	}
	if err := repo.Upsert(ctx, user); err != nil {
		t.Fatalf("Upsert() error: %v", err)
	}

	var storedAccess, storedRefresh string
	if err := db.QueryRowContext(ctx, "SELECT encrypted_token, encrypted_refresh_token FROM users WHERE id = $1", user.ID).
		Scan(&storedAccess, &storedRefresh); err != nil {
		t.Fatalf("raw select error: %v", err)
	}
	if storedAccess == "" || strings.Contains(storedAccess, "ghu_access") || strings.Contains(storedRefresh, "ghr_refresh") {
		t.Fatalf("tokens not encrypted at rest: access=%q refresh=%q", storedAccess, storedRefresh)
	}

	got, err := repo.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if got.GitHubToken.AccessToken != "ghu_access" || got.GitHubToken.RefreshToken != "ghr_refresh" ||
		!got.GitHubToken.AccessTokenExpiresAt.Equal(accessExp) || !got.GitHubToken.RefreshTokenExpiresAt.Equal(refreshExp) {
		t.Fatalf("token round trip mismatch: %#v", got.GitHubToken)
	}

	noExpiry := &entity.User{GitHubID: 43, Login: "plain"}
	if err := repo.Upsert(ctx, noExpiry); err != nil {
		t.Fatalf("Upsert() error: %v", err)
	}
	gotPlain, _ := repo.GetByGitHubID(ctx, 43)
	if !gotPlain.GitHubToken.AccessTokenExpiresAt.IsZero() || gotPlain.GitHubToken.AccessToken != "" {
		t.Fatalf("expected zero token, got %#v", gotPlain.GitHubToken)
	}
}

func TestUserRepository_UndecryptableTokenIsDropped(t *testing.T) {
	t.Parallel()
	db := openInMemoryTestDB(t)
	ctx := context.Background()

	if err := persistence.NewUserRepository(db, testCipher(t)).Upsert(ctx, &entity.User{
		GitHubID: 7, Login: "rotated", GitHubToken: entity.OAuthToken{AccessToken: "ghu_old", RefreshToken: "ghr_old"},
	}); err != nil {
		t.Fatalf("Upsert() error: %v", err)
	}

	// A new key (e.g. TOKEN_ENCRYPTION_KEY rotated) cannot read old tokens: the user must sign in again.
	got, err := persistence.NewUserRepository(db, testCipher(t)).GetByGitHubID(ctx, 7)
	if err != nil {
		t.Fatalf("GetByGitHubID() error: %v", err)
	}
	if got.GitHubToken.AccessToken != "" || got.GitHubToken.RefreshToken != "" {
		t.Fatalf("expected undecryptable tokens to be dropped, got %#v", got.GitHubToken)
	}
}

func TestSessionRepository_Lifecycle(t *testing.T) {
	t.Parallel()
	db := openInMemoryTestDB(t)
	ctx := context.Background()

	user := &entity.User{GitHubID: 99, Login: "sess"}
	if err := persistence.NewUserRepository(db, testCipher(t)).Upsert(ctx, user); err != nil {
		t.Fatalf("Upsert() error: %v", err)
	}

	sessions := persistence.NewSessionRepository(db)
	expires := time.Unix(1_800_000_000, 0).UTC()
	if err := sessions.Create(ctx, &entity.Session{TokenHash: "hash-1", UserID: user.ID, ExpiresAt: expires}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	got, err := sessions.GetByTokenHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("GetByTokenHash() error: %v", err)
	}
	if got.UserID != user.ID || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("unexpected session: %#v", got)
	}

	if _, err := sessions.GetByTokenHash(ctx, "missing"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	if err := sessions.Delete(ctx, "hash-1"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if _, err := sessions.GetByTokenHash(ctx, "hash-1"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := sessions.Create(canceled, &entity.Session{TokenHash: "h", UserID: user.ID, ExpiresAt: expires}); err == nil {
		t.Fatal("expected canceled context to abort Create")
	}
	if _, err := sessions.GetByTokenHash(canceled, "h"); err == nil {
		t.Fatal("expected canceled context to abort GetByTokenHash")
	}
	if err := sessions.Delete(canceled, "h"); err == nil {
		t.Fatal("expected canceled context to abort Delete")
	}
}

func TestRepositoryRepository_AccessTracking(t *testing.T) {
	t.Parallel()
	db := openInMemoryTestDB(t)
	ctx := context.Background()
	repos := persistence.NewRepositoryRepository(db)

	web := &entity.Repository{GitHubRepoID: 900, InstallationID: 20, Owner: "acme", Name: "web", FullName: "acme/web", DefaultBranch: "main"}
	api := &entity.Repository{GitHubRepoID: 901, InstallationID: 20, Owner: "acme", Name: "api", FullName: "acme/api", DefaultBranch: "main"}
	other := &entity.Repository{GitHubRepoID: 100, InstallationID: 10, Owner: "octo", Name: "x", FullName: "octo/x", DefaultBranch: "main"}
	for _, r := range []*entity.Repository{web, api, other} {
		if err := repos.Upsert(ctx, r); err != nil {
			t.Fatalf("Upsert(%s) error: %v", r.FullName, err)
		}
	}

	got, _ := repos.GetByID(ctx, web.ID)
	if got.Access != entity.RepositoryAccessActive {
		t.Fatalf("new repositories start active, got %q", got.Access)
	}

	byInstallation, err := repos.ListByInstallation(ctx, 20)
	if err != nil || len(byInstallation) != 2 {
		t.Fatalf("ListByInstallation(20) = %+v, %v", byInstallation, err)
	}

	web.ApplyAccess(entity.RepositoryAccessRemoved, 0)
	if err := repos.SaveAccess(ctx, web); err != nil {
		t.Fatalf("SaveAccess() error: %v", err)
	}
	got, _ = repos.GetByGitHubRepoID(ctx, 900)
	if got.Access != entity.RepositoryAccessRemoved || got.InstallationID != 20 {
		t.Fatalf("expected removed under installation 20, got %+v", got)
	}

	web.ApplyAccess(entity.RepositoryAccessActive, 30)
	_ = repos.SaveAccess(ctx, web)
	if moved, _ := repos.ListByInstallation(ctx, 30); len(moved) != 1 || moved[0].Access != entity.RepositoryAccessActive {
		t.Fatalf("expected acme/web active under installation 30, got %+v", moved)
	}

	// Importing again (Upsert) restores access.
	api.ApplyAccess(entity.RepositoryAccessSuspended, 0)
	_ = repos.SaveAccess(ctx, api)
	api.Access = ""
	_ = repos.Upsert(ctx, api)
	if got, _ := repos.GetByID(ctx, api.ID); got.Access != entity.RepositoryAccessActive {
		t.Fatalf("re-import must restore access, got %q", got.Access)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repos.ListByInstallation(canceled, 20); err == nil {
		t.Error("ListByInstallation: expected cancellation error")
	}
	if err := repos.SaveAccess(canceled, web); err == nil {
		t.Error("SaveAccess: expected cancellation error")
	}
}

func TestRepositoryRepository_IsFollowing(t *testing.T) {
	t.Parallel()
	db := openInMemoryTestDB(t)
	ctx := context.Background()
	users := persistence.NewUserRepository(db, testCipher(t))
	repos := persistence.NewRepositoryRepository(db)

	alice := &entity.User{GitHubID: 1, Login: "alice"}
	bob := &entity.User{GitHubID: 2, Login: "bob"}
	_ = users.Upsert(ctx, alice)
	_ = users.Upsert(ctx, bob)
	repo := &entity.Repository{GitHubRepoID: 900, Owner: "acme", Name: "web", FullName: "acme/web", DefaultBranch: "main"}
	_ = repos.Upsert(ctx, repo)
	_ = repos.SetUserFollow(ctx, alice.ID, repo.ID, true)

	if ok, err := repos.IsFollowing(ctx, alice.ID, repo.ID); err != nil || !ok {
		t.Fatalf("alice follows acme/web: got %v, %v", ok, err)
	}
	if ok, err := repos.IsFollowing(ctx, bob.ID, repo.ID); err != nil || ok {
		t.Fatalf("bob does not follow acme/web: got %v, %v", ok, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repos.IsFollowing(canceled, alice.ID, repo.ID); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestPullRequestRepository_CheckRunsAndHeadLookup(t *testing.T) {
	t.Parallel()
	db := openInMemoryTestDB(t)
	ctx := context.Background()
	repos := persistence.NewRepositoryRepository(db)
	prs := persistence.NewPullRequestRepository(db)

	repo := &entity.Repository{GitHubRepoID: 900, Owner: "acme", Name: "web", FullName: "acme/web", DefaultBranch: "main"}
	_ = repos.Upsert(ctx, repo)
	open := &entity.PullRequest{RepositoryID: repo.ID, RepositoryName: "acme/web", GitHubPRID: 1, Number: 1, State: entity.PRStateOpen,
		MergeableState: entity.MergeableStateUnknown, CIStatus: entity.CIStatusNone, HeadSHA: "abc", BaseRef: "main"}
	closed := &entity.PullRequest{RepositoryID: repo.ID, RepositoryName: "acme/web", GitHubPRID: 2, Number: 2, State: entity.PRStateClosed,
		MergeableState: entity.MergeableStateUnknown, CIStatus: entity.CIStatusNone, HeadSHA: "abc", BaseRef: "main"}
	_ = prs.Upsert(ctx, open)
	_ = prs.Upsert(ctx, closed)

	if got, err := prs.ListOpenByHeadSHA(ctx, repo.ID, "abc"); err != nil || len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("ListOpenByHeadSHA() = %+v, %v", got, err)
	}

	if _, err := prs.GetCheckRun(ctx, 77); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("unknown run: expected ErrNotFound, got %v", err)
	}
	run := entity.CheckRun{ID: 77, RepositoryID: repo.ID, HeadSHA: "abc", Name: "test", Status: "in_progress"}
	if err := prs.UpsertCheckRun(ctx, run); err != nil {
		t.Fatalf("UpsertCheckRun() error: %v", err)
	}
	run.Status, run.Conclusion = "completed", "success"
	_ = prs.UpsertCheckRun(ctx, run) // same id: updated, not duplicated
	_ = prs.UpsertCheckRun(ctx, entity.CheckRun{ID: 78, RepositoryID: repo.ID, HeadSHA: "other", Status: "queued"})

	runs, err := prs.ListCheckRuns(ctx, repo.ID, "abc")
	if err != nil || len(runs) != 1 || runs[0].Status != "completed" || runs[0].Conclusion != "success" {
		t.Fatalf("ListCheckRuns() = %+v, %v", runs, err)
	}
	got, err := prs.GetCheckRun(ctx, 77)
	if err != nil || got.Name != "test" || got.Conclusion != "success" {
		t.Fatalf("GetCheckRun() = %+v, %v", got, err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := prs.ListOpenByHeadSHA(canceled, repo.ID, "abc"); err == nil {
		t.Error("ListOpenByHeadSHA: expected cancellation error")
	}
	if err := prs.UpsertCheckRun(canceled, run); err == nil {
		t.Error("UpsertCheckRun: expected cancellation error")
	}
	if _, err := prs.ListCheckRuns(canceled, repo.ID, "abc"); err == nil {
		t.Error("ListCheckRuns: expected cancellation error")
	}
	if _, err := prs.GetCheckRun(canceled, 77); err == nil {
		t.Error("GetCheckRun: expected cancellation error")
	}
}

func TestRepositoryRepository_CodeownersStatusRoundTrip(t *testing.T) {
	t.Parallel()
	db := openInMemoryTestDB(t)
	ctx := context.Background()
	repos := persistence.NewRepositoryRepository(db)

	repo := &entity.Repository{GitHubRepoID: 1, Owner: "a", Name: "b", FullName: "a/b", DefaultBranch: "main", CodeownersStatus: entity.CodeownersAbsent}
	_ = repos.Upsert(ctx, repo)
	got, _ := repos.GetByID(ctx, repo.ID)
	if got.CodeownersStatus != entity.CodeownersAbsent {
		t.Fatalf("expected absent, got %q", got.CodeownersStatus)
	}
	followed, _ := repos.ListByInstallation(ctx, 0)
	if len(followed) != 1 || followed[0].CodeownersStatus != entity.CodeownersAbsent {
		t.Fatalf("list must carry codeowners status: %+v", followed)
	}
}

func TestPullRequestRepository_HeadSHA(t *testing.T) {
	t.Parallel()
	db := openInMemoryTestDB(t)
	ctx := context.Background()
	repos := persistence.NewRepositoryRepository(db)
	prs := persistence.NewPullRequestRepository(db)
	repo := &entity.Repository{GitHubRepoID: 900, Owner: "acme", Name: "web", FullName: "acme/web", DefaultBranch: "main"}
	_ = repos.Upsert(ctx, repo)
	_ = prs.Upsert(ctx, &entity.PullRequest{RepositoryID: repo.ID, RepositoryName: "acme/web", GitHubPRID: 1, Number: 7, State: entity.PRStateOpen,
		MergeableState: entity.MergeableStateUnknown, CIStatus: entity.CIStatusNone, HeadSHA: "sha-7", BaseRef: "main"})

	if head, ok := prs.HeadSHA(ctx, repo.ID, 7); !ok || head != "sha-7" {
		t.Fatalf("HeadSHA(7) = %q, %v", head, ok)
	}
	if _, ok := prs.HeadSHA(ctx, repo.ID, 8); ok {
		t.Fatal("unknown PR must report no head")
	}
}

func TestPullRequestRepository_ChangedFilesReplaceOnNewCommit(t *testing.T) {
	t.Parallel()
	db := openInMemoryTestDB(t)
	ctx := context.Background()
	repos := persistence.NewRepositoryRepository(db)
	prs := persistence.NewPullRequestRepository(db)
	repo := &entity.Repository{GitHubRepoID: 900, Owner: "acme", Name: "web", FullName: "acme/web", DefaultBranch: "main"}
	_ = repos.Upsert(ctx, repo)
	newPR := func(n int) *entity.PullRequest {
		pr := &entity.PullRequest{RepositoryID: repo.ID, RepositoryName: "acme/web", GitHubPRID: int64(n), Number: n, Title: "t", State: entity.PRStateOpen,
			MergeableState: entity.MergeableStateUnknown, CIStatus: entity.CIStatusNone, HeadSHA: "sha1", BaseRef: "main"}
		if err := prs.Upsert(ctx, pr); err != nil {
			t.Fatal(err)
		}
		return pr
	}
	one, two := newPR(1), newPR(2)

	if got, err := prs.ListChangedFiles(ctx, []uint64{one.ID, two.ID}); err != nil || len(got) != 0 {
		t.Fatalf("nothing stored yet: %+v, %v", got, err)
	}
	first := entity.PRChangedFiles{PullRequestID: one.ID, HeadSHA: "sha1", Files: []entity.ChangedFile{
		{Path: "src/a.ts", Status: "modified", Additions: 3, Deletions: 1, Patch: "@@ big patch"}, {Path: "docs/b.md", Status: "added", Additions: 9}}}
	if err := prs.SaveChangedFiles(ctx, first); err != nil {
		t.Fatalf("SaveChangedFiles() error: %v", err)
	}
	_ = prs.SaveChangedFiles(ctx, first) // idempotent: same row
	_ = prs.SaveChangedFiles(ctx, entity.PRChangedFiles{PullRequestID: two.ID, HeadSHA: "sha1"})

	got, err := prs.ListChangedFiles(ctx, []uint64{one.ID, two.ID, 999})
	if err != nil || len(got) != 2 {
		t.Fatalf("ListChangedFiles() = %+v, %v", got, err)
	}
	if f := got[one.ID]; f.HeadSHA != "sha1" || len(f.Files) != 2 || f.Files[0].Path != "src/a.ts" || f.Files[0].Additions != 3 || f.Files[1].Status != "added" {
		t.Fatalf("unexpected files: %+v", f)
	}
	if got[one.ID].Files[0].Patch != "" {
		t.Fatal("patches are not stored: ownership needs only the paths")
	}
	if f := got[two.ID]; f.HeadSHA != "sha1" || len(f.Files) != 0 {
		t.Fatalf("a PR that changes no file is still evaluated: %+v", f)
	}

	// A new commit replaces the PR's files instead of adding a second row.
	_ = prs.SaveChangedFiles(ctx, entity.PRChangedFiles{PullRequestID: one.ID, HeadSHA: "sha2", Files: []entity.ChangedFile{{Path: "src/c.ts"}}})
	got, _ = prs.ListChangedFiles(ctx, []uint64{one.ID})
	if f := got[one.ID]; f.HeadSHA != "sha2" || len(f.Files) != 1 || f.Files[0].Path != "src/c.ts" {
		t.Fatalf("expected the files of the new commit: %+v", f)
	}
	if empty, err := prs.ListChangedFiles(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("no ids: %+v, %v", empty, err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := prs.SaveChangedFiles(canceled, first); err == nil {
		t.Error("SaveChangedFiles: expected cancellation error")
	}
	if _, err := prs.ListChangedFiles(canceled, []uint64{one.ID}); err == nil {
		t.Error("ListChangedFiles: expected cancellation error")
	}
}

func TestRepositoryRepository_ListFollowerIDs(t *testing.T) {
	t.Parallel()
	db := openInMemoryTestDB(t)
	ctx := context.Background()
	users := persistence.NewUserRepository(db, testCipher(t))
	repos := persistence.NewRepositoryRepository(db)
	repo := &entity.Repository{GitHubRepoID: 900, Owner: "acme", Name: "web", FullName: "acme/web", DefaultBranch: "main"}
	_ = repos.Upsert(ctx, repo)
	var ids []uint64
	for i, login := range []string{"ana", "bia", "caio"} {
		u := &entity.User{GitHubID: int64(100 + i), Login: login}
		if err := users.Upsert(ctx, u); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	_ = repos.SetUserFollow(ctx, ids[2], repo.ID, true)
	_ = repos.SetUserFollow(ctx, ids[0], repo.ID, true)

	got, err := repos.ListFollowerIDs(ctx, repo.ID)
	if err != nil || len(got) != 2 || got[0] != ids[0] || got[1] != ids[2] {
		t.Fatalf("ListFollowerIDs() = %v, %v (want %v and %v)", got, err, ids[0], ids[2])
	}
	if none, err := repos.ListFollowerIDs(ctx, 999); err != nil || len(none) != 0 {
		t.Fatalf("unknown repository: %v, %v", none, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repos.ListFollowerIDs(canceled, repo.ID); err == nil {
		t.Error("expected cancellation error")
	}
}
