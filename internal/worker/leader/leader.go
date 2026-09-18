package leader

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	tryLeaderSQL          = `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`
	unlockSQL             = `SELECT pg_advisory_unlock(hashtextextended($1, 0))`
	dedicatedPoolMaxConns = 8
)

// Elector acquires session-scoped advisory locks on dedicated pooled
// connections. Callers must invoke the returned release func when held=true.
type Elector struct {
	pool     *pgxpool.Pool
	log      *slog.Logger
	ownsPool bool
	close    sync.Once
}

func New(pool *pgxpool.Pool, log *slog.Logger) *Elector {
	if log == nil {
		log = slog.Default()
	}
	return &Elector{pool: pool, log: log}
}

// NewDedicated creates an elector backed by a small pool reserved exclusively
// for advisory locks. Leader leases are session-scoped and deliberately pin a
// connection for the duration of a sweep; keeping those connections out of the
// request/task pool prevents long reconcilers from starving application SQL.
func NewDedicated(ctx context.Context, source *pgxpool.Pool, log *slog.Logger) (*Elector, error) {
	if source == nil {
		return nil, fmt.Errorf("create dedicated leader pool: source pool is nil")
	}
	pool, err := pgxpool.NewWithConfig(ctx, dedicatedConfig(source.Config()))
	if err != nil {
		return nil, fmt.Errorf("create dedicated leader pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping dedicated leader pool: %w", err)
	}
	elector := New(pool, log)
	elector.ownsPool = true
	return elector, nil
}

func dedicatedConfig(source *pgxpool.Config) *pgxpool.Config {
	config := source.Copy()
	config.MinConns = 0
	config.MaxConns = dedicatedPoolMaxConns
	return config
}

// Close releases a pool owned by NewDedicated. Electors created with New use
// a caller-owned pool and are unaffected.
func (e *Elector) Close() {
	if e == nil || !e.ownsPool || e.pool == nil {
		return
	}
	e.close.Do(e.pool.Close)
}

// TryLeader acquires a session-scoped advisory lock for jobName on a dedicated
// connection. When held=false the returned release func is a no-op.
func (e *Elector) TryLeader(ctx context.Context, jobName string) (func(), bool, error) {
	if e == nil || e.pool == nil {
		return func() {}, false, nil
	}
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire advisory-lock connection: %w", err)
	}

	var held bool
	if err := conn.QueryRow(ctx, tryLeaderSQL, jobName).Scan(&held); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("pg_try_advisory_lock(%q): %w", jobName, err)
	}
	if !held {
		conn.Release()
		return func() {}, false, nil
	}

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var unlocked bool
			if err := conn.QueryRow(unlockCtx, unlockSQL, jobName).Scan(&unlocked); err != nil {
				e.log.Warn("worker leader unlock failed", "job", jobName, "error", err)
			} else if !unlocked {
				e.log.Warn("worker leader unlock returned false", "job", jobName)
			}
			conn.Release()
		})
	}
	return release, true, nil
}
