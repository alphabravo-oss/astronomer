package deploy

import (
	"path/filepath"
	"strings"
	"testing"
)

const (
	parentEnvMarkerName  = "ASTRONOMER_PARENT_ENV_ISOLATION_MARKER"
	parentEnvMarkerValue = "astronomer-parent-only"
	argoEnvMarkerName    = "ASTRONOMER_ARGO_ENV_ISOLATION_MARKER"
	argoEnvMarkerValue   = "astronomer-argo-only"
)

type envMarkerLocation struct {
	kind      string
	workload  string
	container string
	value     string
}

// Helm 3.21.0 temporarily coalesces the parent's values into argo-cd while
// evaluating argo-cd's nested redis-ha dependency. The upstream argo-cd chart
// defines server.env as a list while Astronomer intentionally exposes
// server.env as a map. Keep this render contract until that Helm warning is
// resolved upstream; the warning must never become real value leakage.
func TestServerEnvironmentValuesRemainIsolatedFromArgoCD(t *testing.T) {
	parentSet := "server.env." + parentEnvMarkerName + "=" + parentEnvMarkerValue
	argoNameSet := "argo-cd.server.env[0].name=" + argoEnvMarkerName
	argoValueSet := "argo-cd.server.env[0].value=" + argoEnvMarkerValue
	productionValues := filepath.Join(repoRoot(t), "deploy", "chart", "values-production.yaml")
	productionSets := append([]string{}, productionWiringSets...)
	productionSets = append(productionSets,
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
		"managementBackup.encryptionKeyBackup.wrappingSecretRef.name=astronomer-key-wrap",
		parentSet,
		argoNameSet,
		argoValueSet,
	)

	tests := []struct {
		name       string
		valueFiles []string
		sets       []string
		wantParent bool
		wantArgo   bool
	}{
		{name: "default values"},
		{name: "Astronomer server env", sets: []string{parentSet}, wantParent: true},
		{name: "Argo server env", sets: []string{argoNameSet, argoValueSet}, wantArgo: true},
		{
			name:       "combined env",
			sets:       []string{parentSet, argoNameSet, argoValueSet},
			wantParent: true,
			wantArgo:   true,
		},
		{
			name:       "production values",
			valueFiles: []string{productionValues},
			sets:       productionSets,
			wantParent: true,
			wantArgo:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := helmTemplateWithValueFiles(t, tt.valueFiles, tt.sets...)
			docs := parseRenderedDocs(t, out)

			assertEnvMarkerLocation(t, docs, out, parentEnvMarkerName, parentEnvMarkerValue,
				tt.wantParent, "Deployment", "astronomer-server", "server")
			assertEnvMarkerLocation(t, docs, out, argoEnvMarkerName, argoEnvMarkerValue,
				tt.wantArgo, "Deployment", "astro-argocd-server", "server")
		})
	}
}

func assertEnvMarkerLocation(
	t *testing.T,
	docs []renderedDoc,
	rendered, markerName, markerValue string,
	want bool,
	wantKind, wantWorkload, wantContainer string,
) {
	t.Helper()

	wantCount := 0
	if want {
		wantCount = 1
	}
	if got := strings.Count(rendered, markerName); got != wantCount {
		t.Fatalf("rendered marker name %q occurs %d times, want %d", markerName, got, wantCount)
	}
	if got := strings.Count(rendered, markerValue); got != wantCount {
		t.Fatalf("rendered marker value %q occurs %d times, want %d", markerValue, got, wantCount)
	}

	locations := findEnvMarkerLocations(docs, markerName)
	if len(locations) != wantCount {
		t.Fatalf("environment marker %q has %d workload locations, want %d: %#v", markerName, len(locations), wantCount, locations)
	}
	if !want {
		return
	}

	got := locations[0]
	if got.kind != wantKind || got.workload != wantWorkload || got.container != wantContainer {
		t.Fatalf(
			"environment marker %q leaked to %s/%s container %q; want %s/%s container %q",
			markerName, got.kind, got.workload, got.container, wantKind, wantWorkload, wantContainer,
		)
	}
	if got.value != markerValue {
		t.Fatalf("environment marker %q value = %q, want %q", markerName, got.value, markerValue)
	}
}

func findEnvMarkerLocations(docs []renderedDoc, markerName string) []envMarkerLocation {
	var locations []envMarkerLocation
	for _, doc := range docs {
		podSpec := podSpecFor(doc)
		if podSpec == nil {
			continue
		}
		for _, field := range []string{"initContainers", "containers"} {
			for _, container := range containerList(podSpec, field) {
				rawEnv, _ := container["env"].([]any)
				for _, raw := range rawEnv {
					env, ok := raw.(map[string]any)
					if !ok || stringValue(env["name"]) != markerName {
						continue
					}
					locations = append(locations, envMarkerLocation{
						kind:      stringValue(doc["kind"]),
						workload:  stringAt(doc, "metadata", "name"),
						container: stringValue(container["name"]),
						value:     stringValue(env["value"]),
					})
				}
			}
		}
	}
	return locations
}
