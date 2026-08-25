package sqlc

import (
	"strings"
	"testing"
)

func TestDeleteDefaultMonitoringBackendIfUnusedGuardsManagedState(t *testing.T) {
	for _, fragment := range []string{
		"cmc.status NOT IN ('uninstalled', 'not_configured')",
		"auth_config->'sharedThanos'->>'status'",
		"auth_config->'sharedAlertmanager'->>'status'",
		"auth_config->'sharedGrafana'->>'status'",
		"auth_config->'sharedLoki'->>'status'",
		"IN ('', 'not_configured', 'uninstalled')",
		"RETURNING mb.id",
	} {
		if !strings.Contains(deleteDefaultMonitoringBackendIfUnused, fragment) {
			t.Fatalf("guarded monitoring backend delete query is missing %q", fragment)
		}
	}
}
