package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/stdlib"

	"github.com/alphabravocompany/astronomer-go/internal/db/migrations"
)

// ExpectedSchemaVersion is the highest embedded migration version. It is
// derived from the embedded goose set at init, so a new migration moves the
// readiness floor with it and nothing has to be bumped by hand.
var ExpectedSchemaVersion = migrations.Latest()

// Migrate applies every pending embedded migration under the session
// advisory lock (guide ch.02). The server calls it before it accepts traffic;
// a failure is terminal for the process.
func (d *DB) Migrate(ctx context.Context, logger *slog.Logger) (migrations.Result, error) {
	if d == nil || d.pool == nil {
		return migrations.Result{}, fmt.Errorf("migrate: nil pool")
	}
	// Migrations run on dedicated connections that are closed afterwards,
	// never on the application pool: migration SQL may change session state
	// (00001 sets search_path='' for the session), which must not leak into
	// connections the application later reuses.
	cfg := d.pool.Config().ConnConfig.Copy()
	cfg.Tracer = nil
	sqlDB := stdlib.OpenDB(*cfg)
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxIdleConns(0)
	return migrations.Up(ctx, sqlDB, logger)
}

// SchemaVersion returns the applied goose schema version. It reports 0 — not
// an error — when the version table does not exist yet (a fresh database
// nothing has migrated), so readiness treats "not migrated yet" as not-ready
// rather than errored.
func (d *DB) SchemaVersion(ctx context.Context) (int64, error) {
	if d == nil || d.pool == nil {
		return 0, nil
	}
	var exists bool
	if err := d.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, migrations.VersionTable).Scan(&exists); err != nil {
		return 0, fmt.Errorf("schema version: probe %s: %w", migrations.VersionTable, err)
	}
	if !exists {
		return 0, nil
	}
	var version int64
	if err := d.pool.QueryRow(ctx, `SELECT COALESCE(MAX(version_id), 0) FROM `+migrations.VersionTable+` WHERE is_applied`).Scan(&version); err != nil {
		return 0, fmt.Errorf("schema version: read %s: %w", migrations.VersionTable, err)
	}
	return version, nil
}

// WaitForSchema blocks until the applied schema version reaches
// ExpectedSchemaVersion or ctx ends. The worker uses it so a replica that
// starts before the server has migrated waits instead of crash-looping
// against missing tables; only the server applies migrations.
func (d *DB) WaitForSchema(ctx context.Context, logger *slog.Logger) error {
	const poll = 2 * time.Second
	lastLogged := time.Time{}
	for {
		version, err := d.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		if version >= ExpectedSchemaVersion {
			return nil
		}
		if logger != nil && time.Since(lastLogged) >= 10*time.Second {
			logger.Info("waiting for database schema",
				"event", "schema_wait", "applied", version, "expected", ExpectedSchemaVersion)
			lastLogged = time.Now()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for schema version %d (applied %d): %w", ExpectedSchemaVersion, version, ctx.Err())
		case <-time.After(poll):
		}
	}
}
