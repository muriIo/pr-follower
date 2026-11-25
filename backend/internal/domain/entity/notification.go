package entity

import (
	"fmt"
	"time"
)

// NotificationKind is what a notification is about. Each kind has its own look and its own
// on/off switch (GLOSSARY.md).
type NotificationKind string

const (
	// NotificationKindTurn: a PR entered the user's Minha Vez.
	NotificationKindTurn NotificationKind = "turn"
	// NotificationKindRisk: a Dependabot analysis came out Alto Risco.
	NotificationKindRisk NotificationKind = "risk"
	// NotificationKindInvolved: something that ends or undoes the user's work on a PR they acted on.
	NotificationKindInvolved NotificationKind = "involved"
)

// NotificationKinds are the kinds a user can switch on and off, in the order the settings list them.
var NotificationKinds = []NotificationKind{NotificationKindTurn, NotificationKindRisk, NotificationKindInvolved}

// NotificationReason is why a notification of a kind was created; the wording follows it.
type NotificationReason string

const (
	NotificationReasonReviewRequested   NotificationReason = "review_requested"
	NotificationReasonNewCommit         NotificationReason = "new_commit"
	NotificationReasonSnoozeEnded       NotificationReason = "snooze_ended"
	NotificationReasonAuthorReplied     NotificationReason = "author_replied"
	NotificationReasonHighRisk          NotificationReason = "high_risk"
	NotificationReasonMerged            NotificationReason = "merged"
	NotificationReasonClosed            NotificationReason = "closed"
	NotificationReasonApprovalDismissed NotificationReason = "approval_dismissed"
	NotificationReasonCIFailed          NotificationReason = "ci_failed"
)

// Notification tells one user that something just became theirs to act on (ADR-0017). It holds
// facts, never text: each delivery channel phrases it in the reader's language.
type Notification struct {
	ID             uint64             `json:"id"`
	UserID         uint64             `json:"-"`
	Kind           NotificationKind   `json:"kind"`
	Reason         NotificationReason `json:"reason"`
	RepositoryID   uint64             `json:"repository_id"`
	RepositoryName string             `json:"repository_name"`
	PullRequestID  uint64             `json:"pull_request_id"`
	PRNumber       int                `json:"pr_number"`
	PRTitle        string             `json:"pr_title"`
	// CommitSHA is the head commit the notification is about, "" when any commit is the same news
	// (a merge happens once per PR).
	CommitSHA  string            `json:"commit_sha,omitempty"`
	ActorLogin string            `json:"actor_login,omitempty"`
	Details    map[string]string `json:"details,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	ReadAt     *time.Time        `json:"read_at,omitempty"`
	// PopUp says whether the user's settings let it show outside the notification center. It is
	// not stored: a channel learns it when the notification is handed over.
	PopUp bool `json:"pop_up"`
}

// Key identifies the news within one user's notifications: the same kind, reason, PR and commit is
// the same notification however many times an activity is read (ADR-0011).
func (n Notification) Key() string {
	return fmt.Sprintf("%s:%s:%d:%s", n.Kind, n.Reason, n.PullRequestID, n.CommitSHA)
}

// NotificationSettings are one user's choices about which notifications pop up. The notification
// center lists every notification whatever they say.
type NotificationSettings struct {
	// Paused stops every popup while the browser permission stays as it is.
	Paused bool `json:"paused"`
	// PopupsOff are the kinds that never pop up.
	PopupsOff []NotificationKind `json:"popups_off"`
}

// PopsUp says whether a notification of kind may show outside the notification center.
func (s NotificationSettings) PopsUp(kind NotificationKind) bool {
	if s.Paused {
		return false
	}
	for _, off := range s.PopupsOff {
		if off == kind {
			return false
		}
	}
	return true
}

// Validate refuses a kind PR Follower does not have.
func (s NotificationSettings) Validate() error {
	for _, off := range s.PopupsOff {
		known := false
		for _, kind := range NotificationKinds {
			known = known || off == kind
		}
		if !known {
			return fmt.Errorf("%w: unknown notification kind %q", ErrInvalidInput, off)
		}
	}
	return nil
}

// NotificationWatch is what the notifier last saw of one PR for one user, so it can tell a change
// from a state that merely persists.
type NotificationWatch struct {
	UserID        uint64    `json:"-"`
	PullRequestID uint64    `json:"-"`
	RepositoryID  uint64    `json:"-"`
	Turn          TurnState `json:"turn"`
	HeadSHA       string    `json:"head_sha"`
	// ApprovedSHA is the commit the user's standing approval covers, "" when they have none.
	ApprovedSHA string `json:"approved_sha"`
	// Risk is the verdict of the head commit's Dependabot analysis, "" when there is none.
	Risk RiskVerdict `json:"risk"`
}
