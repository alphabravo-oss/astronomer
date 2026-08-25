package deploy

import (
	"strings"
	"testing"
)

func TestManagementBackupEnablementPropagatesToBothProcesses(t *testing.T) {
	for _, enabled := range []string{"true", "false"} {
		t.Run(enabled, func(t *testing.T) {
			out := helmTemplate(t, "managementBackup.enabled="+enabled)
			want := "MANAGEMENT_BACKUP_ENABLED: \"" + enabled + "\""
			if strings.Count(out, want) != 1 {
				t.Fatalf("shared config does not contain exactly one %q", want)
			}
			for _, deployment := range []string{"astronomer-server", "astronomer-worker"} {
				marker := "kind: Deployment\nmetadata:\n  name: " + deployment
				start := strings.Index(out, marker)
				if start < 0 {
					t.Fatalf("missing %s deployment", deployment)
				}
				section := out[start:]
				if next := strings.Index(section[1:], "\n---"); next >= 0 {
					section = section[:next+1]
				}
				if !strings.Contains(section, "configMapRef:") || !strings.Contains(section, "name: astronomer-config") {
					t.Fatalf("%s does not consume the shared management-backup config", deployment)
				}
			}
		})
	}
}
