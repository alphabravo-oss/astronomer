package migrations_test

// Static content test for migration 049_cluster_templates.
//
// We don't run the migration against Postgres in unit tests — the CI
// helm-test path covers that through the migrate-job container. What we
// DO check here is the shape of the SQL, so a well-meaning future edit
// can't accidentally:
//
//   - Drop the FK ON DELETE RESTRICT on cluster_template_applications,
//     which is what stops a template from being deleted while still
//     bound to clusters. The handler's count-first 409 IS belt; this
//     FK is the suspenders.
//   - Forget the PRIMARY KEY (cluster_id) on
//     cluster_template_applications, which encodes "one template per
//     cluster" — without it, repeated Apply calls would insert
//     duplicates instead of upserting in place.
//   - Drop the UNIQUE constraint on cluster_templates.name, which
//     would let the picker UI show two "production-web" entries.
//
// The .down.sql gets the same scrutiny so an operator rolling back
// doesn't strand orphan tables.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration049File(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path := filepath.Join(dir, name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestMigration_ClusterTemplates_UpContent(t *testing.T) {
	up := loadMigration049File(t, "049_cluster_templates.up.sql")

	for _, want := range []string{
		"CREATE TABLE cluster_templates",
		"CREATE TABLE cluster_template_applications",
		"CREATE TABLE cluster_registration_policies",
		// Uniqueness on the template name — the picker UX relies on this.
		"name        VARCHAR(128) NOT NULL UNIQUE",
		// 1:1 binding per cluster — the cluster_id PK encodes the constraint.
		"cluster_id    UUID        PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE",
		// FK with ON DELETE RESTRICT — the gate that prevents deleting a
		// template that's still applied somewhere.
		"REFERENCES cluster_templates(id) ON DELETE RESTRICT",
		// Reverse-lookup index for "is this template in use anywhere?".
		"CREATE INDEX idx_cluster_template_applications_template",
		// JSONB columns default to '{}' so newly-inserted rows never
		// hold SQL NULL (which the application layer doesn't expect).
		"spec        JSONB        NOT NULL DEFAULT '{}'",
		"spec_snapshot JSONB       NOT NULL DEFAULT '{}'",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_ClusterTemplates_DownContent(t *testing.T) {
	down := loadMigration049File(t, "049_cluster_templates.down.sql")

	for _, want := range []string{
		"DROP TABLE IF EXISTS cluster_registration_policies",
		"DROP TABLE IF EXISTS cluster_template_applications",
		"DROP TABLE IF EXISTS cluster_templates",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}

	// Order matters — the two per-cluster tables FK to cluster_templates
	// and must drop first. Find each line's position and assert ordering.
	posPolicy := strings.Index(down, "cluster_registration_policies")
	posApp := strings.Index(down, "cluster_template_applications")
	posTmpl := strings.Index(down, "DROP TABLE IF EXISTS cluster_templates")
	if posPolicy < 0 || posApp < 0 || posTmpl < 0 {
		t.Fatalf("missing one of the expected DROP statements")
	}
	if posTmpl < posApp || posTmpl < posPolicy {
		t.Errorf("cluster_templates dropped before its dependents; FK rollback would fail")
	}
}
