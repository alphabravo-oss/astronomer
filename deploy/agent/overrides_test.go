package agenttemplate

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestAgentOverridesRejectsUnsafeAndUnboundedInput(t *testing.T) {
	cases := []string{
		`{"unknown":true}`,
		`{"proxy":{"https_proxy":"https://user:secret@proxy.example"}}`,
		`{"proxy":{"no_proxy":"safe.example\nINJECTED=true"}}`,
		`{"tolerations":[{"key":"","operator":"Exists"}]}`,
		`{"tolerations":[{"key":"dedicated","operator":"Exists","value":"agent"}]}`,
		`{"resources":{"requests":{"cpu":"2"},"limits":{"cpu":"1"}}}`,
		`{"affinity":{"node":{"preferred":[{"weight":101,"preference":{"match_expressions":[{"key":"zone","operator":"Exists"}]}}]}}}`,
	}
	for _, input := range cases {
		var overrides AgentOverrides
		if err := json.Unmarshal([]byte(input), &overrides); err == nil {
			t.Fatalf("json.Unmarshal(%s) succeeded, want validation error", input)
		}
	}
}

func TestAgentOverridesDigestIsCanonicalAndChangesWithConfiguration(t *testing.T) {
	a := AgentOverrides{Resources: &AgentResources{Requests: AgentResourceValues{CPU: "250m"}}}
	b := AgentOverrides{Resources: &AgentResources{Requests: AgentResourceValues{CPU: "250m"}}}
	c := AgentOverrides{Resources: &AgentResources{Requests: AgentResourceValues{CPU: "300m"}}}
	aDigest, _ := a.Digest()
	bDigest, _ := b.Digest()
	cDigest, _ := c.Digest()
	if aDigest != bDigest || aDigest == cDigest {
		t.Fatalf("digests not deterministic/sensitive: a=%s b=%s c=%s", aDigest, bDigest, cDigest)
	}
}

func TestRenderInstallYAMLAppliesOverridesWithoutRelaxingPlatformPlacement(t *testing.T) {
	overrides := AgentOverrides{
		Tolerations: []AgentToleration{{Key: "dedicated", Operator: "Equal", Value: "platform", Effect: "NoSchedule"}},
		Affinity:    &AgentAffinity{Node: &AgentNodeAffinity{Required: []AgentNodeSelectorTerm{{MatchExpressions: []AgentNodeSelectorRequirement{{Key: "topology.kubernetes.io/zone", Operator: "In", Values: []string{"west-a"}}}}}}},
		Resources:   &AgentResources{Requests: AgentResourceValues{CPU: "250m", Memory: "256Mi"}, Limits: AgentResourceValues{CPU: "1", Memory: "1Gi"}},
		Proxy:       &AgentProxy{HTTPSProxy: "http://proxy.internal:3128", NoProxy: ".svc,10.0.0.0/8"},
	}
	manifest := RenderInstallYAML(InstallTemplateData{ServerURL: "https://astro.example", ClusterID: "cluster", RegistrationToken: "token", AgentImage: "agent:v1", AgentOverrides: overrides})
	digest, _ := overrides.Digest()
	for _, required := range []string{
		`management.astronomer.io/agent-configuration-digest: "` + digest + `"`,
		"kubernetes.io/os: linux", "key: dedicated", "value: platform",
		"cpu: 250m", "memory: 1Gi", "name: HTTPS_PROXY", `value: "http://proxy.internal:3128"`,
		"topology.kubernetes.io/zone", "west-a",
		"allowPrivilegeEscalation: false", "readOnlyRootFilesystem: true",
		"name: HELM_CACHE_HOME", "value: /tmp/helm/cache",
		"name: HELM_CONFIG_HOME", "value: /tmp/helm/config",
		"name: HELM_DATA_HOME", "value: /tmp/helm/data",
	} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("manifest missing %q", required)
		}
	}
}

func TestAgentOverridesApplyToPodSpecReplacesManagedRuntimeFields(t *testing.T) {
	spec := corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Env: []corev1.EnvVar{{Name: "KEEP", Value: "yes"}, {Name: "HTTPS_PROXY", Value: "old"}}}}}
	overrides := AgentOverrides{Proxy: &AgentProxy{HTTPSProxy: "http://proxy.internal:3128"}, Resources: &AgentResources{Limits: AgentResourceValues{Memory: "1Gi"}}}
	if err := overrides.ApplyToPodSpec(&spec, 0); err != nil {
		t.Fatal(err)
	}
	if spec.Containers[0].Env[0].Name != "KEEP" || spec.Containers[0].Env[1].Value != "http://proxy.internal:3128" {
		t.Fatalf("env = %#v", spec.Containers[0].Env)
	}
	if got := spec.Containers[0].Resources.Limits.Memory().String(); got != "1Gi" {
		t.Fatalf("memory limit=%s", got)
	}
	if len(spec.Tolerations) != 2 || spec.Affinity != nil {
		t.Fatalf("managed scheduling defaults not restored: %#v", spec)
	}
}
