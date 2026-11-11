package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func activity(id string, repoID uint64, at time.Time) entity.ActivityEvent {
	return entity.ActivityEvent{
		ID: id, Type: "pr.opened", RepositoryID: repoID, RepositoryName: "acme/web",
		PullRequestID: 7, PRNumber: 3, ActorLogin: "octocat",
		Params: map[string]string{"title": "feat: x"}, OccurredAt: at,
	}
}

func TestActivityLog_AppendAssignsIncreasingSequenceAndKeepsEveryField(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := persistence.NewActivityLog(openInMemoryTestDB(t))
	at := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

	s1, err := log.Append(ctx, activity("a", 1, at))
	if err != nil {
		t.Fatalf("Append() error: %v", err)
	}
	s2, _ := log.Append(ctx, activity("b", 1, at.Add(time.Second)))
	if s2 <= s1 {
		t.Fatalf("sequence must increase, got %d then %d", s1, s2)
	}

	got, err := log.ListSince(ctx, 0, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("ListSince() = %v, %v", got, err)
	}
	ev := got[0].Event
	if got[0].Seq != s1 || ev.ID != "a" || ev.Type != "pr.opened" || ev.RepositoryID != 1 || ev.RepositoryName != "acme/web" ||
		ev.PullRequestID != 7 || ev.PRNumber != 3 || ev.ActorLogin != "octocat" || ev.Params["title"] != "feat: x" || !ev.OccurredAt.Equal(at) {
		t.Fatalf("event did not round-trip: %#v", got[0])
	}
}

func TestActivityLog_ListSinceReturnsOnlyLaterEventsInOrderAndHonoursLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := persistence.NewActivityLog(openInMemoryTestDB(t))
	now := time.Now().UTC()
	var seqs []int64
	for _, id := range []string{"a", "b", "c"} {
		s, _ := log.Append(ctx, activity(id, 1, now))
		seqs = append(seqs, s)
	}

	after, _ := log.ListSince(ctx, seqs[0], 10)
	if len(after) != 2 || after[0].Event.ID != "b" || after[1].Event.ID != "c" {
		t.Fatalf("expected b, c after the first, got %#v", after)
	}
	limited, _ := log.ListSince(ctx, 0, 1)
	if len(limited) != 1 || limited[0].Event.ID != "a" {
		t.Fatalf("expected only the oldest with limit 1, got %#v", limited)
	}
	latest, err := log.LatestSeq(ctx)
	if err != nil || latest != seqs[2] {
		t.Fatalf("LatestSeq() = %d, %v; want %d", latest, err, seqs[2])
	}
}

func TestActivityLog_LatestSeqIsZeroWhenEmpty(t *testing.T) {
	t.Parallel()
	got, err := persistence.NewActivityLog(openInMemoryTestDB(t)).LatestSeq(context.Background())
	if err != nil || got != 0 {
		t.Fatalf("LatestSeq() = %d, %v; want 0", got, err)
	}
}

func TestActivityLog_RecentIsScopedToRepositoriesNewestFirstAndLimited(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := persistence.NewActivityLog(openInMemoryTestDB(t))
	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	_, _ = log.Append(ctx, activity("r1-old", 1, base))
	_, _ = log.Append(ctx, activity("r2-mid", 2, base.Add(time.Minute)))
	_, _ = log.Append(ctx, activity("r3-hidden", 3, base.Add(2*time.Minute)))
	_, _ = log.Append(ctx, activity("r1-new", 1, base.Add(3*time.Minute)))

	got, err := log.Recent(ctx, []uint64{1, 2}, 10)
	if err != nil {
		t.Fatalf("Recent() error: %v", err)
	}
	ids := []string{}
	for _, ev := range got {
		ids = append(ids, ev.ID)
	}
	want := []string{"r1-new", "r2-mid", "r1-old"}
	if len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("Recent() = %v, want %v (repository 3 must never appear)", ids, want)
	}

	limited, _ := log.Recent(ctx, []uint64{1, 2}, 2)
	if len(limited) != 2 || limited[0].ID != "r1-new" || limited[1].ID != "r2-mid" {
		t.Fatalf("limit must keep the newest, got %#v", limited)
	}
	none, _ := log.Recent(ctx, nil, 10)
	if len(none) != 0 {
		t.Fatalf("no repositories means no events, got %#v", none)
	}
}

func TestActivityLog_PruneDeletesOnlyOlderEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := persistence.NewActivityLog(openInMemoryTestDB(t))
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	_, _ = log.Append(ctx, activity("old", 1, base))
	_, _ = log.Append(ctx, activity("new", 1, base.Add(48*time.Hour)))

	n, err := log.Prune(ctx, base.Add(24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("Prune() = %d, %v; want 1", n, err)
	}
	left, _ := log.ListSince(ctx, 0, 10)
	if len(left) != 1 || left[0].Event.ID != "new" {
		t.Fatalf("expected only the new event, got %#v", left)
	}
}

func TestActivityLog_HonoursCancelledContext(t *testing.T) {
	t.Parallel()
	log := persistence.NewActivityLog(openInMemoryTestDB(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := log.Append(ctx, activity("a", 1, time.Now())); err == nil {
		t.Fatal("Append() must fail on a cancelled context")
	}
	if _, err := log.Recent(ctx, []uint64{1}, 5); err == nil {
		t.Fatal("Recent() must fail on a cancelled context")
	}
}
