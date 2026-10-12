package resolver

import (
	"bytes"
	"strings"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestArtifactConfigurationMatchesExactInlineEmptyObjectOnly(t *testing.T) {
	for _, name := range []string{"valid-inline", "valid-regular", "regular-json-caller-owned", "invalid-digest", "other-algorithm", "zero-size", "oversized", "descriptor-size", "descriptor-digest", "config-bytes", "inline-bytes", "inline-object", "inline-array", "inline-media", "external-url"} {
		t.Run(name, func(t *testing.T) {
			config := []byte("{}")
			descriptor := ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: int64(len(config)), Data: append([]byte(nil), config...)}
			expected := false
			switch name {
			case "valid-inline":
				expected = true
			case "valid-regular":
				descriptor.Data = nil
				expected = true
			case "regular-json-caller-owned":
				// This helper binds bytes, not the caller's JSON interpretation/schema.
				config = []byte(`{"duplicate":1,"duplicate":2}`)
				descriptor.Data = nil
				descriptor.Digest, descriptor.Size = digest.FromBytes(config), int64(len(config))
				expected = true
			case "invalid-digest":
				descriptor.Digest = "sha256:not-a-digest"
			case "other-algorithm":
				descriptor.Digest = digest.Digest("sha512:" + strings.Repeat("a", 128))
			case "zero-size":
				descriptor.Size = 0
			case "oversized":
				config = bytes.Repeat([]byte("a"), 1<<20+1)
				descriptor.Digest, descriptor.Size, descriptor.Data = digest.FromBytes(config), int64(len(config)), nil
			case "descriptor-size":
				descriptor.Size++
			case "descriptor-digest":
				descriptor.Digest = digest.FromString("different configuration")
			case "config-bytes":
				config = []byte("[]")
			case "inline-bytes":
				descriptor.Data = []byte("[]")
			case "inline-object", "inline-array":
				config = []byte(`{"nonempty":true}`)
				if name == "inline-array" {
					config = []byte("[]")
				}
				descriptor.Digest, descriptor.Size, descriptor.Data = digest.FromBytes(config), int64(len(config)), append([]byte(nil), config...)
			case "inline-media":
				descriptor.MediaType = "application/vnd.aquasec.trivy.config.v1+json"
			case "external-url":
				descriptor.URLs = []string{"https://unapproved.example.test/config"}
			}
			beforeConfig, beforeData := append([]byte(nil), config...), append([]byte(nil), descriptor.Data...)
			if ArtifactConfigurationMatches(descriptor, config) != expected {
				t.Fatal("configuration binding decision mismatched exact control")
			}
			if !bytes.Equal(config, beforeConfig) || !bytes.Equal(descriptor.Data, beforeData) {
				t.Fatal("configuration binding modified original bytes")
			}
		})
	}
}
