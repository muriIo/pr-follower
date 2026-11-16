-- +goose Up
-- GitHub's pull_request.updated_at (Unix seconds) of the newest webhook applied to each PR. GitHub
-- may deliver webhooks out of order (ADR-0011): a delivery older than this is ignored, so a late
-- "synchronize" can never reopen or move a PR that was merged after it.
CREATE TABLE IF NOT EXISTS pull_request_github_versions (
    pull_request_id BIGINT PRIMARY KEY REFERENCES pull_requests(id) ON DELETE CASCADE,
    github_updated_at BIGINT NOT NULL
);
