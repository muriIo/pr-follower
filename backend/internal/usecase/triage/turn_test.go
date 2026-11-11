package triage_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/triage"
)

type fakeFiles struct {
	byPR    map[uint64]entity.PRChangedFiles
	listErr error
}

func (f *fakeFiles) SaveChangedFiles(_ context.Context, files entity.PRChangedFiles) error {
	f.byPR[files.PullRequestID] = files
	return nil
}

func (f *fakeFiles) ListChangedFiles(_ context.Context, ids []uint64) (map[uint64]entity.PRChangedFiles, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := map[uint64]entity.PRChangedFiles{}
	for _, id := range ids {
		if v, ok := f.byPR[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

// fakeSyncer plays GitHub for the load-time fallback: it knows the files of some PRs.
type fakeSyncer struct {
	store    *fakeFiles
	github   map[uint64][]entity.ChangedFile
	err      error
	asUser   []uint64 // PR ids synced with the reader's token
	onSynced func()
}

func (f *fakeSyncer) SyncWithToken(context.Context, string, entity.Repository, entity.PullRequest) error {
	return nil
}
func (f *fakeSyncer) SyncAsFollower(context.Context, entity.Repository, entity.PullRequest) error {
	return nil
}
func (f *fakeSyncer) SyncAsUser(_ context.Context, _ uint64, _ entity.Repository, pr entity.PullRequest) error {
	f.asUser = append(f.asUser, pr.ID)
	if f.onSynced != nil {
		f.onSynced()
	}
	if f.err != nil {
		return f.err
	}
	files, ok := f.github[pr.ID]
	if !ok {
		return entity.ErrNotFound
	}
	f.store.byPR[pr.ID] = entity.PRChangedFiles{PullRequestID: pr.ID, HeadSHA: pr.HeadSHA, Files: files}
	return nil
}

type turnFixture struct {
	uc     *triage.UseCase
	prs    *fakePRRepo
	repos  *fakeRepoRepo
	files  *fakeFiles
	syncer *fakeSyncer
}

func openPR(id uint64, mutate ...func(*entity.PullRequest)) entity.PullRequest {
	pr := entity.PullRequest{ID: id, RepositoryID: 10, RepositoryName: "acme/web", Number: int(id), Title: "t", AuthorLogin: "alice",
		State: entity.PRStateOpen, CIStatus: entity.CIStatusSuccess, MergeableState: entity.MergeableStateClean, HeadSHA: "sha1"}
	for _, m := range mutate {
		m(&pr)
	}
	return pr
}

// murilo owns /src/app/ (by login) and /infra/ (through his team); bia owns /docs/.
func newTurnFixture(prs ...entity.PullRequest) turnFixture {
	repos := &fakeRepoRepo{repos: []entity.Repository{{ID: 10, Owner: "acme", Name: "web", FullName: "acme/web",
		CodeownersStatus: entity.CodeownersPresent,
		CachedCodeownersRules: []entity.CodeownerRule{
			{Pattern: "/src/app/", Owners: []string{"@murilo"}},
			{Pattern: "/infra/", Owners: []string{"@acme/platform"}},
			{Pattern: "/docs/", Owners: []string{"@bia"}},
		}}}}
	prRepo := &fakePRRepo{prs: prs, states: map[uint64]entity.UserPRState{}, analyses: map[uint64]*entity.DependabotAnalysis{}}
	files := &fakeFiles{byPR: map[uint64]entity.PRChangedFiles{}}
	syncer := &fakeSyncer{store: files, github: map[uint64][]entity.ChangedFile{}}
	users := &fakeUserRepo{user: &entity.User{ID: 1, Login: "murilo", Teams: []string{"@acme/platform"}}}
	uc := triage.NewUseCase(users, repos, prRepo, &fakePublisher{}, files, syncer)
	return turnFixture{uc: uc, prs: prRepo, repos: repos, files: files, syncer: syncer}
}

func (f turnFixture) changes(prID uint64, sha string, paths ...string) {
	files := make([]entity.ChangedFile, 0, len(paths))
	for _, p := range paths {
		files = append(files, entity.ChangedFile{Path: p, Additions: 1})
	}
	f.files.byPR[prID] = entity.PRChangedFiles{PullRequestID: prID, HeadSHA: sha, Files: files}
}

func (f turnFixture) snapshot(t *testing.T) *triage.WorkspaceSnapshot {
	t.Helper()
	snap, err := f.uc.GetWorkspaceSnapshot(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetWorkspaceSnapshot() error: %v", err)
	}
	return snap
}

func ids(items []triage.InboxItem) []uint64 {
	out := []uint64{}
	for _, it := range items {
		out = append(out, it.PullRequest.ID)
	}
	slices.Sort(out)
	return out
}

func TestTurn_OnlyPRsTouchingMyFilesAreMyTurn(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1), openPR(2), openPR(3))
	f.changes(1, "sha1", "src/app/cart.ts", "docs/readme.md") // mine by login (one of two files)
	f.changes(2, "sha1", "infra/deploy.yml")                  // mine through my team
	f.changes(3, "sha1", "docs/guide.md")                     // bia's

	snap := f.snapshot(t)
	if got := ids(snap.InboxWaitingOnMe); !slices.Equal(got, []uint64{1, 2}) {
		t.Fatalf("my turn = %v, want PRs 1 and 2", got)
	}
	if len(snap.InboxDimmed) != 0 || len(snap.OwnershipPendingPRIDs) != 0 {
		t.Fatalf("a PR that touches none of my files is not in my inbox at all: dimmed=%v pending=%v", ids(snap.InboxDimmed), snap.OwnershipPendingPRIDs)
	}
	for _, item := range snap.InboxWaitingOnMe {
		if item.UserState.TurnState != entity.TurnStateWaitingOnMe || item.UserState.UserID != 1 || item.UserState.PullRequestID != item.PullRequest.ID {
			t.Fatalf("unexpected derived state: %+v", item.UserState)
		}
	}
	owned := snap.InboxWaitingOnMe[0].UserState.OwnedFilesChanged
	if len(owned) != 1 || owned[0].Path != "src/app/cart.ts" {
		t.Fatalf("only the files I own are listed as mine: %+v", owned)
	}
	seat := snap.Seats[0]
	if seat.OpenPRCount != 3 || seat.WaitingOnMeCount != 2 || len(seat.PullRequests) != 3 {
		t.Fatalf("every open PR stays in the seat: %+v", seat)
	}
	if len(f.syncer.asUser) != 0 {
		t.Fatalf("files already stored for the head commit are not read again: %v", f.syncer.asUser)
	}
}

func TestTurn_NeverClaimsATurnThatWasNotEvaluated(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1), openPR(2), openPR(3))
	// The old default: a stored WAITING_ON_ME row, written before anything checked the files.
	f.prs.states[1] = entity.UserPRState{PullRequestID: 1, UserID: 1, TurnState: entity.TurnStateWaitingOnMe}
	f.changes(2, "old-sha", "src/app/cart.ts") // files of a previous commit prove nothing about this one
	f.syncer.err = errors.New("github is down")

	snap := f.snapshot(t)
	if len(snap.InboxWaitingOnMe) != 0 || len(snap.InboxDimmed) != 0 {
		t.Fatalf("no turn without evaluated files: waiting=%v dimmed=%v", ids(snap.InboxWaitingOnMe), ids(snap.InboxDimmed))
	}
	slices.Sort(snap.OwnershipPendingPRIDs)
	if !slices.Equal(snap.OwnershipPendingPRIDs, []uint64{1, 2, 3}) {
		t.Fatalf("pending = %v, want 1, 2 and 3", snap.OwnershipPendingPRIDs)
	}
	if snap.Seats[0].WaitingOnMeCount != 0 || snap.Seats[0].OpenPRCount != 3 {
		t.Fatalf("unexpected seat: %+v", snap.Seats[0])
	}
}

func TestTurn_ReadsMissingFilesWithTheReadersTokenWithinABudget(t *testing.T) {
	t.Parallel()
	var prs []entity.PullRequest
	for id := uint64(1); id <= 13; id++ {
		prs = append(prs, openPR(id))
	}
	f := newTurnFixture(prs...)
	for id := uint64(1); id <= 13; id++ {
		f.syncer.github[id] = []entity.ChangedFile{{Path: "src/app/x.ts"}}
	}

	snap := f.snapshot(t)
	if len(f.syncer.asUser) != triage.MaxOwnershipSyncsPerLoad {
		t.Fatalf("one load reads at most %d PRs from GitHub, read %d", triage.MaxOwnershipSyncsPerLoad, len(f.syncer.asUser))
	}
	if len(snap.InboxWaitingOnMe) != triage.MaxOwnershipSyncsPerLoad || len(snap.OwnershipPendingPRIDs) != 13-triage.MaxOwnershipSyncsPerLoad {
		t.Fatalf("waiting=%d pending=%d", len(snap.InboxWaitingOnMe), len(snap.OwnershipPendingPRIDs))
	}

	// The next load finishes the rest; a third reads nothing.
	snap = f.snapshot(t)
	if len(snap.InboxWaitingOnMe) != 13 || len(snap.OwnershipPendingPRIDs) != 0 || len(f.syncer.asUser) != 13 {
		t.Fatalf("second load: waiting=%d pending=%d reads=%d", len(snap.InboxWaitingOnMe), len(snap.OwnershipPendingPRIDs), len(f.syncer.asUser))
	}
	_ = f.snapshot(t)
	if len(f.syncer.asUser) != 13 {
		t.Fatalf("evaluated PRs are not read again: %d reads", len(f.syncer.asUser))
	}
}

func TestTurn_GateAndSnoozeDimMyPRs(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(
		openPR(1, func(p *entity.PullRequest) { p.IsDraft = true }),
		openPR(2, func(p *entity.PullRequest) { p.CIStatus = entity.CIStatusFailure }),
		openPR(3, func(p *entity.PullRequest) { p.CIStatus = entity.CIStatusPending }),
		openPR(4, func(p *entity.PullRequest) { p.MergeableState = entity.MergeableStateConflicting }),
		openPR(5),
		openPR(6),
		openPR(7, func(p *entity.PullRequest) { p.CIStatus = entity.CIStatusNone }), // repository without CI
		openPR(8, func(p *entity.PullRequest) { p.IsDraft = true }),                 // closed gate, not mine
	)
	for id := uint64(1); id <= 7; id++ {
		f.changes(id, "sha1", "src/app/x.ts")
	}
	f.changes(8, "sha1", "docs/x.md")
	f.prs.states[5] = entity.UserPRState{PullRequestID: 5, UserID: 1, TurnState: entity.TurnStateSnoozed,
		SnoozedUntilCondition: entity.SnoozeConditionNextCommit, SnoozedAtSHA: "sha1"}
	// Snoozed until the next commit, and that commit arrived: back to my turn.
	f.prs.states[6] = entity.UserPRState{PullRequestID: 6, UserID: 1, TurnState: entity.TurnStateSnoozed,
		SnoozedUntilCondition: entity.SnoozeConditionNextCommit, SnoozedAtSHA: "older"}

	snap := f.snapshot(t)
	if got := ids(snap.InboxWaitingOnMe); !slices.Equal(got, []uint64{6, 7}) {
		t.Fatalf("my turn = %v, want 6 and 7", got)
	}
	if got := ids(snap.InboxDimmed); !slices.Equal(got, []uint64{1, 2, 3, 4, 5}) {
		t.Fatalf("dimmed = %v, want 1..5", got)
	}
	states := map[uint64]entity.TurnState{}
	for _, item := range snap.InboxDimmed {
		states[item.PullRequest.ID] = item.UserState.TurnState
		if item.UserState.TurnState == entity.TurnStateHiddenGate && len(item.UserState.OwnedFilesChanged) != 1 {
			t.Fatalf("a gated PR #%d still lists my files: %+v", item.PullRequest.ID, item.UserState)
		}
	}
	for id := uint64(1); id <= 4; id++ {
		if states[id] != entity.TurnStateHiddenGate {
			t.Errorf("PR %d: expected HIDDEN_GATE, got %q", id, states[id])
		}
	}
	if states[5] != entity.TurnStateSnoozed {
		t.Errorf("PR 5: expected SNOOZED, got %q", states[5])
	}
}

func TestTurn_RepositoriesWithoutCodeownersAssignNothing(t *testing.T) {
	t.Parallel()
	for name, status := range map[string]entity.CodeownersStatus{"absent": entity.CodeownersAbsent, "not checked": entity.CodeownersUnknown} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newTurnFixture(openPR(1))
			f.repos.repos[0].CodeownersStatus = status
			f.repos.repos[0].CachedCodeownersRules = nil
			f.syncer.github[1] = []entity.ChangedFile{{Path: "src/app/x.ts"}}

			snap := f.snapshot(t)
			if len(snap.InboxWaitingOnMe) != 0 || len(snap.InboxDimmed) != 0 || len(snap.OwnershipPendingPRIDs) != 0 {
				t.Fatalf("no CODEOWNERS, no turn and nothing pending: %+v", snap)
			}
			if len(f.syncer.asUser) != 0 {
				t.Fatal("the files are not read when no rule could match them")
			}
		})
	}
}

func TestTurn_KeepsWhatIDidInTheApp(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1), openPR(2))
	f.changes(1, "sha1", "docs/x.md") // not mine, but I approved it from the Seats
	f.prs.states[1] = entity.UserPRState{PullRequestID: 1, UserID: 1, TurnState: entity.TurnStateApprovedByMe}
	f.changes(2, "sha1", "src/app/x.ts")
	f.prs.states[2] = entity.UserPRState{PullRequestID: 2, UserID: 1, TurnState: entity.TurnStateApprovedByMe}

	snap := f.snapshot(t)
	if len(snap.InboxWaitingOnMe) != 0 || !slices.Equal(ids(snap.InboxDimmed), []uint64{1, 2}) {
		t.Fatalf("a PR I approved is dimmed, not my turn: waiting=%v dimmed=%v", ids(snap.InboxWaitingOnMe), ids(snap.InboxDimmed))
	}
	for _, item := range snap.InboxDimmed {
		if item.UserState.TurnState != entity.TurnStateApprovedByMe {
			t.Fatalf("expected APPROVED_BY_ME, got %+v", item.UserState)
		}
	}
}

func TestTurn_WithoutAFilesStoreEverythingIsPending(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1))
	uc := triage.NewUseCase(&fakeUserRepo{user: &entity.User{ID: 1, Login: "murilo"}}, f.repos, f.prs, &fakePublisher{}, nil, nil)

	snap, err := uc.GetWorkspaceSnapshot(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.InboxWaitingOnMe) != 0 || !slices.Equal(snap.OwnershipPendingPRIDs, []uint64{1}) {
		t.Fatalf("nothing can be evaluated: %+v", snap)
	}
}

func TestTurn_PropagatesFailuresAndCancellation(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1), openPR(2))
	f.files.listErr = errors.New("db down")
	if _, err := f.uc.GetWorkspaceSnapshot(context.Background(), 1); !errors.Is(err, f.files.listErr) {
		t.Fatalf("expected the store failure, got %v", err)
	}
	f.files.listErr = nil

	ctx, cancel := context.WithCancel(context.Background())
	f.syncer.onSynced = cancel
	if _, err := f.uc.GetWorkspaceSnapshot(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(f.syncer.asUser) != 1 {
		t.Fatalf("a canceled load stops reading from GitHub, read %d", len(f.syncer.asUser))
	}
}
