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
