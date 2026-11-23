package service_test

import (
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/service"
)

func change(eventType string, before *entity.NotificationWatch, after entity.NotificationWatch) service.NotificationChange {
	return service.NotificationChange{
		Event:     entity.ActivityEvent{Type: eventType, ActorLogin: "carol", RepositoryID: 10, RepositoryName: "arvore/hive-web", PullRequestID: 77, PRNumber: 35},
		Recipient: entity.User{ID: 1, Login: "Alice"},
		PR:        entity.PullRequest{ID: 77, RepositoryID: 10, RepositoryName: "arvore/hive-web", Number: 35, Title: "Fix login", HeadSHA: after.HeadSHA, State: entity.PRStateOpen},
		Before:    before,
		After:     after,
	}
}

func watch(turn entity.TurnState, head string) entity.NotificationWatch {
	return entity.NotificationWatch{Turn: turn, HeadSHA: head}
}

func only(t *testing.T, got []entity.Notification) entity.Notification {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("want one notification, got %+v", got)
	}
	return got[0]
}

func TestDecideNotifications_EnteringMinhaVezNotifiesWithItsReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		before *entity.NotificationWatch
		after  entity.NotificationWatch
		want   entity.NotificationReason
	}{
		{"a new PR", nil, watch(entity.TurnStateWaitingOnMe, "s1"), entity.NotificationReasonReviewRequested},
		{"leaves the draft gate", ptr(watch(entity.TurnStateHiddenGate, "s1")), watch(entity.TurnStateWaitingOnMe, "s1"), entity.NotificationReasonReviewRequested},
		{"a snooze ends", ptr(watch(entity.TurnStateSnoozed, "s1")), watch(entity.TurnStateWaitingOnMe, "s1"), entity.NotificationReasonSnoozeEnded},
		{"a commit after my approval", ptr(watch(entity.TurnStateApprovedByMe, "s1")), watch(entity.TurnStateWaitingOnMe, "s2"), entity.NotificationReasonNewCommit},
		{"a commit after I commented", ptr(watch(entity.TurnStateWaitingOnAuthor, "s1")), watch(entity.TurnStateWaitingOnMe, "s2"), entity.NotificationReasonNewCommit},
		{"the author replied", ptr(watch(entity.TurnStateWaitingOnAuthor, "s1")), watch(entity.TurnStateWaitingOnMe, "s1"), entity.NotificationReasonAuthorReplied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := only(t, service.DecideNotifications(change("pr.synchronize", tc.before, tc.after)))
			if n.Kind != entity.NotificationKindTurn || n.Reason != tc.want {
				t.Fatalf("got %s/%s, want turn/%s", n.Kind, n.Reason, tc.want)
			}
			if n.UserID != 1 || n.PullRequestID != 77 || n.PRNumber != 35 || n.PRTitle != "Fix login" || n.RepositoryName != "arvore/hive-web" || n.CommitSHA != tc.after.HeadSHA || n.ActorLogin != "carol" {
				t.Fatalf("the notification must carry the PR and who caused it: %+v", n)
			}
		})
	}
}

func TestDecideNotifications_StayingInMinhaVezOrLeavingItIsSilent(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]service.NotificationChange{
		"still mine, new commit": change("pr.synchronize", ptr(watch(entity.TurnStateWaitingOnMe, "s1")), watch(entity.TurnStateWaitingOnMe, "s2")),
		"left for the author":    change("pr.reviewed", ptr(watch(entity.TurnStateWaitingOnMe, "s1")), watch(entity.TurnStateWaitingOnAuthor, "s1")),
		"a teammate approved":    change("pr.approved", ptr(watch(entity.TurnStateWaitingOnMe, "s1")), watch(entity.TurnStateTeammateApproved, "s1")),
		"not my files":           change("pr.opened", nil, watch(entity.TurnStateHiddenGate, "s1")),
	} {
		if got := service.DecideNotifications(c); len(got) != 0 {
			t.Errorf("%s: want nothing, got %+v", name, got)
		}
	}
}

func TestDecideNotifications_MyOwnActionsNeverNotifyMe(t *testing.T) {
	t.Parallel()
	c := change("pr.synchronize", ptr(watch(entity.TurnStateHiddenGate, "s1")), watch(entity.TurnStateWaitingOnMe, "s1"))
	c.Event.ActorLogin = "alice" // the recipient, whatever the case
	if got := service.DecideNotifications(c); len(got) != 0 {
		t.Fatalf("my own action notified me: %+v", got)
	}
	merged := change("pr.merged", ptr(watch(entity.TurnStateApprovedByMe, "s1")), watch(entity.TurnStateMergedOrClosed, "s1"))
	merged.Event.ActorLogin, merged.Involved = "ALICE", true
	if got := service.DecideNotifications(merged); len(got) != 0 {
		t.Fatalf("my own merge notified me: %+v", got)
	}
}

func TestDecideNotifications_HighRiskOncePerClimb(t *testing.T) {
	t.Parallel()
	risky := func(before *entity.NotificationWatch, verdict entity.RiskVerdict) service.NotificationChange {
		after := watch(entity.TurnStateHiddenGate, "s2")
		after.Risk = verdict
		c := change("dependabot.analyzed", before, after)
		c.Event.ActorLogin = "dependabot[bot]"
		c.Event.Params = map[string]string{"package": "lodash", "from": "4.17.20", "to": "5.0.0", "verdict": string(verdict)}
		c.PR.IsDependabot = true
		return c
	}
	n := only(t, service.DecideNotifications(risky(nil, entity.RiskVerdictHigh)))
	if n.Kind != entity.NotificationKindRisk || n.Reason != entity.NotificationReasonHighRisk || n.Details["package"] != "lodash" || n.Details["to"] != "5.0.0" {
		t.Fatalf("unexpected risk notification: %+v", n)
	}

	medium := watch(entity.TurnStateHiddenGate, "s1")
	medium.Risk = entity.RiskVerdictMedium
	only(t, service.DecideNotifications(risky(&medium, entity.RiskVerdictHigh))) // climbed back

	high := watch(entity.TurnStateHiddenGate, "s1")
	high.Risk = entity.RiskVerdictHigh
	if got := service.DecideNotifications(risky(&high, entity.RiskVerdictHigh)); len(got) != 0 {
		t.Fatalf("a rebase that stays Alto Risco must not notify again: %+v", got)
	}
	if got := service.DecideNotifications(risky(nil, entity.RiskVerdictMedium)); len(got) != 0 {
		t.Fatalf("only Alto Risco notifies: %+v", got)
	}

	claimed := risky(nil, entity.RiskVerdictHigh)
	me := uint64(1)
	claimed.PR.ClaimedByUserID = &me
	if got := service.DecideNotifications(claimed); len(got) != 0 {
		t.Fatalf("the user who claimed the PR already knows: %+v", got)
	}
	closed := risky(nil, entity.RiskVerdictHigh)
	closed.PR.State = entity.PRStateClosed
	if got := service.DecideNotifications(closed); len(got) != 0 {
		t.Fatalf("a closed PR needs nobody: %+v", got)
	}
}

func TestDecideNotifications_InvolvedPRs(t *testing.T) {
	t.Parallel()

	merged := change("pr.merged", ptr(watch(entity.TurnStateApprovedByMe, "s1")), watch(entity.TurnStateMergedOrClosed, "s1"))
	merged.PR.State = entity.PRStateMerged
	if got := service.DecideNotifications(merged); len(got) != 0 {
		t.Fatalf("a merge of a PR I never acted on is not mine: %+v", got)
	}
	merged.Involved = true
	if n := only(t, service.DecideNotifications(merged)); n.Kind != entity.NotificationKindInvolved || n.Reason != entity.NotificationReasonMerged || n.CommitSHA != "" {
		t.Fatalf("want involved/merged once per PR, got %+v", n)
	}
	closed := change("pr.closed", ptr(watch(entity.TurnStateWaitingOnAuthor, "s1")), watch(entity.TurnStateMergedOrClosed, "s1"))
	closed.Involved, closed.PR.State = true, entity.PRStateClosed
	if n := only(t, service.DecideNotifications(closed)); n.Reason != entity.NotificationReasonClosed {
		t.Fatalf("want involved/closed, got %+v", n)
	}

	before := watch(entity.TurnStateApprovedByMe, "s1")
	before.ApprovedSHA = "s1"
	dismissed := change("pr.review_dismissed", &before, watch(entity.TurnStateWaitingOnMe, "s1"))
	dismissed.Involved = true
	if n := only(t, service.DecideNotifications(dismissed)); n.Kind != entity.NotificationKindInvolved || n.Reason != entity.NotificationReasonApprovalDismissed {
		t.Fatalf("a dismissed approval is one involved notification, not a turn one too: %+v", n)
	}

	approved := watch(entity.TurnStateApprovedByMe, "s1")
	approved.ApprovedSHA = "s1"
	red := change("ci.failed", &approved, approved)
	red.Involved = true
	if n := only(t, service.DecideNotifications(red)); n.Reason != entity.NotificationReasonCIFailed || n.CommitSHA != "s1" {
		t.Fatalf("want involved/ci_failed for the approved commit, got %+v", n)
	}
	notApproved := change("ci.failed", ptr(watch(entity.TurnStateWaitingOnAuthor, "s1")), watch(entity.TurnStateWaitingOnAuthor, "s1"))
	notApproved.Involved = true
	if got := service.DecideNotifications(notApproved); len(got) != 0 {
		t.Fatalf("red CI matters only on a commit I approved: %+v", got)
	}
}

func TestNotification_KeyTellsDuplicatesApart(t *testing.T) {
	t.Parallel()
	a := entity.Notification{Kind: entity.NotificationKindTurn, Reason: entity.NotificationReasonNewCommit, PullRequestID: 77, CommitSHA: "s2"}
	b := a
	if a.Key() != b.Key() {
		t.Fatal("the same notification must have the same key")
	}
	b.CommitSHA = "s3"
	if a.Key() == b.Key() {
		t.Fatal("another commit is another notification")
	}
}

func TestNotificationSettings_PopsUp(t *testing.T) {
	t.Parallel()
	s := entity.NotificationSettings{PopupsOff: []entity.NotificationKind{entity.NotificationKindRisk}}
	if !s.PopsUp(entity.NotificationKindTurn) || s.PopsUp(entity.NotificationKindRisk) {
		t.Fatalf("a kind switched off must not pop up, others must: %+v", s)
	}
	s.Paused = true
	if s.PopsUp(entity.NotificationKindTurn) {
		t.Fatal("paused notifications never pop up")
	}
	if err := (entity.NotificationSettings{PopupsOff: []entity.NotificationKind{"nope"}}).Validate(); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
}

func ptr[T any](v T) *T { return &v }
