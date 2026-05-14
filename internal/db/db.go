package db

import (
	"context"
	"fmt"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgxpool.Pool with convenience methods.
type DB struct {
	pool *pgxpool.Pool
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
	// Deliberately NOT calling otelpgx.WithIncludeQueryParameters():
	// query args carry user IDs, encrypted token blobs, INSERT bodies,
	// etc. — recording them on spans both leaks PII/secrets to the
	// OTLP backend and explodes per-span attribute size. The SQL text
	// alone is sufficient for trace-driven debugging.
	cfg.ConnConfig.Tracer = newCompositeQueryTracer(
		otelpgx.NewTracer(),
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

// SchemaHealth asserts the golang-migrate `schema_migrations` table is
// in a single, clean row state. Two pathologies have been observed in
// production and both deserve a hard fail at startup rather than a
// late-running migrate job that loops:
//
//  1. Multiple rows. golang-migrate expects exactly one row representing
//     current state. The .247 incident on 2026-05-13 showed
//     {version=84 dirty=t, version=86 dirty=f} simultaneously — likely
//     from a manual INSERT mid-incident. Subsequent `migrate up` runs
//     refused to start.
//
//  2. dirty=true. A prior migration crashed midway; the schema is in an
//     indeterminate state. The chart's preflight job catches this on
//     install, but a long-running server pod that survived the bad
//     migration would happily keep serving traffic against a corrupt
//     schema. Refusing to start on (re)deploy surfaces the problem
//     loudly.
//
// Returns nil when:
//   - The table doesn't exist (fresh install — migrate creates it on
//     first apply).
//   - The table is empty (also fresh install).
//   - Exactly one row exists with dirty=false.
//
// On any other shape, returns an error with the offending state inlined
// so the operator's runbook (force the version they meant to land on,
// or roll back the rogue row) is one query away.
func (d *DB) SchemaHealth(ctx context.Context) error {
	if d == nil || d.pool == nil {
		return fmt.Errorf("schema health: nil pool")
	}
	// Try the count first; gracefully handle the table-missing case by
	// inspecting the SQLSTATE.
	var count int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		// 42P01 = undefined_table — pre-migration fresh install.
		if isUndefinedTable(err) {
			return nil
		}
		return fmt.Errorf("schema health: query schema_migrations: %w", err)
	}
	if count == 0 {
		return nil
	}
	if count > 1 {
		// Surface the actual rows so the on-call doesn't have to log
		// into the pod to investigate.
		rows, err := d.pool.Query(ctx, `SELECT version, dirty FROM schema_migrations ORDER BY version`)
		if err != nil {
			return fmt.Errorf(
				"schema health: schema_migrations has %d rows but failed to enumerate: %w "+
					"(expected exactly 1 — multiple rows indicate manual INSERT drift; "+
					"recover by DELETing all but the target version and `migrate force` if needed)",
				count, err,
			)
		}
		defer rows.Close()
		var summary string
		for rows.Next() {
			var v int64
			var dirty bool
			if err := rows.Scan(&v, &dirty); err != nil {
				continue
			}
			if summary != "" {
				summary += ", "
			}
			summary += fmt.Sprintf("{version=%d dirty=%t}", v, dirty)
		}
		return fmt.Errorf(
			"schema health: schema_migrations has %d rows: %s — expected exactly 1. "+
				"This is the .247 drift pattern; recover by `DELETE FROM schema_migrations WHERE version != <target>;` "+
				"then `migrate force <target>` if any row is dirty",
			count, summary,
		)
	}
	var version int64
	var dirty bool
	if err := d.pool.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		return fmt.Errorf("schema health: read single row: %w", err)
	}
	if dirty {
		return fmt.Errorf(
			"schema health: schema_migrations.dirty=true at version=%d — previous migration crashed midway. "+
				"Inspect with `SELECT * FROM schema_migrations`, decide whether to roll forward or back, "+
				"then `migrate force %d` before restarting",
			version, version,
		)
	}
	return nil
}

// isUndefinedTable reports whether err carries the Postgres 42P01
// "relation does not exist" code. We match by string because the pgx
// driver wraps the error and lifting *pgconn.PgError out cleanly here
// would add a dependency without clear benefit — the SQLSTATE text is
// stable across pgx versions.
func isUndefinedTable(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return containsAny(s, []string{"SQLSTATE 42P01", "undefined_table", `relation "schema_migrations" does not exist`})
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if n == "" {
			continue
		}
		if indexOf(haystack, n) >= 0 {
			return true
		}
	}
	return false
}

// indexOf is a tiny helper so we don't pull strings into this file just
// for one call site. Returns -1 when needle is absent.
func indexOf(haystack, needle string) int {
	hl, nl := len(haystack), len(needle)
	if nl == 0 || nl > hl {
		if nl == 0 {
			return 0
		}
		return -1
	}
	for i := 0; i+nl <= hl; i++ {
		if haystack[i:i+nl] == needle {
			return i
		}
	}
	return -1
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
// there pressure NOW?" is whether it's increasing. State is
// per-process — concurrent /readyz probes may all observe true once
// then all observe false on the next round, which is fine: the kubelet
// probe cadence is wide enough that the signal stays meaningful.
var lastEmptyAcquireCount int64

func (d *DB) PoolWaitingForConn() bool {
	if d == nil || d.pool == nil {
		return false
	}
	stat := d.pool.Stat()
	current := stat.EmptyAcquireCount()
	delta := current - lastEmptyAcquireCount
	lastEmptyAcquireCount = current
	return stat.IdleConns() == 0 && delta > 0
}
