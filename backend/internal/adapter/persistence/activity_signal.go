package persistence

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

// activityChannel is the Postgres NOTIFY channel replicas use to tell each other the activity log
// has news. The notification carries no payload: readers go to the log.
const activityChannel = "pr_follower_activity"

const defaultReconnectDelay = 5 * time.Second

// PostgresActivitySignal is the port.ActivitySignal for several API replicas over one Postgres:
// Notify is NOTIFY and Listen is a dedicated LISTEN connection that reconnects by itself.
type PostgresActivitySignal struct {
	db             *sql.DB
	dsn            string
	reconnectDelay time.Duration
}

var _ port.ActivitySignal = (*PostgresActivitySignal)(nil)

// NewPostgresActivitySignal notifies through db and listens on its own connection to dsn (a
// LISTEN cannot share the pool, which hands the connection to other work).
func NewPostgresActivitySignal(db *sql.DB, dsn string) *PostgresActivitySignal {
	return &PostgresActivitySignal{db: db, dsn: dsn, reconnectDelay: defaultReconnectDelay}
}

// SetReconnectDelay sets how long Listen waits after losing its connection before dialling again.
func (s *PostgresActivitySignal) SetReconnectDelay(d time.Duration) {
	if d > 0 {
		s.reconnectDelay = d
	}
}

func (s *PostgresActivitySignal) Notify(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	_, err := s.db.ExecContext(ctx, "SELECT pg_notify($1, '')", activityChannel)
	return err
}

func (s *PostgresActivitySignal) Listen(ctx context.Context) <-chan struct{} {
	out := make(chan struct{}, 1)
	wake := func() {
		select {
		case out <- struct{}{}:
		default: // one is already pending; wake-ups coalesce
		}
	}
	go func() {
		defer close(out)
		for ctx.Err() == nil {
			err := s.listenOnce(ctx, wake)
			if ctx.Err() != nil {
				return
			}
			slog.Warn("activity listener disconnected; reconnecting", "err", err, "in", s.reconnectDelay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.reconnectDelay):
			}
		}
	}()
	return out
}

// listenOnce holds one LISTEN connection until it fails or ctx ends. It wakes the reader right after
// subscribing, so anything appended while there was no connection is read.
func (s *PostgresActivitySignal) listenOnce(ctx context.Context, wake func()) error {
	conn, err := resilience.Retry(ctx, resilience.DefaultBackoffConfig(), func(ctx context.Context) (*pgx.Conn, error) {
		return pgx.Connect(ctx, s.dsn)
	})
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()

	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{activityChannel}.Sanitize()); err != nil {
		return err
	}
	wake()
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return err
		}
		wake()
	}
}
