package database_test

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/murilo/pr-follower/backend/internal/infrastructure/database"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

func TestConnectWithBackoff_RunsGooseUpMigrationsAndSetsPoolBounds(t *testing.T) {
	t.Parallel()

	sqlDB, err := database.Connect(context.Background(), database.Options{
		DSN:          "sqlite://:memory:",
		MaxOpenConns: 15,
		MaxIdleConns: 5,
		Backoff: resilience.BackoffConfig{
			BaseDelay:   5 * time.Millisecond,
			MaxDelay:    20 * time.Millisecond,
			MaxAttempts: 2,
		},
	})
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer sqlDB.Close()

	if stats := sqlDB.Stats(); stats.MaxOpenConnections != 15 {
		t.Errorf("expected MaxOpenConnections 15, got %d", stats.MaxOpenConnections)
	}

	// Verify goose applied forward migration 00001
	var version int64
	if err := sqlDB.QueryRowContext(context.Background(), "SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1").Scan(&version); err != nil {
		t.Fatalf("expected goose_db_version table to exist and have applied migration: %v", err)
	}
	if version < 1 {
		t.Errorf("expected goose migration version >= 1, got %d", version)
	}
}

func TestValidateForwardOnlyMigrations_ForbidsGooseDown(t *testing.T) {
	t.Parallel()

	validFS := fstest.MapFS{
		"migrations/00001_init.sql": &fstest.MapFile{
			Data: []byte("-- +goose Up\nCREATE TABLE test (id INT);\n"),
		},
	}
	if err := database.ValidateForwardOnlyMigrations(validFS); err != nil {
		t.Fatalf("expected valid forward-only migration to pass, got %v", err)
	}

	forbiddenDownFS := fstest.MapFS{
		"migrations/00002_bad.sql": &fstest.MapFile{
			Data: []byte("-- +goose Up\nALTER TABLE test ADD COLUMN name TEXT;\n-- +goose Down\nDROP TABLE test;\n"),
		},
	}
	if err := database.ValidateForwardOnlyMigrations(forbiddenDownFS); err == nil {
		t.Fatal("expected error when migration contains '-- +goose Down', got nil")
	}
}
