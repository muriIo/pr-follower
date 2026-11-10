package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port                string
	DatabaseURL         string
	GitHubWebhookSecret string
	GitHubAppID         string
	GitHubAppSlug       string
	GitHubClientID      string
	GitHubClientSecret  string
	AllowedOrigin       string
	PublicURL           string
	TokenEncryptionKey  string
	DBMaxOpenConns      int
	DBMaxIdleConns      int
}

func LoadFromEnv() (*Config, error) {
	return &Config{
		Port:                getEnvOrDefault("PORT", "8080"),
		DatabaseURL:         getEnvOrDefault("DATABASE_URL", "postgres://prfollower:prfollower_secret@localhost:5432/pr_follower?sslmode=disable"),
		GitHubWebhookSecret: getEnvOrDefault("GITHUB_WEBHOOK_SECRET", "dev-webhook-secret"),
		GitHubAppID:         getEnvOrDefault("GITHUB_APP_ID", ""),
		GitHubAppSlug:       getEnvOrDefault("GITHUB_APP_SLUG", ""),
		GitHubClientID:      getEnvOrDefault("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:  getEnvOrDefault("GITHUB_CLIENT_SECRET", ""),
		AllowedOrigin:       getEnvOrDefault("ALLOWED_ORIGIN", "*"),
		PublicURL:           strings.TrimRight(getEnvOrDefault("PUBLIC_URL", "http://localhost:4200"), "/"),
		TokenEncryptionKey:  getEnvOrDefault("TOKEN_ENCRYPTION_KEY", ""),
		DBMaxOpenConns:      getEnvIntOrDefault("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:      getEnvIntOrDefault("DB_MAX_IDLE_CONNS", 10),
	}, nil
}

// GitHubCallbackURL is the GitHub App "Callback URL" (ADR-0010). It goes through the public
// origin so the session cookie is set on the same origin the SPA is served from.
func (c *Config) GitHubCallbackURL() string {
	return c.PublicURL + "/api/v1/auth/github/callback"
}

// GitHubLoginConfigured reports whether GitHub sign-in can run.
func (c *Config) GitHubLoginConfigured() bool {
	return c.GitHubClientID != "" && c.GitHubClientSecret != ""
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
