package releasecontract

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsUnknownMutableAndMismatchedRelease(t *testing.T) {
	manifest := validManifest()
	path := filepath.Join(t.TempDir(), "release.json")
	write := func(value any) {
		t.Helper()
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(manifest)
	_, projection, err := Load(path, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if projection.AgentImage != manifest.Astronomer.Images[0].Reference || projection.FluxDigest == "" {
		t.Fatalf("projection = %+v", projection)
	}
	if _, _, err := Load(path, "1.0.1"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("version mismatch error = %v", err)
	}

	var raw map[string]any
	payload, _ := json.Marshal(manifest)
	_ = json.Unmarshal(payload, &raw)
	raw["legacy_provider"] = "forbidden"
	write(raw)
	if _, _, err := Load(path, "1.0.0"); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}
}

func TestApplyMirrorMappingRequiresExactManifestAndDigestPreservation(t *testing.T) {
	manifest := validManifest()
	root := t.TempDir()
	manifestPath := filepath.Join(root, "release.json")
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	projection, err := manifest.Validate("1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	sources := []string{projection.AgentImage, projection.FluxRepository + "@" + projection.FluxDigest, projection.BundleRepository + "@" + projection.BundleDigest}
	entries := make([]map[string]string, 0, len(sources))
	for index, source := range sources {
		digest := strings.SplitN(source, "@", 2)[1]
		entries = append(entries, map[string]string{
			"id": strings.Repeat(string("abc"[index]), 16), "kind": "container_image", "copy_tool": "skopeo",
			"source": source, "target": "mirror.example.test/release/subject-" + string(rune('a'+index)) + "@" + digest, "digest": digest,
		})
	}
	mapping := map[string]any{
		"schema_version": 1, "release_version": "v1.0.0",
		"release_manifest_digest": fmt.Sprintf("sha256:%x", sha256.Sum256(payload)),
		"destination_registry":    "mirror.example.test",
		"registry_rewrites":       map[string]string{"registry.example.test": "mirror.example.test"},
		"entries":                 entries,
	}
	mappingPath := filepath.Join(root, "mapping.json")
	writeMapping := func() {
		t.Helper()
		encoded, marshalErr := json.Marshal(mapping)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := os.WriteFile(mappingPath, encoded, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	writeMapping()
	mirrored, err := ApplyMirrorMapping(mappingPath, manifestPath, projection)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(mirrored.AgentImage, "mirror.example.test/") || mirrored.FluxDigest != projection.FluxDigest {
		t.Fatalf("mirrored projection = %+v", mirrored)
	}
	mapping["release_manifest_digest"] = digest('f')
	writeMapping()
	if _, err := ApplyMirrorMapping(mappingPath, manifestPath, projection); err == nil || !strings.Contains(err.Error(), "does not bind") {
		t.Fatalf("manifest mismatch error = %v", err)
	}
}

func validManifest() Manifest {
	var manifest Manifest
	manifest.SchemaVersion = 1
	manifest.Release.Version = "v1.0.0"
	manifest.Release.InstallMode = "fresh_only"
	manifest.Release.SigningPolicy.CertificateOIDCIssuer = "https://token.actions.githubusercontent.com"
	manifest.Release.SigningPolicy.CertificateIdentity = "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/v1.0.0"
	manifest.Compatibility.Kubernetes.MinimumMinor = "1.33"
	manifest.Compatibility.Kubernetes.MaximumMinor = "1.35"
	manifest.Compatibility.AgentProtocol.Name = "astronomer.delivery"
	manifest.Compatibility.AgentProtocol.Minimum = 2
	manifest.Compatibility.AgentProtocol.Maximum = 2
	manifest.Flux.Version = "v2.9.3"
	manifest.Flux.APIs = []string{"a", "b", "c"}
	manifest.BuiltInBundles.CatalogDigest = digest('a')
	manifest.Charlie.CapabilityDisclosureDigest = digest('b')
	manifest.Charlie.QualifiedVersion = "v1.0.63"
	manifest.Charlie.SigningPolicy.CertificateOIDCIssuer = "https://token.actions.githubusercontent.com"
	manifest.Charlie.SigningPolicy.CertificateIdentity = "https://github.com/alphabravo-oss/charlie/.github/workflows/release.yml@refs/tags/v1.0.63"
	manifest.BuiltInBundles.Components = append(manifest.BuiltInBundles.Components, struct {
		Slug         string   `json:"slug"`
		Chart        string   `json:"chart"`
		ChartVersion string   `json:"chart_version"`
		ChartDigest  string   `json:"chart_digest"`
		Images       []string `json:"images"`
	}{Slug: "built-in"})
	for index, name := range []string{"agent", "frontend", "migrate", "server", "shell", "worker"} {
		manifest.Astronomer.Images = append(manifest.Astronomer.Images, testArtifact(name, "cdef01"[index]))
		manifest.Astronomer.RuntimeImages = append(manifest.Astronomer.RuntimeImages, RuntimeImage{SourceReference: name + ":v1.0.0", Reference: manifest.Astronomer.Images[index].Reference})
	}
	manifest.Astronomer.Chart = testArtifact("chart", '2')
	manifest.Flux.Distribution = testArtifact("flux", '3')
	manifest.BuiltInBundles.Artifact = testArtifact("bundles", '4')
	manifest.Charlie.Artifact = testArtifact("charlie", '5')
	for index, name := range []string{"source-controller", "kustomize-controller", "helm-controller"} {
		manifest.Flux.Controllers = append(manifest.Flux.Controllers, testArtifact(name, "678"[index]))
	}
	return manifest
}

func testArtifact(name string, character byte) Artifact {
	reference := "registry.example.test/release/" + name + "@" + digest(character)
	return Artifact{Name: name, Kind: "oci_artifact", Reference: reference, ContentDigest: digest(character), Evidence: Evidence{
		Signature: "cosign://" + reference, SBOM: "cosign-attestation://" + reference + "#spdxjson",
		Provenance: "cosign-attestation://" + reference + "#slsaprovenance",
	}}
}

func digest(character byte) string { return "sha256:" + strings.Repeat(string(character), 64) }
