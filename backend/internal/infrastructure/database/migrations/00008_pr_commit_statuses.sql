-- +goose Up
-- Legacy commit statuses (Jenkins, Vercel and other CI that does not use check runs). Each update of a
-- status is a new row with a new id; the latest per context is what GitHub shows. They have their own
-- id space, so they cannot share pr_check_runs.
CREATE TABLE IF NOT EXISTS pr_commit_statuses (
    status_id BIGINT PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    head_sha TEXT NOT NULL,
    context TEXT NOT NULL,
    state TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    target_url TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pr_commit_statuses_repo_head ON pr_commit_statuses (repository_id, head_sha);
