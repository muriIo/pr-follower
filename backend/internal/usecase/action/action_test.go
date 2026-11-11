package action_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/action"
)

type fakePRRepo struct {
	prs    map[uint64]*entity.PullRequest
	states map[uint64]*entity.UserPRState
}

func (f *fakePRRepo) Upsert(_ context.Context, pr *entity.PullRequest) error {
	f.prs[pr.ID] = pr
	return nil
}
func (f *fakePRRepo) GetByID(_ context.Context, id uint64) (*entity.PullRequest, error) {
	if p, ok := f.prs[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) GetByRepoAndNumber(_ context.Context, _ uint64, _ int) (*entity.PullRequest, error) {
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) ListOpenByRepoIDs(_ context.Context, _ []uint64) ([]entity.PullRequest, error) {
	return nil, nil
}
func (f *fakePRRepo) SaveAnalysis(_ context.Context, _ *entity.DependabotAnalysis) error { return nil }
func (f *fakePRRepo) GetAnalysisByPRAndSHA(_ context.Context, _ uint64, _ string) (*entity.DependabotAnalysis, error) {
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) UpsertUserPRState(_ context.Context, s *entity.UserPRState) error {
	f.states[s.PullRequestID] = s
	return nil
}
func (f *fakePRRepo) GetUserPRState(_ context.Context, prID, _ uint64) (*entity.UserPRState, error) {
	if s, ok := f.states[prID]; ok {
		return s, nil
	}
	return nil, entity.ErrNotFound
}
func (f *fakePRRepo) ListUserPRStates(_ context.Context, _ uint64, _ []uint64) (map[uint64]entity.UserPRState, error) {
	return nil, nil
}

type fakeRepoRepo struct{}

func (f *fakeRepoRepo) Upsert(_ context.Context, _ *entity.Repository) error { return nil }
func (f *fakeRepoRepo) GetByID(_ context.Context, id uint64) (*entity.Repository, error) {
	return &entity.Repository{ID: id, Owner: "arvore", Name: "hive-web", FullName: "arvore/hive-web"}, nil
}
func (f *fakeRepoRepo) GetByGitHubRepoID(_ context.Context, _ int64) (*entity.Repository, error) {
	return nil, entity.ErrNotFound
}
func (f *fakeRepoRepo) ListFollowedByUser(_ context.Context, _ uint64) ([]entity.Repository, error) {
	return nil, nil
}
func (f *fakeRepoRepo) SetUserFollow(_ context.Context, _, _ uint64, _ bool) error { return nil }

type fakePub struct{ events []entity.ActivityEvent }

func (f *fakePub) Publish(_ context.Context, ev entity.ActivityEvent) error {
	f.events = append(f.events, ev)
	return nil
}

type fakeUsers struct{}

func (fakeUsers) Upsert(context.Context, *entity.User) error { return nil }
func (fakeUsers) GetByID(_ context.Context, id uint64) (*entity.User, error) {
	switch id {
	case 1:
		return &entity.User{ID: 1, Login: "octocat"}, nil
	case 2:
		return &entity.User{ID: 2, Login: "bia"}, nil
	}
	return nil, entity.ErrNotFound
}
func (fakeUsers) GetByGitHubID(context.Context, int64) (*entity.User, error) {
	return nil, entity.ErrNotFound
}

type fakeTokens struct{ err error }

func (f fakeTokens) AccessToken(_ context.Context, userID uint64) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "ghu_" + string(rune('0'+userID)), nil
}

// fakeGitHub plays GitHub: it holds each PR's live state and applies the writes to it.
type fakeGitHub struct {
	live    map[int]*entity.RemotePullRequestState
	reviews map[int][]entity.PullRequestReview
	nextID  int64

	// failures
	approveErr, mergeErr, commentErr error
	applyThenFail                    bool // the write is applied, then the answer is lost
	stateErr, reviewsErr             error
	onMerge                          func(prNumber int)

	// what the use case did
	writeTokens []string
	approved    []int
	merged      []int
	comments    []string
	stateReads  int
}

func newFakeGitHub(prs ...int) *fakeGitHub {
	f := &fakeGitHub{live: map[int]*entity.RemotePullRequestState{}, reviews: map[int][]entity.PullRequestReview{}}
	for _, n := range prs {
		f.live[n] = &entity.RemotePullRequestState{Open: true, HeadSHA: "sha-" + string(rune('0'+n%10)), AuthorLogin: "alice"}
	}
	return f
}

func (f *fakeGitHub) GetFileContent(context.Context, entity.Repository, string, string) ([]byte, error) {
	return nil, nil
}
func (f *fakeGitHub) ListPRChangedFiles(context.Context, entity.Repository, int) ([]entity.ChangedFile, error) {
	return nil, nil
}
func (f *fakeGitHub) StreamRepoTarball(context.Context, entity.Repository, string) (io.ReadCloser, error) {
	return nil, nil
}

func (f *fakeGitHub) ApprovePullRequest(ctx context.Context, token string, _ entity.Repository, n int, _ string) (entity.PullRequestReview, error) {
	if err := ctx.Err(); err != nil {
		return entity.PullRequestReview{}, context.Cause(ctx)
	}
	f.writeTokens = append(f.writeTokens, token)
	f.approved = append(f.approved, n)
	if f.approveErr != nil && !f.applyThenFail {
		return entity.PullRequestReview{}, f.approveErr
	}
	f.nextID++
	review := entity.PullRequestReview{GitHubReviewID: 1000 + f.nextID, ReviewerLogin: loginOf(token), State: entity.ReviewApproved,
		CommitSHA: f.live[n].HeadSHA, SubmittedAt: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}
	f.reviews[n] = append(f.reviews[n], review)
	if f.approveErr != nil {
		return entity.PullRequestReview{}, f.approveErr
	}
	return review, nil
}

func (f *fakeGitHub) MergePullRequest(ctx context.Context, token string, _ entity.Repository, n int, _ string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	f.writeTokens = append(f.writeTokens, token)
	f.merged = append(f.merged, n)
	if f.mergeErr != nil && !f.applyThenFail {
		return f.mergeErr
	}
	f.live[n].Open, f.live[n].Merged = false, true
	if f.onMerge != nil {
		f.onMerge(n)
	}
	return f.mergeErr
}

func (f *fakeGitHub) CreatePRComment(ctx context.Context, token string, _ entity.Repository, _ int, comment string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	f.writeTokens = append(f.writeTokens, token)
	if f.commentErr != nil {
		return f.commentErr
	}
	f.comments = append(f.comments, comment)
	return nil
}

func (f *fakeGitHub) GetPullRequest(_ context.Context, _ string, _, _ string, n int) (entity.RemotePullRequestState, error) {
	f.stateReads++
	if f.stateErr != nil {
		return entity.RemotePullRequestState{}, f.stateErr
	}
	live, ok := f.live[n]
	if !ok {
		return entity.RemotePullRequestState{}, entity.ErrNotFound
	}
	return *live, nil
}

func (f *fakeGitHub) ListPullRequestReviews(_ context.Context, _ string, _, _ string, n int) ([]entity.PullRequestReview, error) {
	if f.reviewsErr != nil {
		return nil, f.reviewsErr
	}
	return f.reviews[n], nil
}

func loginOf(token string) string {
	if token == "ghu_2" {
		return "bia"
	}
	return "octocat"
}

type fakeReviewStore struct{ saved []entity.PullRequestReview }

func (f *fakeReviewStore) UpsertReview(_ context.Context, r entity.PullRequestReview) (bool, error) {
	f.saved = append(f.saved, r)
	return true, nil
}
func (f *fakeReviewStore) ListReviews(context.Context, []uint64) (map[uint64][]entity.PullRequestReview, error) {
	return nil, nil
}

type fixture struct {
	uc      *action.UseCase
	prs     *fakePRRepo
	gh      *fakeGitHub
	reviews *fakeReviewStore
	pub     *fakePub
}

func newFixture(tokens fakeTokens, prs ...*entity.PullRequest) fixture {
	repo := &fakePRRepo{prs: map[uint64]*entity.PullRequest{}, states: map[uint64]*entity.UserPRState{}}
	numbers := []int{}
	for _, pr := range prs {
		repo.prs[pr.ID] = pr
		numbers = append(numbers, pr.Number)
	}
	gh := newFakeGitHub(numbers...)
	reviews, pub := &fakeReviewStore{}, &fakePub{}
	uc := action.NewUseCase(action.Deps{PRs: repo, Repos: &fakeRepoRepo{}, Users: fakeUsers{}, Tokens: tokens,
		GitHub: gh, State: gh, Reviews: reviews, Pub: pub})
	return fixture{uc: uc, prs: repo, gh: gh, reviews: reviews, pub: pub}
}

func openPR(id uint64, number int, author string) *entity.PullRequest {
	return &entity.PullRequest{ID: id, RepositoryID: 10, Number: number, AuthorLogin: author, State: entity.PRStateOpen, HeadSHA: "sha-" + string(rune('0'+number%10))}
}

var ctx = context.Background()

func TestApprovePR_ActsAsTheUserAndStoresTheReviewGitHubRecorded(t *testing.T) {
	t.Parallel()
	f := newFixture(fakeTokens{}, openPR(1, 11, "alice"))

	if err := f.uc.ApprovePR(ctx, 1, 1, "LGTM"); err != nil {
		t.Fatalf("ApprovePR() error: %v", err)
	}
	if len(f.gh.approved) != 1 || f.gh.writeTokens[0] != "ghu_1" {
		t.Fatalf("expected one approval with the user's token: %v %v", f.gh.approved, f.gh.writeTokens)
	}
	if len(f.reviews.saved) != 1 || f.reviews.saved[0].PullRequestID != 1 || f.reviews.saved[0].State != entity.ReviewApproved {
		t.Fatalf("the review GitHub recorded is stored against the PR: %+v", f.reviews.saved)
	}
	if len(f.pub.events) != 1 || f.pub.events[0].Type != "pr.approved" || f.pub.events[0].ActorLogin != "octocat" {
		t.Fatalf("expected one pr.approved event by octocat: %+v", f.pub.events)
	}
	if len(f.prs.states) != 0 {
		t.Fatalf("approval comes from the review GitHub records, not from a stored flag: %+v", f.prs.states)
	}
}

func TestApprovePR_NobodyApprovesTheirOwnPullRequest(t *testing.T) {
	t.Parallel()
	for name, author := range map[string]string{"same login": "octocat", "other letter case": "OctoCat"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(fakeTokens{}, openPR(1, 11, author))
			if err := f.uc.ApprovePR(ctx, 1, 1, ""); !errors.Is(err, entity.ErrOwnPullRequest) {
				t.Fatalf("expected ErrOwnPullRequest, got %v", err)
			}
			if len(f.gh.approved) != 0 || len(f.pub.events) != 0 || f.gh.stateReads != 0 {
				t.Fatalf("nothing may reach GitHub for an own PR: approved=%v reads=%d events=%v", f.gh.approved, f.gh.stateReads, f.pub.events)
			}
		})
	}

	// The stored author is stale, GitHub says the user wrote it: still refused, before writing.
	f := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	f.gh.live[11].AuthorLogin = "octocat"
	if err := f.uc.ApprovePR(ctx, 1, 1, ""); !errors.Is(err, entity.ErrOwnPullRequest) || len(f.gh.approved) != 0 {
		t.Fatalf("GitHub's author wins: err=%v approved=%v", err, f.gh.approved)
	}

	// Someone else can approve it.
	g := newFixture(fakeTokens{}, openPR(1, 11, "octocat"))
	if err := g.uc.ApprovePR(ctx, 2, 1, ""); err != nil || len(g.gh.approved) != 1 {
		t.Fatalf("a teammate may approve octocat's PR: err=%v approved=%v", err, g.gh.approved)
	}
}

func TestApprovePR_AnApprovalAlreadyOnTheHeadIsNotRepeated(t *testing.T) {
	t.Parallel()
	f := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	head := f.gh.live[11].HeadSHA
	f.gh.reviews[11] = []entity.PullRequestReview{{GitHubReviewID: 7, ReviewerLogin: "octocat", State: entity.ReviewApproved, CommitSHA: head,
		SubmittedAt: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)}}

	if err := f.uc.ApprovePR(ctx, 1, 1, ""); err != nil {
		t.Fatalf("ApprovePR() error: %v", err)
	}
	if len(f.gh.approved) != 0 || len(f.pub.events) != 0 {
		t.Fatalf("already approved on GitHub: no write, no event: approved=%v events=%v", f.gh.approved, f.pub.events)
	}
	if len(f.reviews.saved) != 1 || f.reviews.saved[0].GitHubReviewID != 7 {
		t.Fatalf("the review GitHub already has is recorded: %+v", f.reviews.saved)
	}

	// An approval of an older commit, or one later replaced by "changes requested", does not count.
	for name, reviews := range map[string][]entity.PullRequestReview{
		"older commit": {{GitHubReviewID: 8, ReviewerLogin: "octocat", State: entity.ReviewApproved, CommitSHA: "old"}},
		"changes requested since": {
			{GitHubReviewID: 9, ReviewerLogin: "octocat", State: entity.ReviewApproved, CommitSHA: head, SubmittedAt: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)},
			{GitHubReviewID: 10, ReviewerLogin: "octocat", State: entity.ReviewChangesRequested, CommitSHA: head, SubmittedAt: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)},
		},
		"someone else approved": {{GitHubReviewID: 11, ReviewerLogin: "bia", State: entity.ReviewApproved, CommitSHA: head}},
	} {
		g := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
		g.gh.reviews[11] = reviews
		if err := g.uc.ApprovePR(ctx, 1, 1, ""); err != nil || len(g.gh.approved) != 1 {
			t.Errorf("%s: my approval is still needed: err=%v approved=%v", name, err, g.gh.approved)
		}
	}
}

func TestApprovePR_RefusesWhatIsNotOpenAndWhatItCannotCheck(t *testing.T) {
	t.Parallel()
	f := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	f.gh.live[11].Open = false
	if err := f.uc.ApprovePR(ctx, 1, 1, ""); !errors.Is(err, entity.ErrConflict) || len(f.gh.approved) != 0 {
		t.Fatalf("a closed PR cannot be approved: err=%v approved=%v", err, f.gh.approved)
	}

	boom := errors.New("github is down")
	for name, mutate := range map[string]func(*fakeGitHub){
		"state unreadable":   func(g *fakeGitHub) { g.stateErr = boom },
		"reviews unreadable": func(g *fakeGitHub) { g.reviewsErr = boom },
	} {
		g := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
		mutate(g.gh)
		if err := g.uc.ApprovePR(ctx, 1, 1, ""); !errors.Is(err, boom) || len(g.gh.approved) != 0 {
			t.Errorf("%s: never act on a state that could not be checked: err=%v approved=%v", name, err, g.gh.approved)
		}
	}

	h := newFixture(fakeTokens{err: entity.ErrUnauthorized}, openPR(1, 11, "alice"))
	if err := h.uc.ApprovePR(ctx, 1, 1, ""); !errors.Is(err, entity.ErrUnauthorized) || h.gh.stateReads != 0 {
		t.Fatalf("a signed-out user cannot act: err=%v", err)
	}
	if err := h.uc.ApprovePR(ctx, 99, 1, ""); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("an unknown user cannot act: %v", err)
	}
}

func TestApprovePR_AnAmbiguousAnswerIsConfirmedAgainstGitHub(t *testing.T) {
	t.Parallel()
	// The approval was applied but the answer was lost: GitHub shows it, so it succeeded.
	applied := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	applied.gh.approveErr, applied.gh.applyThenFail = entity.ErrOutcomeUnknown, true
	if err := applied.uc.ApprovePR(ctx, 1, 1, ""); err != nil {
		t.Fatalf("a confirmed approval is a success: %v", err)
	}
	if len(applied.gh.approved) != 1 || len(applied.pub.events) != 1 || len(applied.reviews.saved) != 1 {
		t.Fatalf("one write, one event, one stored review: %v %v %v", applied.gh.approved, applied.pub.events, applied.reviews.saved)
	}

	// Not applied (or cannot tell): say so, and change nothing.
	lost := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	lost.gh.approveErr = entity.ErrOutcomeUnknown
	if err := lost.uc.ApprovePR(ctx, 1, 1, ""); !errors.Is(err, entity.ErrOutcomeUnknown) {
		t.Fatalf("expected ErrOutcomeUnknown, got %v", err)
	}
	if len(lost.gh.approved) != 1 || len(lost.pub.events) != 0 || len(lost.reviews.saved) != 0 {
		t.Fatalf("unconfirmed: nothing may be recorded or published, and no second write: %v", lost.pub.events)
	}

	cannotCheck := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	cannotCheck.gh.approveErr = entity.ErrOutcomeUnknown
	cannotCheck.gh.applyThenFail = true
	cannotCheck.gh.reviews[11] = nil
	after := &failingAfterWrite{fakeGitHub: cannotCheck.gh}
	uc := action.NewUseCase(action.Deps{PRs: cannotCheck.prs, Repos: &fakeRepoRepo{}, Users: fakeUsers{}, Tokens: fakeTokens{},
		GitHub: after, State: after, Reviews: cannotCheck.reviews, Pub: cannotCheck.pub})
	if err := uc.ApprovePR(ctx, 1, 1, ""); !errors.Is(err, entity.ErrOutcomeUnknown) {
		t.Fatalf("when the check itself fails the outcome stays unknown: %v", err)
	}

	// A definite refusal is passed on untouched, with nothing recorded.
	for _, refusal := range []error{entity.ErrForbidden, entity.ErrConflict, entity.ErrNotFound} {
		g := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
		g.gh.approveErr = refusal
		if err := g.uc.ApprovePR(ctx, 1, 1, ""); !errors.Is(err, refusal) || errors.Is(err, entity.ErrOutcomeUnknown) || len(g.pub.events) != 0 {
			t.Errorf("refusal %v: got %v, events %v", refusal, err, g.pub.events)
		}
	}
}

// failingAfterWrite reads fine before the write and fails every read after it.
type failingAfterWrite struct{ *fakeGitHub }

func (f *failingAfterWrite) ListPullRequestReviews(ctx context.Context, token, owner, name string, n int) ([]entity.PullRequestReview, error) {
	if len(f.approved) > 0 || len(f.merged) > 0 {
		return nil, errors.New("github is down")
	}
	return f.fakeGitHub.ListPullRequestReviews(ctx, token, owner, name, n)
}

func (f *failingAfterWrite) GetPullRequest(ctx context.Context, token, owner, name string, n int) (entity.RemotePullRequestState, error) {
	if len(f.approved) > 0 || len(f.merged) > 0 {
		return entity.RemotePullRequestState{}, errors.New("github is down")
	}
	return f.fakeGitHub.GetPullRequest(ctx, token, owner, name, n)
}

func TestMergePR_ActsAsTheUserAndRecordsTheMerge(t *testing.T) {
	t.Parallel()
	f := newFixture(fakeTokens{}, openPR(1, 11, "octocat")) // merging your own PR is allowed

	if err := f.uc.MergePR(ctx, 1, 1, ""); err != nil {
		t.Fatalf("MergePR() error: %v", err)
	}
	if len(f.gh.merged) != 1 || f.gh.writeTokens[0] != "ghu_1" {
		t.Fatalf("one merge with the user's token: %v %v", f.gh.merged, f.gh.writeTokens)
	}
	if f.prs.prs[1].State != entity.PRStateMerged {
		t.Fatalf("expected the PR stored as merged, got %q", f.prs.prs[1].State)
	}
	if len(f.pub.events) != 1 || f.pub.events[0].Type != "pr.merged" || f.pub.events[0].ActorLogin != "octocat" || f.pub.events[0].Params["method"] != "squash" {
		t.Fatalf("expected one pr.merged event by octocat with the squash method: %+v", f.pub.events)
	}
}

func TestMergePR_ConvergesWhenGitHubAlreadyMergedIt(t *testing.T) {
	t.Parallel()
	f := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	f.gh.live[11].Open, f.gh.live[11].Merged = false, true

	if err := f.uc.MergePR(ctx, 1, 1, "squash"); err != nil {
		t.Fatalf("merging a merged PR converges: %v", err)
	}
	if len(f.gh.merged) != 0 || len(f.pub.events) != 0 {
		t.Fatalf("no second merge, no event: %v %v", f.gh.merged, f.pub.events)
	}

	closed := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	closed.gh.live[11].Open = false
	if err := closed.uc.MergePR(ctx, 1, 1, ""); !errors.Is(err, entity.ErrConflict) || len(closed.gh.merged) != 0 {
		t.Fatalf("a PR closed without merging cannot be merged: err=%v", err)
	}
}

func TestMergePR_AnAmbiguousAnswerIsConfirmedAgainstGitHub(t *testing.T) {
	t.Parallel()
	applied := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	applied.gh.mergeErr, applied.gh.applyThenFail = entity.ErrOutcomeUnknown, true
	if err := applied.uc.MergePR(ctx, 1, 1, ""); err != nil || len(applied.pub.events) != 1 || applied.prs.prs[1].State != entity.PRStateMerged {
		t.Fatalf("a confirmed merge is a success: err=%v events=%v", err, applied.pub.events)
	}

	lost := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	lost.gh.mergeErr = entity.ErrOutcomeUnknown
	if err := lost.uc.MergePR(ctx, 1, 1, ""); !errors.Is(err, entity.ErrOutcomeUnknown) {
		t.Fatalf("expected ErrOutcomeUnknown, got %v", err)
	}
	if len(lost.pub.events) != 0 || lost.prs.prs[1].State != entity.PRStateOpen {
		t.Fatalf("unconfirmed: the PR stays open here and nothing is published: %+v", lost.pub.events)
	}

	notMergeable := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	notMergeable.gh.mergeErr = entity.ErrConflict
	if err := notMergeable.uc.MergePR(ctx, 1, 1, ""); !errors.Is(err, entity.ErrConflict) || len(notMergeable.pub.events) != 0 || notMergeable.prs.prs[1].State != entity.PRStateOpen {
		t.Fatalf("a refused merge changes nothing: err=%v", err)
	}
}

func TestBatchApproveAndMerge_StopsOnCancelAndRefusesOwnPRsBeforeWriting(t *testing.T) {
	t.Parallel()
	f := newFixture(fakeTokens{}, openPR(2, 12, "dependabot[bot]"), openPR(3, 13, "dependabot[bot]"))
	cctx, cancel := context.WithCancelCause(context.Background())
	f.gh.onMerge = func(n int) {
		if n == 12 {
			cancel(entity.ErrOperationCanceled)
		}
	}
	merged, err := f.uc.BatchApproveAndMerge(cctx, 1, []uint64{2, 3}, "squash")
	if !errors.Is(err, entity.ErrOperationCanceled) {
		t.Fatalf("expected ErrOperationCanceled on batch cancel, got %v", err)
	}
	if len(merged) != 1 || merged[0] != 2 {
		t.Fatalf("expected only PR 2 to complete before cancellation, got %v", merged)
	}

	// One of the PRs is the user's own: nothing is approved or merged, not even the others.
	g := newFixture(fakeTokens{}, openPR(2, 12, "dependabot[bot]"), openPR(3, 13, "octocat"))
	done, err := g.uc.BatchApproveAndMerge(ctx, 1, []uint64{2, 3}, "squash")
	if !errors.Is(err, entity.ErrOwnPullRequest) || len(done) != 0 {
		t.Fatalf("expected ErrOwnPullRequest and nothing done: err=%v done=%v", err, done)
	}
	if len(g.gh.approved) != 0 || len(g.gh.merged) != 0 {
		t.Fatalf("the batch must be refused before any write: approved=%v merged=%v", g.gh.approved, g.gh.merged)
	}
}

func TestRunDependabotCommand_CommentsAsTheUser(t *testing.T) {
	t.Parallel()
	f := newFixture(fakeTokens{}, openPR(2, 12, "dependabot[bot]"))

	if err := f.uc.RunDependabotCommand(ctx, 1, 2, "rebase"); err != nil {
		t.Fatalf("RunDependabotCommand() error: %v", err)
	}
	if len(f.gh.comments) != 1 || f.gh.comments[0] != "@dependabot rebase" || f.gh.writeTokens[0] != "ghu_1" {
		t.Fatalf("expected the command as the user: %v %v", f.gh.comments, f.gh.writeTokens)
	}
	if len(f.pub.events) != 1 || f.pub.events[0].Type != "dependabot.command" || f.pub.events[0].ActorLogin != "octocat" {
		t.Fatalf("unexpected events: %+v", f.pub.events)
	}

	if err := f.uc.RunDependabotCommand(ctx, 1, 2, "  "); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("an empty command is invalid input, got %v", err)
	}
	f.gh.commentErr = entity.ErrOutcomeUnknown
	f.pub.events = nil
	if err := f.uc.RunDependabotCommand(ctx, 1, 2, "recreate"); !errors.Is(err, entity.ErrOutcomeUnknown) || len(f.pub.events) != 0 {
		t.Fatalf("an unconfirmed comment is reported, not announced: err=%v events=%v", err, f.pub.events)
	}
	signedOut := newFixture(fakeTokens{err: entity.ErrUnauthorized}, openPR(2, 12, "dependabot[bot]"))
	if err := signedOut.uc.RunDependabotCommand(ctx, 1, 2, "rebase"); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("a signed-out user cannot comment: %v", err)
	}
}

func TestActions_HonorCancellation(t *testing.T) {
	t.Parallel()
	f := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := f.uc.ApprovePR(canceled, 1, 1, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("ApprovePR: %v", err)
	}
	if err := f.uc.MergePR(canceled, 1, 1, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("MergePR: %v", err)
	}
	if err := f.uc.RunDependabotCommand(canceled, 1, 1, "rebase"); !errors.Is(err, context.Canceled) {
		t.Errorf("RunDependabotCommand: %v", err)
	}
	if len(f.gh.writeTokens) != 0 {
		t.Errorf("a canceled request writes nothing: %v", f.gh.writeTokens)
	}
}
