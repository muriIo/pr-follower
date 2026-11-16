-- +goose Up
-- Why the latest analysis of a Dependabot PR's commit failed, so the Radar can say "analysis failed"
-- with the reason instead of "not analysed". One row per PR and commit: a later failure of the same
-- commit replaces it, and an analysis of the commit (in dependabot_analyses) supersedes it.
CREATE TABLE IF NOT EXISTS dependabot_analysis_failures (
    pull_request_id BIGINT NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    head_sha TEXT NOT NULL,
    error_kind TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    failed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (pull_request_id, head_sha)
);
