package resolver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

func TestVerifiedArtifactMetadataAuthenticatesLargeDescriptorWithoutFetchingPayload(t *testing.T) {
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	trust := t.TempDir()
	if err := os.WriteFile(filepath.Join(trust, "qa.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"valid", "untrusted-key", "oversized-layer", "external-layer", "inline-layer", "wrong-media", "changed-manifest", "changed-config", "partial-config", "duplicate-config"} {
		t.Run(name, func(t *testing.T) {
			config := []byte(`{}`)
			if name == "duplicate-config" {
				config = []byte(`{"version":1,"version":1}`)
			}
			layer := ocispec.Descriptor{MediaType: "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip", Digest: digest.FromString("large-opaque-QA-database"), Size: 980383808}
			switch name {
			case "oversized-layer":
				layer.Size = 2<<30 + 1
			case "external-layer":
				layer.URLs = []string{"https://unapproved.example.test/database"}
			case "inline-layer":
				layer.Data = []byte("unapproved inline content")
			case "wrong-media":
				layer.MediaType = "application/vnd.other.payload"
			}
			manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: int64(len(config))}, Layers: []ocispec.Descriptor{layer}}
			if name == "partial-config" {
				manifest.Config.Size++
			}
			manifestBytes, _ := json.Marshal(manifest)
			subject := digest.FromBytes(manifestBytes).String()
			claim := []byte(`{"critical":{"type":"cosign container image signature","identity":{"docker-reference":"original.example.test/database"},"image":{"Docker-manifest-digest":"` + subject + `"}},"optional":{}}`)
			signed, err := signer.SignMessage(bytes.NewReader(claim))
			if err != nil {
				t.Fatal(err)
			}
			signatureManifest, _ := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: manifest.Config, Layers: []ocispec.Descriptor{{MediaType: cosignSimpleSigningMediaType, Digest: digest.FromBytes(claim), Size: int64(len(claim)), Annotations: map[string]string{cosignSignatureAnnotation: base64.StdEncoding.EncodeToString(signed)}}}})
			rootReads, payloadReads := 0, 0
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				var body []byte
				media := "application/octet-stream"
				switch {
				case strings.Contains(request.URL.Path, "/manifests/sha256-"):
					body = signatureManifest
					media = ocispec.MediaTypeImageManifest
				case strings.HasSuffix(request.URL.Path, "/manifests/"+subject):
					rootReads++
					body = manifestBytes
					media = ocispec.MediaTypeImageManifest
					if name == "changed-manifest" {
						body = []byte(`{}`)
					}
				case strings.HasSuffix(request.URL.Path, "/blobs/"+digest.FromBytes(claim).String()):
					body = claim
					media = cosignSimpleSigningMediaType
				case strings.HasSuffix(request.URL.Path, "/blobs/"+manifest.Config.Digest.String()):
					body = config
					if name == "changed-config" {
						body = []byte(`[]`)
					}
				default:
					payloadReads++
					return nil, errors.New("QA refuses large payload allocation or unrelated network")
				}
				header := http.Header{"Content-Type": []string{media}, "Docker-Content-Digest": []string{digest.FromBytes(body).String()}}
				return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request}, nil
			})}
			policy := model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}
			if name == "untrusted-key" {
				policy.KeyRef = "missing"
			}
			metadata, verification, err := FetchVerifiedOCIArtifactMetadata(t.Context(), client, "mirror.example.test/database", subject, "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip", policy, trust, 2<<30)
			if name == "valid" {
				if err != nil || verification.Status != "verified" || !bytes.Equal(metadata.Manifest, manifestBytes) || !bytes.Equal(metadata.Config, config) || metadata.Layer.Size != 980383808 {
					t.Fatalf("large descriptor positive control: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid signed metadata accepted")
			}
			if payloadReads != 0 {
				t.Fatal("metadata authentication downloaded payload or used external descriptor location")
			}
			if name == "untrusted-key" && rootReads != 0 {
				t.Fatal("manifest fetched before signature acceptance")
			}
		})
	}
}
