package triage_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/triage"
)

type fakeReviewStore struct {
	byPR map[uint64][]entity.PullRequestReview
	err  error
}

func (f *fakeReviewStore) UpsertReview(_ context.Context, r entity.PullRequestReview) (bool, error) {
	f.byPR[r.PullRequestID] = append(f.byPR[r.PullRequestID], r)
	return true, nil
}

func (f *fakeReviewStore) ListReviews(_ context.Context, ids []uint64) (map[uint64][]entity.PullRequestReview, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[uint64][]entity.PullRequestReview{}
	for _, id := range ids {
		if v, ok := f.byPR[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

func (f turnFixture) reviewed(prID uint64, login string, state entity.ReviewState, sha string) {
	id := int64(len(f.reviews.byPR[prID]) + 1)
	f.reviews.byPR[prID] = append(f.reviews.byPR[prID], entity.PullRequestReview{
		GitHubReviewID: int64(prID)*100 + id, PullRequestID: prID, ReviewerLogin: login, State: state, CommitSHA: sha,
		SubmittedAt: time.Date(2026, 11, 10, 9, int(id), 0, 0, time.UTC),
	})
}

func TestTurn_ReviewsMoveAPRAwayFromMyTurn(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1), openPR(2), openPR(3), openPR(4))
	for id := uint64(1); id <= 4; id++ {
		f.changes(id, "sha1", "src/app/cart.ts")
	}
	f.reviewed(1, "murilo", entity.ReviewApproved, "sha1")         // I approved this commit
	f.reviewed(2, "bia", entity.ReviewApproved, "sha1")            // a teammate approved this commit
	f.reviewed(3, "murilo", entity.ReviewChangesRequested, "sha1") // I asked for changes, the author has not replied
	f.reviewed(4, "alice", entity.ReviewApproved, "sha1")          // the author "approving" their own PR

	snap := f.snapshot(t)
	if got := ids(snap.InboxWaitingOnMe); !slices.Equal(got, []uint64{4}) {
		t.Fatalf("my turn = %v, want only PR 4", got)
	}
	if got := ids(snap.InboxDimmed); !slices.Equal(got, []uint64{1, 2, 3}) {
		t.Fatalf("dimmed = %v, want PRs 1, 2 and 3 (mine, but not my move)", got)
	}
	states := map[uint64]entity.TurnState{}
	for _, it := range snap.InboxDimmed {
		states[it.PullRequest.ID] = it.UserState.TurnState
	}
	want := map[uint64]entity.TurnState{1: entity.TurnStateApprovedByMe, 2: entity.TurnStateTeammateApproved, 3: entity.TurnStateWaitingOnAuthor}
	for id, w := range want {
		if states[id] != w {
			t.Errorf("PR %d turn state = %q, want %q", id, states[id], w)
		}
	}
	for _, it := range snap.InboxDimmed {
		if it.PullRequest.ID == 2 && it.UserState.ApprovedByLogin != "bia" {
			t.Errorf("the badge names who approved: %+v", it.UserState)
		}
	}
	if snap.Seats[0].WaitingOnMeCount != 1 {
		t.Errorf("the seat counts only the PR that is my turn: %+v", snap.Seats[0])
	}
}

func TestTurn_ANewCommitReturnsThePRToMe(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1), openPR(2))
	f.changes(1, "sha1", "src/app/cart.ts")
	f.changes(2, "sha1", "src/app/cart.ts")
	f.reviewed(1, "murilo", entity.ReviewApproved, "sha1")
	f.reviewed(2, "murilo", entity.ReviewChangesRequested, "sha1")
	if got := ids(f.snapshot(t).InboxWaitingOnMe); len(got) != 0 {
		t.Fatalf("both reviewed: nothing is my turn, got %v", got)
	}

	// The author pushes: the reviews were of the old commit.
	for i := range f.prs.prs {
		f.prs.prs[i].HeadSHA = "sha2"
	}
	f.changes(1, "sha2", "src/app/cart.ts")
	f.changes(2, "sha2", "src/app/cart.ts")
	if got := ids(f.snapshot(t).InboxWaitingOnMe); !slices.Equal(got, []uint64{1, 2}) {
		t.Fatalf("a new commit needs a new review: my turn = %v, want 1 and 2", got)
	}
}

func TestTurn_ReviewsOfPRsIDoNotOwnChangeNothing(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1))
	f.changes(1, "sha1", "docs/guide.md") // bia's files
	f.reviewed(1, "bia", entity.ReviewApproved, "sha1")

	snap := f.snapshot(t)
	if len(snap.InboxWaitingOnMe)+len(snap.InboxDimmed) != 0 {
		t.Fatalf("a PR I do not own never reaches my inbox: %+v", snap)
	}
}

func TestTurn_FailedReviewReadIsAnError(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1))
	f.changes(1, "sha1", "src/app/cart.ts")
	boom := errors.New("db down")
	f.reviews.err = boom
	if _, err := f.uc.GetWorkspaceSnapshot(context.Background(), 1); !errors.Is(err, boom) {
		t.Fatalf("expected the store error (never guess a turn without reviews), got %v", err)
	}
}

func TestTurn_DependabotPRsCarryMyApprovalAndTheTeammateWhoApproved(t *testing.T) {
	t.Parallel()
	bump := func(id uint64) entity.PullRequest {
		return openPR(id, func(p *entity.PullRequest) { p.IsDependabot, p.AuthorLogin = true, "dependabot[bot]" })
	}
	f := newTurnFixture(bump(1), bump(2), bump(3), bump(4))
	f.reviewed(1, "murilo", entity.ReviewApproved, "sha1") // I approved the head
	f.reviewed(2, "murilo", entity.ReviewApproved, "old")  // I approved an older commit
	f.reviewed(3, "bia", entity.ReviewApproved, "sha1")    // a teammate approved the head
	// PR 4 has no review

	snap := f.snapshot(t)
	byID := map[uint64]triageItem{}
	for _, it := range snap.DependabotPRs {
		byID[it.PullRequest.ID] = triageItem{it.ApprovedByMe, it.ApprovedByLogin}
	}
	want := map[uint64]triageItem{1: {true, ""}, 2: {false, ""}, 3: {false, "bia"}, 4: {false, ""}}
	for id, w := range want {
		if byID[id] != w {
			t.Errorf("PR %d: approved by me / by = %+v, want %+v", id, byID[id], w)
		}
	}

	// A Dependabot PR still never enters the inbox.
	if len(snap.InboxWaitingOnMe)+len(snap.InboxDimmed) != 0 {
		t.Fatalf("bumps stay out of Minha Vez: %+v", snap)
	}
}

type triageItem struct {
	approvedByMe    bool
	approvedByLogin string
}

func TestSnapshot_CarriesWhoReviewedEachPR(t *testing.T) {
	t.Parallel()
	bump := openPR(3, func(p *entity.PullRequest) { p.IsDependabot, p.AuthorLogin = true, "dependabot[bot]" })
	f := newTurnFixture(openPR(1), openPR(2), bump, openPR(4))
	f.changes(1, "sha1", "docs/x.md")    // not mine: still visible in its drawer
	f.changes(2, "sha1", "src/app/x.ts") // mine
	f.changes(4, "sha1", "src/app/y.ts")
	f.reviewed(1, "bia", entity.ReviewApproved, "sha1")
	f.reviewed(2, "bia", entity.ReviewChangesRequested, "old")
	f.reviewed(2, "carol", entity.ReviewApproved, "sha1")
	f.reviewed(3, "bia", entity.ReviewApproved, "sha1")
	f.reviewed(2, "alice", entity.ReviewCommented, "sha1") // the author: not a review

	snap := f.snapshot(t)

	login := func(id uint64) []string {
		out := []string{}
		for _, d := range snap.Reviews[id] {
			out = append(out, d.Reviewer+":"+string(d.State))
		}
		return out
	}
	if got := login(1); len(got) != 1 || got[0] != "bia:APPROVED" {
		t.Errorf("PR 1 (not mine): %v", got)
	}
	if got := login(2); len(got) != 2 || got[0] != "carol:APPROVED" || got[1] != "bia:CHANGES_REQUESTED" {
		t.Errorf("PR 2 (current first, stale after): %v", got)
	}
	if snap.Reviews[2][1].Current {
		t.Errorf("a review of an older commit is not current: %+v", snap.Reviews[2][1])
	}
	if got := login(3); len(got) != 1 {
		t.Errorf("a Dependabot PR's reviewers are shown too: %v", got)
	}
	if _, ok := snap.Reviews[4]; ok {
		t.Errorf("a PR nobody reviewed has no entry: %+v", snap.Reviews)
	}
}

func TestSnapshot_WithoutAReviewStoreThereAreNoReviews(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1))
	uc := triage.NewUseCase(&fakeUserRepo{user: &entity.User{ID: 1, Login: "murilo"}}, f.repos, f.prs, &fakePublisher{}, nil, nil, nil)
	snap, err := uc.GetWorkspaceSnapshot(context.Background(), 1)
	if err != nil || snap.Reviews == nil || len(snap.Reviews) != 0 {
		t.Fatalf("an empty map, never null: %+v, %v", snap, err)
	}
}
