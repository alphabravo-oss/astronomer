package db

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgxpool.Pool with convenience methods.
type DB struct {
	pool                *pgxpool.Pool
	poolSaturationState poolSaturationState
}

type poolSaturationState struct {
	lastEmptyAcquireCount atomic.Int64
}

func (s *poolSaturationState) waitingForConnection(idleConns int32, emptyAcquireCount int64) bool {
	previous := s.lastEmptyAcquireCount.Swap(emptyAcquireCount)
	return idleConns == 0 && emptyAcquireCount > previous
}

// PoolConfig holds the operator-tunable knobs for Connect. Zero values
// fall back to the defaults below — operators tune them via the chart's
// database.* values.
type PoolConfig struct {
	MaxConns          int32         // 0 → 25
	MinConns          int32         // 0 → 5
	MaxConnLifetime   time.Duration // 0 → 30m
	MaxConnIdleTime   time.Duration // 0 → 5m
	HealthCheckPeriod time.Duration // 0 → 30s
}

// Default sizing — preserved from the historical hard-coded values so
// existing installs see no behavioural change.
const (
	defaultMaxConns          = 25
	defaultMinConns          = 5
	defaultMaxConnLifetime   = 30 * time.Minute
	defaultMaxConnIdleTime   = 5 * time.Minute
	defaultHealthCheckPeriod = 30 * time.Second
)

// Connect creates a new DB with a configured connection pool and verifies
// connectivity with a ping before returning. Uses pool defaults.
func Connect(ctx context.Context, databaseURL string) (*DB, error) {
	return ConnectWithConfig(ctx, databaseURL, PoolConfig{})
}

// ConnectWithConfig is Connect with operator-tunable pool sizing. Zero
// fields fall through to the defaults so callers can override only what
// they need.
func ConnectWithConfig(ctx context.Context, databaseURL string, pc PoolConfig) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}

	cfg.MaxConns = pickInt32(pc.MaxConns, defaultMaxConns)
	cfg.MinConns = pickInt32(pc.MinConns, defaultMinConns)
	cfg.MaxConnLifetime = pickDuration(pc.MaxConnLifetime, defaultMaxConnLifetime)
	cfg.MaxConnIdleTime = pickDuration(pc.MaxConnIdleTime, defaultMaxConnIdleTime)
	cfg.HealthCheckPeriod = pickDuration(pc.HealthCheckPeriod, defaultHealthCheckPeriod)
	// Emit OTel db.* spans for every query alongside the existing
	// Prometheus query metrics. otelpgx fires before/after each query;
	// the historical queryTracer captures duration histograms.
	//
	// Export operation/timing without SQL text, connection details or bind values.
	// SQL literals can contain private data even when bound parameters are off.
	cfg.ConnConfig.Tracer = newCompositeQueryTracer(
		otelpgx.NewTracer(otelpgx.WithDisableSQLStatementInAttributes(), otelpgx.WithDisableConnectionDetailsInAttributes()),
		NewQueryTracer(),
	)

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

func pickInt32(v, fallback int32) int32 {
	if v <= 0 {
		return fallback
	}
	return v
}

func pickDuration(v, fallback time.Duration) time.Duration {
	if v <= 0 {
		return fallback
	}
	return v
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

// SchemaHealth asserts the applied schema is the one this binary was built
// against and that every durable JSONB column is governed. Migrations are
// applied at boot by the server (Migrate), so a version mismatch here means a
// replica is running against a database another, newer or older, binary
// owns — refuse to start rather than serve against an unknown schema.
func (d *DB) SchemaHealth(ctx context.Context) error {
	if d == nil || d.pool == nil {
		return fmt.Errorf("schema health: nil pool")
	}
	version, err := d.SchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("schema health: %w", err)
	}
	if version != ExpectedSchemaVersion {
		return fmt.Errorf("schema health: applied schema version %d != expected %d", version, ExpectedSchemaVersion)
	}
	var ungoverned int
	if err := d.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM public.durable_json_schema_coverage
		WHERE NOT governed OR NOT writer_validation_enabled
	`).Scan(&ungoverned); err != nil {
		return fmt.Errorf("schema health: query durable JSON governance coverage: %w", err)
	}
	if ungoverned != 0 {
		return fmt.Errorf("schema health: %d durable JSONB columns lack a versioned contract or writer validation trigger", ungoverned)
	}
	return nil
}

// PoolWaitingForConn reports whether callers are currently queueing on
// an empty pool. Implements the dbPoolSaturationReporter contract the
// readiness handler uses. True when:
//
//   - The pool has zero available (idle) conns right now AND
//   - At least one acquire has been waiting (the monotonic
//     EmptyAcquireCount counter advanced since the last probe)
//
// We compare against the last observed count rather than the absolute
// value because EmptyAcquireCount is monotonic; what matters for "is
// there pressure NOW?" is whether it's increasing. State belongs to the
// specific DB wrapper and is updated atomically so concurrent readiness
// probes and multiple server instances cannot race or influence one another.

func (d *DB) PoolWaitingForConn() bool {
	if d == nil || d.pool == nil {
		return false
	}
	stat := d.pool.Stat()
	return d.poolSaturationState.waitingForConnection(stat.IdleConns(), stat.EmptyAcquireCount())
}
