package action_test

import (
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

// headOf is the commit the fake GitHub reports as head of PR number 11.
const headOf11 = "sha-1"

func running(name string) entity.CheckRun {
	return entity.CheckRun{ID: 1, Name: name, Status: "in_progress"}
}
func passed(name string) entity.CheckRun {
	return entity.CheckRun{ID: 2, Name: name, Status: "completed", Conclusion: "success"}
}
func failed(name string) entity.CheckRun {
	return entity.CheckRun{ID: 3, Name: name, Status: "completed", Conclusion: "failure"}
}

func TestChecksStillRunning_BlockApprovingAndMerging(t *testing.T) {
	t.Parallel()
	pendingStatus := []entity.CommitStatus{{ID: 9, Context: "Vercel", State: "pending", Description: "Vercel is deploying your app"}}
	cases := map[string]func(f *fakeGitHub){
		"a check run still running": func(f *fakeGitHub) {
			f.runs = map[string][]entity.CheckRun{headOf11: {passed("build"), running("test")}}
		},
		"a check run queued": func(f *fakeGitHub) {
			f.runs = map[string][]entity.CheckRun{headOf11: {{ID: 1, Name: "build", Status: "queued"}}}
		},
		"a legacy status still pending": func(f *fakeGitHub) {
			f.runs = map[string][]entity.CheckRun{headOf11: {passed("build")}}
			f.commitStatuses = map[string][]entity.CommitStatus{headOf11: pendingStatus}
		},
		"a re-run of a failed check that is still going": func(f *fakeGitHub) {
			f.runs = map[string][]entity.CheckRun{headOf11: {failed("build"), {ID: 8, Name: "build", Status: "in_progress"}}}
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
			arrange(f.gh)

			if err := f.uc.ApprovePR(ctx, 1, 1, ""); !errors.Is(err, entity.ErrChecksRunning) {
				t.Fatalf("approve: expected ErrChecksRunning, got %v", err)
			}
			if err := f.uc.MergePR(ctx, 1, 1, ""); !errors.Is(err, entity.ErrChecksRunning) {
				t.Fatalf("merge: expected ErrChecksRunning, got %v", err)
			}
			if len(f.gh.approved) != 0 || len(f.gh.merged) != 0 || len(f.pub.events) != 0 || len(f.reviews.saved) != 0 {
				t.Fatalf("nothing may be written, stored or announced: approved=%v merged=%v events=%v", f.gh.approved, f.gh.merged, f.pub.events)
			}
		})
	}
}

func TestChecksThatAreNotRunning_DoNotBlock(t *testing.T) {
	t.Parallel()
	cases := map[string]func(f *fakeGitHub){
		"no checks reported": func(f *fakeGitHub) {},
		"all passed": func(f *fakeGitHub) {
			f.runs = map[string][]entity.CheckRun{headOf11: {passed("build"), passed("test")}}
		},
		"a failure is not a blocker": func(f *fakeGitHub) {
			f.runs = map[string][]entity.CheckRun{headOf11: {failed("build"), passed("test")}}
		},
		"a failed check that was re-run and passed": func(f *fakeGitHub) {
			f.runs = map[string][]entity.CheckRun{headOf11: {failed("build"), {ID: 8, Name: "build", Status: "completed", Conclusion: "success"}}}
		},
		"checks of another commit": func(f *fakeGitHub) { f.runs = map[string][]entity.CheckRun{"older-commit": {running("test")}} },
		"a finished status": func(f *fakeGitHub) {
			f.commitStatuses = map[string][]entity.CommitStatus{headOf11: {{ID: 9, Context: "Vercel", State: "success"}}}
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
			arrange(f.gh)
			if err := f.uc.ApprovePR(ctx, 1, 1, ""); err != nil {
				t.Fatalf("approve: %v", err)
			}
			if err := f.uc.MergePR(ctx, 1, 1, ""); err != nil {
				t.Fatalf("merge: %v", err)
			}
		})
	}
}

func TestChecksThatCannotBeRead_NeverLetAnActionThrough(t *testing.T) {
	t.Parallel()
	f := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	boom := errors.New("github is down")
	f.gh.checksErr = boom

	if err := f.uc.ApprovePR(ctx, 1, 1, ""); !errors.Is(err, boom) {
		t.Fatalf("approve: expected the read error, got %v", err)
	}
	if err := f.uc.MergePR(ctx, 1, 1, ""); !errors.Is(err, boom) {
		t.Fatalf("merge: expected the read error, got %v", err)
	}
	if len(f.gh.approved) != 0 || len(f.gh.merged) != 0 {
		t.Fatal("never act on checks that could not be checked")
	}
}

func TestChecksRunning_DoNotStandInTheWayOfWhatNeedsNoWrite(t *testing.T) {
	t.Parallel()
	// An approval GitHub already has, and a PR GitHub already merged, converge without writing:
	// there is nothing the running checks could be blocking.
	approved := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	approved.gh.runs = map[string][]entity.CheckRun{headOf11: {running("test")}}
	approved.gh.reviews[11] = []entity.PullRequestReview{{GitHubReviewID: 7, ReviewerLogin: "octocat", State: entity.ReviewApproved, CommitSHA: headOf11}}
	if err := approved.uc.ApprovePR(ctx, 1, 1, ""); err != nil {
		t.Fatalf("an existing approval converges: %v", err)
	}

	merged := newFixture(fakeTokens{}, openPR(1, 11, "alice"))
	merged.gh.runs = map[string][]entity.CheckRun{headOf11: {running("test")}}
	merged.gh.live[11].Open, merged.gh.live[11].Merged = false, true
	if err := merged.uc.MergePR(ctx, 1, 1, ""); err != nil {
		t.Fatalf("a merged PR converges: %v", err)
	}
}

func TestBatchApproveAndMerge_StopsAtAPRWhoseChecksAreRunning(t *testing.T) {
	t.Parallel()
	f := newFixture(fakeTokens{}, openPR(2, 12, "dependabot[bot]"), openPR(3, 13, "dependabot[bot]"))
	f.gh.runs = map[string][]entity.CheckRun{"sha-3": {running("test")}} // PR 13's head is sha-3

	done, err := f.uc.BatchApproveAndMerge(ctx, 1, []uint64{2, 3}, "squash")
	if !errors.Is(err, entity.ErrChecksRunning) {
		t.Fatalf("expected ErrChecksRunning, got %v", err)
	}
	if len(done) != 1 || done[0] != 2 {
		t.Fatalf("PR 2 was clear and is done; PR 3 must not be: %v", done)
	}
	for _, n := range f.gh.approved {
		if n == 13 {
			t.Fatal("the PR with running checks must not be approved")
		}
	}
	for _, n := range f.gh.merged {
		if n == 13 {
			t.Fatal("the PR with running checks must not be merged")
		}
	}
}
