-- +goose Up
-- CI and CODEOWNERS state come from GitHub, never from defaults.

-- Whether a repository has a CODEOWNERS file: '' (not checked) | present | absent.
ALTER TABLE repositories ADD COLUMN codeowners_status TEXT NOT NULL DEFAULT '';

-- Check runs reported by check_run webhooks; a PR's CI is derived from the runs on its head.
CREATE TABLE IF NOT EXISTS pr_check_runs (
    check_run_id BIGINT PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    head_sha TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    conclusion TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pr_check_runs_repo_head ON pr_check_runs (repository_id, head_sha);

-- Until now every PR was stored as PENDING by default and no check run was ever recorded,
-- so PENDING meant "unknown". Say so.
UPDATE pull_requests SET ci_status = 'NONE' WHERE ci_status = 'PENDING';
