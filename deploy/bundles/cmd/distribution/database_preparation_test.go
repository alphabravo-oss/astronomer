package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func configureDescriptorDatabaseFixture(t *testing.T, catalogFile, assetFile string, large bool) assetIndex {
	t.Helper()
	assets := configureScannerDatabaseFixture(t, catalogFile, assetFile)
	assets.SchemaVersion = 3
	catalogBytes, err := boundedFile(catalogFile, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := builtinbundles.Parse(catalogBytes)
	if err != nil {
		t.Fatal(err)
	}
	for index := range assets.Assets {
		asset := &assets.Assets[index]
		data, err := boundedFile(asset.ArtifactManifestFile, 1<<20)
		var manifest ocispec.Manifest
		if err != nil || closedJSON(data, &manifest) != nil {
			t.Fatal("QA database manifest missing")
		}
		if asset.ID == "trivy-java-database" {
			manifest.Layers[0].MediaType = "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip"
			if large {
				file, err := os.OpenFile(asset.File, os.O_WRONLY|os.O_TRUNC, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(distribution.MaximumArchiveBytes + 1); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				reader, err := os.Open(asset.File)
				if err != nil {
					t.Fatal(err)
				}
				hash := sha256.New()
				count, err := io.CopyBuffer(hash, reader, make([]byte, 64<<10))
				_ = reader.Close()
				if err != nil || count != distribution.MaximumArchiveBytes+1 {
					t.Fatal("large sparse QA input unavailable")
				}
				asset.Digest = fmt.Sprintf("sha256:%x", hash.Sum(nil))
				manifest.Layers[0].Digest, manifest.Layers[0].Size = digest.Digest(asset.Digest), count
			}
		}
		if err := writeJSON(asset.ArtifactManifestFile, manifest); err != nil {
			t.Fatal(err)
		}
		data, err = boundedFile(asset.ArtifactManifestFile, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		asset.ArtifactSubject = "registry.example.test/" + asset.ID + "@" + sha(data)
		asset.Path = "assets/" + asset.ID + ".oci-manifest.json"
		trivy := catalog.Components[0].Values["trivy"].(map[string]any)
		key := "dbRepository"
		if asset.ID == "trivy-java-database" {
			key = "javaDbRepository"
		}
		trivy[key] = strings.TrimPrefix(asset.ArtifactSubject, "registry.example.test/")
	}
	if err := writeJSON(assetFile, assets); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(catalogFile, catalog); err != nil {
		t.Fatal(err)
	}
	return assets
}

func TestDescriptorProducerStreamsLargeChildAndKeepsParentBounded(t *testing.T) {
	catalog, charts, assetFile, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
	assets := configureDescriptorDatabaseFixture(t, catalog, assetFile, true)
	result, err := produce(catalog, charts)
	if err != nil {
		t.Fatal(err)
	}
	layout := filepath.Join(t.TempDir(), "descriptor-layout")
	index, err := buildLayout(result, charts, layout, "registry.example.test/distribution", assetFile, platforms)
	if err != nil || index.SchemaVersion != 2 {
		t.Fatalf("streamed producer positive: %v", err)
	}
	payload, err := boundedFile(filepath.Join(layout, "distribution.tar.gz"), 1<<20)
	if err != nil {
		t.Fatal("large child leaked into bounded parent")
	}
	_, bom, err := distribution.ReadArchive(payload, index.Release, index.CatalogDigest, "builtins")
	if err != nil || bom.AssetContractVersion != distribution.DescriptorAssetContract {
		t.Fatalf("canonical descriptor parent invalid: %v", err)
	}
	if err := validateLayout(layout); err != nil {
		t.Fatal(err)
	}
	snapshot, cleanup, err := snapshotPreparedLayout(layout)
	if err != nil {
		t.Fatalf("streamed owned snapshot: %v", err)
	}
	defer cleanup()
	largeDigest := assets.Assets[1].Digest
	child := filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(largeDigest, "sha256:"))
	if info, err := os.Stat(child); err != nil || info.Size() != distribution.MaximumArchiveBytes+1 {
		t.Fatal("exact large original child missing")
	}
	file, err := os.OpenFile(child, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{1}, 0); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	_ = file.Close()
	if validateLayout(layout) == nil {
		t.Fatal("changed original child accepted")
	}
	if err := validateLayout(snapshot); err != nil {
		t.Fatalf("owned snapshot followed later source mutation: %v", err)
	}
	assets.SchemaVersion = 2
	if err := writeJSON(assetFile, assets); err != nil {
		t.Fatal(err)
	}
	denied := filepath.Join(t.TempDir(), "embedded-denied")
	if _, err := buildLayout(result, charts, denied, "registry.example.test/distribution", assetFile, platforms); err == nil {
		t.Fatal("large payload expanded historical embedded cap")
	}
	if _, err := os.Stat(denied); !os.IsNotExist(err) {
		t.Fatal("failed embedded output retained")
	}
}

func TestStreamedDatabaseInputRejectsPartialOversizeLinksAndHashChanges(t *testing.T) {
	for _, name := range []string{"valid", "partial", "changed", "oversized", "symlink"} {
		t.Run(name, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "input")
			payload := []byte("QA exact payload")
			if err := os.WriteFile(input, payload, 0600); err != nil {
				t.Fatal(err)
			}
			claim := ocispec.Descriptor{Digest: digest.FromBytes(payload), Size: int64(len(payload)), MediaType: "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"}
			switch name {
			case "partial":
				claim.Size++
			case "changed":
				claim.Digest = digest.FromString("changed")
			case "oversized":
				claim.Size = distribution.MaximumDatabaseLayerBytes + 1
			case "symlink":
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(input, link); err != nil {
					t.Fatal(err)
				}
				input = link
			}
			output := filepath.Join(t.TempDir(), "output")
			err := streamPreparedDatabase(input, output, claim)
			if name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				data, err := boundedFile(output, 1024)
				if err != nil || string(data) != string(payload) {
					t.Fatal("streamed capture changed accepted bytes")
				}
			} else if err == nil {
				t.Fatal("invalid source bytes accepted")
			}
		})
	}
}
