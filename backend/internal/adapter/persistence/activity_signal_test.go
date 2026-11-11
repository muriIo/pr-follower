package persistence_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
)

// The signal needs a real Postgres (LISTEN/NOTIFY has no SQLite equivalent). Point TEST_DATABASE_URL
// at a throwaway database to run these; without it they are skipped.
func postgresForSignal(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, dsn
}

func wait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if !ok {
			t.Fatalf("channel closed while waiting for %s", what)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestPostgresActivitySignal_NotifyWakesEveryListenerAndConnectionCatchesUp(t *testing.T) {
	db, dsn := postgresForSignal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	replicaA := persistence.NewPostgresActivitySignal(db, dsn)
	replicaB := persistence.NewPostgresActivitySignal(db, dsn)
	onA := replicaA.Listen(ctx)
	onB := replicaB.Listen(ctx)
	wait(t, onA, "A's catch-up wake after connecting")
	wait(t, onB, "B's catch-up wake after connecting")

	if err := replicaA.Notify(ctx); err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	wait(t, onA, "A's own notification")
	wait(t, onB, "B's notification from A")
}

func TestPostgresActivitySignal_ListenerClosesWithItsContext(t *testing.T) {
	db, dsn := postgresForSignal(t)
	ctx, cancel := context.WithCancel(context.Background())
	ch := persistence.NewPostgresActivitySignal(db, dsn).Listen(ctx)
	wait(t, ch, "the first wake")
	cancel()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the channel never closed after the context ended")
		}
	}
}

func TestPostgresActivitySignal_ListenerReconnectsAfterItsConnectionIsKilled(t *testing.T) {
	db, dsn := postgresForSignal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signal := persistence.NewPostgresActivitySignal(db, dsn)
	signal.SetReconnectDelay(10 * time.Millisecond)
	ch := signal.Listen(ctx)
	wait(t, ch, "the first wake")

	// Kill every other session of this database that is waiting in LISTEN.
	if _, err := db.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE pid <> pg_backend_pid() AND datname = current_database() AND query LIKE 'LISTEN %'`); err != nil {
		t.Fatalf("terminate listener: %v", err)
	}
	wait(t, ch, "the catch-up wake after reconnecting")

	if err := signal.Notify(ctx); err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	wait(t, ch, "a notification on the new connection")
}
