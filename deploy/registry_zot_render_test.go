package deploy

import (
	"regexp"
	"strings"
	"testing"
)

var renderedImagePattern = regexp.MustCompile(`(?m)^\s+(?:image|imageName):\s+"?([^"\s]+)"?\s*$`)

func renderedImages(rendered string) []string {
	var images []string
	for _, match := range renderedImagePattern.FindAllStringSubmatch(rendered, -1) {
		images = append(images, match[1])
	}
	return images
}

func TestZotRegistryRendersEveryImageFromClusterLocalHost(t *testing.T) {
	const zotHost = "astronomer-zot.default.svc.cluster.local:5000"
	rendered := helmTemplate(t, "registry.zot.enabled=true", "dex.enabled=true", "postgres.mode=cloudNativePG", "postgres.bundled.enabled=false")
	images := renderedImages(rendered)
	if len(images) < 6 {
		t.Fatalf("expected the full image set, got %v", images)
	}
	for _, image := range images {
		if strings.Contains(image, "project-zot/zot@") {
			// The registry cannot serve its own image (pre-loaded on nodes).
			continue
		}
		if !strings.HasPrefix(image, zotHost+"/") {
			t.Errorf("image %q does not use the cluster-local registry %s", image, zotHost)
		}
	}
	for _, want := range []string{
		zotHost + "/alphabravo-oss/astronomer-go-server:",
		zotHost + "/busybox:",
		zotHost + "/dexidp/dex:",
		zotHost + "/cloudnative-pg/postgresql:17.11",
		`REGISTRY_LOCAL: "` + zotHost + `"`,
		`REGISTRY_PLAIN_HTTP: "` + zotHost + `"`,
		`REGISTRY_FALLBACKS: ""`,
		`AGENT_IMAGE_REPOSITORY: "` + zotHost + `/alphabravo-oss/astronomer-go-agent"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered chart lacks %q", want)
		}
	}
	zot := renderedDocumentContaining(t, rendered, "name: zot\n")
	for _, want := range []string{"kind: Deployment", "runAsNonRoot: true", "readOnlyRootFilesystem: true", "allowPrivilegeEscalation: false", `drop: ["ALL"]`, "type: RuntimeDefault", "claimName: astronomer-zot", "automountServiceAccountToken: false"} {
		if !strings.Contains(zot, want) {
			t.Errorf("zot Deployment lacks %q", want)
		}
	}
	docs := parseRenderedDocs(t, rendered)
	for _, kind := range []string{"Service", "PersistentVolumeClaim", "NetworkPolicy", "ConfigMap"} {
		if !renderedDocExists(docs, kind, "astronomer-zot") {
			t.Errorf("missing %s/astronomer-zot", kind)
		}
	}
	if !renderedDocExists(docs, "NetworkPolicy", "astronomer-zot-clients") {
		t.Error("missing server/worker egress policy to the registry")
	}
}

func TestOnlineProfileKeepsOnlineDefaults(t *testing.T) {
	rendered := helmTemplate(t)
	if strings.Contains(rendered, "astronomer-zot") {
		t.Fatal("zot resources rendered while registry.zot.enabled=false")
	}
	if !strings.Contains(rendered, `REGISTRY_LOCAL: ""`) {
		t.Fatal("online profile must leave REGISTRY_LOCAL empty")
	}
	if !strings.Contains(rendered, "ghcr.io/alphabravo-oss/astronomer-go-server:") {
		t.Fatal("online profile must render the GHCR first-party default")
	}
}

func TestImageRegistryWinsOverZotAndFallbacksRender(t *testing.T) {
	rendered := helmTemplate(t, "registry.zot.enabled=true", "image.registry=mirror.corp:5000", "registry.fallbacks[0]=fallback.corp/astro")
	for _, want := range []string{`REGISTRY_LOCAL: "mirror.corp:5000"`, `REGISTRY_FALLBACKS: "fallback.corp/astro"`, `REGISTRY_PLAIN_HTTP: ""`, "mirror.corp:5000/alphabravo-oss/astronomer-go-worker:", "mirror.corp:5000/project-zot/zot@sha256:"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered chart lacks %q", want)
		}
	}
}
