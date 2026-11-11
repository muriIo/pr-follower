package entity_test

import (
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func TestReviewStateFromGitHub_NormalisesBothSpellings(t *testing.T) {
	t.Parallel()
	cases := map[string]entity.ReviewState{
		"approved":          entity.ReviewApproved, // webhook payloads
		"APPROVED":          entity.ReviewApproved, // REST list
		"changes_requested": entity.ReviewChangesRequested,
		"CHANGES_REQUESTED": entity.ReviewChangesRequested,
		"commented":         entity.ReviewCommented,
		"DISMISSED":         entity.ReviewDismissed,
		"PENDING":           entity.ReviewUnknown, // a draft review is nobody's decision
		"":                  entity.ReviewUnknown,
	}
	for in, want := range cases {
		if got := entity.ReviewStateFromGitHub(in); got != want {
			t.Errorf("ReviewStateFromGitHub(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPullRequestReview_SupersedesNeverRevivesADismissedReview(t *testing.T) {
	t.Parallel()
	approved := entity.PullRequestReview{GitHubReviewID: 1, State: entity.ReviewApproved}
	dismissed := entity.PullRequestReview{GitHubReviewID: 1, State: entity.ReviewDismissed}

	if !dismissed.Supersedes(approved) {
		t.Error("a dismissal must replace the approval it dismisses")
	}
	if approved.Supersedes(dismissed) {
		t.Error("a late or redelivered 'submitted' must not revive a dismissed review")
	}
	if !approved.Supersedes(approved) {
		t.Error("the same state converges")
	}
}
