-- +goose Up
-- Reviews as GitHub recorded them, one row per GitHub review id. The turn engine derives
-- "approved by me", "teammate approved" and "waiting on author" from these on read.
CREATE TABLE IF NOT EXISTS pr_reviews (
    github_review_id BIGINT PRIMARY KEY,
    pull_request_id BIGINT NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    reviewer_login TEXT NOT NULL,
    state TEXT NOT NULL,
    commit_sha TEXT NOT NULL DEFAULT '',
    submitted_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pr_reviews_pull_request ON pr_reviews (pull_request_id);
