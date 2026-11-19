package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"testing/fstest"
	"time"

	_ "github.com/glebarez/go-sqlite"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// MigrationLockID is the Postgres advisory lock every API replica takes while it migrates, so
// replicas starting together apply each migration once (ADR-0016).
const MigrationLockID int64 = 4097083626

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
		dialect := goose.DialectPostgres
		var migrationFS fs.FS = embeddedMigrations

		if strings.HasPrefix(opts.DSN, "sqlite://") {
			driverName = "sqlite"
			dsn = strings.TrimPrefix(opts.DSN, "sqlite://")
			dialect = goose.DialectSQLite3
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

// runGooseForwardMigrations applies the pending migrations. On Postgres it holds a session-level
// advisory lock (MigrationLockID) while it does, so replicas starting together wait for the first
// one instead of racing it through the same migrations (ADR-0016). The wait is bounded (5 min) and
// ends as soon as ctx does (ADR-0007). SQLite (tests only) has no advisory locks and one process.
func runGooseForwardMigrations(ctx context.Context, db *sql.DB, dialect goose.Dialect, fsys fs.FS) error {
	migrations, err := fs.Sub(fsys, "migrations")
	if err != nil {
		return resilience.Permanent(err)
	}
	opts := []goose.ProviderOption{goose.WithDisableGlobalRegistry(true)}
	if dialect == goose.DialectPostgres {
		locker, err := lock.NewPostgresSessionLocker(
			lock.WithLockID(MigrationLockID),
			lock.WithLockTimeout(1, 300), // try every second, for up to 5 minutes
		)
		if err != nil {
			return resilience.Permanent(err)
		}
		opts = append(opts, goose.WithSessionLocker(locker))
	}
	provider, err := goose.NewProvider(dialect, db, migrations, opts...)
	if err != nil {
		return resilience.Permanent(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		if ctxErr := context.Cause(ctx); ctxErr != nil {
			return fmt.Errorf("migrations: %w", ctxErr)
		}
		return err
	}
	return nil
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
