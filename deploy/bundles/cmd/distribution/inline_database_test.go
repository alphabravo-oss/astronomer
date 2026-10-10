package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
)

func configureInlineDescriptorDatabaseFixture(t *testing.T, catalogFile, assetFile string) assetIndex {
	t.Helper()
	assets := configureDescriptorDatabaseFixture(t, catalogFile, assetFile, false)
	data, err := boundedFile(catalogFile, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := builtinbundles.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	for index := range assets.Assets {
		asset := &assets.Assets[index]
		original, err := boundedFile(asset.ArtifactManifestFile, 1<<20)
		var manifest ocispec.Manifest
		if err != nil || closedJSON(original, &manifest) != nil {
			t.Fatal("QA original manifest unavailable")
		}
		config := []byte("{}")
		manifest.Config = ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: 2, Data: bytes.Clone(config)}
		if err := os.WriteFile(asset.ArtifactConfigFile, config, 0600); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(asset.ArtifactManifestFile, manifest); err != nil {
			t.Fatal(err)
		}
		original, err = boundedFile(asset.ArtifactManifestFile, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		asset.ArtifactSubject = "registry.example.test/" + asset.ID + "@" + sha(original)
		key := "dbRepository"
		if asset.ID == "trivy-java-database" {
			key = "javaDbRepository"
		}
		catalog.Components[0].Values["trivy"].(map[string]any)[key] = strings.TrimPrefix(asset.ArtifactSubject, "registry.example.test/")
	}
	if err := writeJSON(assetFile, assets); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(catalogFile, catalog); err != nil {
		t.Fatal(err)
	}
	return assets
}

func TestInlineDescriptorProducerRetainsExactOriginalAndRequiresConfigMember(t *testing.T) {
	for _, name := range []string{"valid", "missing-config", "changed-config", "inline-data", "inline-size", "inline-media", "inline-url", "inline-layer", "legacy-contract"} {
		t.Run(name, func(t *testing.T) {
			catalog, charts, assetFile, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
			assets := configureInlineDescriptorDatabaseFixture(t, catalog, assetFile)
			asset := &assets.Assets[0]
			original, err := boundedFile(asset.ArtifactManifestFile, 1<<20)
			var manifest ocispec.Manifest
			if err != nil || closedJSON(original, &manifest) != nil {
				t.Fatal("fixture manifest unavailable")
			}
			switch name {
			case "missing-config":
				asset.ArtifactConfigFile = filepath.Join(t.TempDir(), "absent")
			case "changed-config":
				if err := os.WriteFile(asset.ArtifactConfigFile, []byte("[]"), 0600); err != nil {
					t.Fatal(err)
				}
			case "inline-data":
				manifest.Config.Data = []byte("[]")
			case "inline-size":
				manifest.Config.Size++
			case "inline-media":
				manifest.Config.MediaType = "application/vnd.aquasec.trivy.config.v1+json"
			case "inline-url":
				manifest.Config.URLs = []string{"https://unapproved.example.test/config"}
			case "inline-layer":
				manifest.Layers[0].Data = []byte("unapproved inline payload")
			case "legacy-contract":
				assets.SchemaVersion = 2
			}
			if name != "valid" && strings.HasPrefix(name, "inline-") {
				if err := writeJSON(asset.ArtifactManifestFile, manifest); err != nil {
					t.Fatal(err)
				}
				// Keep the negative's manifest and supplied subject coherent, so failure
				// exercises descriptor content policy rather than a stale outer hash.
				changed, err := boundedFile(asset.ArtifactManifestFile, 1<<20)
				if err != nil {
					t.Fatal(err)
				}
				asset.ArtifactSubject = "registry.example.test/" + asset.ID + "@" + sha(changed)
				catalogData, err := boundedFile(catalog, 4<<20)
				if err != nil {
					t.Fatal(err)
				}
				sourceCatalog, err := builtinbundles.Parse(catalogData)
				if err != nil {
					t.Fatal(err)
				}
				sourceCatalog.Components[0].Values["trivy"].(map[string]any)["dbRepository"] = strings.TrimPrefix(asset.ArtifactSubject, "registry.example.test/")
				if err := writeJSON(catalog, sourceCatalog); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeJSON(assetFile, assets); err != nil {
				t.Fatal(err)
			}
			output, err := produce(catalog, charts)
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(t.TempDir(), "layout")
			subjects, err := buildLayout(output, charts, directory, "registry.example.test/distribution", assetFile, platforms)
			if name != "valid" {
				if err == nil {
					t.Fatal("invalid inline original emitted accepted layout")
				}
				if _, err := os.Lstat(directory); !os.IsNotExist(err) {
					t.Fatal("failed original graph left output")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := validateLayout(directory); err != nil {
				t.Fatal(err)
			}
			_, subject, _ := strings.Cut(asset.ArtifactSubject, "@")
			retained, err := boundedFile(filepath.Join(directory, "blobs", "sha256", subject[7:]), 1<<20)
			if err != nil || !bytes.Equal(retained, original) {
				t.Fatal("producer stripped inline bytes or changed original subject")
			}
			configPath := filepath.Join(directory, "blobs", "sha256", manifest.Config.Digest.Encoded())
			config, err := boundedFile(configPath, 1<<20)
			if err != nil || !bytes.Equal(config, []byte("{}")) {
				t.Fatal("exact separate configuration blob absent")
			}
			parent, err := boundedFile(filepath.Join(directory, "distribution.tar.gz"), distribution.MaximumArchiveBytes)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := distribution.ReadArchive(parent, subjects.Release, subjects.CatalogDigest, "builtins"); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(configPath); err != nil {
				t.Fatal(err)
			}
			if validateLayout(directory) == nil {
				t.Fatal("inline bytes excused missing archived configuration blob")
			}
		})
	}
}

func TestOfflineCaptureRetainsInlineEmptyConfigWithoutReplacementFetch(t *testing.T) {
	for _, name := range []string{"valid", "data-mismatch", "wrong-media", "external-url", "existing-conflict"} {
		t.Run(name, func(t *testing.T) {
			config, payload := []byte("{}"), []byte("bounded QA payload")
			configuration := ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: 2, Data: bytes.Clone(config)}
			switch name {
			case "data-mismatch":
				configuration.Data = []byte("[]")
			case "wrong-media":
				configuration.MediaType = "application/vnd.aquasec.trivy.config.v1+json"
			case "external-url":
				configuration.URLs = []string{"https://unapproved.example.test/config"}
			}
			layer := ocispec.Descriptor{MediaType: "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip", Digest: digest.FromBytes(payload), Size: int64(len(payload))}
			original, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: configuration, Layers: []ocispec.Descriptor{layer}})
			if err != nil {
				t.Fatal(err)
			}
			subject := sha(original)
			var configReads, layerReads, unexpected atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var data []byte
				switch r.URL.Path {
				case "/v2/db/manifests/" + subject:
					data = original
					w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
				case "/v2/db/blobs/" + layer.Digest.String():
					data = payload
					layerReads.Add(1)
				case "/v2/db/blobs/" + configuration.Digest.String():
					configReads.Add(1)
					http.NotFound(w, r)
					return
				default:
					unexpected.Add(1)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Docker-Content-Digest", sha(data))
				_, _ = w.Write(data)
			}))
			defer server.Close()
			repository, err := remote.NewRepository(strings.TrimPrefix(server.URL, "http://") + "/db")
			if err != nil {
				t.Fatal(err)
			}
			repository.PlainHTTP = true
			repository.Client = server.Client()
			graph := &capturedGraph{blobs: map[string][]byte{}}
			if name == "existing-conflict" {
				graph.blobs[configuration.Digest.String()] = []byte("[]")
			}
			actual, err := captureManifest(t.Context(), repository, subject, graph, false)
			if name == "valid" {
				if err != nil || actual != subject || !bytes.Equal(graph.blobs[subject], original) || !bytes.Equal(graph.blobs[configuration.Digest.String()], config) || layerReads.Load() != 1 {
					t.Fatal("inline config capture lost exact graph", err)
				}
			} else if err == nil || layerReads.Load() != 0 {
				t.Fatal("invalid inline config reached payload capture")
			}
			if configReads.Load() != 0 || unexpected.Load() != 0 {
				t.Fatal("inline config capture attempted replacement or unrelated fetch")
			}
		})
	}
}

// Outer manifest bytes/subject remain consistent; these malformed claims must
// fail descriptor validation before their Encoded values become filesystem paths.
func TestPreparedConfigAndLayerRejectMalformedDigestBeforePathUse(t *testing.T) {
	for _, name := range []string{"config", "layer", "database-config", "database-layer"} {
		t.Run(name, func(t *testing.T) {
			config, payload := []byte("{}"), []byte("bounded QA payload")
			configuration := ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: 2}
			layer := ocispec.Descriptor{MediaType: "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip", Digest: digest.FromBytes(payload), Size: int64(len(payload))}
			malformed := digest.Digest("sha256:../../unapproved-file")
			if strings.HasSuffix(name, "config") {
				configuration.Digest = malformed
			} else {
				layer.Digest = malformed
			}
			original, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: configuration, Layers: []ocispec.Descriptor{layer}})
			if err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			blobs := filepath.Join(directory, "blobs", "sha256")
			if err := os.MkdirAll(blobs, 0700); err != nil {
				t.Fatal(err)
			}
			subject := sha(original)
			if err := os.WriteFile(filepath.Join(blobs, subject[7:]), original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(blobs, digest.FromBytes(config).Encoded()), config, 0600); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(name, "database-") {
				asset := distribution.Asset{DatabaseDescriptor: &distribution.DatabaseDescriptor{Digest: layer.Digest.String(), Size: layer.Size, MediaType: layer.MediaType}}
				published := publishedAssetSubject{Digest: subject, ArchiveDigest: layer.Digest.String(), MediaType: layer.MediaType}
				err = validatePreparedDatabase(directory, published, asset)
				if err == nil || err.Error() != "prepared database differs from signed descriptor" {
					t.Fatal("malformed database descriptor reached filesystem path use", err)
				}
			} else {
				err = validateSubjectBlob(directory, subject, layer.Digest.String(), layer.MediaType)
				expected := "OCI layout config descriptor invalid"
				if name == "layer" {
					expected = "emitted OCI payload digest is invalid"
				}
				if err == nil || err.Error() != expected {
					t.Fatal("malformed descriptor reached filesystem path use", err)
				}
			}
		})
	}
}
