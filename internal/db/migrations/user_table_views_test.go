package migrations_test

import (
	"strings"
	"testing"
)

func TestUserTableViewsMigration(t *testing.T) {
	up := readMigration(t, "067_user_table_views.up.sql")
	down := readMigration(t, "067_user_table_views.down.sql")
	for _, want := range []string{
		"user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE",
		"UNIQUE (user_id, table_key, name)",
		"WHERE is_default",
		"jsonb_typeof(state) = 'object'",
		"'public', 'user_table_views', 'state', 1, 'object'",
		"durable_json_validate_write",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing %q", want)
		}
	}
	for _, want := range []string{"DROP TRIGGER IF EXISTS durable_json_validate_write ON public.user_table_views", "table_name = 'user_table_views'", "DROP TABLE IF EXISTS public.user_table_views"} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing %q", want)
		}
	}
}
