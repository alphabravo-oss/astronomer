package deploy

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

type renderedDoc map[string]any

func parseRenderedDocs(t *testing.T, out string) []renderedDoc {
	t.Helper()
	decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(out), 4096)
	var docs []renderedDoc
	for {
		var doc renderedDoc
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode rendered manifest: %v", err)
		}
		if len(doc) == 0 || doc["kind"] == nil {
			continue
		}
		docs = append(docs, doc)
	}
	return docs
}

func TestChartHooksAreLimitedToShortLivedJobs(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	allowedHooks := map[string]string{
		"Job/astronomer-migrate":   "post-install,post-upgrade",
		"Job/astronomer-preflight": "pre-install,pre-upgrade",
	}
	seen := map[string]bool{}

	for _, doc := range docs {
		annotations := nestedMap(doc, "metadata", "annotations")
		if annotations == nil {
			continue
		}
		hook := stringValue(annotations["helm.sh/hook"])
		if hook == "" {
			continue
		}
		key := fmt.Sprintf("%s/%s", stringValue(doc["kind"]), stringAt(doc, "metadata", "name"))
		want, ok := allowedHooks[key]
		if !ok {
			t.Fatalf("only short-lived migration/preflight Jobs may use Helm hooks; found hook on %s", key)
		}
		if hook != want {
			t.Fatalf("%s hook mismatch: got %q, want %q", key, hook, want)
		}
		seen[key] = true
	}

	for key := range allowedHooks {
		if !seen[key] {
			t.Fatalf("expected Helm hook resource %s was not rendered", key)
		}
	}
}

func TestServiceAccountAndRuntimeRBACAreManagedReleaseResources(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	for _, target := range []struct {
		kind string
		name string
	}{
		{kind: "ServiceAccount", name: "astronomer"},
		{kind: "ClusterRole", name: "astronomer"},
		{kind: "ClusterRoleBinding", name: "astronomer"},
	} {
		doc := findRenderedDoc(t, docs, target.kind, target.name)
		annotations := nestedMap(doc, "metadata", "annotations")
		if annotations == nil {
			continue
		}
		for _, forbidden := range []string{"helm.sh/hook", "helm.sh/resource-policy"} {
			if stringValue(annotations[forbidden]) != "" {
				t.Fatalf("%s/%s must be a normal managed release resource, but has %s=%q", target.kind, target.name, forbidden, stringValue(annotations[forbidden]))
			}
		}
	}
}

func TestBootstrapSecretAndServerEnvAreWired(t *testing.T) {
	const bootstrapPassword = "operator-provided-initial-password"
	const bootstrapEmail = "admin@example.com"
	docs := parseRenderedDocs(t, helmTemplate(t,
		"bootstrap.password="+bootstrapPassword,
		"bootstrap.email="+bootstrapEmail,
	))

	secret := findRenderedDoc(t, docs, "Secret", "astronomer-bootstrap")
	stringData := nestedMap(secret, "stringData")
	if got := stringValue(stringData["password"]); got != bootstrapPassword {
		t.Fatalf("bootstrap secret password = %q, want %q", got, bootstrapPassword)
	}

	server := findRenderedDoc(t, docs, "Deployment", "astronomer-server")
	container := findContainer(t, podSpecFor(server), "containers", "server")
	passwordEnv := findEnvVar(t, container, "ASTRONOMER_BOOTSTRAP_PASSWORD")
	ref := nestedMap(passwordEnv, "valueFrom", "secretKeyRef")
	if got := stringValue(ref["name"]); got != "astronomer-bootstrap" {
		t.Fatalf("bootstrap password secret name = %q, want astronomer-bootstrap", got)
	}
	if got := stringValue(ref["key"]); got != "password" {
		t.Fatalf("bootstrap password secret key = %q, want password", got)
	}

	emailEnv := findEnvVar(t, container, "ASTRONOMER_BOOTSTRAP_EMAIL")
	if got := stringValue(emailEnv["value"]); got != bootstrapEmail {
		t.Fatalf("bootstrap email env = %q, want %q", got, bootstrapEmail)
	}
}

func TestRenderedContainersDeclareImagePullPolicy(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t,
		"managementLogging.enabled=true",
		"managementLogging.endpoint=http://loki.observability.svc:3100",
		"managementBackup.enabled=true",
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=backup-creds",
		"managementRestoreDrill.enabled=true",
		"dex.enabled=true",
	))

	for _, doc := range docs {
		podSpec := podSpecFor(doc)
		if podSpec == nil {
			continue
		}
		workload := fmt.Sprintf("%s/%s", stringValue(doc["kind"]), stringAt(doc, "metadata", "name"))
		for _, field := range []string{"initContainers", "containers"} {
			for _, container := range containerList(podSpec, field) {
				if stringValue(container["image"]) == "" {
					continue
				}
				if stringValue(container["imagePullPolicy"]) == "" {
					t.Fatalf("%s %s %q does not declare imagePullPolicy", workload, field, stringValue(container["name"]))
				}
			}
		}
	}
}

func TestGlobalImageRegistryAndPullPolicyApplyToCoreImages(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t,
		"image.registry=registry.example.com/platform",
		"image.pullPolicy=Always",
		"image.server.tag=v-server",
		"image.worker.tag=v-worker",
		"image.migrate.tag=v-migrate",
		"frontend.image.tag=v-frontend",
	))

	assertContainerImage(t, docs, "Deployment", "astronomer-server", "containers", "server", "registry.example.com/platform/astronomer-go-server:v-server", "Always")
	assertContainerImage(t, docs, "Deployment", "astronomer-server", "initContainers", "migrate", "registry.example.com/platform/astronomer-go-migrate:v-migrate", "Always")
	assertContainerImage(t, docs, "Deployment", "astronomer-worker", "containers", "worker", "registry.example.com/platform/astronomer-go-worker:v-worker", "Always")
	assertContainerImage(t, docs, "Deployment", "astronomer-frontend", "containers", "frontend", "registry.example.com/platform/astronomer-frontend:v-frontend", "Always")
	assertContainerImage(t, docs, "Job", "astronomer-migrate", "containers", "migrate", "registry.example.com/platform/astronomer-go-migrate:v-migrate", "Always")
	assertContainerImage(t, docs, "StatefulSet", "astronomer-postgres", "containers", "postgres", "registry.example.com/platform/postgres:16-alpine", "Always")
	assertContainerImage(t, docs, "StatefulSet", "astronomer-redis", "containers", "redis", "registry.example.com/platform/redis:7-alpine", "Always")
}

func assertContainerImage(t *testing.T, docs []renderedDoc, kind, name, field, containerName, wantImage, wantPullPolicy string) {
	t.Helper()
	doc := findRenderedDoc(t, docs, kind, name)
	container := findContainer(t, podSpecFor(doc), field, containerName)
	if got := stringValue(container["image"]); got != wantImage {
		t.Fatalf("%s/%s %s %q image = %q, want %q", kind, name, field, containerName, got, wantImage)
	}
	if got := stringValue(container["imagePullPolicy"]); got != wantPullPolicy {
		t.Fatalf("%s/%s %s %q imagePullPolicy = %q, want %q", kind, name, field, containerName, got, wantPullPolicy)
	}
}

func findRenderedDoc(t *testing.T, docs []renderedDoc, kind, name string) renderedDoc {
	t.Helper()
	for _, doc := range docs {
		if stringValue(doc["kind"]) == kind && stringAt(doc, "metadata", "name") == name {
			return doc
		}
	}
	t.Fatalf("rendered %s/%s not found", kind, name)
	return nil
}

func podSpecFor(doc renderedDoc) map[string]any {
	switch stringValue(doc["kind"]) {
	case "Deployment", "StatefulSet", "DaemonSet":
		return nestedMap(doc, "spec", "template", "spec")
	case "Job":
		return nestedMap(doc, "spec", "template", "spec")
	case "CronJob":
		return nestedMap(doc, "spec", "jobTemplate", "spec", "template", "spec")
	default:
		return nil
	}
}

func findContainer(t *testing.T, podSpec map[string]any, field, name string) map[string]any {
	t.Helper()
	for _, container := range containerList(podSpec, field) {
		if stringValue(container["name"]) == name {
			return container
		}
	}
	t.Fatalf("%s container %q not found", field, name)
	return nil
}

func containerList(podSpec map[string]any, field string) []map[string]any {
	if podSpec == nil {
		return nil
	}
	rawList, _ := podSpec[field].([]any)
	containers := make([]map[string]any, 0, len(rawList))
	for _, raw := range rawList {
		if container, ok := raw.(map[string]any); ok {
			containers = append(containers, container)
		}
	}
	return containers
}

func findEnvVar(t *testing.T, container map[string]any, name string) map[string]any {
	t.Helper()
	rawList, _ := container["env"].([]any)
	for _, raw := range rawList {
		env, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringValue(env["name"]) == name {
			return env
		}
	}
	t.Fatalf("env var %q not found", name)
	return nil
}

func stringAt(root map[string]any, path ...string) string {
	if len(path) == 0 {
		return ""
	}
	cur := root
	for _, key := range path[:len(path)-1] {
		cur = nestedMap(cur, key)
		if cur == nil {
			return ""
		}
	}
	return stringValue(cur[path[len(path)-1]])
}

func nestedMap(root map[string]any, path ...string) map[string]any {
	cur := root
	for _, key := range path {
		next, ok := cur[key].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
