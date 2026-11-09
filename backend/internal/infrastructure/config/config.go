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
	GitHubClientID      string
	GitHubClientSecret  string
	AllowedOrigin       string
	DBMaxOpenConns      int
	DBMaxIdleConns      int
}

func LoadFromEnv() (*Config, error) {
	return &Config{
		Port:                getEnvOrDefault("PORT", "8080"),
		DatabaseURL:         getEnvOrDefault("DATABASE_URL", "postgres://prfollower:prfollower_secret@localhost:5432/pr_follower?sslmode=disable"),
		GitHubWebhookSecret: getEnvOrDefault("GITHUB_WEBHOOK_SECRET", "dev-webhook-secret"),
		GitHubAppID:         getEnvOrDefault("GITHUB_APP_ID", ""),
		GitHubClientID:      getEnvOrDefault("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:  getEnvOrDefault("GITHUB_CLIENT_SECRET", ""),
		AllowedOrigin:       getEnvOrDefault("ALLOWED_ORIGIN", "*"),
		DBMaxOpenConns:      getEnvIntOrDefault("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:      getEnvIntOrDefault("DB_MAX_IDLE_CONNS", 10),
	}, nil
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
