# PR Follower

A team-shared triage workspace for the pull requests a person reviews as a CODEOWNER and the Dependabot PRs of the repositories they follow.

## Triage

**Followed repository**:
A repository a user chose to see in their workspace. "Follow" means only this.
_Avoid_: Watched repository, subscribed repository

**Turn**:
Whose move a PR is waiting on, from one user's point of view, for the PR's current head commit.
_Avoid_: Status, queue state

**Minha Vez**:
The set of PRs whose turn is the user's: waiting on their review.
_Avoid_: Inbox, my queue, waiting on me (in the UI)

**Involved PR**:
A PR the user acted on (approved, requested changes, commented, claimed or merged), until it is merged, closed or handed off. Snoozing does not involve.
_Avoid_: Followed PR, watched PR, subscribed PR

**Hand-off**:
One user passing a PR they are expected to review to a teammate, with an optional note.
_Avoid_: Reassign, delegate, transfer

## Notifications

**Notification**:
A message to one user that something just became theirs to act on. It is created by a change, never by a state that merely persists, so a PR already in Minha Vez does not notify again.
_Avoid_: Alert, event, activity

**Notification kind**:
What a notification is about: its turn (a PR entered Minha Vez), a high risk (a Dependabot analysis came out Alto Risco), an involved PR (merged or closed, approval dismissed, CI red after approval), or a hand-off. Each kind has its own look and its own on/off switch.
_Avoid_: Notification type, category

**Delivery channel**:
A way a notification reaches the user outside the API: the open panel, Web Push, or a future app. The API creates each notification once and the channels only deliver it.
_Avoid_: Provider, transport, sink

**Notification center**:
The place in the panel listing a user's notifications, read and unread, with the notification settings.
_Avoid_: Inbox, notification panel

**Activity**:
The team-wide log of everything that happened in followed repositories. Activity is not a notification: most activity notifies nobody.
_Avoid_: Notifications, feed

**Browser permission**:
Whether the browser lets this site show system notifications (`granted`, `denied`, `default`). It is the user's browser setting, which the panel can request but never override.
_Avoid_: Notifications enabled

**Paused notifications**:
The user's choice, in the panel, to stop system notifications while the browser permission stays granted. Notifications still reach the notification center.
_Avoid_: Disabled, muted, silenced
