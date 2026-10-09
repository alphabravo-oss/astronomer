package main

import (
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestPreparedLayoutSnapshotRejectsOpenOrChangedGraph(t *testing.T) {
	for _, mutation := range []string{"extra", "symlink", "index-reference", "missing-config", "changed-config", "layout-version"} {
		t.Run(mutation, func(t *testing.T) {
			catalog, charts, assets, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
			output, err := produce(catalog, charts)
			if err != nil {
				t.Fatal(err)
			}
			layout := filepath.Join(t.TempDir(), "layout")
			index, err := buildLayout(output, charts, layout, "registry.example.test/distribution", assets, platforms)
			if err != nil {
				t.Fatal(err)
			}
			manifestBytes, err := boundedFile(filepath.Join(layout, "blobs", "sha256", index.Digest[7:]), 1<<20)
			var manifest ocispec.Manifest
			if err != nil || closedJSON(manifestBytes, &manifest) != nil {
				t.Fatal("fixture manifest missing")
			}
			configPath := filepath.Join(layout, "blobs", "sha256", manifest.Config.Digest.Encoded())
			switch mutation {
			case "extra":
				err = os.WriteFile(filepath.Join(layout, "unbound.txt"), []byte("unbound"), 0600)
			case "symlink":
				err = os.Symlink(configPath, filepath.Join(layout, "unbound-link"))
			case "index-reference":
				data, readErr := boundedFile(filepath.Join(layout, "index.json"), 1<<20)
				var index ocispec.Index
				if readErr != nil || closedJSON(data, &index) != nil {
					t.Fatal("fixture index missing")
				}
				index.Manifests[0].Annotations[ocispec.AnnotationRefName] = "same-version-changed-subject"
				err = writeJSON(filepath.Join(layout, "index.json"), index)
			case "missing-config":
				err = os.Remove(configPath)
			case "changed-config":
				err = os.WriteFile(configPath, []byte("{\"changed\":true}"), 0600)
			case "layout-version":
				err = os.WriteFile(filepath.Join(layout, "oci-layout"), []byte("{\"imageLayoutVersion\":\"future\"}"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, cleanup, err := snapshotPreparedLayout(layout); err == nil {
				cleanup()
				t.Fatal("open or changed graph accepted")
			}
		})
	}
}

func TestPreparedSnapshotDoesNotFollowLaterInputChanges(t *testing.T) {
	catalog, charts, assets, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
	output, err := produce(catalog, charts)
	if err != nil {
		t.Fatal(err)
	}
	layout := filepath.Join(t.TempDir(), "layout")
	if _, err := buildLayout(output, charts, layout, "registry.example.test/distribution", assets, platforms); err != nil {
		t.Fatal(err)
	}
	snapshot, cleanup, err := snapshotPreparedLayout(layout)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := os.WriteFile(filepath.Join(layout, "distribution.tar.gz"), []byte("changed after capture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateLayout(layout); err == nil {
		t.Fatal("fixture mutation ineffective")
	}
	if err := validateLayout(snapshot); err != nil {
		t.Fatalf("owned snapshot followed mutable input: %v", err)
	}
}
