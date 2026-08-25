package migrations_test

import (
	"strings"
	"testing"
)

func TestGatekeeperReconciliationMigrationIsSearchPathIndependentAndReversible(t *testing.T) {
	up := readMigration(t, "019_gatekeeper_constraint_reconciliation.up.sql")
	for _, required := range []string{
		"ALTER TABLE public.authored_constraints",
		"CREATE INDEX idx_authored_constraints_reconcile_due",
		"ON public.authored_constraints",
		"DEFAULT 'present'",
		"DEFAULT 'pending'",
		"DEFAULT 1",
	} {
		if !strings.Contains(up, required) {
			t.Errorf("up migration missing %q", required)
		}
	}

	down := readMigration(t, "019_gatekeeper_constraint_reconciliation.down.sql")
	for _, required := range []string{
		"DROP INDEX IF EXISTS public.idx_authored_constraints_reconcile_due",
		"ALTER TABLE public.authored_constraints",
		"DROP COLUMN IF EXISTS desired_state",
	} {
		if !strings.Contains(down, required) {
			t.Errorf("down migration missing %q", required)
		}
	}
}
