package database_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/murilo/pr-follower/backend/internal/infrastructure/database"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

// The migration lock needs a real Postgres (advisory locks have no SQLite equivalent). Point
// TEST_DATABASE_URL at a server whose role may CREATE DATABASE; each test migrates its own
// throwaway database, so the shared test database is never touched.
func freshPostgresDatabase(t *testing.T) string {
	t.Helper()
	adminDSN := os.Getenv("TEST_DATABASE_URL")
	if adminDSN == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	name := fmt.Sprintf("pf_migration_lock_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
	})

	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func oneAttempt() resilience.BackoffConfig {
	return resilience.BackoffConfig{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, MaxAttempts: 1}
}

// Replicas starting together on an empty database must not race each other through the
// migrations: each version is applied exactly once and every replica starts.
func TestConnect_ConcurrentReplicasMigrateOnceUnderTheLock(t *testing.T) {
	dsn := freshPostgresDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const replicas = 4
	var wg sync.WaitGroup
	errs := make([]error, replicas)
	start := make(chan struct{})
	for i := range replicas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			db, err := database.Connect(ctx, database.Options{DSN: dsn, MaxOpenConns: 5, Backoff: oneAttempt()})
			if err == nil {
				_ = db.Close()
			}
			errs[i] = err
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("replica %d failed to start: %v", i, err)
		}
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	var duplicated int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM (
		SELECT version_id FROM goose_db_version WHERE version_id > 0 GROUP BY version_id HAVING count(*) > 1
	) d`).Scan(&duplicated); err != nil {
		t.Fatalf("read goose_db_version: %v", err)
	}
	if duplicated != 0 {
		t.Errorf("%d migration versions were applied more than once", duplicated)
	}
}

// While another process holds the migration lock, Connect waits for it, gives up when its
// context ends (ADR-0007), and migrates once the lock is released.
func TestConnect_WaitsForTheMigrationLockAndHonoursCancellation(t *testing.T) {
	dsn := freshPostgresDatabase(t)
	ctx := context.Background()

	holder, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer holder.Close()
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", database.MigrationLockID); err != nil {
		t.Fatalf("take the lock: %v", err)
	}

	blocked, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if db, err := database.Connect(blocked, database.Options{DSN: dsn, Backoff: oneAttempt()}); err == nil {
		_ = db.Close()
		t.Fatal("Connect migrated while another process held the migration lock")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the wait to end with the context deadline, got %v", err)
	}

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", database.MigrationLockID); err != nil {
		t.Fatalf("release the lock: %v", err)
	}
	db, err := database.Connect(ctx, database.Options{DSN: dsn, Backoff: oneAttempt()})
	if err != nil {
		t.Fatalf("Connect after the lock was released: %v", err)
	}
	defer db.Close()
}
