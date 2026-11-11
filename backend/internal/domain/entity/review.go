package entity

import (
	"strings"
	"time"
)

// ReviewState is what a reviewer decided in one review.
type ReviewState string

const (
	ReviewApproved         ReviewState = "APPROVED"
	ReviewChangesRequested ReviewState = "CHANGES_REQUESTED"
	ReviewCommented        ReviewState = "COMMENTED"
	ReviewDismissed        ReviewState = "DISMISSED"
	// ReviewUnknown is a draft (PENDING) or unrecognised review: nobody's decision yet.
	ReviewUnknown ReviewState = ""
)

// ReviewStateFromGitHub reads GitHub's state, spelled lowercase in webhooks and uppercase in REST.
func ReviewStateFromGitHub(s string) ReviewState {
	switch ReviewState(strings.ToUpper(s)) {
	case ReviewApproved:
		return ReviewApproved
	case ReviewChangesRequested:
		return ReviewChangesRequested
	case ReviewCommented:
		return ReviewCommented
	case ReviewDismissed:
		return ReviewDismissed
	default:
		return ReviewUnknown
	}
}

// PullRequestReview is one review GitHub recorded on a PR, kept by GitHub's review id.
type PullRequestReview struct {
	GitHubReviewID int64
	PullRequestID  uint64
	ReviewerLogin  string
	State          ReviewState
	// CommitSHA is the commit the reviewer looked at; an approval only covers that commit.
	CommitSHA   string
	SubmittedAt time.Time
}

// Supersedes reports whether r may replace the stored version of the same review: a dismissal is
// final, so a late or redelivered "submitted" never revives a dismissed review (ADR-0011).
func (r PullRequestReview) Supersedes(prev PullRequestReview) bool {
	return prev.State != ReviewDismissed || r.State == ReviewDismissed
}
