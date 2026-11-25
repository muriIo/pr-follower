package service

import (
	"maps"
	"strings"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

// NotificationChange is one activity seen from one user: the PR as it is now, what the notifier
// last saw of it for them and what it sees now.
type NotificationChange struct {
	Event     entity.ActivityEvent
	Recipient entity.User
	PR        entity.PullRequest
	// Before is nil for a PR the user has never been shown since notifications started for the
	// repository: a PR opened after that.
	Before *entity.NotificationWatch
	After  entity.NotificationWatch
	// Involved: the user acted on the PR (reviewed, commented, claimed).
	Involved bool
}

// DecideNotifications returns what a change means for the user (ADR-0017): a notification is
// a transition into something theirs to act on, never a state that merely persists, and never
// something they did themselves.
func DecideNotifications(c NotificationChange) []entity.Notification {
	if strings.EqualFold(c.Event.ActorLogin, c.Recipient.Login) {
		return nil
	}
	if n, ok := involvedNotification(c); ok {
		return []entity.Notification{n}
	}
	if n, ok := turnNotification(c); ok {
		return []entity.Notification{n}
	}
	if n, ok := riskNotification(c); ok {
		return []entity.Notification{n}
	}
	return nil
}

func involvedNotification(c NotificationChange) (entity.Notification, bool) {
	if !c.Involved {
		return entity.Notification{}, false
	}
	switch {
	case c.Event.Type == "pr.merged" && c.PR.State == entity.PRStateMerged:
		return newNotification(c, entity.NotificationKindInvolved, entity.NotificationReasonMerged, ""), true
	case c.Event.Type == "pr.closed" && c.PR.State == entity.PRStateClosed:
		return newNotification(c, entity.NotificationKindInvolved, entity.NotificationReasonClosed, ""), true
	case c.Event.Type == "pr.review_dismissed" && c.Before != nil && c.Before.ApprovedSHA != "" &&
		c.Before.ApprovedSHA == c.PR.HeadSHA && c.After.ApprovedSHA == "":
		return newNotification(c, entity.NotificationKindInvolved, entity.NotificationReasonApprovalDismissed, c.PR.HeadSHA), true
	case c.Event.Type == "ci.failed" && c.After.ApprovedSHA != "" && c.After.ApprovedSHA == c.PR.HeadSHA:
		return newNotification(c, entity.NotificationKindInvolved, entity.NotificationReasonCIFailed, c.PR.HeadSHA), true
	}
	return entity.Notification{}, false
}

func turnNotification(c NotificationChange) (entity.Notification, bool) {
	if c.After.Turn != entity.TurnStateWaitingOnMe {
		return entity.Notification{}, false
	}
	reason := entity.NotificationReasonReviewRequested
	if b := c.Before; b != nil {
		newCommit := b.HeadSHA != c.After.HeadSHA
		switch b.Turn {
		case entity.TurnStateWaitingOnMe:
			return entity.Notification{}, false // still mine: not news
		case entity.TurnStateSnoozed:
			reason = entity.NotificationReasonSnoozeEnded
		case entity.TurnStateApprovedByMe, entity.TurnStateTeammateApproved:
			if newCommit {
				reason = entity.NotificationReasonNewCommit
			}
		case entity.TurnStateWaitingOnAuthor:
			reason = entity.NotificationReasonAuthorReplied
			if newCommit {
				reason = entity.NotificationReasonNewCommit
			}
		}
	}
	return newNotification(c, entity.NotificationKindTurn, reason, c.After.HeadSHA), true
}

func riskNotification(c NotificationChange) (entity.Notification, bool) {
	if c.Event.Type != "dependabot.analyzed" || c.After.Risk != entity.RiskVerdictHigh || c.PR.State != entity.PRStateOpen {
		return entity.Notification{}, false
	}
	if c.Before != nil && c.Before.Risk == entity.RiskVerdictHigh {
		return entity.Notification{}, false // a rebase that stays Alto Risco is not news
	}
	if c.PR.ClaimedByUserID != nil && *c.PR.ClaimedByUserID == c.Recipient.ID {
		return entity.Notification{}, false // whoever claimed it already knows
	}
	n := newNotification(c, entity.NotificationKindRisk, entity.NotificationReasonHighRisk, c.After.HeadSHA)
	n.Details = map[string]string{}
	for _, k := range []string{"package", "from", "to"} {
		if v, ok := c.Event.Params[k]; ok {
			n.Details[k] = v
		}
	}
	return n, true
}

func newNotification(c NotificationChange, kind entity.NotificationKind, reason entity.NotificationReason, commit string) entity.Notification {
	n := entity.Notification{
		UserID:         c.Recipient.ID,
		Kind:           kind,
		Reason:         reason,
		RepositoryID:   c.PR.RepositoryID,
		RepositoryName: c.PR.RepositoryName,
		PullRequestID:  c.PR.ID,
		PRNumber:       c.PR.Number,
		PRTitle:        c.PR.Title,
		CommitSHA:      commit,
		ActorLogin:     c.Event.ActorLogin,
		CreatedAt:      time.Now().UTC(),
	}
	if n.RepositoryName == "" {
		n.RepositoryName = c.Event.RepositoryName
	}
	if len(c.Event.Params) > 0 && kind != entity.NotificationKindRisk {
		n.Details = maps.Clone(c.Event.Params)
	}
	return n
}
