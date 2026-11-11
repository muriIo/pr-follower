package service

import (
	"strings"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

// ReviewFacts are the review inputs of the turn state for one user on one PR.
type ReviewFacts struct {
	// ApprovedByMeSHA is the commit my standing approval covers ("" when I have none).
	ApprovedByMeSHA string
	// TeammateApprovedAtSHA is the head commit when someone other than me and the author
	// approved it ("" otherwise).
	TeammateApprovedAtSHA string
	// TeammateApprovedBy is who approved it (the first login by name when several did).
	TeammateApprovedBy string
	// UserActedAtSHA is the commit of my latest non-dismissed review.
	UserActedAtSHA string
}

// DeriveReviewFacts reads the recorded reviews: each reviewer's latest decision (approve, request
// changes, dismissed) stands, comments never undo one. Commits compare against the head, so a
// new push makes older reviews stale without any bookkeeping.
func DeriveReviewFacts(reviews []entity.PullRequestReview, pr entity.PullRequest, myLogin string) ReviewFacts {
	decision := map[string]entity.PullRequestReview{}
	var latestMine entity.PullRequestReview
	haveMine := false
	for _, r := range reviews {
		if r.State == entity.ReviewUnknown {
			continue
		}
		login := strings.ToLower(r.ReviewerLogin)
		if r.State != entity.ReviewCommented {
			if cur, ok := decision[login]; !ok || later(r, cur) {
				decision[login] = r
			}
		}
		if login == strings.ToLower(myLogin) && r.State != entity.ReviewDismissed {
			if !haveMine || later(r, latestMine) {
				latestMine, haveMine = r, true
			}
		}
	}

	var facts ReviewFacts
	me, author := strings.ToLower(myLogin), strings.ToLower(pr.AuthorLogin)
	for login, r := range decision {
		if r.State != entity.ReviewApproved {
			continue
		}
		switch {
		case login == me:
			facts.ApprovedByMeSHA = r.CommitSHA
		case login != author && r.CommitSHA == pr.HeadSHA:
			facts.TeammateApprovedAtSHA = pr.HeadSHA
			if facts.TeammateApprovedBy == "" || r.ReviewerLogin < facts.TeammateApprovedBy {
				facts.TeammateApprovedBy = r.ReviewerLogin
			}
		}
	}
	if haveMine {
		facts.UserActedAtSHA = latestMine.CommitSHA
	}
	return facts
}

// later orders reviews by submission time, then by GitHub's increasing review id.
func later(a, b entity.PullRequestReview) bool {
	if !a.SubmittedAt.Equal(b.SubmittedAt) {
		return a.SubmittedAt.After(b.SubmittedAt)
	}
	return a.GitHubReviewID > b.GitHubReviewID
}
