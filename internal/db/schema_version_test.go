package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

var migrationVersionRe = regexp.MustCompile(`^(\d+)_.*\.up\.sql$`)

// TestExpectedSchemaVersionMatchesMigrations is the C-03 drift guard: the
// version embedded in the binary must equal the highest migration on disk, so a
// new migration can never ship without the readiness floor moving with it.
func TestExpectedSchemaVersionMatchesMigrations(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("migrations"))
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var maxVersion int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationVersionRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			t.Fatalf("parse migration version %q: %v", e.Name(), err)
		}
		if v > maxVersion {
			maxVersion = v
		}
	}
	if maxVersion == 0 {
		t.Fatal("found no *.up.sql migrations — version regex is broken, not the migrations")
	}
	if ExpectedSchemaVersion != maxVersion {
		t.Fatalf("ExpectedSchemaVersion=%d but max migration on disk is %d; "+
			"bump the constant in schema_version.go when adding a migration", ExpectedSchemaVersion, maxVersion)
	}
}
