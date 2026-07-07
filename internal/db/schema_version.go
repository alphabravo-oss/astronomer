package db

import "context"

// ExpectedSchemaVersion is the highest migration number the binary was built
// against. /readyz gates the pod out of Service rotation until the applied
// schema reaches at least this version (C-03), which closes the upgrade-window
// gap where new code serves traffic against the old schema because migrations
// run post-upgrade.
//
// It MUST equal max(internal/db/migrations/*.up.sql). TestExpectedSchemaVersionMatchesMigrations
// (schema_version_test.go) fails the build when a new migration is added without
// bumping this constant, so it can't silently drift.
const ExpectedSchemaVersion int64 = 133

// SchemaVersion returns the currently-applied golang-migrate schema version
// (max(version) in schema_migrations). It reports 0 — not an error — when the
// table does not exist yet or is empty (a fresh, pre-migration install), so the
// readiness probe treats "not migrated yet" as not-ready rather than errored.
func (d *DB) SchemaVersion(ctx context.Context) (int64, error) {
	if d == nil || d.pool == nil {
		return 0, nil
	}
	var version int64
	if err := d.pool.QueryRow(ctx, `SELECT COALESCE(max(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		if isUndefinedTable(err) {
			return 0, nil
		}
		return 0, err
	}
	return version, nil
}
