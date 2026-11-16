-- name: UpsertUser :one
INSERT INTO users (github_id, login, name, avatar_url, teams_json, encrypted_token, encrypted_refresh_token, access_token_expires_at, refresh_token_expires_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, CURRENT_TIMESTAMP)
ON CONFLICT (github_id) DO UPDATE SET
    login = EXCLUDED.login,
    name = EXCLUDED.name,
    avatar_url = EXCLUDED.avatar_url,
    teams_json = EXCLUDED.teams_json,
    encrypted_token = EXCLUDED.encrypted_token,
    encrypted_refresh_token = EXCLUDED.encrypted_refresh_token,
    access_token_expires_at = EXCLUDED.access_token_expires_at,
    refresh_token_expires_at = EXCLUDED.refresh_token_expires_at,
    updated_at = CURRENT_TIMESTAMP
RETURNING id, github_id, login, name, avatar_url, teams_json, encrypted_token, created_at, updated_at, encrypted_refresh_token, access_token_expires_at, refresh_token_expires_at;

-- name: GetUserByID :one
SELECT id, github_id, login, name, avatar_url, teams_json, encrypted_token, created_at, updated_at, encrypted_refresh_token, access_token_expires_at, refresh_token_expires_at
FROM users
WHERE id = $1;

-- name: GetUserByGitHubID :one
SELECT id, github_id, login, name, avatar_url, teams_json, encrypted_token, created_at, updated_at, encrypted_refresh_token, access_token_expires_at, refresh_token_expires_at
FROM users
WHERE github_id = $1;

-- name: UpsertRepository :one
INSERT INTO repositories (github_repo_id, installation_id, owner, name, full_name, default_branch, codeowners_json, access, codeowners_status, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, CURRENT_TIMESTAMP)
ON CONFLICT (github_repo_id) DO UPDATE SET
    installation_id = EXCLUDED.installation_id,
    access = EXCLUDED.access,
    codeowners_status = EXCLUDED.codeowners_status,
    owner = EXCLUDED.owner,
    name = EXCLUDED.name,
    full_name = EXCLUDED.full_name,
    default_branch = EXCLUDED.default_branch,
    codeowners_json = EXCLUDED.codeowners_json,
    updated_at = CURRENT_TIMESTAMP
RETURNING id, github_repo_id, installation_id, owner, name, full_name, default_branch, codeowners_json, created_at, updated_at, access, codeowners_status;

-- name: GetRepositoryByID :one
SELECT id, github_repo_id, installation_id, owner, name, full_name, default_branch, codeowners_json, created_at, updated_at, access, codeowners_status
FROM repositories
WHERE id = $1;

-- name: GetRepositoryByGitHubRepoID :one
SELECT id, github_repo_id, installation_id, owner, name, full_name, default_branch, codeowners_json, created_at, updated_at, access, codeowners_status
FROM repositories
WHERE github_repo_id = $1;

-- name: ListFollowedRepositoriesByUser :many
SELECT r.id, r.github_repo_id, r.installation_id, r.owner, r.name, r.full_name, r.default_branch, r.codeowners_json, r.created_at, r.updated_at, r.access, r.codeowners_status
FROM repositories r
JOIN user_repo_follows urf ON urf.repository_id = r.id
WHERE urf.user_id = $1
ORDER BY r.full_name ASC;

-- name: FollowRepository :exec
INSERT INTO user_repo_follows (user_id, repository_id)
VALUES ($1, $2)
ON CONFLICT (user_id, repository_id) DO NOTHING;

-- name: UnfollowRepository :exec
DELETE FROM user_repo_follows
WHERE user_id = $1 AND repository_id = $2;

-- name: UpsertPullRequest :one
INSERT INTO pull_requests (
    repository_id, repository_name, github_pr_id, number, title, author_login, html_url,
    is_dependabot, is_draft, state, mergeable_state, ci_status, head_sha, base_ref,
    claimed_by_user_id, claimed_by_login, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13, $14,
    $15, $16, CURRENT_TIMESTAMP
)
ON CONFLICT (github_pr_id) DO UPDATE SET
    title = EXCLUDED.title,
    is_draft = EXCLUDED.is_draft,
    state = EXCLUDED.state,
    mergeable_state = EXCLUDED.mergeable_state,
    ci_status = EXCLUDED.ci_status,
    head_sha = EXCLUDED.head_sha,
    claimed_by_user_id = EXCLUDED.claimed_by_user_id,
    claimed_by_login = EXCLUDED.claimed_by_login,
    updated_at = CURRENT_TIMESTAMP
RETURNING id, repository_id, repository_name, github_pr_id, number, title, author_login, html_url,
          is_dependabot, is_draft, state, mergeable_state, ci_status, head_sha, base_ref,
          claimed_by_user_id, claimed_by_login, updated_at;

-- name: AdvancePullRequestGitHubVersion :execrows
-- Moves a PR's GitHub version forward only: no row is written when the stored one is newer.
INSERT INTO pull_request_github_versions (pull_request_id, github_updated_at)
VALUES ($1, $2)
ON CONFLICT (pull_request_id) DO UPDATE SET github_updated_at = EXCLUDED.github_updated_at
WHERE pull_request_github_versions.github_updated_at <= EXCLUDED.github_updated_at;

-- name: GetPullRequestByID :one
SELECT id, repository_id, repository_name, github_pr_id, number, title, author_login, html_url,
       is_dependabot, is_draft, state, mergeable_state, ci_status, head_sha, base_ref,
       claimed_by_user_id, claimed_by_login, updated_at
FROM pull_requests
WHERE id = $1;

-- name: GetPullRequestByRepoAndNumber :one
SELECT id, repository_id, repository_name, github_pr_id, number, title, author_login, html_url,
       is_dependabot, is_draft, state, mergeable_state, ci_status, head_sha, base_ref,
       claimed_by_user_id, claimed_by_login, updated_at
FROM pull_requests
WHERE repository_id = $1 AND number = $2;

-- name: UpsertDependabotAnalysis :one
INSERT INTO dependabot_analyses (
    pull_request_id, head_sha, ecosystem, package_name, from_version, to_version,
    semver_bump_type, is_direct_dep, dep_scope, risk_verdict, risk_score,
    verdict_summary_key, path_graph_json, blast_radius_json, advisories_json, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11,
    $12, $13, $14, $15, CURRENT_TIMESTAMP
)
ON CONFLICT (pull_request_id, head_sha) DO UPDATE SET
    risk_verdict = EXCLUDED.risk_verdict,
    risk_score = EXCLUDED.risk_score,
    verdict_summary_key = EXCLUDED.verdict_summary_key,
    path_graph_json = EXCLUDED.path_graph_json,
    blast_radius_json = EXCLUDED.blast_radius_json,
    advisories_json = EXCLUDED.advisories_json,
    updated_at = CURRENT_TIMESTAMP
RETURNING id, pull_request_id, head_sha, ecosystem, package_name, from_version, to_version,
          semver_bump_type, is_direct_dep, dep_scope, risk_verdict, risk_score,
          verdict_summary_key, path_graph_json, blast_radius_json, advisories_json, updated_at;

-- name: GetDependabotAnalysisByPRAndSHA :one
SELECT id, pull_request_id, head_sha, ecosystem, package_name, from_version, to_version,
       semver_bump_type, is_direct_dep, dep_scope, risk_verdict, risk_score,
       verdict_summary_key, path_graph_json, blast_radius_json, advisories_json, updated_at
FROM dependabot_analyses
WHERE pull_request_id = $1 AND head_sha = $2;

-- name: UpsertUserPRState :one
INSERT INTO pr_user_states (
    pull_request_id, user_id, turn_state, team_already_approved, approved_by_login,
    snoozed_until_condition, snoozed_at_sha, owned_files_json, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, CURRENT_TIMESTAMP
)
ON CONFLICT (pull_request_id, user_id) DO UPDATE SET
    turn_state = EXCLUDED.turn_state,
    team_already_approved = EXCLUDED.team_already_approved,
    approved_by_login = EXCLUDED.approved_by_login,
    snoozed_until_condition = EXCLUDED.snoozed_until_condition,
    snoozed_at_sha = EXCLUDED.snoozed_at_sha,
    owned_files_json = EXCLUDED.owned_files_json,
    updated_at = CURRENT_TIMESTAMP
RETURNING id, pull_request_id, user_id, turn_state, team_already_approved, approved_by_login,
          snoozed_until_condition, snoozed_at_sha, owned_files_json, updated_at;

-- name: GetUserPRState :one
SELECT id, pull_request_id, user_id, turn_state, team_already_approved, approved_by_login,
       snoozed_until_condition, snoozed_at_sha, owned_files_json, updated_at
FROM pr_user_states
WHERE pull_request_id = $1 AND user_id = $2;

-- name: CreateSession :exec
-- idempotent: token_hash is the SHA-256 of a random 256-bit token, so a retry cannot collide.
INSERT INTO sessions (token_hash, user_id, expires_at)
VALUES ($1, $2, $3);

-- name: GetSessionByTokenHash :one
SELECT token_hash, user_id, expires_at, created_at
FROM sessions
WHERE token_hash = $1;

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE token_hash = $1;

-- name: DeleteExpiredSessions :execrows
-- A session expires at expires_at (Unix seconds), like entity.Session.IsExpired.
DELETE FROM sessions
WHERE expires_at <= $1;

-- name: ListRepositoriesByInstallation :many
SELECT id, github_repo_id, installation_id, owner, name, full_name, default_branch, codeowners_json, created_at, updated_at, access, codeowners_status
FROM repositories
WHERE installation_id = $1
ORDER BY full_name ASC;

-- name: UpdateRepositoryAccess :exec
UPDATE repositories
SET installation_id = $2, access = $3, updated_at = CURRENT_TIMESTAMP
WHERE id = $1;

-- name: CountUserRepositoryFollow :one
SELECT COUNT(*) FROM user_repo_follows
WHERE user_id = $1 AND repository_id = $2;

-- name: UpsertCheckRun :exec
INSERT INTO pr_check_runs (check_run_id, repository_id, head_sha, name, status, conclusion, title, details_url, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CURRENT_TIMESTAMP)
ON CONFLICT (check_run_id) DO UPDATE SET
    head_sha = EXCLUDED.head_sha,
    name = EXCLUDED.name,
    status = EXCLUDED.status,
    conclusion = EXCLUDED.conclusion,
    title = EXCLUDED.title,
    details_url = EXCLUDED.details_url,
    updated_at = CURRENT_TIMESTAMP;

-- name: GetCheckRun :one
SELECT check_run_id, repository_id, head_sha, name, status, conclusion, title, details_url
FROM pr_check_runs
WHERE check_run_id = $1;

-- name: ListCheckRunsByHead :many
SELECT check_run_id, repository_id, head_sha, name, status, conclusion, title, details_url
FROM pr_check_runs
WHERE repository_id = $1 AND head_sha = $2
ORDER BY check_run_id ASC;

-- name: UpsertCommitStatus :exec
INSERT INTO pr_commit_statuses (status_id, repository_id, head_sha, context, state, description, target_url, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, CURRENT_TIMESTAMP)
ON CONFLICT (status_id) DO UPDATE SET
    head_sha = EXCLUDED.head_sha,
    context = EXCLUDED.context,
    state = EXCLUDED.state,
    description = EXCLUDED.description,
    target_url = EXCLUDED.target_url,
    updated_at = CURRENT_TIMESTAMP;

-- name: GetCommitStatus :one
SELECT status_id, repository_id, head_sha, context, state, description, target_url
FROM pr_commit_statuses
WHERE status_id = $1;

-- name: ListCommitStatusesByHead :many
SELECT status_id, repository_id, head_sha, context, state, description, target_url
FROM pr_commit_statuses
WHERE repository_id = $1 AND head_sha = $2
ORDER BY status_id ASC;

-- name: ListOpenPullRequestsByHead :many
SELECT id, repository_id, repository_name, github_pr_id, number, title, author_login, html_url,
       is_dependabot, is_draft, state, mergeable_state, ci_status, head_sha, base_ref,
       claimed_by_user_id, claimed_by_login, updated_at
FROM pull_requests
WHERE repository_id = $1 AND head_sha = $2 AND state = 'OPEN'
ORDER BY number ASC;

-- name: UpsertPullRequestChangedFiles :exec
INSERT INTO pr_changed_files (pull_request_id, head_sha, files_json, updated_at)
VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
ON CONFLICT (pull_request_id) DO UPDATE SET
    head_sha = EXCLUDED.head_sha,
    files_json = EXCLUDED.files_json,
    updated_at = CURRENT_TIMESTAMP;

-- name: ListRepositoryFollowerIDs :many
SELECT user_id FROM user_repo_follows
WHERE repository_id = $1
ORDER BY user_id ASC;

-- name: GetPullRequestReview :one
SELECT github_review_id, pull_request_id, reviewer_login, state, commit_sha, submitted_at
FROM pr_reviews
WHERE github_review_id = $1;

-- name: UpsertPullRequestReview :exec
INSERT INTO pr_reviews (github_review_id, pull_request_id, reviewer_login, state, commit_sha, submitted_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, CURRENT_TIMESTAMP)
ON CONFLICT (github_review_id) DO UPDATE SET
    state = EXCLUDED.state,
    commit_sha = EXCLUDED.commit_sha,
    updated_at = CURRENT_TIMESTAMP;

-- name: InsertActivityEvent :one
-- idempotent: a log, not state. Webhooks only publish when state changed (ADR-0011), and a repeated activity is a repeated fact; readers deliver by seq, so a retry costs at most one duplicate row.
INSERT INTO activity_events (event_id, type, repository_id, repository_name, pull_request_id, pr_number, actor_login, params_json, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING seq;

-- name: ListActivityEventsSince :many
SELECT seq, event_id, type, repository_id, repository_name, pull_request_id, pr_number, actor_login, params_json, occurred_at
FROM activity_events
WHERE seq > $1
ORDER BY seq ASC
LIMIT $2;

-- name: ListRecentActivityEventsByRepository :many
SELECT seq, event_id, type, repository_id, repository_name, pull_request_id, pr_number, actor_login, params_json, occurred_at
FROM activity_events
WHERE repository_id = $1
ORDER BY seq DESC
LIMIT $2;

-- name: GetLatestActivitySeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS BIGINT) AS seq FROM activity_events;

-- name: DeleteActivityEventsBefore :execrows
DELETE FROM activity_events WHERE occurred_at < $1;

-- name: ClaimSyncJob :one
-- Starts a run unless one is going (running with a fresh heartbeat). No row back means not claimed.
INSERT INTO sync_jobs (
    user_id, run_id, state, progress_total, progress_done, progress_current,
    result_json, error_json, cancel_requested, started_at, heartbeat_ms, finished_at
) VALUES (
    sqlc.arg(user_id), sqlc.arg(run_id), 'running', 0, 0, '', '', '', FALSE, sqlc.arg(started_at), sqlc.arg(heartbeat_ms), NULL
)
ON CONFLICT (user_id) DO UPDATE SET
    run_id = EXCLUDED.run_id,
    state = 'running',
    progress_total = 0,
    progress_done = 0,
    progress_current = '',
    result_json = '',
    error_json = '',
    cancel_requested = FALSE,
    started_at = EXCLUDED.started_at,
    heartbeat_ms = EXCLUDED.heartbeat_ms,
    finished_at = NULL
WHERE sync_jobs.state <> 'running' OR sync_jobs.heartbeat_ms < sqlc.arg(stale_before_ms)
RETURNING run_id;

-- name: GetSyncJob :one
SELECT user_id, run_id, state, progress_total, progress_done, progress_current, result_json, error_json,
       cancel_requested, started_at, heartbeat_ms, finished_at
FROM sync_jobs
WHERE user_id = $1;

-- name: HeartbeatSyncJob :one
UPDATE sync_jobs
SET progress_total = sqlc.arg(progress_total),
    progress_done = sqlc.arg(progress_done),
    progress_current = sqlc.arg(progress_current),
    heartbeat_ms = sqlc.arg(heartbeat_ms)
WHERE user_id = sqlc.arg(user_id) AND run_id = sqlc.arg(run_id) AND state = 'running'
RETURNING cancel_requested;

-- name: FinishSyncJob :exec
UPDATE sync_jobs
SET state = sqlc.arg(state),
    progress_total = sqlc.arg(progress_total),
    progress_done = sqlc.arg(progress_done),
    progress_current = sqlc.arg(progress_current),
    result_json = sqlc.arg(result_json),
    error_json = sqlc.arg(error_json),
    heartbeat_ms = sqlc.arg(heartbeat_ms),
    finished_at = sqlc.arg(finished_at)
WHERE user_id = sqlc.arg(user_id) AND run_id = sqlc.arg(run_id);

-- name: RequestSyncJobCancel :execrows
UPDATE sync_jobs
SET cancel_requested = TRUE
WHERE user_id = sqlc.arg(user_id) AND state = 'running' AND heartbeat_ms >= sqlc.arg(stale_before_ms);

-- name: UpsertAnalysisFailure :exec
INSERT INTO dependabot_analysis_failures (pull_request_id, head_sha, error_kind, message, failed_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (pull_request_id, head_sha) DO UPDATE SET
    error_kind = EXCLUDED.error_kind,
    message = EXCLUDED.message,
    failed_at = EXCLUDED.failed_at;

-- name: GetAnalysisFailure :one
SELECT pull_request_id, head_sha, error_kind, message, failed_at
FROM dependabot_analysis_failures
WHERE pull_request_id = $1 AND head_sha = $2;
