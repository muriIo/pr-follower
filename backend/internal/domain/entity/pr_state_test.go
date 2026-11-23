package entity_test

import (
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func TestPullRequest_ReviewGateAcceptsRepositoriesWithoutCI(t *testing.T) {
	t.Parallel()
	pr := entity.PullRequest{State: entity.PRStateOpen, CIStatus: entity.CIStatusNone, MergeableState: entity.MergeableStateUnknown}
	if !pr.IsReadyForReviewGate() {
		t.Fatal("a PR with no CI has nothing to wait for and must reach reviewers")
	}
	pr.CIStatus = entity.CIStatusPending
	if pr.IsReadyForReviewGate() {
		t.Fatal("running CI still gates the PR")
	}
}

func TestMergeableStateFromGitHub(t *testing.T) {
	t.Parallel()
	cases := map[string]entity.MergeableState{
		"clean":     entity.MergeableStateClean,
		"has_hooks": entity.MergeableStateClean,
		"dirty":     entity.MergeableStateConflicting,
		"blocked":   entity.MergeableStateBlocked,
		"behind":    entity.MergeableStateBehind,
		"unstable":  entity.MergeableStateUnstable,
		"draft":     entity.MergeableStateDraft,
		"unknown":   entity.MergeableStateUnknown,
		"":          entity.MergeableStateUnknown,
		"weird":     entity.MergeableStateUnknown,
	}
	for in, want := range cases {
		if got := entity.MergeableStateFromGitHub(in); got != want {
			t.Errorf("MergeableStateFromGitHub(%q) = %q, want %q", in, got, want)
		}
	}
}

// A review or a check can lift or add a branch-protection block, so a state that rests on them is
// unknown until GitHub is asked again; conflicts, an out-of-date branch and a draft stay.
func TestMergeableState_AfterReviewOrCheckChange(t *testing.T) {
	t.Parallel()
	cases := map[entity.MergeableState]entity.MergeableState{
		entity.MergeableStateBlocked:     entity.MergeableStateUnknown,
		entity.MergeableStateClean:       entity.MergeableStateUnknown,
		entity.MergeableStateUnstable:    entity.MergeableStateUnknown,
		entity.MergeableStateUnknown:     entity.MergeableStateUnknown,
		entity.MergeableStateConflicting: entity.MergeableStateConflicting,
		entity.MergeableStateBehind:      entity.MergeableStateBehind,
		entity.MergeableStateDraft:       entity.MergeableStateDraft,
	}
	for in, want := range cases {
		if got := in.AfterReviewOrCheckChange(); got != want {
			t.Errorf("%q.AfterReviewOrCheckChange() = %q, want %q", in, got, want)
		}
	}
}

func TestDeriveCIStatus(t *testing.T) {
	t.Parallel()
	run := func(status, conclusion string) entity.CheckRun {
		return entity.CheckRun{Status: status, Conclusion: conclusion}
	}
	cases := []struct {
		name string
		runs []entity.CheckRun
		want entity.CIStatus
	}{
		{"no checks reported", nil, entity.CIStatusNone},
		{"all passed or neutral", []entity.CheckRun{run("completed", "success"), run("completed", "neutral"), run("completed", "skipped")}, entity.CIStatusSuccess},
		{"one still running", []entity.CheckRun{run("completed", "success"), run("in_progress", "")}, entity.CIStatusPending},
		{"queued counts as running", []entity.CheckRun{run("queued", "")}, entity.CIStatusPending},
		{"failure wins over running", []entity.CheckRun{run("in_progress", ""), run("completed", "failure")}, entity.CIStatusFailure},
		{"timeouts and cancellations fail", []entity.CheckRun{run("completed", "timed_out")}, entity.CIStatusFailure},
		{"action required fails", []entity.CheckRun{run("completed", "action_required")}, entity.CIStatusFailure},
		{"cancelled fails", []entity.CheckRun{run("completed", "cancelled")}, entity.CIStatusFailure},
		{"stale runs are ignored", []entity.CheckRun{run("completed", "stale")}, entity.CIStatusNone},
	}
	for _, tc := range cases {
		if got := entity.DeriveCIStatus(tc.runs); got != tc.want {
			t.Errorf("%s: DeriveCIStatus() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCheckRun_Supersedes(t *testing.T) {
	t.Parallel()
	queued := entity.CheckRun{Status: "queued"}
	running := entity.CheckRun{Status: "in_progress"}
	done := entity.CheckRun{Status: "completed", Conclusion: "success"}

	if !running.Supersedes(queued) || !done.Supersedes(running) || !done.Supersedes(queued) {
		t.Fatal("a run moves forward: queued -> in_progress -> completed")
	}
	if running.Supersedes(done) || queued.Supersedes(running) {
		t.Fatal("a late, redelivered event must not move a run backwards")
	}
	rerun := entity.CheckRun{Status: "completed", Conclusion: "failure"}
	if !rerun.Supersedes(done) {
		t.Fatal("a completed run can be re-reported with a new conclusion")
	}
}

func TestRepository_CodeownersStatus(t *testing.T) {
	t.Parallel()
	if (entity.Repository{}).CodeownersKnown() {
		t.Fatal("an unchecked repository does not know whether it has CODEOWNERS")
	}
	if !(entity.Repository{CodeownersStatus: entity.CodeownersAbsent}).CodeownersKnown() {
		t.Fatal("absent is a known state")
	}
}

func TestIsDependabotAuthor(t *testing.T) {
	t.Parallel()
	for login, want := range map[string]bool{
		"dependabot[bot]":  true,
		"Dependabot":       true,
		"app/dependabot":   true,
		"octocat":          false,
		"":                 false,
		"renovate[bot]":    false,
		"not-a-dependabo7": false,
	} {
		if got := entity.IsDependabotAuthor(login); got != want {
			t.Errorf("IsDependabotAuthor(%q) = %v, want %v", login, got, want)
		}
	}
}

func TestPRChangedFiles_IsCurrentOnlyForTheHeadCommit(t *testing.T) {
	t.Parallel()
	pr := entity.PullRequest{HeadSHA: "abc"}
	if !(entity.PRChangedFiles{HeadSHA: "abc"}).IsCurrentFor(pr) {
		t.Error("files read at the head commit are current")
	}
	if (entity.PRChangedFiles{HeadSHA: "old"}).IsCurrentFor(pr) || (entity.PRChangedFiles{}).IsCurrentFor(pr) {
		t.Error("files of another commit, or none, are not current")
	}
}

func TestLatestCheckRuns_KeepsOnlyTheLatestRunOfEachCheck(t *testing.T) {
	t.Parallel()
	runs := []entity.CheckRun{
		{ID: 1, Name: "build", Status: "completed", Conclusion: "failure"},
		{ID: 9, Name: "build", Status: "completed", Conclusion: "success"}, // a re-run of "build"
		{ID: 4, Name: "test", Status: "in_progress"},
		{ID: 3, Name: "lint", Status: "completed", Conclusion: "stale"}, // superseded by GitHub itself
		{ID: 5, Name: "", Status: "completed", Conclusion: "success"},   // no name: cannot be matched, kept
		{ID: 6, Name: "", Status: "completed", Conclusion: "success"},
	}

	got := entity.LatestCheckRuns(runs)

	var ids []int64
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	want := []int64{5, 6, 9, 4} // by name: the unnamed ones, then build, then test (lint was stale)
	if len(ids) != len(want) {
		t.Fatalf("LatestCheckRuns() ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("LatestCheckRuns() ids = %v, want %v", ids, want)
		}
	}
}

func TestDeriveCIStatus_AReRunThatPassesClearsTheEarlierFailure(t *testing.T) {
	t.Parallel()
	failedThenPassed := []entity.CheckRun{
		{ID: 1, Name: "build", Status: "completed", Conclusion: "failure"},
		{ID: 2, Name: "build", Status: "completed", Conclusion: "success"},
		{ID: 3, Name: "test", Status: "completed", Conclusion: "success"},
	}
	if got := entity.DeriveCIStatus(failedThenPassed); got != entity.CIStatusSuccess {
		t.Fatalf("a passing re-run replaces the failed run: got %q", got)
	}

	// The other way round: a re-run that is still going is pending, not "failed before".
	rerunning := []entity.CheckRun{
		{ID: 1, Name: "build", Status: "completed", Conclusion: "failure"},
		{ID: 2, Name: "build", Status: "in_progress"},
	}
	if got := entity.DeriveCIStatus(rerunning); got != entity.CIStatusPending {
		t.Fatalf("a re-run in progress is pending: got %q", got)
	}

	// A later failure still fails it.
	passedThenFailed := []entity.CheckRun{
		{ID: 1, Name: "build", Status: "completed", Conclusion: "success"},
		{ID: 2, Name: "build", Status: "completed", Conclusion: "failure"},
	}
	if got := entity.DeriveCIStatus(passedThenFailed); got != entity.CIStatusFailure {
		t.Fatalf("got %q", got)
	}
}

func TestCommitStatus_AsCheckRun(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ status, conclusion string }{
		"pending": {"in_progress", ""},
		"success": {"completed", "success"},
		"failure": {"completed", "failure"},
		"error":   {"completed", "failure"}, // GitHub shows an errored status as failing
		"":        {"in_progress", ""},      // a state nobody reported is not a result
	}
	for state, want := range cases {
		s := entity.CommitStatus{ID: 7, RepositoryID: 3, HeadSHA: "abc", Context: "Vercel", State: state, Description: "Vercel is deploying your app", TargetURL: "https://vercel.com/x"}
		got := s.AsCheckRun()
		if got.Status != want.status || got.Conclusion != want.conclusion {
			t.Errorf("state %q: status/conclusion = %q/%q, want %q/%q", state, got.Status, got.Conclusion, want.status, want.conclusion)
		}
		if got.ID != 7 || got.RepositoryID != 3 || got.HeadSHA != "abc" || got.Name != "Vercel" ||
			got.Title != "Vercel is deploying your app" || got.DetailsURL != "https://vercel.com/x" || got.Source != entity.CheckSourceStatus {
			t.Errorf("state %q: unexpected mapping %+v", state, got)
		}
	}
}

func TestLatestCheckRuns_AStatusAndACheckRunWithTheSameNameAreDifferentChecks(t *testing.T) {
	t.Parallel()
	runs := []entity.CheckRun{
		{ID: 5, Name: "Vercel", Status: "completed", Conclusion: "success", Source: entity.CheckSourceCheckRun},
		{ID: 2, Name: "Vercel", Status: "in_progress", Source: entity.CheckSourceStatus}, // a different id space
		{ID: 1, Name: "Vercel", Status: "completed", Conclusion: "failure", Source: entity.CheckSourceStatus},
		{ID: 9, Name: "legacy", Status: "completed", Conclusion: "success"}, // no source: a check run
	}
	got := entity.LatestCheckRuns(runs)
	if len(got) != 3 {
		t.Fatalf("expected the check run, the latest status and legacy: %+v", got)
	}
	for _, r := range got {
		if r.Source == entity.CheckSourceStatus && r.ID != 2 {
			t.Fatalf("the latest status wins within its own source: %+v", r)
		}
	}
	// And a pending status keeps CI from claiming success, whatever the check runs said.
	if ci := entity.DeriveCIStatus(runs[:2]); ci != entity.CIStatusPending {
		t.Fatalf("a check run that passed and a status still pending is pending, got %q", ci)
	}
}
