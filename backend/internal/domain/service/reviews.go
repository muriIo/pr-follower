package service

import (
	"sort"
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

// LatestReviewDecisions lists where each reviewer stands on a PR, for display. A reviewer's latest
// approval, change request or dismissal stands and a comment never replaces it; one who only
// commented is listed as having commented. The author's own reviews and drafts are not reviews.
// Current ones come first, then the newest.
func LatestReviewDecisions(reviews []entity.PullRequestReview, pr entity.PullRequest) []entity.ReviewDecision {
	author := strings.ToLower(pr.AuthorLogin)
	type standing struct{ decision, comment *entity.PullRequestReview }
	byReviewer := map[string]*standing{}
	for i := range reviews {
		r := &reviews[i]
		login := strings.ToLower(r.ReviewerLogin)
		if r.State == entity.ReviewUnknown || login == "" || login == author {
			continue
		}
		st := byReviewer[login]
		if st == nil {
			st = &standing{}
			byReviewer[login] = st
		}
		slot := &st.decision
		if r.State == entity.ReviewCommented {
			slot = &st.comment
		}
		if *slot == nil || later(*r, **slot) {
			*slot = r
		}
	}

	out := make([]entity.ReviewDecision, 0, len(byReviewer))
	for _, st := range byReviewer {
		shown := st.decision
		if shown == nil {
			shown = st.comment
		}
		out = append(out, entity.ReviewDecision{
			Reviewer: shown.ReviewerLogin, State: shown.State, CommitSHA: shown.CommitSHA,
			SubmittedAt: shown.SubmittedAt, Current: shown.CommitSHA == pr.HeadSHA,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Current != b.Current {
			return a.Current
		}
		if !a.SubmittedAt.Equal(b.SubmittedAt) {
			return a.SubmittedAt.After(b.SubmittedAt)
		}
		return strings.ToLower(a.Reviewer) < strings.ToLower(b.Reviewer)
	})
	return out
}
