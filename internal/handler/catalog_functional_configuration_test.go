package handler

import "testing"

func TestCatalogFunctionalConfigurationChecks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		slug   string
		values map[string]any
		block  bool
	}{
		{"loki object storage needs buckets", "loki", map[string]any{"loki": map[string]any{"storage": map[string]any{"type": "s3"}}}, true},
		{"loki filesystem does not need buckets", "loki", map[string]any{"loki": map[string]any{"storage": map[string]any{"type": "filesystem"}}}, false},
		{"collector needs explicit runtime", "opentelemetry-collector", map[string]any{"mode": "", "image": map[string]any{"repository": ""}}, true},
		{"collector configured", "opentelemetry-collector", map[string]any{"mode": "deployment", "image": map[string]any{"repository": "otel/opentelemetry-collector-k8s"}}, false},
		{"external dns needs owner", "external-dns", map[string]any{"txtOwnerId": nil}, true},
		{"fluent bit explicit output", "fluent-bit", map[string]any{"config": map[string]any{"outputs": "[OUTPUT]\n Name http"}}, false},
		{"velero needs storage", "velero", map[string]any{"configuration": map[string]any{"backupStorageLocation": []any{}}}, true},
		{"velero disabled backups without node agent remains nonfunctional", "velero", map[string]any{"backupsEnabled": false, "snapshotsEnabled": false, "deployNodeAgent": false, "configuration": map[string]any{"backupStorageLocation": []any{}}}, true},
		{"velero node agent only is explicit", "velero", map[string]any{"backupsEnabled": false, "snapshotsEnabled": false, "deployNodeAgent": true, "configuration": map[string]any{"backupStorageLocation": []any{}}}, false},
		{"velero storage configured", "velero", map[string]any{"configuration": map[string]any{"backupStorageLocation": []any{map[string]any{"name": "default"}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocked := false
			addCatalogFunctionalConfigurationChecks(tc.slug, tc.values, func(_, status, _, _ string) {
				blocked = blocked || status == "blocking"
			})
			if blocked != tc.block {
				t.Fatalf("blocked=%v, want %v", blocked, tc.block)
			}
		})
	}
}
