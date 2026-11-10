-- +goose Up
-- ADR-0010: GitHub is the only identity provider. Users hold encrypted GitHub user tokens
-- (encrypted_token is the access token); sessions are stored by SHA-256 hash only.
-- Expiry instants are Unix seconds (0 = unknown) so the schema runs on Postgres and SQLite.

ALTER TABLE users ADD COLUMN encrypted_refresh_token TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN access_token_expires_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN refresh_token_expires_at BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions (user_id);
