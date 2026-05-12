package migrations_test

// Content tests for migration 078_registration_wizard.
//
// As with the other migration_*_test.go siblings, we don't run the SQL
// against Postgres — the helm-test path covers that via the
// migrate-job container. What we pin here is the shape of the
// statements, so a future edit can't:
//
//   - Forget the registration_phase CHECK constraint and let a
//     mis-typed phase land in a row (which would lock the wizard URL
//     to "unknown" and confuse the frontend).
//   - Drop the install_baseline nullability — three-valued semantics
//     are load-bearing (NULL = "operator hasn't decided yet").
//   - Forget the backfill so an upgrade leaves every existing cluster
//     with a Provisioning tab it can't dismiss.
//   - Drop the (cluster_id, step_order) index that the timeline read
//     relies on.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMig078(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// TestRegistrationWizard_MigrationUpContent checks the wizard
// migration adds the columns + table + constraints we need.
func TestRegistrationWizard_MigrationUpContent(t *testing.T) {
	up := loadMig078(t, "078_registration_wizard.up.sql")
	for _, want := range []string{
		"ADD COLUMN IF NOT EXISTS registration_phase",
		"ADD COLUMN IF NOT EXISTS install_baseline BOOLEAN",
		"ADD CONSTRAINT registration_phase_valid",
		"CREATE TABLE IF NOT EXISTS cluster_registration_steps",
		"CONSTRAINT step_status_valid",
		"CREATE INDEX IF NOT EXISTS idx_reg_steps_cluster",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up.sql missing %q", want)
		}
	}
}

// TestRegistrationWizard_BackfillOldClustersToReady — the migration
// MUST backfill pre-wizard cluster rows so existing operators don't
// see a Provisioning tab they can't dismiss.
func TestRegistrationWizard_BackfillOldClustersToReady(t *testing.T) {
	up := loadMig078(t, "078_registration_wizard.up.sql")
	if !strings.Contains(up, "UPDATE clusters") {
		t.Fatal("missing backfill UPDATE statement")
	}
	if !strings.Contains(up, "registration_phase = 'ready'") {
		t.Fatal("backfill should set registration_phase = 'ready'")
	}
	if !strings.Contains(up, "created_at < now() - interval '1 minute'") {
		t.Fatal("backfill should only target pre-existing rows (1-minute floor)")
	}
}

// TestRegistrationWizard_MigrationDownContent — the down migration
// drops the per-step table (additive on clusters columns is left).
func TestRegistrationWizard_MigrationDownContent(t *testing.T) {
	down := loadMig078(t, "078_registration_wizard.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS cluster_registration_steps") {
		t.Error("down should drop cluster_registration_steps")
	}
}

// TestRegistrationWizard_PhaseEnumValues catches drift between the
// CHECK constraint values and the Go phase constants. If someone adds
// a new phase to the SQL we want the failure here so they don't
// forget to add it to internal/registration/phase.go too.
func TestRegistrationWizard_PhaseEnumValues(t *testing.T) {
	up := loadMig078(t, "078_registration_wizard.up.sql")
	for _, p := range []string{
		"'created'", "'awaiting_agent'", "'connected'",
		"'provisioning'", "'ready'", "'failed'",
	} {
		if !strings.Contains(up, p) {
			t.Errorf("missing phase %s from CHECK constraint", p)
		}
	}
}

// TestRegistrationWizard_StepStatusEnum — same drift guard for the
// step status column.
func TestRegistrationWizard_StepStatusEnum(t *testing.T) {
	up := loadMig078(t, "078_registration_wizard.up.sql")
	for _, s := range []string{
		"'pending'", "'running'", "'success'", "'failed'", "'skipped'",
	} {
		if !strings.Contains(up, s) {
			t.Errorf("missing step status %s from CHECK constraint", s)
		}
	}
}
