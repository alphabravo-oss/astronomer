package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDescriptorProducerRejectsChangedInputsAndLeavesNoAcceptedOutput(t *testing.T) {
	for _, mutation := range []string{"payload", "partial", "symlink", "category", "member", "config", "role", "subject"} {
		t.Run(mutation, func(t *testing.T) {
			catalog, charts, assetFile, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
			assets := configureDescriptorDatabaseFixture(t, catalog, assetFile, false)
			result, err := produce(catalog, charts)
			if err != nil {
				t.Fatal(err)
			}
			asset := &assets.Assets[0]
			switch mutation {
			case "payload":
				data, readErr := boundedFile(asset.File, 1024)
				if readErr != nil {
					t.Fatal(readErr)
				}
				data[0] ^= 1
				err = os.WriteFile(asset.File, data, 0600)
			case "partial":
				err = os.Truncate(asset.File, 1)
			case "symlink":
				link := filepath.Join(t.TempDir(), "link")
				err = os.Symlink(asset.File, link)
				asset.File = link
			case "category":
				asset.Category = "tool"
			case "member":
				asset.Path = "assets/unbound.tgz"
			case "config":
				err = os.WriteFile(asset.ArtifactConfigFile, []byte(`[]`), 0600)
			case "role":
				asset.ID = "valkey"
				asset.Path = "assets/valkey.oci-manifest.json"
			case "subject":
				asset.ArtifactSubject = "registry.example.test/wrong@" + sha([]byte("changed"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := writeJSON(assetFile, assets); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "failed-layout")
			if _, err := buildLayout(result, charts, output, "registry.example.test/distribution", assetFile, platforms); err == nil {
				t.Fatal("changed descriptor inputs emitted accepted layout")
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatal("failed descriptor output was retained")
			}
		})
	}
}
