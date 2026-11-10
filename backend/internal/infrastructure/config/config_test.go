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

func TestLoadFromEnv_GitHubLoginSettings(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://prfollower.example.com/")
	t.Setenv("TOKEN_ENCRYPTION_KEY", "a2V5")
	t.Setenv("GITHUB_CLIENT_ID", "Iv1.client")
	t.Setenv("GITHUB_CLIENT_SECRET", "shh")

	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error: %v", err)
	}
	if cfg.PublicURL != "https://prfollower.example.com" {
		t.Errorf("PublicURL = %q, want trailing slash trimmed", cfg.PublicURL)
	}
	if cfg.TokenEncryptionKey != "a2V5" {
		t.Errorf("TokenEncryptionKey = %q", cfg.TokenEncryptionKey)
	}
	if cfg.GitHubCallbackURL() != "https://prfollower.example.com/api/v1/auth/github/callback" {
		t.Errorf("GitHubCallbackURL() = %q", cfg.GitHubCallbackURL())
	}
	if !cfg.GitHubLoginConfigured() {
		t.Error("expected GitHub login to be configured")
	}
	if !cfg.SecureCookies() {
		t.Error("expected secure cookies for an https public URL")
	}
}

func TestLoadFromEnv_GitHubLoginDefaults(t *testing.T) {
	t.Setenv("PUBLIC_URL", "")
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GITHUB_CLIENT_SECRET", "")

	cfg, _ := config.LoadFromEnv()
	if cfg.PublicURL != "http://localhost:4200" {
		t.Errorf("PublicURL default = %q", cfg.PublicURL)
	}
	if cfg.GitHubLoginConfigured() {
		t.Error("GitHub login must not be configured without client credentials")
	}
	if cfg.SecureCookies() {
		t.Error("http public URL must not use secure cookies")
	}
}
