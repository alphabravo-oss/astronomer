package distribution

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestDatabaseMetadataZeroObjectCannotGrantFileAuthority(t *testing.T) {
	var empty VerifiedOfflineDatabaseGraph
	var output bytes.Buffer
	if empty.DatabaseLayers() != nil || empty.DatabaseMetadataStatus().Verification != "failed" {
		t.Fatal("zero metadata proof claimed authentication")
	}
	if empty.CopyDatabaseLayer(t.Context(), "", bytes.NewReader([]byte("untrusted")), &output) == nil || output.Len() != 0 {
		t.Fatal("zero metadata object wrote data")
	}
	if _, err := VerifyOfflineDatabaseMetadata(nil, OfflineOptions{}, nil, nil); err == nil {
		t.Fatal("nil context accepted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := VerifyOfflineDatabaseMetadata(canceled, OfflineOptions{Profile: "builtins"}, nil, nil); err == nil {
		t.Fatal("canceled metadata context continued")
	}
}

func TestMissingDatabaseDescriptorBoundsNeverAllocatePayloads(t *testing.T) {
	subject := digest.FromString("declared only; no payload qualification")
	descriptor := ocispec.Descriptor{MediaType: "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip", Digest: subject, Size: MaximumDatabaseLayerBytes}
	claims := map[string]ocispec.Descriptor{}
	if err := retainMissingDatabaseDescriptor(map[string][]byte{}, claims, descriptor); err != nil {
		t.Fatal(err)
	}
	if claims[subject.String()].Size != MaximumDatabaseLayerBytes {
		t.Fatal("bounded missing claim not retained")
	}
	for name, mutate := range map[string]func(*ocispec.Descriptor){
		"oversized":       func(value *ocispec.Descriptor) { value.Size++ },
		"zero":            func(value *ocispec.Descriptor) { value.Size = 0 },
		"unrelated media": func(value *ocispec.Descriptor) { value.MediaType = "application/octet-stream" },
		"external URL":    func(value *ocispec.Descriptor) { value.URLs = []string{"https://unapproved.invalid/layer"} },
		"inline":          func(value *ocispec.Descriptor) { value.Data = []byte("inline") },
	} {
		t.Run(name, func(t *testing.T) {
			changed := descriptor
			mutate(&changed)
			if retainMissingDatabaseDescriptor(map[string][]byte{}, map[string]ocispec.Descriptor{}, changed) == nil {
				t.Fatal("invalid missing descriptor accepted")
			}
		})
	}
	if retainMissingDatabaseDescriptor(map[string][]byte{subject.String(): []byte("partial")}, map[string]ocispec.Descriptor{}, descriptor) == nil {
		t.Fatal("metadata graph accepted partial database bytes")
	}
	changedMedia := descriptor
	changedMedia.MediaType = "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip"
	if retainMissingDatabaseDescriptor(map[string][]byte{}, claims, changedMedia) == nil {
		t.Fatal("same digest accepted contradictory media")
	}
	changed := descriptor
	changed.Size--
	if retainMissingDatabaseDescriptor(map[string][]byte{}, claims, changed) == nil {
		t.Fatal("same digest accepted contradictory size")
	}
}

func TestOnlyPrivateMetadataConstructorMayOmitDatabaseLayer(t *testing.T) {
	config := []byte("{}")
	signature := []byte("structural fixture only; not a trusted signature")
	layer := ocispec.Descriptor{MediaType: "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip", Digest: digest.FromString("declared missing bytes"), Size: MaximumArchiveBytes + 1}
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{Digest: digest.FromBytes(config), Size: int64(len(config))}, Layers: []ocispec.Descriptor{layer}}
	signed := manifest
	signed.Layers = []ocispec.Descriptor{{MediaType: "application/vnd.dev.cosign.simplesigning.v1+json", Digest: digest.FromBytes(signature), Size: int64(len(signature))}}
	metadata, _ := json.Marshal(manifest)
	signing, _ := json.Marshal(signed)
	subjects := []StoredSubject{{Repository: "private.example.test/db", Digest: contentDigest(metadata), SignatureDigest: contentDigest(signing)}}
	blobs := map[string][]byte{contentDigest(config): config, contentDigest(signature): signature, contentDigest(metadata): metadata, contentDigest(signing): signing}
	if _, err := newOfflineRegistry(subjects, blobs); err == nil {
		t.Fatal("existing offline verifier relaxed missing-layer rules")
	}
	store, err := newOfflineRegistryGraph(subjects, blobs, true)
	if err != nil || len(store.databases) != 1 {
		t.Fatal("private structural metadata graph unavailable", err)
	}
	// Structural completeness is not a signature verdict or staging permission.
	if proof, err := VerifyOfflineDatabaseMetadata(t.Context(), OfflineOptions{Profile: "builtins"}, subjects, blobs); err == nil || proof != nil {
		t.Fatal("untrusted structural metadata forged opaque authority")
	}
}
