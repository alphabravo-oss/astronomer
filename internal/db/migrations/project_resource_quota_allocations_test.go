package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestProjectResourceQuotaAllocationMigrationTracksAppliedNamespaceShares(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("041_project_resource_quota_allocations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE public.project_resource_quota_allocations",
		"PRIMARY KEY (project_id, cluster_id, namespace)",
		"REFERENCES public.projects(id) ON DELETE CASCADE",
		"pod_count integer NOT NULL DEFAULT 0 CHECK (pod_count >= 0)",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration missing %q", want)
		}
	}
}
