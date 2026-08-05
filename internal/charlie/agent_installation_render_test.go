package charlie

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestAgentArgoValuesRenderOnlyHardenedGenericAgent(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is unavailable")
	}
	chart := filepath.Clean(filepath.Join("..", "..", "..", "charlie", "deployments", "helm", "charlie-agent"))
	if _, err := os.Stat(filepath.Join(chart, "Chart.yaml")); err != nil {
		t.Skip("sibling Charlie generic-agent chart is unavailable")
	}
	installer, _, _, _ := testAgentInstaller(t)
	spec := testAgentInstallSpec(t)
	packageFixture := newOnboardingFixture(t)
	packageFixture.object["replica_count"] = 3
	credentials := packageFixture.object["credentials"].([]any)
	packageFixture.object["credentials"] = append(credentials[:2], append([]any{
		map[string]any{"purpose": "agent_enrollment", "replica_ordinal": 2, "credential": "enrollment-secret-value-00000000003", "expires_at": packageFixture.now.Add(30 * time.Minute).Format(time.RFC3339)},
	}, credentials[2:]...)...)
	spec.OnboardingPackage = packageFixture.signed(t)
	spec.ReplicaCount = 3
	names := agentResourceNames(spec, installer.agentNamespace)
	application, err := installer.application(spec, names)
	if err != nil {
		t.Fatal(err)
	}
	values, found, err := unstructured.NestedString(application.Object, "spec", "source", "helm", "values")
	if err != nil || !found {
		t.Fatalf("Argo Helm values unavailable: %v", err)
	}
	valuesFile := filepath.Join(t.TempDir(), "values.json")
	if err := os.WriteFile(valuesFile, []byte(values), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(context.Background(), helm, "template", names.Application, chart, "-f", valuesFile)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("generic agent values do not render: %v\n%s", err, stderr.String())
	}
	rendered := stdout.String()
	for _, expected := range []string{
		"kind: StatefulSet", "replicas: 3", "automountServiceAccountToken: false", "kind: PodDisruptionBudget",
		"kind: NetworkPolicy", "image: \"" + spec.ImageReference + "\"",
		"secretName: \"" + names.Enrollment + "\"", "secretName: \"" + names.BridgeTLS + "\"",
		"secretName: \"" + names.MCPClientTLS + "\"", "port: 7444",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("generic agent render missing %q", expected)
		}
	}
	for _, forbidden := range []string{
		"kind: Secret", "kind: Role", "kind: RoleBinding", "kind: ClusterRole", "kind: ClusterRoleBinding",
		"automountServiceAccountToken: true", ":latest", "enrollment-secret-value", spec.ArtifactCredential,
		"app.kubernetes.io/component: central", "name: charlie-central",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("generic agent render contains forbidden %q", forbidden)
		}
	}
}
