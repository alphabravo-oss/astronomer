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
