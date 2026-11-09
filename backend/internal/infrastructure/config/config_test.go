package config_test

import (
	"testing"

	"github.com/murilo/pr-follower/backend/internal/infrastructure/config"
)

func TestLoadFromEnv_DefaultsAndOverrides(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/custom_db?sslmode=disable")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "top-secret")
	t.Setenv("DB_MAX_OPEN_CONNS", "30")

	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error: %v", err)
	}

	if cfg.Port != "9090" {
		t.Errorf("expected Port 9090, got %q", cfg.Port)
	}
	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/custom_db?sslmode=disable" {
		t.Errorf("unexpected DatabaseURL: %q", cfg.DatabaseURL)
	}
	if cfg.GitHubWebhookSecret != "top-secret" {
		t.Errorf("unexpected GitHubWebhookSecret: %q", cfg.GitHubWebhookSecret)
	}
	if cfg.DBMaxOpenConns != 30 {
		t.Errorf("expected DBMaxOpenConns 30, got %d", cfg.DBMaxOpenConns)
	}
}
