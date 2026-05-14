package migrations_test

// Content test for migration 089_seed_docker_hardened_repo. Pins the
// upstream URL + the idempotent ON CONFLICT clause so a future edit
// that "tidies up" by dropping idempotency would fail.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMig089(t *testing.T, name string) string {
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

func TestDockerHardenedRepoSeed_UpContent(t *testing.T) {
	up := loadMig089(t, "089_seed_docker_hardened_repo.up.sql")
	mustContain := []string{
		"INSERT INTO helm_repositories",
		"'docker-hardened-images'",
		"github.com/docker-hardened-images/catalog",
		"ON CONFLICT (name) DO NOTHING",
	}
	for _, s := range mustContain {
		if !strings.Contains(up, s) {
			t.Errorf("up file missing required content:\n  %q", s)
		}
	}
}

func TestDockerHardenedRepoSeed_DownTargets(t *testing.T) {
	down := loadMig089(t, "089_seed_docker_hardened_repo.down.sql")
	if !strings.Contains(down, "DELETE FROM helm_repositories") || !strings.Contains(down, "'docker-hardened-images'") {
		t.Errorf("down file must DELETE the specific row")
	}
}
