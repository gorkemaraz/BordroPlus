// Package db owns the PostgreSQL connection pool.
package db

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect opens a pool and waits (with backoff) until the database answers or
// timeout elapses. Retrying here keeps the API resilient to a DB that restarts.
func Connect(ctx context.Context, url string, maxConns int32, timeout time.Duration, log *slog.Logger) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 15 * time.Second
	cfg.ConnConfig.RuntimeParams["application_name"] = "bordroplus"
	cfg.ConnConfig.RuntimeParams["timezone"] = "Europe/Istanbul"
	// Bound both halves of opening a connection. While the database
	// container is stopped, Docker's DNS does not answer "no such host" but
	// lets the lookup time out (8 s in the chaos run); ConnectTimeout only
	// covers the TCP dial, so the lookup gets its own deadline. A request that
	// needs a new connection then fails within seconds with 503.
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	cfg.ConnConfig.LookupFunc = func(ctx context.Context, host string) ([]string, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return net.DefaultResolver.LookupHost(ctx, host)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	deadline := time.Now().Add(timeout)
	backoff := 250 * time.Millisecond
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return pool, nil
		}
		if time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("database not reachable after %s: %w", timeout, err)
		}
		log.Warn("database not ready, retrying", "err", err, "backoff", backoff.String())
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}
