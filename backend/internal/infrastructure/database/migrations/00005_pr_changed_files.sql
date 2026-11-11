-- +goose Up
-- The files a PR changes at its head commit, read from GitHub. They are the same for every
-- user, so CODEOWNERS ownership (and the turn) is derived from them when the workspace is read.
-- One row per PR; a new commit replaces it. Patches are not stored (the drawer reads them live).
CREATE TABLE IF NOT EXISTS pr_changed_files (
    pull_request_id BIGINT PRIMARY KEY REFERENCES pull_requests(id) ON DELETE CASCADE,
    head_sha TEXT NOT NULL,
    files_json TEXT NOT NULL DEFAULT '[]',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
