package sqlc

import (
	"strings"
	"testing"
)

func TestDeactivateInactiveUsersSecurityContract(t *testing.T) {
	for _, clause := range []string{
		"SET is_active = false",
		"tokens_invalidated_at = GREATEST(",
		"AND is_superuser = false",
		"AND is_service = false",
		"COALESCE(last_login, date_joined, created_at) < $2::timestamptz",
		"DELETE FROM sso_sessions AS session",
		"'user.inactive_retention.deactivated'",
		"INSERT INTO audit_log",
		"JOIN audited ON audited.resource_id = affected.id::text",
	} {
		if !strings.Contains(deactivateInactiveUsers, clause) {
			t.Errorf("DeactivateInactiveUsers missing security clause %q", clause)
		}
	}
}
