-- +goose Up
-- ADR-0010: installation webhooks track whether the GitHub App still reaches a repository.
-- active | removed (uninstalled or deselected) | suspended (installation suspended).

ALTER TABLE repositories ADD COLUMN access TEXT NOT NULL DEFAULT 'active';

CREATE INDEX IF NOT EXISTS idx_repositories_installation_id ON repositories (installation_id);
