package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func TestMemberAccessStore_ListsUsersWithAccessAndRevokesIdempotently(t *testing.T) {
	t.Parallel()
	db := openInMemoryTestDB(t)
	ctx := context.Background()
	users := persistence.NewUserRepository(db, testCipher(t))
	sessions := persistence.NewSessionRepository(db)
	repos := persistence.NewRepositoryRepository(db)
	prs := persistence.NewPullRequestRepository(db)
	jobs := persistence.NewSyncJobStore(db)
	store := persistence.NewMemberAccessStore(db)

	leaver := &entity.User{GitHubID: 1, Login: "leaver", GitHubToken: entity.OAuthToken{AccessToken: "ghu_secret"}}
	follower := &entity.User{GitHubID: 2, Login: "follower"}
	idle := &entity.User{GitHubID: 3, Login: "idle"}
	for _, u := range []*entity.User{leaver, follower, idle} {
		if err := users.Upsert(ctx, u); err != nil {
			t.Fatalf("Upsert(%s): %v", u.Login, err)
		}
	}
	repo := &entity.Repository{GitHubRepoID: 10, Owner: "acme", Name: "web", FullName: "acme/web", DefaultBranch: "main"}
	if err := repos.Upsert(ctx, repo); err != nil {
		t.Fatal(err)
	}
	pr := &entity.PullRequest{RepositoryID: repo.ID, GitHubPRID: 100, Number: 1, Title: "t", State: entity.PRStateOpen, HeadSHA: "sha"}
	if err := prs.Upsert(ctx, pr); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(sessions.Create(ctx, &entity.Session{TokenHash: "h1", UserID: leaver.ID, ExpiresAt: now.Add(time.Hour)}))
	must(sessions.Create(ctx, &entity.Session{TokenHash: "h2", UserID: leaver.ID, ExpiresAt: now.Add(time.Hour)}))
	must(repos.SetUserFollow(ctx, leaver.ID, repo.ID, true))
	must(repos.SetUserFollow(ctx, follower.ID, repo.ID, true))
	must(prs.UpsertUserPRState(ctx, &entity.UserPRState{PullRequestID: pr.ID, UserID: leaver.ID, TurnState: entity.TurnStateSnoozed}))
	must(prs.UpsertUserPRState(ctx, &entity.UserPRState{PullRequestID: pr.ID, UserID: follower.ID, TurnState: entity.TurnStateSnoozed}))
	if _, err := jobs.ClaimSyncJob(ctx, leaver.ID, "run-1", now, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	listed, err := store.ListUsersWithAccess(ctx)
	if err != nil {
		t.Fatalf("ListUsersWithAccess: %v", err)
	}
	if len(listed) != 2 || listed[0].ID != leaver.ID || listed[1].ID != follower.ID {
		t.Fatalf("expected the users with a session or a follow (not idle), got %+v", listed)
	}
	if listed[0].Login != "leaver" || listed[0].GitHubID != 1 || listed[0].GitHubToken.AccessToken != "" {
		t.Fatalf("expected id, login and github id without tokens, got %+v", listed[0])
	}

	removed, err := store.RevokeUserAccess(ctx, leaver.ID)
	if err != nil {
		t.Fatalf("RevokeUserAccess: %v", err)
	}
	if want := (entity.RevokedAccess{Sessions: 2, Follows: 1, PRStates: 1, SyncJobs: 1}); removed != want {
		t.Fatalf("removed %+v, want %+v", removed, want)
	}
	if _, err := sessions.GetByTokenHash(ctx, "h1"); err == nil {
		t.Fatal("the leaver's sessions must be gone")
	}
	if followed, _ := repos.ListFollowedByUser(ctx, leaver.ID); len(followed) != 0 {
		t.Fatalf("the leaver's follows must be gone: %v", followed)
	}
	if _, err := jobs.GetSyncJob(ctx, leaver.ID); err == nil {
		t.Fatal("the leaver's sync job must be gone")
	}
	if _, err := prs.GetUserPRState(ctx, pr.ID, follower.ID); err != nil {
		t.Fatalf("another user's state must stay: %v", err)
	}
	if followed, _ := repos.ListFollowedByUser(ctx, follower.ID); len(followed) != 1 {
		t.Fatal("another user's follows must stay")
	}
	if _, err := users.GetByID(ctx, leaver.ID); err != nil {
		t.Fatalf("the user row stays (claims and history refer to it): %v", err)
	}

	again, err := store.RevokeUserAccess(ctx, leaver.ID)
	if err != nil || again.Any() {
		t.Fatalf("a second revoke (another replica) changes nothing: %+v, %v", again, err)
	}
	if listed, _ := store.ListUsersWithAccess(ctx); len(listed) != 1 || listed[0].ID != follower.ID {
		t.Fatalf("a revoked user is no longer listed: %+v", listed)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.ListUsersWithAccess(canceled); err == nil {
		t.Fatal("canceled list must fail")
	}
	if _, err := store.RevokeUserAccess(canceled, follower.ID); err == nil {
		t.Fatal("canceled revoke must fail")
	}
}
