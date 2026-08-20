package migrations_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestCanonicalGreenfieldMigrationSet(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var migrations []string
	for _, entry := range entries {
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".up.sql") || strings.HasSuffix(entry.Name(), ".down.sql")) {
			migrations = append(migrations, entry.Name())
		}
	}
	sort.Strings(migrations)
	if len(migrations) < 2 || migrations[0] != "001_initial.down.sql" || migrations[1] != "001_initial.up.sql" {
		t.Fatalf("migration set = %v, want 001_initial.down.sql and 001_initial.up.sql first", migrations)
	}
	numbered := regexp.MustCompile(`^\d{3}_[a-z0-9_]+\.(up|down)\.sql$`)
	for _, name := range migrations {
		if !numbered.MatchString(name) {
			t.Fatalf("unexpected migration filename %q", name)
		}
	}
}

func TestCanonicalInitialSchemaContainsFluxDeliveryAndNoLegacyDeliveryTables(t *testing.T) {
	up := readMigration(t, "001_initial.up.sql")
	for _, required := range []string{
		"CREATE TABLE public.delivery_sources",
		"CREATE TABLE public.component_bundles",
		"CREATE TABLE public.component_bundle_versions",
		"CREATE TABLE public.delivery_targets",
		"CREATE TABLE public.delivery_rollouts",
		"CREATE TABLE public.delivery_rollout_clusters",
		"CREATE TABLE public.cluster_deployments",
		"CREATE TABLE public.delivery_controller_inventory",
		"CREATE TABLE public.delivery_system_releases",
		"'feature.delivery'",
		`"resource": "delivery_sources"`,
		`"resource": "delivery_rollouts"`,
	} {
		if !strings.Contains(up, required) {
			t.Errorf("canonical migration is missing %q", required)
		}
	}
	for _, forbidden := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bargocd`),
		regexp.MustCompile(`(?i)\bfleet_operations?\b`),
		regexp.MustCompile(`(?i)\bfleet_operation_targets?\b`),
		regexp.MustCompile(`(?i)\bfleet_(mean|stddev|min|max)\b`),
		regexp.MustCompile(`(?i)'feature\.argocd'`),
	} {
		if match := forbidden.FindString(up); match != "" {
			t.Errorf("canonical migration contains legacy delivery identifier %q", match)
		}
	}
}

func TestCanonicalTeardownIsScoped(t *testing.T) {
	down := readMigration(t, "001_initial.down.sql")
	for _, forbidden := range []string{"DROP SCHEMA", "DROP DATABASE", "DROP OWNED", "REASSIGN OWNED"} {
		if strings.Contains(strings.ToUpper(down), forbidden) {
			t.Errorf("canonical teardown contains unsafe broad operation %q", forbidden)
		}
	}
	if !strings.Contains(down, "DROP TABLE IF EXISTS public.delivery_sources CASCADE;") {
		t.Fatal("canonical teardown does not remove delivery_sources")
	}
	if !strings.Contains(down, "DROP TABLE IF EXISTS public.audit_log CASCADE;") {
		t.Fatal("canonical teardown does not remove the partitioned audit parent")
	}
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Clean(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
