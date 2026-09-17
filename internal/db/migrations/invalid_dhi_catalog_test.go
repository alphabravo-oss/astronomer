package migrations_test

import (
	"strings"
	"testing"
)

func TestInvalidDHICatalogMigrationIsNarrowAndReversible(t *testing.T) {
	const (
		migrationID = "41f57bbb-f0c0-41ac-97c6-24051334943a"
		invalidURL  = "https://github.com/docker-hardened-images/catalog/raw/main/chart"
		marker      = "disabled invalid seed: Docker Hardened Image charts require an authenticated OCI catalog"
	)

	up := readMigration(t, "051_disable_invalid_dhi_catalog.up.sql")
	for _, required := range []string{
		"UPDATE public.helm_repositories",
		"SET enabled = false",
		migrationID,
		"name = 'docker-hardened-images'",
		invalidURL,
		"repo_type = 'helm'",
		"auth_type = 'none'",
		marker,
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("invalid DHI catalog up migration missing %q", required)
		}
	}

	down := readMigration(t, "051_disable_invalid_dhi_catalog.down.sql")
	for _, required := range []string{
		"UPDATE public.helm_repositories",
		"SET enabled = true",
		migrationID,
		invalidURL,
		"enabled = false",
		marker,
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("invalid DHI catalog down migration missing %q", required)
		}
	}
}
