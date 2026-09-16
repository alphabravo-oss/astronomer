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
	for _, name := range []string{"astronomer-management-backup", "astronomer-restore-drill"} {
		doc := renderedDocumentContaining(t, out, "name: "+name)
		if !strings.Contains(doc, "automountServiceAccountToken: false") {
			t.Fatalf("%s mounts a Kubernetes API token:\n%s", name, doc)
		}
		if strings.Contains(doc, "serviceAccountName:") {
			t.Fatalf("%s is coupled to a Kubernetes ServiceAccount:\n%s", name, doc)
		}
	}
}

func renderedDocumentContaining(t *testing.T, rendered, needle string) string {
	t.Helper()
	for _, doc := range strings.Split(rendered, "\n---\n") {
		if strings.Contains(doc, needle) {
			return doc
		}
	}
	t.Fatalf("rendered document containing %q not found", needle)
	return ""
}

func assertRenderedContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered chart missing %q:\n%s", want, out)
		}
	}
}
