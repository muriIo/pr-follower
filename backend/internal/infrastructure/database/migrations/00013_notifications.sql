-- +goose Up
-- Notifications (ADR-0017): created once by the notifier, listed by the notification center and handed
-- to every delivery channel. A row holds facts, never text. `dedup_key` (kind, reason, PR, commit) makes
-- a second reading of the same activity create nothing. Kept 30 days.
CREATE TABLE IF NOT EXISTS notifications (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    dedup_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    reason TEXT NOT NULL,
    repository_id BIGINT NOT NULL,
    repository_name TEXT NOT NULL DEFAULT '',
    pull_request_id BIGINT NOT NULL,
    pr_number BIGINT NOT NULL,
    pr_title TEXT NOT NULL DEFAULT '',
    commit_sha TEXT NOT NULL DEFAULT '',
    actor_login TEXT NOT NULL DEFAULT '',
    details_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL,
    read_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_notifications_user_key ON notifications (user_id, dedup_key);
CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications (user_id, id);
CREATE INDEX IF NOT EXISTS idx_notifications_created_at ON notifications (created_at);

-- Each user's notification settings: a global pause and the kinds that never pop up (comma-separated).
CREATE TABLE IF NOT EXISTS notification_settings (
    user_id BIGINT PRIMARY KEY,
    paused BOOLEAN NOT NULL DEFAULT FALSE,
    popups_off TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL
);

-- What the notifier last saw of a PR for a user, so it can tell a change from a state that persists.
CREATE TABLE IF NOT EXISTS notification_watches (
    user_id BIGINT NOT NULL,
    pull_request_id BIGINT NOT NULL,
    repository_id BIGINT NOT NULL,
    turn TEXT NOT NULL DEFAULT '',
    head_sha TEXT NOT NULL DEFAULT '',
    approved_sha TEXT NOT NULL DEFAULT '',
    risk TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, pull_request_id)
);

CREATE INDEX IF NOT EXISTS idx_notification_watches_repo ON notification_watches (user_id, repository_id);

-- A follow the notifier has seen: its open PRs were recorded silently, so only what changes after
-- this notifies. A follow without a row is new (or predates notifications).
CREATE TABLE IF NOT EXISTS notification_baselines (
    user_id BIGINT NOT NULL,
    repository_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, repository_id)
);

-- How far each reader of a log has read it: the notifier reads the activity log, a delivery channel
-- that needs a shared position reads the notifications.
CREATE TABLE IF NOT EXISTS log_positions (
    name TEXT PRIMARY KEY,
    seq BIGINT NOT NULL
);
