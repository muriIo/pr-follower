package repoimport_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/repoimport"
)

type resyncWorld struct {
	uc     *repoimport.UseCase
	api    *fakeUserAPI
	prs    *fakePRs
	events *fakeEvents
	checks *fakeChecks
	repo   *entity.Repository
}

// followedWeb imports acme/web, whose open PRs are #7, #8 (Dependabot) and #9, and returns the world.
func followedWeb(t *testing.T) resyncWorld {
	t.Helper()
	uc, api, _, events, prs, checks := setupBackfill()
	twoOpenPRs(api)
	repo, err := uc.Import(context.Background(), userID, 20, 200)
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}
	events.events = nil // only what the resync announces matters here
	return resyncWorld{uc: uc, api: api, prs: prs, events: events, checks: checks, repo: repo}
}

func (w resyncWorld) pr(n int) entity.PullRequest { return w.prs.prs[[2]uint64{w.repo.ID, uint64(n)}] }

func (w resyncWorld) listOnly(numbers ...int) {
	keep := map[int]bool{}
	for _, n := range numbers {
		keep[n] = true
	}
	var open []entity.RemotePullRequest
	for _, p := range w.api.openPRs["acme/web"] {
		if keep[p.Number] {
			open = append(open, p)
		}
	}
	w.api.openPRs["acme/web"] = open
}

func TestResync_ClosesWhatGitHubNoLongerHasOpenAndSaysHow(t *testing.T) {
	t.Parallel()
	w := followedWeb(t)
	w.listOnly(7) // #8 was merged and #9 closed while the API was down
	w.api.pulls = map[string]entity.RemotePullRequestState{
		"acme/web#8": {Open: false, Merged: true, HeadSHA: "sha8"},
		"acme/web#9": {Open: false, Merged: false, HeadSHA: "sha9"},
	}

	res, err := w.uc.Resync(context.Background(), userID)
	if err != nil {
		t.Fatalf("Resync() error: %v", err)
	}
	if w.pr(8).State != entity.PRStateMerged || w.pr(9).State != entity.PRStateClosed || w.pr(7).State != entity.PRStateOpen {
		t.Fatalf("states after resync: #7 %q, #8 %q, #9 %q", w.pr(7).State, w.pr(8).State, w.pr(9).State)
	}
	if res.Repositories != 1 || res.Merged != 1 || res.Closed != 1 || len(res.Failed) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	types := map[string]int{}
	for _, e := range w.events.events {
		types[e.Type]++
		if e.RepositoryName != "acme/web" || e.PullRequestID == 0 {
			t.Errorf("each close names its repository and PR: %+v", e)
		}
		if e.ActorLogin != "octocat" {
			t.Errorf("the user who synced found it, so it is not news to them (ADR-0017): %+v", e)
		}
	}
	if types["pr.merged"] != 1 || types["pr.closed"] != 1 || len(w.events.events) != 2 {
		t.Fatalf("every close is announced, so other users' open drawers follow: %+v", w.events.events)
	}
}

func TestResync_NeverClosesAPRGitHubStillHasOpen(t *testing.T) {
	t.Parallel()
	w := followedWeb(t)
	w.listOnly(7) // the listing is capped: #9 is missing from it but is open on GitHub
	w.api.pulls = map[string]entity.RemotePullRequestState{
		"acme/web#8": {Open: true, HeadSHA: "sha8"},
		"acme/web#9": {Open: true, HeadSHA: "sha9"},
	}

	if _, err := w.uc.Resync(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if w.pr(8).State != entity.PRStateOpen || w.pr(9).State != entity.PRStateOpen || len(w.events.events) != 0 {
		t.Fatalf("an open PR stays open and nothing is announced: %q %q %+v", w.pr(8).State, w.pr(9).State, w.events.events)
	}

	// A PR GitHub no longer knows at all is closed.
	delete(w.api.pulls, "acme/web#9")
	if _, err := w.uc.Resync(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if w.pr(9).State != entity.PRStateClosed {
		t.Fatalf("a PR that no longer exists is closed, got %q", w.pr(9).State)
	}
}

func TestResync_RefreshesWhatChangedWhileNoWebhookArrived(t *testing.T) {
	t.Parallel()
	w := followedWeb(t)
	open := w.api.openPRs["acme/web"]
	for i := range open {
		if open[i].Number == 7 {
			open[i].Title, open[i].HeadSHA, open[i].IsDraft = "feat: cart (v2)", "sha7b", true // pushed, retitled and made a draft
		}
	}
	w.api.checkRuns["sha7b"] = []entity.CheckRun{{ID: 72, HeadSHA: "sha7b", Name: "build", Status: "completed", Conclusion: "failure"}}

	res, err := w.uc.Resync(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	got := w.pr(7)
	if got.Title != "feat: cart (v2)" || got.HeadSHA != "sha7b" || !got.IsDraft {
		t.Fatalf("the PR must follow GitHub: %+v", got)
	}
	if got.CIStatus != entity.CIStatusFailure {
		t.Fatalf("CI is re-derived from the new head's checks, got %q", got.CIStatus)
	}
	if got.MergeableState != entity.MergeableStateUnknown {
		t.Fatalf("a new commit has no known merge state yet, got %q", got.MergeableState)
	}
	if res.Updated != 1 {
		t.Fatalf("only #7 changed: %+v", res)
	}
}

// The list GitHub returns has no merge state, so the resync reads each open PR: the user asked for
// GitHub's word, and a BLOCKED lifted by an approval must not survive it. A state GitHub is still
// computing is stored as unknown; a PR whose state cannot be read keeps the stored one.
func TestResync_ReadsTheMergeStateGitHubHasNow(t *testing.T) {
	t.Parallel()
	w := followedWeb(t)
	for _, n := range []int{7, 8, 9} {
		pr := w.pr(n)
		pr.MergeableState = entity.MergeableStateBlocked
		w.prs.prs[[2]uint64{w.repo.ID, uint64(n)}] = pr
	}
	w.api.openPRs["acme/web"] = append(w.api.openPRs["acme/web"], entity.RemotePullRequest{
		GitHubPRID: 10001, Number: 10, Title: "fix: late", AuthorLogin: "bia", HeadSHA: "sha10", BaseRef: "main"})
	w.api.pulls = map[string]entity.RemotePullRequestState{
		"acme/web#7":  {Open: true, HeadSHA: w.pr(7).HeadSHA, MergeableState: entity.MergeableStateClean},
		"acme/web#8":  {Open: true, HeadSHA: w.pr(8).HeadSHA, MergeableState: entity.MergeableStateUnknown},
		"acme/web#10": {Open: true, HeadSHA: "sha10", MergeableState: entity.MergeableStateBehind},
		// #9 cannot be read
	}

	res, err := w.uc.Resync(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	for n, want := range map[int]entity.MergeableState{
		7:  entity.MergeableStateClean,
		8:  entity.MergeableStateUnknown,
		9:  entity.MergeableStateBlocked,
		10: entity.MergeableStateBehind,
	} {
		if got := w.pr(n).MergeableState; got != want {
			t.Errorf("#%d merge state = %q, want %q", n, got, want)
		}
	}
	if res.Updated != 2 || res.Added != 1 {
		t.Fatalf("#7 and #8 changed, #10 was added: %+v", res)
	}

	// A head GitHub moved since the list was read: that state is not about the stored commit.
	pr := w.pr(7)
	w.api.pulls["acme/web#7"] = entity.RemotePullRequestState{Open: true, HeadSHA: "newer", MergeableState: entity.MergeableStateConflicting}
	if _, err := w.uc.Resync(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if got := w.pr(7).MergeableState; got != pr.MergeableState {
		t.Fatalf("the state of another commit must not be stored, got %q", got)
	}
}

// Reading the merge states is the user's GitHub budget too: a rate limit stops the resync like any
// other read, while any other failure to read one only leaves that PR's state as it was.
func TestResync_MergeStateReadsStopAtTheRateLimitOnly(t *testing.T) {
	t.Parallel()
	w := followedWeb(t)
	w.api.pullErr = errors.New("github is flaky")
	res, err := w.uc.Resync(context.Background(), userID)
	if err != nil || len(res.Failed) != 0 {
		t.Fatalf("an unreadable merge state is not a failure: err=%v %+v", err, res)
	}

	w.api.pullErr = &entity.RateLimitedError{ResetAt: time.Now().Add(time.Hour)}
	res, err = w.uc.Resync(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 1 || !errors.Is(res.Failed[0].Err, entity.ErrRateLimited) {
		t.Fatalf("a rate limit on the merge-state reads is reported: %+v", res)
	}
}

func TestResync_PicksUpWhatAppearedAndReopened(t *testing.T) {
	t.Parallel()
	w := followedWeb(t)
	// #9 was closed here (a webhook) but GitHub has it open again; #10 opened while the API was down.
	closed := w.pr(9)
	closed.State = entity.PRStateClosed
	w.prs.prs[[2]uint64{w.repo.ID, 9}] = closed
	w.api.openPRs["acme/web"] = append(w.api.openPRs["acme/web"], entity.RemotePullRequest{
		GitHubPRID: 10001, Number: 10, Title: "fix: late", AuthorLogin: "bia", HeadSHA: "sha10", BaseRef: "main"})

	res, err := w.uc.Resync(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if w.pr(9).State != entity.PRStateOpen {
		t.Fatalf("a reopened PR is open again, got %q", w.pr(9).State)
	}
	if added := w.pr(10); added.Title != "fix: late" || added.State != entity.PRStateOpen || added.CIStatus != entity.CIStatusNone {
		t.Fatalf("a PR opened meanwhile is stored: %+v", added)
	}
	if res.Added != 1 {
		t.Fatalf("expected one PR added: %+v", res)
	}
}

func TestResync_RedeliverChangesNothingTheSecondTime(t *testing.T) {
	t.Parallel()
	w := followedWeb(t)
	w.listOnly(7)
	w.api.pulls = map[string]entity.RemotePullRequestState{
		"acme/web#8": {Open: false, Merged: true},
		"acme/web#9": {Open: false},
	}
	if _, err := w.uc.Resync(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	writes, announced := w.prs.upserts, len(w.events.events)

	res, err := w.uc.Resync(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if w.prs.upserts != writes || len(w.events.events) != announced {
		t.Fatalf("a second resync converges: writes %d -> %d, events %d -> %d", writes, w.prs.upserts, announced, len(w.events.events))
	}
	if res.Closed+res.Merged+res.Updated+res.Added != 0 {
		t.Fatalf("nothing changed the second time: %+v", res)
	}
}

func TestResync_OneRepositoryFailingDoesNotStopTheOthersAndIsReported(t *testing.T) {
	t.Parallel()
	uc, api, repos, _, prs, _ := setupBackfill()
	twoOpenPRs(api)
	api.openPRs["acme/api"] = nil
	ctx := context.Background()
	web, err := uc.Import(ctx, userID, 20, 200)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.Import(ctx, userID, 20, 201); err != nil {
		t.Fatal(err)
	}
	api.listPRsErr = map[string]error{"acme/api": entity.ErrNotFound} // the app lost access to acme/api
	api.openPRs["acme/web"] = api.openPRs["acme/web"][:1]
	api.pulls = map[string]entity.RemotePullRequestState{"acme/web#8": {Merged: true}, "acme/web#9": {}}

	res, err := uc.Resync(ctx, userID)
	if err != nil {
		t.Fatalf("one repository failing is reported, not fatal: %v", err)
	}
	if prs.prs[[2]uint64{web.ID, 8}].State != entity.PRStateMerged {
		t.Fatal("acme/web must still be resynced")
	}
	if res.Repositories != 2 || len(res.Failed) != 1 || res.Failed[0].Repository != "acme/api" || !errors.Is(res.Failed[0].Err, entity.ErrNotFound) {
		t.Fatalf("expected acme/api reported as failed: %+v", res)
	}
	_ = repos
}

func TestResync_StopsWhenTheUsersGitHubAccessIsGoneOrTheRequestIsCanceled(t *testing.T) {
	t.Parallel()
	w := followedWeb(t)

	w.api.listPRsErr = map[string]error{"acme/web": entity.ErrGitHubTokenExpired}
	if _, err := w.uc.Resync(context.Background(), userID); !errors.Is(err, entity.ErrGitHubTokenExpired) {
		t.Fatalf("a dead token ends the whole resync so the app can sign the user in again, got %v", err)
	}

	w.api.listPRsErr = nil
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.uc.Resync(canceled, userID); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestResync_OnlyTouchesTheRepositoriesTheUserFollows(t *testing.T) {
	t.Parallel()
	uc, api, _, events, prs, _ := setupBackfill()
	twoOpenPRs(api)
	res, err := uc.Resync(context.Background(), userID) // follows nothing yet
	if err != nil || res.Repositories != 0 || len(prs.prs) != 0 || len(events.events) != 0 {
		t.Fatalf("nothing followed, nothing synced: %+v %v", res, err)
	}
}

// threeRepos follows acme/api, acme/web and octocat/dotfiles; the sync visits them in that order.
func threeRepos(t *testing.T) (*repoimport.UseCase, *fakeUserAPI) {
	t.Helper()
	uc, api, _, _, _, _ := setupBackfill()
	twoOpenPRs(api)
	api.openPRs["acme/api"], api.openPRs["octocat/dotfiles"] = nil, nil
	ctx := context.Background()
	for _, in := range [][2]int64{{20, 201}, {20, 200}, {10, 100}} {
		if _, err := uc.Import(ctx, userID, in[0], in[1]); err != nil {
			t.Fatalf("Import() error: %v", err)
		}
	}
	return uc, api
}

func TestResyncReporting_TellsWhereItIsAndVisitsRepositoriesInAStableOrder(t *testing.T) {
	t.Parallel()
	uc, _ := threeRepos(t)
	var seen []entity.SyncProgress

	res, err := uc.ResyncReporting(context.Background(), userID, func(p entity.SyncProgress) { seen = append(seen, p) })
	if err != nil || res.Repositories != 3 {
		t.Fatalf("ResyncReporting() = %+v, %v", res, err)
	}
	want := []entity.SyncProgress{
		{Total: 3, Done: 0, Current: "acme/api"},
		{Total: 3, Done: 1, Current: "acme/web"},
		{Total: 3, Done: 2, Current: "octocat/dotfiles"},
		{Total: 3, Done: 3, Current: ""},
	}
	if len(seen) != len(want) {
		t.Fatalf("progress = %+v, want %+v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("progress[%d] = %+v, want %+v", i, seen[i], want[i])
		}
	}
}

func TestResync_StopsAtGitHubsRateLimitAndSaysWhenToComeBack(t *testing.T) {
	t.Parallel()
	uc, api := threeRepos(t)
	resetAt := time.Now().Add(15 * time.Minute).UTC().Truncate(time.Second)
	api.listPRsErr = map[string]error{"acme/web": &entity.RateLimitedError{ResetAt: resetAt}}

	res, err := uc.Resync(context.Background(), userID)
	if err != nil {
		t.Fatalf("a rate limit is reported in the result, not as a failure of the call: %v", err)
	}
	// The limit is the user's, so the repositories after the one that hit it would only fail the same way.
	if res.Repositories != 2 || res.Skipped != 1 {
		t.Fatalf("acme/api synced, acme/web hit the limit, octocat/dotfiles was not attempted: %+v", res)
	}
	if len(res.Failed) != 1 || res.Failed[0].Repository != "acme/web" || !errors.Is(res.Failed[0].Err, entity.ErrRateLimited) {
		t.Fatalf("expected acme/web reported as rate limited: %+v", res.Failed)
	}
	if !res.RateLimitedUntil.Equal(resetAt) {
		t.Fatalf("the result says when the limit resets: %v, want %v", res.RateLimitedUntil, resetAt)
	}
}
