package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestRewriteTargetsCoverAllEncryptedColumns fails the build when a migration
// introduces a Fernet-protected column that keyrotate does not sweep. Without
// this guard, adding a new *_encrypted column and forgetting to list it in
// rewriteTargets silently bricks that column on the next key rotation (the exact
// bug this test exists to prevent: keyrotate previously covered only 3 of ~14
// encrypted columns). A new column must be added to rewriteTargets, or — if its
// ciphertext lives inside a JSONB blob — to jsonbExemptColumns with a reason.
func TestRewriteTargetsCoverAllEncryptedColumns(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "db", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	covered := map[string]bool{}
	for _, tg := range rewriteTargets {
		covered[tg.table+"."+tg.column] = true
	}
	for k := range jsonbExemptColumns {
		covered[k] = true
	}

	// A column definition line whose name contains "encrypted", inside a CREATE
	// TABLE / ALTER TABLE ... ADD COLUMN context. We only care about the column
	// name + owning table, not the full type.
	tableRe := regexp.MustCompile(`(?i)(?:CREATE TABLE(?:\s+IF NOT EXISTS)?|ALTER TABLE(?:\s+IF EXISTS)?(?:\s+ONLY)?)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	// Matches "colname TYPE ..." column defs and "ADD COLUMN [IF NOT EXISTS] colname TYPE".
	colRe := regexp.MustCompile(`(?i)^\s*(?:ADD\s+COLUMN\s+(?:IF NOT EXISTS\s+)?)?([a-zA-Z_][a-zA-Z0-9_]*encrypted[a-zA-Z0-9_]*)\s+(?:TEXT|BYTEA|VARCHAR)`)
	addColInline := regexp.MustCompile(`(?i)ADD\s+COLUMN\s+(?:IF NOT EXISTS\s+)?([a-zA-Z_][a-zA-Z0-9_]*encrypted[a-zA-Z0-9_]*)`)

	found := map[string]string{} // table.column -> migration file

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		curTable := ""
		for _, line := range strings.Split(string(data), "\n") {
			if m := tableRe.FindStringSubmatch(line); m != nil {
				curTable = m[1]
			}
			var col string
			if m := colRe.FindStringSubmatch(line); m != nil {
				col = m[1]
			} else if m := addColInline.FindStringSubmatch(line); m != nil {
				col = m[1]
			}
			if col == "" || curTable == "" {
				continue
			}
			found[curTable+"."+col] = name
		}
	}

	if len(found) == 0 {
		t.Fatal("parsed zero encrypted columns from migrations — the column regex is broken, not the migrations")
	}

	for key, file := range found {
		if !covered[key] {
			t.Errorf("encrypted column %s (introduced in %s) is not swept by keyrotate.\n"+
				"Add it to rewriteTargets in cmd/keyrotate/main.go, or to jsonbExemptColumns with a reason.\n"+
				"An unswept encrypted column becomes undecryptable once the old key is dropped.", key, file)
		}
	}
}

// TestRewriteTargetsCoverChartRepositoryAuthConfig pins the migration-145
// column explicitly.
//
// TestRewriteTargetsCoverAllEncryptedColumns above already derives this from
// the migrations, but only because auth_config_encrypted was deliberately
// shaped to match its regex ("*encrypted*" + TEXT). Had the ciphertext been
// tucked inside the existing auth_config JSONB — the obvious "don't add a
// column" alternative — that guard would have seen nothing, keyrotate would
// have swept nothing, and every stored chart-repository credential would have
// become undecryptable the first time an operator retired a key. This test
// states the requirement directly so the reasoning survives a refactor of the
// regex.
func TestRewriteTargetsCoverChartRepositoryAuthConfig(t *testing.T) {
	for _, tg := range rewriteTargets {
		if tg.table == "helm_repositories" && tg.column == "auth_config_encrypted" {
			if tg.idCol != "id" {
				t.Fatalf("helm_repositories primary key column is %q, want \"id\"", tg.idCol)
			}
			return
		}
	}
	t.Fatal("helm_repositories.auth_config_encrypted is not in rewriteTargets: " +
		"a key rotation would leave every chart-repository credential encrypted under the retired key")
}

// TestRewriteTargetsCoverMonitoringBackendAuthConfig pins the migration-146
// column explicitly, for the same reason as its 145 sibling above.
//
// The monitoring case is the one where an unswept envelope is hardest to
// diagnose. Losing the chart-repository credential shows up as a 401 recorded
// in helm_repositories.last_sync_error, next to the repository name. Losing
// this one shows up as the monitoring backend reporting "degraded", cluster
// metrics silently falling back to synthetic summaries, and alert rules
// evaluating against nothing — three symptoms, none of which says
// "credential", and all of which appear hours after the rotation that caused
// them.
func TestRewriteTargetsCoverMonitoringBackendAuthConfig(t *testing.T) {
	for _, tg := range rewriteTargets {
		if tg.table == "monitoring_backends" && tg.column == "auth_config_encrypted" {
			if tg.idCol != "id" {
				t.Fatalf("monitoring_backends primary key column is %q, want \"id\"", tg.idCol)
			}
			return
		}
	}
	t.Fatal("monitoring_backends.auth_config_encrypted is not in rewriteTargets: " +
		"a key rotation would leave the Thanos/Prometheus/Alertmanager credential encrypted under the retired key")
}

func TestRewriteTargetsCoverCharlieLocalTrust(t *testing.T) {
	for _, tg := range rewriteTargets {
		if tg.table == "charlie_connections" && tg.column == "local_trust_material_encrypted" {
			if tg.idCol != "id" {
				t.Fatalf("charlie_connections primary key column is %q, want id", tg.idCol)
			}
			return
		}
	}
	t.Fatal("charlie_connections.local_trust_material_encrypted is not in rewriteTargets")
}
