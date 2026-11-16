-- +goose Up
-- Each user's latest "sync with GitHub", shared by every API replica: a start, a progress poll or a
-- cancel may land on any of them. `run_id` tells one run from the next, so a run that lost its claim
-- cannot overwrite a newer one. `heartbeat_ms` (Unix milliseconds, compared in SQL the same way on
-- every database) is the running replica's latest sign of life; a stale one means it went away.
CREATE TABLE IF NOT EXISTS sync_jobs (
    user_id BIGINT PRIMARY KEY,
    run_id TEXT NOT NULL,
    state TEXT NOT NULL,
    progress_total BIGINT NOT NULL DEFAULT 0,
    progress_done BIGINT NOT NULL DEFAULT 0,
    progress_current TEXT NOT NULL DEFAULT '',
    result_json TEXT NOT NULL DEFAULT '',
    error_json TEXT NOT NULL DEFAULT '',
    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
    started_at TIMESTAMPTZ NOT NULL,
    heartbeat_ms BIGINT NOT NULL,
    finished_at TIMESTAMPTZ
);
