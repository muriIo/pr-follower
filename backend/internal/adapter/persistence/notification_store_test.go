package persistence_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

func turnNotification(userID, prID uint64, sha string) entity.Notification {
	return entity.Notification{
		UserID: userID, Kind: entity.NotificationKindTurn, Reason: entity.NotificationReasonNewCommit,
		RepositoryID: 10, RepositoryName: "arvore/hive-web", PullRequestID: prID, PRNumber: 35, PRTitle: "Fix login",
		CommitSHA: sha, ActorLogin: "carol", Details: map[string]string{"title": "Fix login"},
		CreatedAt: time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC),
	}
}

func TestNotificationStore_CreateIsIdempotentByUserAndKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := persistence.NewNotificationStore(openInMemoryTestDB(t))

	created, err := store.CreateNotifications(ctx, []entity.Notification{turnNotification(1, 77, "s1"), turnNotification(2, 77, "s1")})
	if err != nil || len(created) != 2 || created[0].ID == 0 || created[1].ID <= created[0].ID {
		t.Fatalf("CreateNotifications() = %+v, %v", created, err)
	}
	again, err := store.CreateNotifications(ctx, []entity.Notification{turnNotification(1, 77, "s1"), turnNotification(1, 77, "s2")})
	if err != nil || len(again) != 1 || again[0].CommitSHA != "s2" {
		t.Fatalf("only the new commit is a new notification: %+v, %v", again, err)
	}

	got, err := store.ListNotifications(ctx, 1, 0, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("ListNotifications() = %+v, %v", got, err)
	}
	n := got[1]
	if got[0].CommitSHA != "s2" || n.UserID != 1 || n.Kind != entity.NotificationKindTurn || n.Reason != entity.NotificationReasonNewCommit ||
		n.RepositoryID != 10 || n.RepositoryName != "arvore/hive-web" || n.PullRequestID != 77 || n.PRNumber != 35 ||
		n.PRTitle != "Fix login" || n.ActorLogin != "carol" || n.Details["title"] != "Fix login" || !n.CreatedAt.Equal(created[0].CreatedAt) || n.ReadAt != nil {
		t.Fatalf("newest first, every field kept: %+v", got)
	}
	older, _ := store.ListNotifications(ctx, 1, got[0].ID, 10)
	if len(older) != 1 || older[0].ID != got[1].ID {
		t.Fatalf("paging before an id: %+v", older)
	}
	since, _ := store.ListNotificationsSince(ctx, created[0].ID, 10)
	if len(since) != 2 || since[0].UserID != 2 || since[1].CommitSHA != "s2" {
		t.Fatalf("every user's notifications after an id, oldest first: %+v", since)
	}
	if latest, _ := store.LatestNotificationID(ctx); latest != again[0].ID {
		t.Fatalf("LatestNotificationID() = %d, want %d", latest, again[0].ID)
	}
}

func TestNotificationStore_ReadStateIsPerUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := persistence.NewNotificationStore(openInMemoryTestDB(t))
	created, _ := store.CreateNotifications(ctx, []entity.Notification{turnNotification(1, 77, "s1"), turnNotification(1, 78, "s1"), turnNotification(2, 77, "s1")})
	at := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	if n, err := store.MarkNotificationsRead(ctx, 1, []uint64{created[0].ID, created[2].ID}, at); err != nil || n != 1 {
		t.Fatalf("only the user's own notification is marked: %d, %v", n, err)
	}
	if c, _ := store.CountUnreadNotifications(ctx, 1); c != 1 {
		t.Fatalf("unread = %d, want 1", c)
	}
	if n, _ := store.MarkNotificationsRead(ctx, 1, nil, at); n != 1 {
		t.Fatalf("mark all marks the rest: %d", n)
	}
	if c, _ := store.CountUnreadNotifications(ctx, 2); c != 1 {
		t.Fatalf("another user's notifications stay unread, got %d", c)
	}
	got, _ := store.ListNotifications(ctx, 1, 0, 10)
	if got[0].ReadAt == nil || !got[0].ReadAt.Equal(at) {
		t.Fatalf("read time kept: %+v", got[0])
	}
}

func TestNotificationStore_PruneAndSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := persistence.NewNotificationStore(openInMemoryTestDB(t))
	old := turnNotification(1, 77, "s1")
	old.CreatedAt = time.Now().Add(-31 * 24 * time.Hour)
	fresh := turnNotification(1, 78, "s1")
	fresh.CreatedAt = time.Now()
	_, _ = store.CreateNotifications(ctx, []entity.Notification{old, fresh})
	if n, err := store.PruneNotifications(ctx, time.Now().Add(-30*24*time.Hour)); err != nil || n != 1 {
		t.Fatalf("PruneNotifications() = %d, %v", n, err)
	}

	if s, err := store.GetNotificationSettings(ctx, 1); err != nil || s.Paused || len(s.PopupsOff) != 0 {
		t.Fatalf("defaults: everything pops up, got %+v, %v", s, err)
	}
	want := entity.NotificationSettings{Paused: true, PopupsOff: []entity.NotificationKind{entity.NotificationKindRisk, entity.NotificationKindInvolved}}
	for range 2 {
		if err := store.SaveNotificationSettings(ctx, 1, want, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if s, _ := store.GetNotificationSettings(ctx, 1); !s.Paused || len(s.PopupsOff) != 2 || s.PopupsOff[1] != entity.NotificationKindInvolved {
		t.Fatalf("settings round trip: %+v", s)
	}
}

func TestNotifierMemory_WatchesBaselinesAndPositions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openInMemoryTestDB(t)
	mem := persistence.NewNotificationStore(db)
	repos := persistence.NewRepositoryRepository(db)

	if w, err := mem.GetNotificationWatch(ctx, 1, 77); err != nil || w != nil {
		t.Fatalf("never seen: %+v, %v", w, err)
	}
	w := entity.NotificationWatch{UserID: 1, PullRequestID: 77, RepositoryID: 10, Turn: entity.TurnStateWaitingOnMe, HeadSHA: "s1", ApprovedSHA: "s0", Risk: entity.RiskVerdictHigh}
	for range 2 {
		if err := mem.SaveNotificationWatch(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := mem.GetNotificationWatch(ctx, 1, 77); got == nil || *got != w {
		t.Fatalf("watch round trip: %+v", got)
	}

	repo := &entity.Repository{GitHubRepoID: 9001, Owner: "arvore", Name: "hive-web", FullName: "arvore/hive-web", DefaultBranch: "main"}
	if err := repos.Upsert(ctx, repo); err != nil {
		t.Fatal(err)
	}
	user := seedUser(t, db, 4242, "alice")
	if err := repos.SetUserFollow(ctx, user, repo.ID, true); err != nil {
		t.Fatal(err)
	}
	follows, err := mem.ListFollowsWithoutBaseline(ctx, 10)
	if err != nil || len(follows) != 1 || follows[0] != (port.Follow{UserID: user, RepositoryID: repo.ID}) {
		t.Fatalf("a new follow has no baseline: %+v, %v", follows, err)
	}
	if err := mem.SaveNotificationBaseline(ctx, user, repo.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := mem.HasNotificationBaseline(ctx, user, repo.ID); !ok {
		t.Fatal("baseline saved")
	}
	if follows, _ := mem.ListFollowsWithoutBaseline(ctx, 10); len(follows) != 0 {
		t.Fatalf("a seen follow is not listed: %+v", follows)
	}
	_ = mem.SaveNotificationWatch(ctx, entity.NotificationWatch{UserID: user, PullRequestID: 5, RepositoryID: repo.ID, Turn: entity.TurnStateHiddenGate})

	if err := repos.SetUserFollow(ctx, user, repo.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := mem.ForgetUnfollowed(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _ := mem.HasNotificationBaseline(ctx, user, repo.ID); ok {
		t.Fatal("an unfollow forgets the baseline")
	}
	if got, _ := mem.GetNotificationWatch(ctx, user, 5); got != nil {
		t.Fatalf("an unfollow forgets the watches: %+v", got)
	}

	if _, ok, err := mem.LogPosition(ctx, "notifier"); ok || err != nil {
		t.Fatalf("no position yet: %v %v", ok, err)
	}
	_ = mem.SaveLogPosition(ctx, "notifier", 41)
	_ = mem.SaveLogPosition(ctx, "notifier", 42)
	if seq, ok, _ := mem.LogPosition(ctx, "notifier"); !ok || seq != 42 {
		t.Fatalf("position = %d %v, want 42", seq, ok)
	}
}

func seedUser(t *testing.T, db *sql.DB, githubID int64, login string) uint64 {
	t.Helper()
	u := &entity.User{GitHubID: githubID, Login: login}
	if err := persistence.NewUserRepository(db, testCipher(t)).Upsert(context.Background(), u); err != nil {
		t.Fatalf("Upsert(%s): %v", login, err)
	}
	return u.ID
}
