package freshinstall

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type recordingCatalog struct {
	queries []string
	legacy  string
	hasMig  bool
	public  int64
	state   []any
	missing string
}

func (c *recordingCatalog) QueryStrings(_ context.Context, query string, _ ...any) ([]string, error) {
	c.queries = append(c.queries, normalizeSQL(query))
	if strings.Contains(query, "string_agg(c.relname") {
		return []string{c.legacy}, nil
	}
	if strings.Contains(query, "unnest") {
		return []string{c.missing}, nil
	}
	return nil, errors.New("unexpected strings query")
}

func (c *recordingCatalog) QueryValue(_ context.Context, dest any, query string, _ ...any) error {
	c.queries = append(c.queries, normalizeSQL(query))
	switch typed := dest.(type) {
	case *bool:
		*typed = c.hasMig
		return nil
	case *int64:
		*typed = c.public
		return nil
	case []any:
		if len(typed) != len(c.state) {
			return errors.New("state arity")
		}
		for i := range typed {
			switch slot := typed[i].(type) {
			case *int64:
				*slot = c.state[i].(int64)
			case *bool:
				*slot = c.state[i].(bool)
			}
		}
		return nil
	default:
		return errors.New("unexpected dest")
	}
}

func TestEvaluateRejectsV03CatalogWithoutMutation(t *testing.T) {
	before := Snapshot{
		LegacyTables:     []string{"argo" + "cd_instances", "fl" + "eet_operations"},
		HasMigrations:    true,
		PublicTableCount: 40,
		MigrationRows:    158,
		MigrationVersion: 159,
		MigrationDirty:   false,
	}
	fingerprint := before.fingerprint()
	err := Evaluate(before)
	if !errors.Is(err, ErrFreshInstallRequired) || !strings.Contains(err.Error(), "pre-v1 delivery tables detected") {
		t.Fatalf("error = %v", err)
	}
	if before.fingerprint() != fingerprint {
		t.Fatal("evaluate mutated the catalog snapshot")
	}
}

func TestEvaluateRejectsHistoricalMigrationVersions(t *testing.T) {
	err := Evaluate(Snapshot{HasMigrations: true, MigrationRows: 1, MigrationVersion: 159})
	if !errors.Is(err, ErrFreshInstallRequired) || !strings.Contains(err.Error(), "version=159") {
		t.Fatalf("error = %v", err)
	}
}

func TestEvaluateAllowsEmptyDatabase(t *testing.T) {
	if err := Evaluate(Snapshot{}); err != nil {
		t.Fatal(err)
	}
}

func TestInspectIssuesOnlySelectsAgainstAV03ShapedCatalog(t *testing.T) {
	catalog := &recordingCatalog{
		legacy: "argo" + "cd_applications,argo" + "cd_instances,fl" + "eet_operations",
		hasMig: true,
		public: 87,
		state:  []any{int64(158), int64(159), false},
		missing: "delivery_sources,delivery_targets",
	}
	beforeQueries := append([]string(nil), catalog.queries...)
	snapshot, err := Inspect(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(snapshot.LegacyTables, ",") != catalog.legacy {
		t.Fatalf("legacy = %#v", snapshot.LegacyTables)
	}
	if snapshot.MigrationVersion != 159 || snapshot.MigrationRows != 158 {
		t.Fatalf("migration snapshot = %#v", snapshot)
	}
	if err := Evaluate(snapshot); !errors.Is(err, ErrFreshInstallRequired) {
		t.Fatalf("evaluate = %v", err)
	}
	if len(beforeQueries) != 0 {
		t.Fatal("catalog was not empty before inspect")
	}
	for _, query := range catalog.queries {
		upper := strings.ToUpper(query)
		for _, mutation := range []string{"INSERT ", "UPDATE ", "DELETE ", "DROP ", "TRUNCATE ", "ALTER "} {
			if strings.Contains(upper, mutation) {
				t.Fatalf("inspect issued mutating SQL %q in %s", mutation, query)
			}
		}
		if !strings.Contains(upper, "SELECT") {
			t.Fatalf("inspect issued non-select SQL: %s", query)
		}
	}
}

func TestInspectAndEvaluateAreIdempotentOnTheSameCatalog(t *testing.T) {
	catalog := &recordingCatalog{hasMig: false, public: 0}
	first, err := Inspect(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Inspect(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	if first.fingerprint() != second.fingerprint() {
		t.Fatalf("inspect was not stable: %#v vs %#v", first, second)
	}
	if err := Evaluate(first); err != nil {
		t.Fatal(err)
	}
}

func normalizeSQL(query string) string {
	return strings.Join(strings.Fields(query), " ")
}
