package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing/fstest"
	"time"

	_ "github.com/glebarez/go-sqlite"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

var gooseMu sync.Mutex

type Options struct {
	DSN          string
	MaxOpenConns int
	MaxIdleConns int
	Backoff      resilience.BackoffConfig
}

func ValidateForwardOnlyMigrations(fsys fs.FS) error {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := fs.ReadFile(fsys, "migrations/"+entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		lower := strings.ToLower(string(raw))
		if strings.Contains(lower, "+goose down") {
			return fmt.Errorf("forbidden '+goose Down' directive found in %s: migrations must be strictly forward-only", entry.Name())
		}
		if !strings.Contains(lower, "+goose up") {
			return fmt.Errorf("missing '+goose Up' directive in %s", entry.Name())
		}
	}
	return nil
}

func Connect(ctx context.Context, opts Options) (*sql.DB, error) {
	if err := ValidateForwardOnlyMigrations(embeddedMigrations); err != nil {
		return nil, resilience.Permanent(err)
	}

	backoff := opts.Backoff
	if backoff.MaxAttempts <= 0 {
		backoff = resilience.DefaultBackoffConfig()
	}

	return resilience.Retry(ctx, backoff, func(callCtx context.Context) (*sql.DB, error) {
		driverName := "pgx"
		dsn := opts.DSN
		dialect := "postgres"
		var migrationFS fs.FS = embeddedMigrations

		if strings.HasPrefix(opts.DSN, "sqlite://") {
			driverName = "sqlite"
			dsn = strings.TrimPrefix(opts.DSN, "sqlite://")
			dialect = "sqlite3"
			var err error
			migrationFS, err = buildSQLiteCompatibleMigrationFS(embeddedMigrations)
			if err != nil {
				return nil, resilience.Permanent(err)
			}
		}

		sqlDB, err := sql.Open(driverName, dsn)
		if err != nil {
			return nil, err
		}

		if opts.MaxOpenConns > 0 {
			sqlDB.SetMaxOpenConns(opts.MaxOpenConns)
		}
		if opts.MaxIdleConns > 0 {
			sqlDB.SetMaxIdleConns(opts.MaxIdleConns)
		}
		sqlDB.SetConnMaxLifetime(30 * time.Minute)

		if err := sqlDB.PingContext(callCtx); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}

		if err := runGooseForwardMigrations(callCtx, sqlDB, dialect, migrationFS); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}

		return sqlDB, nil
	})
}

func runGooseForwardMigrations(ctx context.Context, db *sql.DB, dialect string, fsys fs.FS) error {
	gooseMu.Lock()
	defer gooseMu.Unlock()

	goose.SetLogger(goose.NopLogger())
	goose.SetBaseFS(fsys)
	if err := goose.SetDialect(dialect); err != nil {
		return err
	}
	return goose.UpContext(ctx, db, "migrations")
}

func buildSQLiteCompatibleMigrationFS(src fs.FS) (fs.FS, error) {
	entries, err := fs.ReadDir(src, "migrations")
	if err != nil {
		return nil, err
	}
	mapFS := make(fstest.MapFS, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := "migrations/" + entry.Name()
		raw, err := fs.ReadFile(src, path)
		if err != nil {
			return nil, err
		}
		adapted := strings.ReplaceAll(string(raw), "BIGSERIAL PRIMARY KEY", "INTEGER PRIMARY KEY AUTOINCREMENT")
		adapted = strings.ReplaceAll(adapted, "TIMESTAMPTZ", "TIMESTAMP")
		mapFS[path] = &fstest.MapFile{Data: []byte(adapted)}
	}
	return mapFS, nil
}
