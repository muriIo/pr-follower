package persistence

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// OperationCancelChannel is the NOTIFY channel replicas use to ask each other to cancel an operation.
const OperationCancelChannel = "pr_follower_operation_cancel"

// subscriberBuffer is how many messages a slow subscriber may fall behind before newer ones are dropped.
const subscriberBuffer = 64

// PostgresReplicaBroadcast is the port.ReplicaBroadcast over one Postgres: NOTIFY with a payload on
// channel, received by a dedicated LISTEN connection per subscription.
type PostgresReplicaBroadcast struct {
	db      *sql.DB
	dsn     string
	channel string
}

var _ port.ReplicaBroadcast = (*PostgresReplicaBroadcast)(nil)

func NewPostgresReplicaBroadcast(db *sql.DB, dsn, channel string) *PostgresReplicaBroadcast {
	return &PostgresReplicaBroadcast{db: db, dsn: dsn, channel: channel}
}

func (b *PostgresReplicaBroadcast) Broadcast(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	_, err := b.db.ExecContext(ctx, "SELECT pg_notify($1, $2)", b.channel, message)
	return err
}

func (b *PostgresReplicaBroadcast) Subscribe(ctx context.Context) <-chan string {
	out := make(chan string, subscriberBuffer)
	go func() {
		defer close(out)
		listenLoop(ctx, b.dsn, b.channel, defaultReconnectDelay, func() {}, func(payload string) {
			select {
			case out <- payload:
			default:
				slog.Warn("replica broadcast subscriber is behind; message dropped", "channel", b.channel)
			}
		})
	}()
	return out
}
