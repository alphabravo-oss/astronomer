package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func testChart(t *testing.T, component builtinbundles.Component, template string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	files := map[string]string{"Chart.yaml": "apiVersion: v2\nname: " + component.Source.Chart + "\nversion: " + component.Source.Version + "\n", "values.yaml": "{}\n", "templates/workload.yaml": template}
	for _, name := range []string{"Chart.yaml", "values.yaml", "templates/workload.yaml"} {
		data := []byte(files[name])
		if err := tw.WriteHeader(&tar.Header{Name: component.Source.Chart + "/" + name, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(data)
	}
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}
func producerFixture(t *testing.T, template string) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	charts := filepath.Join(root, "charts")
	platforms := filepath.Join(root, "platforms")
	_ = os.Mkdir(charts, 0700)
	_ = os.Mkdir(platforms, 0700)
	index := ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, Manifests: []ocispec.Descriptor{}}
	for _, architecture := range []string{"amd64", "arm64"} {
		configBytes, _ := json.Marshal(ocispec.Image{Platform: ocispec.Platform{Architecture: architecture, OS: "linux"}})
		configDigest := digest.FromBytes(configBytes)
		_ = os.WriteFile(filepath.Join(platforms, configDigest.Encoded()+".json"), configBytes, 0600)
		child := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{Digest: configDigest, Size: int64(len(configBytes))}, Layers: []ocispec.Descriptor{{Digest: digest.FromString("fixture"), Size: 7}}}
		childBytes, _ := json.Marshal(child)
		childDigest := digest.FromBytes(childBytes)
		_ = os.WriteFile(filepath.Join(platforms, childDigest.Encoded()+".json"), childBytes, 0600)
		index.Manifests = append(index.Manifests, ocispec.Descriptor{Digest: childDigest, Size: int64(len(childBytes)), Platform: &ocispec.Platform{OS: "linux", Architecture: architecture}})
	}
	indexBytes, _ := json.Marshal(index)
	indexDigest := digest.FromBytes(indexBytes)
	_ = os.WriteFile(filepath.Join(platforms, indexDigest.Encoded()+".json"), indexBytes, 0600)
	image := "registry.example.test/fixture@" + indexDigest.String()
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	for n := range catalog.Components {
		c := &catalog.Components[n]
		c.Images = []string{image}
		c.Values = map[string]any{"image": image}
		content := testChart(t, *c, template)
		c.Source.ChartDigest = sha(content)
		_ = os.WriteFile(filepath.Join(charts, c.Slug+".tgz"), content, 0600)
	}
	catalogFile := filepath.Join(root, "catalog.json")
	_ = writeJSON(catalogFile, catalog)
	assets := filepath.Join(root, "assets.json")
	_ = writeJSON(assets, assetIndex{SchemaVersion: 1, RequiredCategories: []string{"catalog", "chart", "values", "image_inventory"}, Assets: []assetInput{}})
	return catalogFile, charts, assets, platforms
}
func TestProducerRendersAndClosesImmutableOCIChildren(t *testing.T) {
	template := "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: hook\n  annotations:\n    helm.sh/hook: pre-install\nspec:\n  template:\n    spec:\n      initContainers:\n      - name: init\n        image: {{ .Values.image }}\n      containers:\n      - name: main\n        image: {{ .Values.image }}\n      - name: sidecar\n        image: {{ .Values.image }}\n"
	catalog, charts, assets, platforms := producerFixture(t, template)
	output, err := produce(catalog, charts)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "new-layout")
	subjects, err := buildLayout(output, charts, directory, "registry.example.test/distribution", assets, platforms)
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects.Children) != 3 || subjects.Digest == "" || subjects.CatalogDigest == "" {
		t.Fatal("OCI subjects were not closed")
	}
	payload, err := os.ReadFile(filepath.Join(directory, "distribution.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, bom, err := distribution.ReadArchive(payload, subjects.Release, subjects.CatalogDigest, "builtins")
	if err != nil || len(parsed.Components) != 3 || len(bom.Charts) != 3 {
		t.Fatalf("consumer rejected producer: %v", err)
	}
	for _, subject := range subjects.Children {
		manifestBytes, err := os.ReadFile(filepath.Join(directory, "blobs", "sha256", strings.TrimPrefix(subject.Digest, "sha256:")))
		if err != nil || sha(manifestBytes) != subject.Digest {
			t.Fatal("child subject bytes changed")
		}
		var manifest ocispec.Manifest
		if closedJSON(manifestBytes, &manifest) != nil || len(manifest.Layers) != 1 || manifest.Layers[0].Digest.String() != subject.ArchiveDigest {
			t.Fatal("child does not bind original upstream chart")
		}
	}
	// Reproducibility includes gzip/tar timestamps, ordering and OCI digests.
	second, err := buildLayout(output, charts, filepath.Join(t.TempDir(), "new-layout"), "registry.example.test/distribution", assets, platforms)
	if err != nil || subjects.Digest != second.Digest {
		t.Fatal("distribution is not deterministic")
	}
}
func TestProducerFailsBeforeQualifiedOutput(t *testing.T) {
	for _, template := range []string{"{{ fail \"unrenderable\" }}", "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: registry.example.test/fixture:latest\n"} {
		catalog, charts, _, _ := producerFixture(t, template)
		if _, err := produce(catalog, charts); err == nil {
			t.Fatal("unrenderable/mutable profile was accepted")
		}
	}
	catalog, charts, assets, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
	output, err := produce(catalog, charts)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []string{"missing architecture", "changed platform index", "missing category", "changed chart"} {
		t.Run(test, func(t *testing.T) {
			_, fixtureCharts, fixtureAssets, fixturePlatforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
			switch test {
			case "missing architecture", "changed platform index":
				image := output.Catalog.Components[0].Images[0]
				_, subject, _ := strings.Cut(image, "@")
				path := filepath.Join(fixturePlatforms, strings.TrimPrefix(subject, "sha256:")+".json")
				_ = os.WriteFile(path, []byte("changed"), 0600)
			case "missing category":
				_ = writeJSON(fixtureAssets, assetIndex{SchemaVersion: 1, RequiredCategories: []string{"catalog", "chart", "values", "image_inventory", "plugin"}, Assets: []assetInput{}})
			case "changed chart":
				_ = os.WriteFile(filepath.Join(fixtureCharts, output.Catalog.Components[0].Slug+".tgz"), []byte("changed"), 0600)
			}
			directory := filepath.Join(t.TempDir(), "new-layout")
			if _, err := buildLayout(output, fixtureCharts, directory, "registry.example.test/distribution", fixtureAssets, fixturePlatforms); err == nil {
				t.Fatal("incomplete profile emitted qualified output")
			}
			if _, err := os.Stat(directory); err == nil {
				t.Fatal("failed output retained")
			}
		})
	}
	_ = charts
	_ = assets
	_ = platforms
	images := map[string]bool{}
	if collectValueImages(map[string]any{"operatorGeneratedJob": map[string]any{"image": "registry.example.test/job:mutable"}}, images) == nil {
		t.Fatal("unrendered generated image escaped classification")
	}
	if collectValueImages(map[string]any{"dbRepository": "ghcr.io/aquasecurity/trivy-db:2"}, images) == nil {
		t.Fatal("mutable scanner database escaped closure")
	}
}

func TestPlatformClosureRejectsMissingAndContradictoryArchitectures(t *testing.T) {
	catalog, _, _, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
	bytes, err := os.ReadFile(catalog)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := builtinbundles.Parse(bytes)
	if err != nil {
		t.Fatal(err)
	}
	image := parsed.Components[0].Images[0]
	_, subject, _ := strings.Cut(image, "@")
	indexBytes, err := os.ReadFile(filepath.Join(platforms, strings.TrimPrefix(subject, "sha256:")+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var index ocispec.Index
	if closedJSON(indexBytes, &index) != nil {
		t.Fatal("invalid fixture index")
	}
	index.Manifests = index.Manifests[:1]
	incomplete, _ := json.Marshal(index)
	_ = os.WriteFile(filepath.Join(platforms, strings.TrimPrefix(sha(incomplete), "sha256:")+".json"), incomplete, 0600)
	if verifyPlatforms("registry.example.test/fixture@"+sha(incomplete), platforms) == nil {
		t.Fatal("valid digest-bound single architecture index was accepted")
	}
	contradicted := index.Manifests[0]
	contradicted.Platform = &ocispec.Platform{OS: "linux", Architecture: "arm64"}
	index.Manifests = append(index.Manifests, contradicted)
	contradictory, _ := json.Marshal(index)
	_ = os.WriteFile(filepath.Join(platforms, strings.TrimPrefix(sha(contradictory), "sha256:")+".json"), contradictory, 0600)
	if verifyPlatforms("registry.example.test/fixture@"+sha(contradictory), platforms) == nil {
		t.Fatal("index declaration overrode actual configuration architecture")
	}
}

func TestProducerBoundsRegularInputsBeforeAllocation(t *testing.T) {
	root := t.TempDir()
	file, err := os.Create(filepath.Join(root, "oversized-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate((4 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if _, err := produce(filepath.Join(root, "oversized-catalog.json"), root); err == nil {
		t.Fatal("oversized catalog was read")
	}
	if _, err := boundedFile(root, 1<<20); err == nil {
		t.Fatal("directory was read as regular input")
	}
	target := filepath.Join(root, "small")
	_ = os.WriteFile(target, []byte("safe"), 0600)
	link := filepath.Join(root, "symlink")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := boundedFile(link, 1<<20); err == nil {
		t.Fatal("symlink input was read")
	}
}

func TestReviewedRenderUsesDeclaredKubernetesMinor(t *testing.T) {
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	image := "qa.example.test/workload@sha256:" + strings.Repeat("a", 64)
	chart := testChart(t, catalog.Components[0], "{{ if eq .Capabilities.KubeVersion.Minor \"35\" }}apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - name: minor-specific\n    image: "+image+"\n{{ end }}")
	profile, err := renderReviewedChart(chart, map[string]any{}, "qa", "qa", "1.35.0")
	if err != nil || len(profile.Images) != 1 || profile.Images[0] != image {
		t.Fatalf("supported minor-specific image missing: %v", err)
	}
	if _, err := renderReviewedChart(chart, map[string]any{}, "qa", "qa", "invalid"); err == nil {
		t.Fatal("invalid Kubernetes profile accepted")
	}
}

func TestImageClassificationRejectsInvalidQualifiedReference(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("a", 64)
	for _, raw := range []string{"https://registry.example.test/image", "registry.example.test/UpperCase", "alias/image", "registry.example.test/image?token=private", "registry.example.test/image name"} {
		if _, err := pinnedImage(raw + digest); err == nil {
			t.Fatal("invalid qualified image accepted")
		}
	}
	if normalized, err := pinnedImage("registry.example.test:5000/image:display-tag" + digest); err != nil || normalized != "registry.example.test:5000/image"+digest {
		t.Fatalf("immutable image normalization failed: %v", err)
	}
}
