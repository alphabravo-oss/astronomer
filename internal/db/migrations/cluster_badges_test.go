package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestClusterBadgeMigrationUsesValidatedSemanticTokens(t *testing.T) {
	up, err := os.ReadFile("035_cluster_badges.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := string(up)
	for _, required := range []string{
		"ALTER TABLE public.clusters",
		"badge_text varchar(24) NOT NULL DEFAULT ''",
		"badge_color varchar(16) NOT NULL DEFAULT ''",
		"clusters_badge_color_valid",
		"clusters_badge_pair_valid",
	} {
		if !strings.Contains(schema, required) {
			t.Errorf("cluster badge migration missing %q", required)
		}
	}
}
