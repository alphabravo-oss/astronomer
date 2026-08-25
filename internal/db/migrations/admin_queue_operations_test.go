package migrations_test

import (
	"strings"
	"testing"
)

func TestAdminQueueOperationMigrationFencesTargetsAndLeasesWorkers(t *testing.T) {
	up := readMigration(t, "020_admin_queue_operations.up.sql")
	for _, required := range []string{
		"CREATE TABLE public.admin_queue_operations",
		"ON public.admin_queue_operations (queue_name, task_id)",
		"WHERE status IN ('pending', 'running', 'retrying')",
		"CHECK (status IN ('pending', 'running', 'retrying', 'failed', 'succeeded'))",
		"locked_until timestamptz",
		"effect_started_at timestamptz",
		"CHECK (action IN ('retry', 'discard'))",
	} {
		if !strings.Contains(up, required) {
			t.Errorf("up migration missing %q", required)
		}
	}
	if strings.Contains(up, "(action, queue_name, task_id)") {
		t.Fatal("retry and discard must not acquire separate active-operation keys")
	}
	down := readMigration(t, "020_admin_queue_operations.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS public.admin_queue_operations") {
		t.Fatal("down migration does not remove admin_queue_operations")
	}
}
