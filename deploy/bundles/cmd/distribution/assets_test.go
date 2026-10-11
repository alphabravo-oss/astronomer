package main

import (
	"encoding/json"
	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"os"
	"path/filepath"
	"testing"
)

func TestProducerClosesEnabledScannerDatabaseSubjects(t *testing.T) {
	catalogFile, charts, assetFile, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
	_, err := produce(catalogFile, charts)
	if err != nil {
		t.Fatal(err)
	}
	assets := configureScannerDatabaseFixture(t, catalogFile, assetFile)
	result, err := produce(catalogFile, charts)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "layout")
	subjects, err := buildLayout(result, charts, root, "registry.example.test/distribution", assetFile, platforms)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := os.ReadFile(filepath.Join(root, "distribution.tar.gz"))
	_, bom, err := distribution.ReadArchive(payload, subjects.Release, subjects.CatalogDigest, "builtins")
	if err != nil || len(bom.Assets) != 2 {
		t.Fatalf("producer-consumer closure: %v", err)
	}
	if err := validateLayout(root); err != nil {
		t.Fatalf("prepared database graph incomplete: %v", err)
	}
	if len(subjects.Assets) != 2 {
		t.Fatal("captured database subjects were not emitted as OCI children")
	}
	for _, asset := range subjects.Assets {
		if err := validateSubjectBlob(root, asset.Digest, asset.ArchiveDigest, asset.MediaType); err != nil {
			t.Fatal(err)
		}
	}
	changed := subjects
	changed.Assets = append([]publishedAssetSubject(nil), subjects.Assets[:1]...)
	if err := writeJSON(filepath.Join(root, "subjects.json"), changed); err != nil {
		t.Fatal(err)
	}
	if err := validateLayout(root); err == nil {
		t.Fatal("emitted metadata omitted signed database child")
	}
	if err := writeJSON(filepath.Join(root, "subjects.json"), subjects); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func(*assetIndex){func(a *assetIndex) { a.Assets = a.Assets[:1] }, func(a *assetIndex) { a.Assets[0].Digest = sha([]byte("wrong")) }, func(a *assetIndex) { a.Capabilities = []string{} }, func(a *assetIndex) { a.Assets[0].ArtifactConfigFile = "missing-config" }, func(a *assetIndex) { a.Assets[0].ArtifactConfigFile = a.Assets[0].File }, func(a *assetIndex) {
		a.Assets[0].ArtifactSubject = "registry.example.test/wrong@" + sha([]byte("wrong"))
	}} {
		data, _ := json.Marshal(assets)
		var changed assetIndex
		_ = json.Unmarshal(data, &changed)
		mutation(&changed)
		_ = writeJSON(assetFile, changed)
		output := filepath.Join(t.TempDir(), "layout")
		if _, err := buildLayout(result, charts, output, "registry.example.test/distribution", assetFile, platforms); err == nil {
			t.Fatal("incomplete or changed scanner closure emitted qualified output")
		}
		if _, err := os.Stat(output); err == nil {
			t.Fatal("failed qualified output retained")
		}
	}
}

func TestPrerequisiteManifestCannotHideGeneratedDatabaseImage(t *testing.T) {
	catalogFile, charts, assetFile, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
	result, err := produce(catalogFile, charts)
	if err != nil {
		t.Fatal(err)
	}
	image := result.Catalog.Components[0].Images[0]
	content := []byte("apiVersion: postgresql.cnpg.io/v1\nkind: Cluster\nspec:\n  imageName: " + image + "\n")
	file := filepath.Join(t.TempDir(), "cluster.yaml")
	_ = os.WriteFile(file, content, 0600)
	assets := assetIndex{SchemaVersion: 2, Capabilities: []string{}, RequiredCategories: []string{"catalog", "chart", "values", "image_inventory"}, Assets: []assetInput{{ID: "postgres-server", Path: "assets/cluster.yaml", File: file, Digest: sha(content), Category: "database", Images: []string{image}}}}
	_ = writeJSON(assetFile, assets)
	if _, err := buildLayout(result, charts, filepath.Join(t.TempDir(), "layout"), "registry.example.test/distribution", assetFile, platforms); err != nil {
		t.Fatal(err)
	}
	assets.Assets[0].Images = []string{}
	_ = writeJSON(assetFile, assets)
	if _, err := buildLayout(result, charts, filepath.Join(t.TempDir(), "layout"), "registry.example.test/distribution", assetFile, platforms); err == nil {
		t.Fatal("undeclared generated CNPG image accepted")
	}
}

func TestProducerRejectsUnclassifiedAssetInputContract(t *testing.T) {
	file, charts, assetFile, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
	result, err := produce(file, charts)
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []string{
		`{"schema_version":1,"required_categories":["catalog","chart","values","image_inventory"],"assets":[],"capabilities":[]}`,
		`{"schema_version":2,"required_categories":["catalog","chart","values","image_inventory"],"assets":[]}`,
		`{"schema_version":2,"required_categories":["catalog","chart","values","image_inventory"],"capabilities":[]}`,
		`{"schema_version":2,"required_categories":["catalog","chart","values","image_inventory"],"assets":[],"capabilities":[],"allow_unqualified":true}`,
		`{"schema_version":3,"required_categories":["catalog","chart","values","image_inventory"],"assets":[]}`,
		`{"schema_version":4,"required_categories":["catalog","chart","values","image_inventory"],"assets":[],"capabilities":[]}`,
	} {
		_ = os.WriteFile(assetFile, []byte(document), 0600)
		if _, err := buildLayout(result, charts, filepath.Join(t.TempDir(), "layout"), "registry.example.test/distribution", assetFile, platforms); err == nil {
			t.Fatal("unknown/missing asset contract accepted")
		}
	}
}

func configureScannerDatabaseFixture(t *testing.T, catalogFile, assetFile string) assetIndex {
	t.Helper()
	assets := assetIndex{SchemaVersion: 2, Capabilities: []string{"vulnerability-scanning"}, RequiredCategories: []string{"catalog", "chart", "values", "image_inventory", "vulnerability_database"}, Assets: []assetInput{}}
	repositories := map[string]string{}
	for _, role := range []string{"trivy-database", "trivy-java-database"} {
		payload := []byte("QA-only database archive for " + role)
		config := []byte(`{"qa":"captured config"}`)
		manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": map[string]any{"mediaType": "application/vnd.oci.empty.v1+json", "digest": sha(config), "size": len(config)}, "layers": []any{map[string]any{"mediaType": "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip", "digest": sha(payload), "size": len(payload)}}})
		payloadFile := filepath.Join(t.TempDir(), "database.tgz")
		manifestFile := filepath.Join(t.TempDir(), "manifest.json")
		configFile := filepath.Join(t.TempDir(), "config.json")
		_ = os.WriteFile(payloadFile, payload, 0600)
		_ = os.WriteFile(manifestFile, manifest, 0600)
		_ = os.WriteFile(configFile, config, 0600)
		repositories[role] = role + "@" + sha(manifest)
		assets.Assets = append(assets.Assets, assetInput{ID: role, Path: "assets/" + role + ".tgz", File: payloadFile, Digest: sha(payload), Category: "vulnerability_database", Images: []string{}, ArtifactSubject: "registry.example.test/" + repositories[role], ArtifactManifestFile: manifestFile, ArtifactConfigFile: configFile})
	}
	catalogBytes, err := os.ReadFile(catalogFile)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := builtinbundles.Parse(catalogBytes)
	if err != nil {
		t.Fatal(err)
	}
	component := &catalog.Components[0]
	component.Values["operator"] = map[string]any{"vulnerabilityScannerEnabled": true}
	component.Values["trivy"] = map[string]any{"dbRegistry": "registry.example.test", "dbRepository": repositories["trivy-database"], "javaDbRegistry": "registry.example.test", "javaDbRepository": repositories["trivy-java-database"]}
	if err := writeJSON(assetFile, assets); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(catalogFile, catalog); err != nil {
		t.Fatal(err)
	}
	return assets
}
