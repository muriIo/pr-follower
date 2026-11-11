-- +goose Up
-- What GitHub shows beside each check: its short result and where it reports in full.
ALTER TABLE pr_check_runs ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE pr_check_runs ADD COLUMN details_url TEXT NOT NULL DEFAULT '';
