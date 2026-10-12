package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestDescriptorProducerReusesIdenticalDatabaseContent(t *testing.T) {
	catalogFile, charts, assetFile, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
	assets := configureDescriptorDatabaseFixture(t, catalogFile, assetFile, false)
	payload, err := boundedFile(assets.Assets[0].File, 1024)
	if err != nil {
		t.Fatal(err)
	}
	second := &assets.Assets[1]
	if err := os.WriteFile(second.File, payload, 0600); err != nil {
		t.Fatal(err)
	}
	second.Digest = sha(payload)
	data, err := boundedFile(second.ArtifactManifestFile, 1<<20)
	var manifest ocispec.Manifest
	if err != nil || closedJSON(data, &manifest) != nil {
		t.Fatal("duplicate fixture manifest missing")
	}
	manifest.Layers[0].Digest, manifest.Layers[0].Size = digest.FromBytes(payload), int64(len(payload))
	if err := writeJSON(second.ArtifactManifestFile, manifest); err != nil {
		t.Fatal(err)
	}
	data, err = boundedFile(second.ArtifactManifestFile, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	second.ArtifactSubject = "registry.example.test/" + second.ID + "@" + sha(data)
	data, err = boundedFile(catalogFile, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := builtinbundles.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	catalog.Components[0].Values["trivy"].(map[string]any)["javaDbRepository"] = strings.TrimPrefix(second.ArtifactSubject, "registry.example.test/")
	if err := writeJSON(catalogFile, catalog); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(assetFile, assets); err != nil {
		t.Fatal(err)
	}
	result, err := produce(catalogFile, charts)
	if err != nil {
		t.Fatal(err)
	}
	layout := filepath.Join(t.TempDir(), "duplicate-layout")
	index, err := buildLayout(result, charts, layout, "registry.example.test/distribution", assetFile, platforms)
	if err != nil || len(index.Assets) != 2 || index.Assets[0].ArchiveDigest != index.Assets[1].ArchiveDigest {
		t.Fatalf("content-addressed duplicate producer failed: %+v %v", index, err)
	}
	if err := validateLayout(layout); err != nil {
		t.Fatal(err)
	}
	snapshot, cleanup, err := snapshotPreparedLayout(layout)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := validateLayout(snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseReuseRechecksSourceAndExistingOwnedBlob(t *testing.T) {
	for _, mutation := range []string{"valid", "changed-source", "changed-output", "partial-output", "symlink-output"} {
		t.Run(mutation, func(t *testing.T) {
			payload := []byte("identical QA content")
			source, output := filepath.Join(t.TempDir(), "source"), filepath.Join(t.TempDir(), "output")
			for _, file := range []string{source, output} {
				if err := os.WriteFile(file, payload, 0600); err != nil {
					t.Fatal(err)
				}
			}
			claim := ocispec.Descriptor{Digest: digest.FromBytes(payload), Size: int64(len(payload)), MediaType: "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"}
			changed := append([]byte(nil), payload...)
			changed[0] ^= 1
			var err error
			switch mutation {
			case "changed-source":
				err = os.WriteFile(source, changed, 0600)
			case "changed-output":
				err = os.WriteFile(output, changed, 0600)
			case "partial-output":
				err = os.Truncate(output, 1)
			case "symlink-output":
				if err = os.Remove(output); err == nil {
					err = os.Symlink(source, output)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			err = capturePreparedDatabase(source, output, claim)
			if mutation == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unverified source or owned blob reused")
			}
		})
	}
}
