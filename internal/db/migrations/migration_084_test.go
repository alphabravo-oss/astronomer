package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration084File(t *testing.T, name string) string {
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

func TestChartRatingsRecover_MirrorsCanonicalSchema(t *testing.T) {
	up := loadMigration084File(t, "084_chart_ratings_recover.up.sql")

	for _, want := range []string{
		"stars           SMALLINT NOT NULL CHECK (stars BETWEEN 1 AND 5)",
		"note            VARCHAR(280) NOT NULL DEFAULT ''",
		"UNIQUE (user_id, installation_id)",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_chart_ratings_user_chart_unique",
		"ON chart_ratings (user_id, chart_id)",
		"WHERE installation_id IS NULL",
		"CREATE INDEX IF NOT EXISTS idx_chart_ratings_chart ON chart_ratings (chart_id)",
		"avg_stars       NUMERIC(3,2) NOT NULL DEFAULT 0.00",
		"bayesian_score  NUMERIC(4,2) NOT NULL DEFAULT 0.00",
		"weight          INTEGER NOT NULL DEFAULT 0",
		"CHECK (chart_a_id < chart_b_id)",
		"CREATE INDEX IF NOT EXISTS idx_chart_co_a ON chart_co_installation (chart_a_id, weight DESC)",
		"CREATE INDEX IF NOT EXISTS idx_chart_co_b ON chart_co_installation (chart_b_id, weight DESC)",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("084 up migration missing canonical chart-ratings content %q", want)
		}
	}
}

func TestChartRatingsRecover_DoesNotUseLegacyColumnNames(t *testing.T) {
	up := loadMigration084File(t, "084_chart_ratings_recover.up.sql")

	for _, legacy := range []string{
		" rating ",
		" comment ",
		"rating_avg",
		"install_count",
		"co_count",
		"last_seen_at",
		"idx_chart_co_installation_a",
		"idx_chart_co_installation_b",
	} {
		if strings.Contains(up, legacy) {
			t.Errorf("084 up migration should not contain legacy schema fragment %q", legacy)
		}
	}
}
