package persistence

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

// listenLoop holds a LISTEN on channel over its own connection to dsn (a LISTEN cannot share the
// pool, which hands the connection to other work), reconnecting after reconnectDelay until ctx ends.
// onConnected runs after every successful LISTEN, onNotify for every notification.
func listenLoop(ctx context.Context, dsn, channel string, reconnectDelay time.Duration, onConnected func(), onNotify func(payload string)) {
	for ctx.Err() == nil {
		err := listenOnce(ctx, dsn, channel, onConnected, onNotify)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("postgres listener disconnected; reconnecting", "channel", channel, "err", err, "in", reconnectDelay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

func listenOnce(ctx context.Context, dsn, channel string, onConnected func(), onNotify func(payload string)) error {
	conn, err := resilience.Retry(ctx, resilience.DefaultBackoffConfig(), func(ctx context.Context) (*pgx.Conn, error) {
		return pgx.Connect(ctx, dsn)
	})
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()

	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		return err
	}
	onConnected()
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		onNotify(n.Payload)
	}
}
