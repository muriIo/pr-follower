package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port                string
	DatabaseURL         string
	GitHubWebhookSecret string
	GitHubAppID         string
	// GitHubAppPrivateKey is the app's PEM private key, from GITHUB_APP_PRIVATE_KEY_FILE or
	// GITHUB_APP_PRIVATE_KEY. It signs the JWTs that become installation tokens. Never log it.
	GitHubAppPrivateKey string
	GitHubAppSlug       string
	GitHubClientID      string
	GitHubClientSecret  string
	AllowedOrigin       string
	PublicURL           string
	TokenEncryptionKey  string
	DBMaxOpenConns      int
	DBMaxIdleConns      int
	// AllowedGitHubOrgs restricts sign-in to members of these GitHub organizations (ALLOWED_GITHUB_ORGS,
	// comma-separated, case-insensitive; ADR-0015). Empty keeps sign-in open to every GitHub account.
	AllowedGitHubOrgs []string
}

func LoadFromEnv() (*Config, error) {
	privateKey := getEnvOrDefault("GITHUB_APP_PRIVATE_KEY", "")
	if path := getEnvOrDefault("GITHUB_APP_PRIVATE_KEY_FILE", ""); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read GITHUB_APP_PRIVATE_KEY_FILE: %w", err)
		}
		privateKey = strings.TrimSpace(string(raw))
	}
	cfg := &Config{
		Port:                getEnvOrDefault("PORT", "8080"),
		DatabaseURL:         getEnvOrDefault("DATABASE_URL", "postgres://prfollower:prfollower_secret@localhost:5432/pr_follower?sslmode=disable"),
		GitHubWebhookSecret: getEnvOrDefault("GITHUB_WEBHOOK_SECRET", devWebhookSecret),
		GitHubAppID:         getEnvOrDefault("GITHUB_APP_ID", ""),
		GitHubAppPrivateKey: privateKey,
		GitHubAppSlug:       getEnvOrDefault("GITHUB_APP_SLUG", ""),
		GitHubClientID:      getEnvOrDefault("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:  getEnvOrDefault("GITHUB_CLIENT_SECRET", ""),
		AllowedOrigin:       getEnvOrDefault("ALLOWED_ORIGIN", "*"),
		PublicURL:           strings.TrimRight(getEnvOrDefault("PUBLIC_URL", "http://localhost:4200"), "/"),
		TokenEncryptionKey:  getEnvOrDefault("TOKEN_ENCRYPTION_KEY", ""),
		DBMaxOpenConns:      getEnvIntOrDefault("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:      getEnvIntOrDefault("DB_MAX_IDLE_CONNS", 10),
		AllowedGitHubOrgs:   parseOrgList(getEnvOrDefault("ALLOWED_GITHUB_ORGS", "")),
	}
	// Membership is checked as the GitHub App: without its credentials no one could ever sign in.
	if len(cfg.AllowedGitHubOrgs) > 0 && !cfg.GitHubAppConfigured() {
		return nil, fmt.Errorf("ALLOWED_GITHUB_ORGS needs the GitHub App credentials (GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY) to check organization membership")
	}
	// The development secret is public: on a public deployment it would let anyone sign a webhook.
	if cfg.SecureCookies() && cfg.GitHubWebhookSecret == devWebhookSecret {
		return nil, fmt.Errorf("GITHUB_WEBHOOK_SECRET must be set to the GitHub App's webhook secret when PUBLIC_URL is https")
	}
	return cfg, nil
}

// devWebhookSecret lets local development run without configuring a webhook secret. It is public, so
// it is refused when PUBLIC_URL is https (a real deployment).
const devWebhookSecret = "dev-webhook-secret"

// GitHubCallbackURL is the GitHub App "Callback URL" (ADR-0010). It goes through the public
// origin so the session cookie is set on the same origin the SPA is served from.
func (c *Config) GitHubCallbackURL() string {
	return c.PublicURL + "/api/v1/auth/github/callback"
}

// GitHubLoginConfigured reports whether GitHub sign-in can run.
func (c *Config) GitHubLoginConfigured() bool {
	return c.GitHubClientID != "" && c.GitHubClientSecret != ""
}

// GitHubAppConfigured reports whether the server can authenticate as the GitHub App itself
// (installation tokens for reads that have no signed-in user behind them).
func (c *Config) GitHubAppConfigured() bool {
	return c.GitHubAppID != "" && c.GitHubAppPrivateKey != ""
}

// GitHubAppInstallURL is where users install the app on an account (the Vercel-style
// "add GitHub account" link). Empty when GITHUB_APP_SLUG is not set.
func (c *Config) GitHubAppInstallURL() string {
	if c.GitHubAppSlug == "" {
		return ""
	}
	return "https://github.com/apps/" + c.GitHubAppSlug + "/installations/new"
}

// SecureCookies reports whether session cookies must carry the Secure attribute.
func (c *Config) SecureCookies() bool {
	return strings.HasPrefix(c.PublicURL, "https://")
}

// parseOrgList splits a comma-separated list of organization logins: trimmed, lowercase, deduped.
func parseOrgList(raw string) []string {
	var orgs []string
	seen := map[string]bool{}
	for _, org := range strings.Split(raw, ",") {
		org = strings.ToLower(strings.TrimSpace(org))
		if org == "" || seen[org] {
			continue
		}
		seen[org] = true
		orgs = append(orgs, org)
	}
	return orgs
}

func getEnvOrDefault(key, fallback string) string {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	return val
}

func getEnvIntOrDefault(key string, fallback int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(val)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
