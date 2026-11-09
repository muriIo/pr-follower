-- +goose Up
-- Forward-only migration: rollback sections are strictly forbidden.
-- To modify the schema in the future, always create a new numbered migration file.

CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    github_id BIGINT NOT NULL UNIQUE,
    login TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    teams_json TEXT NOT NULL DEFAULT '[]',
    encrypted_token TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS repositories (
    id BIGSERIAL PRIMARY KEY,
    github_repo_id BIGINT NOT NULL UNIQUE,
    installation_id BIGINT NOT NULL DEFAULT 0,
    owner TEXT NOT NULL,
    name TEXT NOT NULL,
    full_name TEXT NOT NULL,
    default_branch TEXT NOT NULL DEFAULT 'main',
    codeowners_json TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user_repo_follows (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, repository_id)
);

CREATE TABLE IF NOT EXISTS pull_requests (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    repository_name TEXT NOT NULL DEFAULT '',
    github_pr_id BIGINT NOT NULL UNIQUE,
    number BIGINT NOT NULL,
    title TEXT NOT NULL,
    author_login TEXT NOT NULL DEFAULT '',
    html_url TEXT NOT NULL DEFAULT '',
    is_dependabot BOOLEAN NOT NULL DEFAULT FALSE,
    is_draft BOOLEAN NOT NULL DEFAULT FALSE,
    state TEXT NOT NULL DEFAULT 'OPEN',
    mergeable_state TEXT NOT NULL DEFAULT 'CLEAN',
    ci_status TEXT NOT NULL DEFAULT 'PENDING',
    head_sha TEXT NOT NULL,
    base_ref TEXT NOT NULL DEFAULT 'main',
    claimed_by_user_id BIGINT,
    claimed_by_login TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pull_requests_repo_state ON pull_requests (repository_id, state);
CREATE INDEX IF NOT EXISTS idx_pull_requests_repo_number ON pull_requests (repository_id, number);

CREATE TABLE IF NOT EXISTS dependabot_analyses (
    id BIGSERIAL PRIMARY KEY,
    pull_request_id BIGINT NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    head_sha TEXT NOT NULL,
    ecosystem TEXT NOT NULL DEFAULT 'npm',
    package_name TEXT NOT NULL,
    from_version TEXT NOT NULL DEFAULT '',
    to_version TEXT NOT NULL DEFAULT '',
    semver_bump_type TEXT NOT NULL DEFAULT 'UNKNOWN',
    is_direct_dep BOOLEAN NOT NULL DEFAULT FALSE,
    dep_scope TEXT NOT NULL DEFAULT 'TRANSITIVE',
    risk_verdict TEXT NOT NULL DEFAULT 'MEDIUM',
    risk_score BIGINT NOT NULL DEFAULT 0,
    verdict_summary_key TEXT NOT NULL DEFAULT '',
    path_graph_json TEXT NOT NULL DEFAULT '[]',
    blast_radius_json TEXT NOT NULL DEFAULT '[]',
    advisories_json TEXT NOT NULL DEFAULT '[]',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (pull_request_id, head_sha)
);

CREATE TABLE IF NOT EXISTS pr_user_states (
    id BIGSERIAL PRIMARY KEY,
    pull_request_id BIGINT NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    turn_state TEXT NOT NULL DEFAULT 'WAITING_ON_ME',
    team_already_approved BOOLEAN NOT NULL DEFAULT FALSE,
    approved_by_login TEXT NOT NULL DEFAULT '',
    snoozed_until_condition TEXT NOT NULL DEFAULT '',
    snoozed_at_sha TEXT NOT NULL DEFAULT '',
    owned_files_json TEXT NOT NULL DEFAULT '[]',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (pull_request_id, user_id)
);
