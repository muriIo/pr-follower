-- +goose Up
-- The activity timeline and the live stream's source. Every API replica appends here and reads it back
-- in `seq` order, so activity survives a restart and reaches users whichever replica they are connected
-- to. `event_id` is not unique on purpose: some activities carry deterministic ids (a repeated follow, a
-- re-analysis of the same commit) and must still be recorded.
CREATE TABLE IF NOT EXISTS activity_events (
    seq BIGSERIAL PRIMARY KEY,
    event_id TEXT NOT NULL,
    type TEXT NOT NULL,
    repository_id BIGINT NOT NULL,
    repository_name TEXT NOT NULL DEFAULT '',
    pull_request_id BIGINT NOT NULL DEFAULT 0,
    pr_number BIGINT NOT NULL DEFAULT 0,
    actor_login TEXT NOT NULL DEFAULT '',
    params_json TEXT NOT NULL DEFAULT '{}',
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_activity_events_repo_seq ON activity_events (repository_id, seq);
CREATE INDEX IF NOT EXISTS idx_activity_events_occurred_at ON activity_events (occurred_at);
