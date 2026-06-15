package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChartRendersHardenedHookSecurityContexts(t *testing.T) {
	out := helmTemplate(t)

	assertRenderedContains(t, out,
		"name: astronomer-migrate",
		"name: migrate",
		"name: astronomer-preflight",
		"name: preflight",
		"runAsNonRoot: true",
		"seccompProfile:",
		"type: RuntimeDefault",
		"allowPrivilegeEscalation: false",
		"readOnlyRootFilesystem: true",
		"drop:",
		"- ALL",
		"name: HOME",
		"value: /tmp",
		"name: tmp",
		"mountPath: /tmp",
	)
}

func TestChartRendersHardenedBackupJobSecurityContexts(t *testing.T) {
	valuesPath := filepath.Join(t.TempDir(), "backup-values.yaml")
	values := []byte(`
managementBackup:
  enabled: true
  s3:
    bucket: astronomer-backups
    credentialsSecretRef:
      name: backup-creds
managementRestoreDrill:
  enabled: true
`)
	if err := os.WriteFile(valuesPath, values, 0o600); err != nil {
		t.Fatalf("write values override: %v", err)
	}

	out := helmTemplateWithValueFiles(t, []string{valuesPath})
	assertRenderedContains(t, out,
		"name: astronomer-management-backup",
		"name: pgdump-s3",
		"name: astronomer-restore-drill",
		"name: restore-runner",
		"name: postgres",
		"allowPrivilegeEscalation: false",
		"readOnlyRootFilesystem: true",
		"readOnlyRootFilesystem: false",
		"drop:",
		"- ALL",
		"name: HOME",
		"value: /tmp",
		"name: scratch",
		"mountPath: /tmp",
	)
}

func assertRenderedContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered chart missing %q:\n%s", want, out)
		}
	}
}
