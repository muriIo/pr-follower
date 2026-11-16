package triage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/triage"
)

// GitHub often sends mergeable_state "unknown" in the opening webhook and no event once it has
// computed it: a workspace load asks GitHub again for the PRs still unknown.

type fakeTokens struct{ err error }

func (f fakeTokens) AccessToken(context.Context, uint64) (string, error) { return "ghu_reader", f.err }

type fakeStateReader struct {
	states map[int]entity.RemotePullRequestState
	err    error
	calls  []int
}

func (f *fakeStateReader) GetPullRequest(_ context.Context, token, _, _ string, number int) (entity.RemotePullRequestState, error) {
	f.calls = append(f.calls, number)
	if token != "ghu_reader" {
		return entity.RemotePullRequestState{}, errors.New("wrong token")
	}
	return f.states[number], f.err
}
func (f *fakeStateReader) ListPullRequestReviews(context.Context, string, string, string, int) ([]entity.PullRequestReview, error) {
	return nil, nil
}
func (f *fakeStateReader) ListCommitCheckRuns(context.Context, string, string, string, string) ([]entity.CheckRun, error) {
	return nil, nil
}
func (f *fakeStateReader) ListCommitStatuses(context.Context, string, string, string, string) ([]entity.CommitStatus, error) {
	return nil, nil
}

func unknownMerge(p *entity.PullRequest) { p.MergeableState = entity.MergeableStateUnknown }

func withMergeState(f turnFixture, tokens fakeTokens, reader *fakeStateReader) *triage.UseCase {
	return triage.NewUseCase(&fakeUserRepo{user: &entity.User{ID: 1, Login: "murilo"}}, f.repos, f.prs, &fakePublisher{}, nil, nil, nil,
		triage.WithMergeStateRefresh(tokens, reader))
}

func seatPR(snap *triage.WorkspaceSnapshot, id uint64) entity.PullRequest {
	for _, seat := range snap.Seats {
		for _, pr := range seat.PullRequests {
			if pr.ID == id {
				return pr
			}
		}
	}
	return entity.PullRequest{}
}

func TestMergeState_AnUnknownStateIsAskedOfGitHubAndStored(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1, unknownMerge), openPR(2), openPR(3, unknownMerge, func(p *entity.PullRequest) { p.HeadSHA = "sha-old" }))
	reader := &fakeStateReader{states: map[int]entity.RemotePullRequestState{
		1: {Open: true, HeadSHA: "sha1", MergeableState: entity.MergeableStateBlocked},
		3: {Open: true, HeadSHA: "sha-new", MergeableState: entity.MergeableStateClean}, // GitHub has a newer commit
	}}

	snap, err := withMergeState(f, fakeTokens{}, reader).GetWorkspaceSnapshot(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetWorkspaceSnapshot() error: %v", err)
	}

	if len(reader.calls) != 2 {
		t.Errorf("only the PRs whose state is unknown are read, got %v", reader.calls)
	}
	if got := seatPR(snap, 1).MergeableState; got != entity.MergeableStateBlocked {
		t.Errorf("the load shows what GitHub says now: %s", got)
	}
	if stored, _ := f.prs.GetByID(context.Background(), 1); stored.MergeableState != entity.MergeableStateBlocked {
		t.Errorf("and stores it for every reader: %s", stored.MergeableState)
	}
	if stored, _ := f.prs.GetByID(context.Background(), 3); stored.MergeableState != entity.MergeableStateUnknown {
		t.Errorf("a state computed for another commit is never stored against this one: %s", stored.MergeableState)
	}
}

func TestMergeState_GitHubStillComputingOrFailingLeavesItUnknownAndTheLoadWorks(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1, unknownMerge))
	stillUnknown := &fakeStateReader{states: map[int]entity.RemotePullRequestState{1: {Open: true, HeadSHA: "sha1", MergeableState: entity.MergeableStateUnknown}}}
	if snap, err := withMergeState(f, fakeTokens{}, stillUnknown).GetWorkspaceSnapshot(context.Background(), 1); err != nil || seatPR(snap, 1).MergeableState != entity.MergeableStateUnknown {
		t.Fatalf("still computing: err=%v", err)
	}

	down := &fakeStateReader{err: entity.ErrGitHubUnavailable}
	if _, err := withMergeState(f, fakeTokens{}, down).GetWorkspaceSnapshot(context.Background(), 1); err != nil {
		t.Fatalf("GitHub failing must not fail the workspace (ADR-0013): %v", err)
	}

	noToken := &fakeStateReader{}
	if _, err := withMergeState(f, fakeTokens{err: entity.ErrGitHubTokenExpired}, noToken).GetWorkspaceSnapshot(context.Background(), 1); err != nil || len(noToken.calls) != 0 {
		t.Fatalf("without a token nothing is read and the load works: err=%v calls=%v", err, noToken.calls)
	}
}

func TestMergeState_OneLoadReadsABoundedNumberOfPRs(t *testing.T) {
	t.Parallel()
	prs := make([]entity.PullRequest, 0, triage.MaxMergeStateReadsPerLoad+5)
	for i := 1; i <= triage.MaxMergeStateReadsPerLoad+5; i++ {
		prs = append(prs, openPR(uint64(i), unknownMerge))
	}
	reader := &fakeStateReader{states: map[int]entity.RemotePullRequestState{}}
	if _, err := withMergeState(newTurnFixture(prs...), fakeTokens{}, reader).GetWorkspaceSnapshot(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if len(reader.calls) != triage.MaxMergeStateReadsPerLoad {
		t.Fatalf("read %d PRs, want %d", len(reader.calls), triage.MaxMergeStateReadsPerLoad)
	}
}
