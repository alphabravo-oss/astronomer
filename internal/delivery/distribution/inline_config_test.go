package distribution

import (
	"bytes"
	"encoding/json"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestInlineDatabaseConfigRequiresExactSeparateOfflineBlob(t *testing.T) {
	for _, name := range []string{"valid", "missing-config", "changed-config", "inline-data", "inline-size", "inline-media", "inline-url", "inline-layer"} {
		t.Run(name, func(t *testing.T) {
			config, payload, signature := []byte("{}"), []byte("bounded QA payload"), []byte("structural signature shape only")
			configuration := ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: 2, Data: bytes.Clone(config)}
			layer := ocispec.Descriptor{MediaType: "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip", Digest: digest.FromBytes(payload), Size: int64(len(payload))}
			switch name {
			case "inline-data":
				configuration.Data = []byte("[]")
			case "inline-size":
				configuration.Size++
			case "inline-media":
				configuration.MediaType = "application/vnd.aquasec.trivy.config.v1+json"
			case "inline-url":
				configuration.URLs = []string{"https://unapproved.example.test/config"}
			case "inline-layer":
				layer.Data = bytes.Clone(payload)
			}
			manifest, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: configuration, Layers: []ocispec.Descriptor{layer}})
			if err != nil {
				t.Fatal(err)
			}
			signed, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: 2}, Layers: []ocispec.Descriptor{{MediaType: "application/vnd.dev.cosign.simplesigning.v1+json", Digest: digest.FromBytes(signature), Size: int64(len(signature))}}})
			if err != nil {
				t.Fatal(err)
			}
			subjects := []StoredSubject{{Repository: "private.example.test/db", Digest: contentDigest(manifest), SignatureDigest: contentDigest(signed)}}
			blobs := map[string][]byte{contentDigest(config): config, contentDigest(payload): payload, contentDigest(signature): signature, contentDigest(manifest): manifest, contentDigest(signed): signed}
			if name == "missing-config" {
				delete(blobs, contentDigest(config))
			}
			if name == "changed-config" {
				blobs[contentDigest(config)] = []byte("[]")
			}
			store, err := newOfflineRegistry(subjects, blobs)
			if name == "valid" {
				if err != nil || !bytes.Equal(store.blobs[contentDigest(manifest)], manifest) || !bytes.Equal(store.blobs[contentDigest(config)], config) {
					t.Fatal("original inline graph rejected or changed", err)
				}
				// Structural closure is not signature verification. It grants no trusted
				// report, payload freshness or release qualification by itself.
			} else if err == nil {
				t.Fatal("incomplete/changed inline graph accepted")
			}
		})
	}
}

func TestInlineEmptyConfigDoesNotExpandEmbeddedAssetContract(t *testing.T) {
	config, payload := []byte("{}"), []byte("bounded QA database")
	layer := ocispec.Descriptor{MediaType: "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip", Digest: digest.FromBytes(payload), Size: int64(len(payload))}
	original, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: 2, Data: bytes.Clone(config)}, Layers: []ocispec.Descriptor{layer}})
	if err != nil {
		t.Fatal(err)
	}
	asset := Asset{ID: "trivy-database", MemberPath: "assets/payload", ArtifactManifestPath: "assets/manifest", ArtifactConfigPath: "assets/config", ArtifactSubject: "supplier.example.test/db@" + contentDigest(original), DatabaseDescriptor: &DatabaseDescriptor{Digest: layer.Digest.String(), Size: layer.Size, MediaType: layer.MediaType}}
	members := map[string][]byte{asset.MemberPath: payload, asset.ArtifactManifestPath: original, asset.ArtifactConfigPath: config}
	if err := validateArtifactAssets(DescriptorAssetContract, []Asset{asset}, members); err != nil {
		t.Fatal("descriptor inline control rejected", err)
	}
	asset.DatabaseDescriptor = nil
	if validateArtifactAssets(EmbeddedAssetContract, []Asset{asset}, members) == nil {
		t.Fatal("inline empty config silently broadened historical embedded contract")
	}
}
