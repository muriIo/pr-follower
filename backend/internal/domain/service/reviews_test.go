package service_test

import (
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/service"
)

var base = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

func review(id int64, login string, state entity.ReviewState, sha string, minutes int) entity.PullRequestReview {
	return entity.PullRequestReview{
		GitHubReviewID: id, ReviewerLogin: login, State: state, CommitSHA: sha,
		SubmittedAt: base.Add(time.Duration(minutes) * time.Minute),
	}
}

func TestDeriveReviewFacts(t *testing.T) {
	t.Parallel()
	pr := entity.PullRequest{AuthorLogin: "alice", HeadSHA: "head"}

	cases := []struct {
		name    string
		reviews []entity.PullRequestReview
		want    service.ReviewFacts
	}{
		{"no reviews", nil, service.ReviewFacts{}},
		{
			"my approval of the head",
			[]entity.PullRequestReview{review(1, "me", entity.ReviewApproved, "head", 1)},
			service.ReviewFacts{ApprovedByMeSHA: "head", UserActedAtSHA: "head"},
		},
		{
			"my approval of an older commit does not cover the head",
			[]entity.PullRequestReview{review(1, "me", entity.ReviewApproved, "old", 1)},
			service.ReviewFacts{ApprovedByMeSHA: "old", UserActedAtSHA: "old"},
		},
		{
			"a teammate approved the head",
			[]entity.PullRequestReview{review(1, "bob", entity.ReviewApproved, "head", 1)},
			service.ReviewFacts{TeammateApprovedAtSHA: "head", TeammateApprovedBy: "bob"},
		},
		{
			"the author approving their own PR is nobody's approval",
			[]entity.PullRequestReview{review(1, "alice", entity.ReviewApproved, "head", 1)},
			service.ReviewFacts{},
		},
		{
			"my latest decision wins: changes requested after approval",
			[]entity.PullRequestReview{
				review(1, "me", entity.ReviewApproved, "head", 1),
				review(2, "me", entity.ReviewChangesRequested, "head", 2),
			},
			service.ReviewFacts{UserActedAtSHA: "head"},
		},
		{
			"a comment does not undo my approval",
			[]entity.PullRequestReview{
				review(1, "me", entity.ReviewApproved, "head", 1),
				review(2, "me", entity.ReviewCommented, "head", 2),
			},
			service.ReviewFacts{ApprovedByMeSHA: "head", UserActedAtSHA: "head"},
		},
		{
			"a dismissed approval counts for nothing",
			[]entity.PullRequestReview{review(1, "me", entity.ReviewDismissed, "head", 1)},
			service.ReviewFacts{},
		},
		{
			"a teammate's dismissed approval counts for nothing",
			[]entity.PullRequestReview{review(1, "bob", entity.ReviewDismissed, "head", 1)},
			service.ReviewFacts{},
		},
		{
			"logins match regardless of case",
			[]entity.PullRequestReview{review(1, "ME", entity.ReviewApproved, "head", 1)},
			service.ReviewFacts{ApprovedByMeSHA: "head", UserActedAtSHA: "head"},
		},
		{
			"a teammate who approved and then requested changes no longer approves",
			[]entity.PullRequestReview{
				review(1, "bob", entity.ReviewApproved, "head", 1),
				review(2, "bob", entity.ReviewChangesRequested, "head", 2),
			},
			service.ReviewFacts{},
		},
		{
			"an approval of the head by any teammate is enough, and the first by name is reported",
			[]entity.PullRequestReview{
				review(1, "bob", entity.ReviewApproved, "old", 1),
				review(2, "dave", entity.ReviewApproved, "head", 2),
				review(3, "carol", entity.ReviewApproved, "head", 3),
			},
			service.ReviewFacts{TeammateApprovedAtSHA: "head", TeammateApprovedBy: "carol"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := service.DeriveReviewFacts(tc.reviews, pr, "me"); got != tc.want {
				t.Errorf("DeriveReviewFacts() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDeriveReviewFacts_SameTimestampBreaksTiesByReviewID(t *testing.T) {
	t.Parallel()
	pr := entity.PullRequest{AuthorLogin: "alice", HeadSHA: "head"}
	got := service.DeriveReviewFacts([]entity.PullRequestReview{
		review(2, "me", entity.ReviewChangesRequested, "head", 5),
		review(1, "me", entity.ReviewApproved, "head", 5),
	}, pr, "me")
	if got.ApprovedByMeSHA != "" {
		t.Errorf("the later review id must win, got %+v", got)
	}
}

func TestLatestReviewDecisions(t *testing.T) {
	t.Parallel()
	pr := entity.PullRequest{AuthorLogin: "alice", HeadSHA: "head"}
	type row struct {
		login   string
		state   entity.ReviewState
		current bool
	}
	rows := func(ds []entity.ReviewDecision) []row {
		out := []row{}
		for _, d := range ds {
			out = append(out, row{d.Reviewer, d.State, d.Current})
		}
		return out
	}

	cases := []struct {
		name    string
		reviews []entity.PullRequestReview
		want    []row
	}{
		{"nobody reviewed", nil, []row{}},
		{
			"each reviewer's latest decision, current ones first",
			[]entity.PullRequestReview{
				review(1, "bob", entity.ReviewApproved, "old", 1),
				review(2, "carol", entity.ReviewChangesRequested, "head", 2),
				review(3, "dave", entity.ReviewApproved, "head", 3),
			},
			[]row{{"dave", entity.ReviewApproved, true}, {"carol", entity.ReviewChangesRequested, true}, {"bob", entity.ReviewApproved, false}},
		},
		{
			"a later decision replaces an earlier one",
			[]entity.PullRequestReview{
				review(1, "bob", entity.ReviewChangesRequested, "old", 1),
				review(2, "bob", entity.ReviewApproved, "head", 2),
			},
			[]row{{"bob", entity.ReviewApproved, true}},
		},
		{
			"a comment never replaces a decision, but a reviewer who only commented is listed",
			[]entity.PullRequestReview{
				review(1, "bob", entity.ReviewApproved, "head", 1),
				review(2, "bob", entity.ReviewCommented, "head", 2),
				review(3, "carol", entity.ReviewCommented, "head", 3),
			},
			[]row{{"carol", entity.ReviewCommented, true}, {"bob", entity.ReviewApproved, true}},
		},
		{
			"a dismissed review is shown as dismissed",
			[]entity.PullRequestReview{review(1, "bob", entity.ReviewDismissed, "head", 1)},
			[]row{{"bob", entity.ReviewDismissed, true}},
		},
		{
			"the author's own comments and drafts are not reviews",
			[]entity.PullRequestReview{
				review(1, "alice", entity.ReviewCommented, "head", 1),
				review(2, "Alice", entity.ReviewApproved, "head", 2),
				review(3, "bob", entity.ReviewUnknown, "head", 3),
			},
			[]row{},
		},
		{
			"logins are matched without regard to case",
			[]entity.PullRequestReview{
				review(1, "Bob", entity.ReviewChangesRequested, "head", 1),
				review(2, "bob", entity.ReviewApproved, "head", 2),
			},
			[]row{{"bob", entity.ReviewApproved, true}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := rows(service.LatestReviewDecisions(tc.reviews, pr))
			if len(got) != len(tc.want) {
				t.Fatalf("LatestReviewDecisions() = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("LatestReviewDecisions() = %+v, want %+v", got, tc.want)
				}
			}
		})
	}
}
