package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	postgresConnectTimeout = 5 * time.Second
	postgresUserTimeoutMS  = "15000"
	postgresKeepaliveIdle  = "10"
	postgresKeepaliveInt   = "5"
	postgresKeepaliveCount = "3"
)

// openPostgres opens a pooled handle with keepalive and tcp_user_timeout so a
// dead peer cannot hold the collection instance lock for the kernel default
// of about two hours.
func openPostgres(ctx context.Context, dsn string) (*sql.DB, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	config.ConnectTimeout = postgresConnectTimeout
	if config.RuntimeParams == nil {
		config.RuntimeParams = make(map[string]string)
	}
	config.RuntimeParams["tcp_keepalives_idle"] = postgresKeepaliveIdle
	config.RuntimeParams["tcp_keepalives_interval"] = postgresKeepaliveInt
	config.RuntimeParams["tcp_keepalives_count"] = postgresKeepaliveCount
	config.RuntimeParams["tcp_user_timeout"] = postgresUserTimeoutMS

	db := stdlib.OpenDB(*config)
	pingCtx, cancel := context.WithTimeout(ctx, postgresConnectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}
