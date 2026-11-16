package config_test

import (
	"os"
	"path/filepath"
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
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")
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

func TestLoadFromEnv_GitHubAppInstallURL(t *testing.T) {
	t.Setenv("GITHUB_APP_SLUG", "pr-follower")
	cfg, _ := config.LoadFromEnv()
	if cfg.GitHubAppInstallURL() != "https://github.com/apps/pr-follower/installations/new" {
		t.Errorf("GitHubAppInstallURL() = %q", cfg.GitHubAppInstallURL())
	}

	t.Setenv("GITHUB_APP_SLUG", "")
	cfg, _ = config.LoadFromEnv()
	if cfg.GitHubAppInstallURL() != "" {
		t.Errorf("expected no install URL without a slug, got %q", cfg.GitHubAppInstallURL())
	}
}

func TestLoadFromEnv_GitHubAppCredentials(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", `-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----`)
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error: %v", err)
	}
	if !cfg.GitHubAppConfigured() || cfg.GitHubAppPrivateKey == "" {
		t.Fatalf("expected the app credentials to be configured: %+v", cfg)
	}

	// A key file wins over the inline value (secrets mounted as files).
	file := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(file, []byte("PEM FROM FILE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_APP_PRIVATE_KEY_FILE", file)
	cfg, err = config.LoadFromEnv()
	if err != nil || cfg.GitHubAppPrivateKey != "PEM FROM FILE" {
		t.Fatalf("expected the file's content, got %q, %v", cfg.GitHubAppPrivateKey, err)
	}

	t.Setenv("GITHUB_APP_PRIVATE_KEY_FILE", filepath.Join(t.TempDir(), "missing.pem"))
	if _, err := config.LoadFromEnv(); err == nil {
		t.Fatal("an unreadable key file must fail loudly, not leave the app silently unconfigured")
	}
}

func TestConfig_GitHubAppNeedsBothIDAndKey(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "")
	cfg, _ := config.LoadFromEnv()
	if cfg.GitHubAppConfigured() {
		t.Fatal("an app id without a key cannot authenticate")
	}
}

// The local default webhook secret is public (it is in this file). On a public https deployment it
// would let anyone sign a forged webhook, so the server refuses to start without a real one.
func TestLoadFromEnv_APublicDeploymentNeedsItsOwnWebhookSecret(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://prfollower.example.com")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")
	if _, err := config.LoadFromEnv(); err == nil {
		t.Fatal("expected an error: GITHUB_WEBHOOK_SECRET is required on an https PUBLIC_URL")
	}

	t.Setenv("GITHUB_WEBHOOK_SECRET", "dev-webhook-secret")
	if _, err := config.LoadFromEnv(); err == nil {
		t.Fatal("expected an error: the public development secret is not a secret")
	}

	t.Setenv("GITHUB_WEBHOOK_SECRET", "a-real-random-secret")
	if _, err := config.LoadFromEnv(); err != nil {
		t.Fatalf("a real secret: %v", err)
	}
}

func TestLoadFromEnv_LocalDevelopmentKeepsTheDefaultWebhookSecret(t *testing.T) {
	t.Setenv("PUBLIC_URL", "http://localhost:4200")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")
	cfg, err := config.LoadFromEnv()
	if err != nil || cfg.GitHubWebhookSecret == "" {
		t.Fatalf("local development works without configuring a secret: %v %q", err, cfg.GitHubWebhookSecret)
	}
}
