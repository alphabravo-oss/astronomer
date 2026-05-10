package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgxpool.Pool with convenience methods.
type DB struct {
	pool *pgxpool.Pool
}

// Connect creates a new DB with a configured connection pool and verifies
// connectivity with a ping before returning.
func Connect(ctx context.Context, databaseURL string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}

	cfg.MaxConns = 25
	cfg.MinConns = 5
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	cfg.ConnConfig.Tracer = NewQueryTracer()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return &DB{pool: pool}, nil
}

// Close closes the underlying connection pool.
func (d *DB) Close() {
	d.pool.Close()
}

// Pool returns the underlying pgxpool.Pool.
func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}

// Health pings the database and returns an error if unreachable.
func (d *DB) Health(ctx context.Context) error {
	return d.pool.Ping(ctx)
}
