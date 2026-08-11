package charlie

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type adminVisibilityRow struct {
	value []byte
	err   error
}

func (r adminVisibilityRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(destinations) != 1 {
		return nil
	}
	target, _ := destinations[0].(*[]byte)
	if target != nil {
		*target = append((*target)[:0], r.value...)
	}
	return nil
}

type adminVisibilityDatabaseFake struct {
	value []byte
}

func (f adminVisibilityDatabaseFake) QueryRow(context.Context, string, ...any) pgx.Row {
	return adminVisibilityRow{value: f.value}
}

func TestAdminVisibilityCatalogExecutesOnlyBoundedJSONProjections(t *testing.T) {
	adapter, err := NewAdminVisibilityCapabilityAdapter(adminVisibilityDatabaseFake{value: []byte(`{"configured":true,"items":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	for name := range AdminVisibilityCapabilityAdapters(adapter) {
		descriptor, ok := capabilityByName(name)
		if !ok {
			t.Fatalf("missing descriptor for %s", name)
		}
		result, err := adapter.Execute(context.Background(), descriptor, nil)
		if err != nil || !json.Valid(result) || len(result) > descriptor.MaxResponseBytes {
			t.Fatalf("%s result=%s err=%v", name, result, err)
		}
	}
}

func TestConfigurationOverviewQueryRedactsContentBearingValues(t *testing.T) {
	query := strings.ToLower(configurationOverviewQuery)
	for _, required := range []string{"secret", "password", "token", "credential", "certificate", "bundle", "redacted"} {
		if !strings.Contains(query, required) {
			t.Fatalf("configuration redaction policy is missing %q", required)
		}
	}
	if strings.Contains(query, "updated_by") || strings.Contains(query, "description") {
		t.Fatal("configuration projection includes operator identity or free-form description")
	}
}

// Release qualification points this test at an already-migrated disposable
// PostgreSQL database. The normal unit suite skips it without infrastructure.
func TestAdminVisibilityQueriesAgainstMigratedPostgres(t *testing.T) {
	dsn := os.Getenv("CHARLIE_VISIBILITY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CHARLIE_VISIBILITY_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	adapter, err := NewAdminVisibilityCapabilityAdapter(pool)
	if err != nil {
		t.Fatal(err)
	}
	for name := range AdminVisibilityCapabilityAdapters(adapter) {
		descriptor, _ := capabilityByName(name)
		result, err := adapter.Execute(context.Background(), descriptor, nil)
		if err != nil {
			t.Fatalf("%s fixed projection failed: %v", name, err)
		}
		if !json.Valid(result) || len(result) > descriptor.MaxResponseBytes {
			t.Fatalf("%s returned an invalid or unbounded result", name)
		}
	}
	runtimeAdapter, err := NewRuntimeCapabilityAdapter(RuntimeCapabilityConfig{Database: pool})
	if err != nil {
		t.Fatal(err)
	}
	for name := range RuntimeCapabilityAdapters(runtimeAdapter) {
		descriptor, _ := capabilityByName(name)
		result, err := runtimeAdapter.Execute(context.Background(), descriptor, nil)
		if err != nil {
			t.Fatalf("%s runtime projection failed: %v", name, err)
		}
		if !json.Valid(result) || len(result) > descriptor.MaxResponseBytes {
			t.Fatalf("%s returned an invalid or unbounded runtime result", name)
		}
	}
}
