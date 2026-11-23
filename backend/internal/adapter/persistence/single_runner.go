package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// SingleRunner is port.SingleRunner over a Postgres session-level advisory lock: the lock belongs to
// one connection, so a replica that dies releases it with its session (ADR-0012). Without Postgres
// (nil: SQLite in tests, one process) the work always runs.
type SingleRunner struct {
	db *sql.DB
}

var _ port.SingleRunner = (*SingleRunner)(nil)

// NewSingleRunner takes a Postgres pool, or nil when the process runs alone.
func NewSingleRunner(postgres *sql.DB) *SingleRunner {
	return &SingleRunner{db: postgres}
}

func (r *SingleRunner) TryRun(ctx context.Context, lockID int64, fn func(ctx context.Context) error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, context.Cause(ctx)
	}
	if r.db == nil {
		return true, fn(ctx)
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("take a connection for the lock: %w", err)
	}
	defer conn.Close()
	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&locked); err != nil {
		return false, fmt.Errorf("try the advisory lock: %w", err)
	}
	if !locked {
		return false, nil
	}
	defer func() {
		// A fresh context: the work's may be over, and an unreleased lock would outlive this run on a pooled connection.
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockID)
	}()
	return true, fn(ctx)
}
