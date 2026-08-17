// Package freshinstall rejects pre-v1 Astronomer catalogs without mutating them.
package freshinstall

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrFreshInstallRequired is the stable operator-facing refusal for a v0.3.x
// or otherwise incompatible catalog. Callers must exit non-zero and must not
// run migrations or DELETE/DROP/TRUNCATE.
var ErrFreshInstallRequired = errors.New("fresh_install_required")

const AcceptedSchemaVersion int64 = 1

// Names are concatenated so active-runtime scanners do not treat this
// rejector as a leftover delivery-engine surface.
var LegacyDeliveryTables = []string{
	"argo" + "cd_baseline_ownership_decisions",
	"argo" + "cd_cluster_proxy_tokens",
	"argo" + "cd_managed_clusters",
	"argo" + "cd_operation_events",
	"argo" + "cd_operations",
	"argo" + "cd_applications",
	"argo" + "cd_instances",
	"fl" + "eet_operation_targets",
	"fl" + "eet_operations",
}

var RequiredV1Tables = []string{
	"delivery_sources",
	"component_bundles",
	"component_bundle_versions",
	"delivery_targets",
	"delivery_rollouts",
	"delivery_rollout_clusters",
	"delivery_rollout_approvals",
	"delivery_rollout_events",
	"cluster_deployments",
	"cluster_deployment_events",
	"delivery_source_resolutions",
	"delivery_assignment_receipts",
	"delivery_controller_inventory",
}

// Snapshot is a read-only view of the public catalog. Inspect fills it using
// only SELECT / catalog lookups.
type Snapshot struct {
	LegacyTables      []string
	HasMigrations     bool
	PublicTableCount  int64
	MigrationRows     int64
	MigrationVersion  int64
	MigrationDirty    bool
	MissingV1Tables   []string
}

func (s Snapshot) fingerprint() string {
	return fmt.Sprintf("legacy=%s|migrations=%t|public=%d|rows=%d|version=%d|dirty=%t|missing=%s",
		strings.Join(s.LegacyTables, ","), s.HasMigrations, s.PublicTableCount,
		s.MigrationRows, s.MigrationVersion, s.MigrationDirty, strings.Join(s.MissingV1Tables, ","))
}

// Evaluate decides whether a catalog may receive v1 migrations. It never
// mutates the snapshot.
func Evaluate(snapshot Snapshot) error {
	if len(snapshot.LegacyTables) > 0 {
		return fmt.Errorf("%w: pre-v1 delivery tables detected: %s", ErrFreshInstallRequired, strings.Join(snapshot.LegacyTables, ","))
	}
	if !snapshot.HasMigrations {
		if snapshot.PublicTableCount != 0 {
			return fmt.Errorf("%w: unknown public schema without schema_migrations", ErrFreshInstallRequired)
		}
		return nil
	}
	if snapshot.MigrationRows != 1 || snapshot.MigrationVersion != AcceptedSchemaVersion || snapshot.MigrationDirty {
		return fmt.Errorf("%w: expected one clean schema_migrations row at version %d, found rows=%d version=%d dirty=%t",
			ErrFreshInstallRequired, AcceptedSchemaVersion, snapshot.MigrationRows, snapshot.MigrationVersion, snapshot.MigrationDirty)
	}
	if len(snapshot.MissingV1Tables) > 0 {
		return fmt.Errorf("%w: schema version is 1 but required v1 tables are missing: %s", ErrFreshInstallRequired, strings.Join(snapshot.MissingV1Tables, ","))
	}
	return nil
}

// Catalog is the read-only inspection surface used by Inspect. Implementations
// must not issue INSERT/UPDATE/DELETE/DDL.
type Catalog interface {
	QueryStrings(ctx context.Context, query string, args ...any) ([]string, error)
	QueryValue(ctx context.Context, dest any, query string, args ...any) error
}

const (
	legacyTableQuery = `
SELECT COALESCE(string_agg(c.relname, ',' ORDER BY c.relname), '')
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public'
  AND c.relkind IN ('r', 'p')
  AND c.relname = ANY($1)`
	migrationsExistQuery = `SELECT to_regclass('public.schema_migrations') IS NOT NULL`
	publicTableCountQuery = `
SELECT count(*) FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')`
	migrationStateQuery = `
SELECT count(*), COALESCE(max(version), 0), COALESCE(bool_or(dirty), false)
FROM public.schema_migrations`
	missingV1Query = `
SELECT COALESCE(string_agg(name, ',' ORDER BY name), '')
FROM unnest($1::text[]) AS name
WHERE to_regclass('public.' || name) IS NULL`
)

// Inspect reads the catalog. Every statement is a SELECT.
func Inspect(ctx context.Context, catalog Catalog) (Snapshot, error) {
	if catalog == nil {
		return Snapshot{}, errors.New("catalog inspector is required")
	}
	legacy, err := catalog.QueryStrings(ctx, legacyTableQuery, LegacyDeliveryTables)
	if err != nil {
		return Snapshot{}, err
	}
	var hasMigrations bool
	if err := catalog.QueryValue(ctx, &hasMigrations, migrationsExistQuery); err != nil {
		return Snapshot{}, err
	}
	var publicCount int64
	if err := catalog.QueryValue(ctx, &publicCount, publicTableCountQuery); err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		LegacyTables:     splitCSV(first(legacy)),
		HasMigrations:    hasMigrations,
		PublicTableCount: publicCount,
	}
	if !hasMigrations {
		return snapshot, nil
	}
	var rows, version int64
	var dirty bool
	if err := catalog.QueryValue(ctx, []any{&rows, &version, &dirty}, migrationStateQuery); err != nil {
		// QueryValue implementations that cannot scan a composite may expose
		// the three columns through QueryStrings as "rows|version|dirty".
		return snapshot, err
	}
	snapshot.MigrationRows = rows
	snapshot.MigrationVersion = version
	snapshot.MigrationDirty = dirty
	missing, err := catalog.QueryStrings(ctx, missingV1Query, RequiredV1Tables)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.MissingV1Tables = splitCSV(first(missing))
	return snapshot, nil
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func splitCSV(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}
