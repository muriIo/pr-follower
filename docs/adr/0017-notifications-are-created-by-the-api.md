# ADR-0017: Notifications Are Created by the API; Delivery Channels Are Adapters

## Status
Accepted (2026-11-23). Builds on ADR-0011 (idempotent integrations) and ADR-0012 (any replica serves any request). Terms as in [`GLOSSARY.md`](../../GLOSSARY.md): notification, notification kind, delivery channel, involved PR.

## Context
PR Follower tells a user when something just became theirs to act on: a PR entering "Minha Vez", a Dependabot analysis that came out Alto Risco, a change to a PR they acted on. A notification is a **transition**, never a state that persists: one sync or batch can move many PRs at once, and notifying every update would flood the user, as the CODEOWNERS e-mails did.

The panel could compare one workspace load with the next and notify from the browser. That works only while a panel tab is open, and every tab and device would decide on its own: two tabs show two popups, the history differs per device, and a delivery that reaches the user with the panel closed (Web Push, a future mobile app) would need the same decision made a second time on the server, where it could disagree with the browser's.

The turn is derived on read (`service.ComputeTurnState`), per viewer; nothing stored says what a user's turn *was*, so a transition can only be seen by remembering it.

## Decision
1. **The API is the only source of notifications.** A notifier (`usecase/notify`) reads the activity log in order, from its own position stored in Postgres; one replica runs it at a time, holding a Postgres advisory lock. For each activity it works out the touched PR's turn for every user following the repository, compares it with the turn it remembered, and creates the notifications the change calls for. Webhooks and user actions keep publishing activity and know nothing about notifications.
2. **Idempotent by key.** Each notification has a unique key (user, PR, kind, reason, commit), so reading an activity twice (a redelivered webhook, a crash before the position is saved) creates nothing new (ADR-0011).
3. **Silent first sight.** The first time the notifier sees a repository for a user (a new follow, or the upgrade that introduces notifications) it records the turns it finds and notifies nothing; PRs opened after that are new. The upgrade starts the notifier at the newest activity, so the log's past is never replayed.
4. **Your own actions never notify you**: an activity whose actor is the recipient creates nothing for them.
5. **Data, not text.** A notification stores its kind, reason, the PR and the facts its wording needs (actor, package, versions). Each channel phrases it in the reader's language (ADR-0008); the API never needs to know it.
6. **Delivery channels are adapters** of `port.NotificationChannel`, under `internal/adapter/notifychannel/`. The open panel's channel (the SSE stream, per user) is always on; Web Push and a future app are further adapters registered by configuration. Grouping a burst into one popup is each channel's choice; the record keeps one notification per PR.
7. **Settings are the user's, stored on the server**: a global pause and an on/off switch per kind decide which notifications pop up on any channel. The notification center lists every notification whatever the switches say. Notifications are kept 30 days.

## Consequences
- **Positive:** every tab and device shows the same notifications, read state and settings; a new delivery channel is one adapter plus its configuration, with no change to what a notification is.
- **Positive:** the decision about what deserves a notification lives in one tested place.
- **Negative:** the notifier evaluates the turn of every follower for each activity, a read per follower and PR; it runs on one replica at a time, so a burst queues behind it rather than spreading across replicas.
- **Negative:** a turn that changes with no activity (a PR's changed files read for the first time on a workspace load) is noticed only at the next activity on that PR.

## Compliance & Automated Gate
`adrcheck.ValidateRepository` fails when a Go file in `backend/internal/` outside `usecase/notify` and `adapter/persistence` (tests excepted) calls `CreateNotifications`, so nothing but the notifier decides that a notification exists; and when a package under `backend/internal/adapter/notifychannel/` has no test function whose name contains `Redeliver` (a channel handed the same notification twice must not deliver it twice, ADR-0011).
