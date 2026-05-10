package leader

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	tryLeaderSQL = `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`
	unlockSQL    = `SELECT pg_advisory_unlock(hashtextextended($1, 0))`
)

// Elector acquires session-scoped advisory locks on dedicated pooled
// connections. Callers must invoke the returned release func when held=true.
type Elector struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

func New(pool *pgxpool.Pool, log *slog.Logger) *Elector {
	if log == nil {
		log = slog.Default()
	}
	return &Elector{pool: pool, log: log}
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

	released := false
	release := func() {
		if released {
			return
		}
		released = true
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(unlockCtx, unlockSQL, jobName).Scan(&unlocked); err != nil {
			e.log.Warn("worker leader unlock failed", "job", jobName, "error", err)
		} else if !unlocked {
			e.log.Warn("worker leader unlock returned false", "job", jobName)
		}
		conn.Release()
	}
	return release, true, nil
}
