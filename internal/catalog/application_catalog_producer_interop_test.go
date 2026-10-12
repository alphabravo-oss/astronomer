//go:build catalogproducerinterop

package catalog

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

// This explicit cross-repository test tag requires an actual selected Python
// producer output, supplied and hash-bound by the finite qualification driver.
// A normal unit invocation cannot mistake an embedded synthetic document for
// evidence that the Python producer generated the accepted document.
type producerCatalogStore struct {
	document sqlc.ReconcileApplicationCatalogV1Params
	writes   int
	count    int64
}

func (s *producerCatalogStore) UpsertDefaultHelmRepository(context.Context, sqlc.UpsertDefaultHelmRepositoryParams) error {
	return errors.New("unexpected legacy catalog repository write")
}
func (s *producerCatalogStore) DeleteBlessedChartsBySource(context.Context, string) error {
	return errors.New("unexpected legacy catalog deletion")
}
func (s *producerCatalogStore) CreateBlessedChart(context.Context, sqlc.CreateBlessedChartParams) error {
	return errors.New("unexpected legacy catalog entry write")
}
func (s *producerCatalogStore) ReconcileApplicationCatalogV1(_ context.Context, p sqlc.ReconcileApplicationCatalogV1Params) (int64, error) {
	s.writes++
	s.document = p
	return s.count, nil
}

func readActualProducerCatalog(t *testing.T) []byte {
	t.Helper()
	path, expected := os.Getenv("ASTRONOMER_W15_CATALOG_INPUT"), os.Getenv("ASTRONOMER_W15_CATALOG_DIGEST")
	if path == "" || !filepath.IsAbs(path) || digest.Digest(expected).Validate() != nil || !strings.HasPrefix(expected, "sha256:") {
		t.Fatal("explicit hash-bound actual Python producer input is required")
	}
	metadata, err := os.Lstat(path)
	if err != nil || !metadata.Mode().IsRegular() || metadata.Size() <= 0 || metadata.Size() > maxCatalogBytes {
		t.Fatal("actual producer input is not a bounded regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil || int64(len(body)) != metadata.Size() || digest.FromBytes(body).String() != expected {
		t.Fatal("actual producer input identity changed")
	}
	return body
}

func TestApplicationCatalogActualProducerSignedOCIConsumer(t *testing.T) {
	body := readActualProducerCatalog(t)
	var actual map[string]any
	if err := json.Unmarshal(body, &actual); err != nil {
		t.Fatal(err)
	}
	applications, ok := actual["applications"].([]any)
	if !ok || len(applications) == 0 {
		t.Fatal("actual producer emitted no applications")
	}
	foundConstellation := false
	for _, value := range applications {
		entry, ok := value.(map[string]any)
		if !ok {
			t.Fatal("actual producer entry shape")
		}
		if entry["slug"] == "constellation" {
			foundConstellation = true
		}
	}
	if !foundConstellation {
		t.Fatal("declared first-party Constellation artifact was omitted")
	}
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	_, otherKey, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	otherPublic, err := cryptoutils.MarshalPublicKeyToPEM(otherKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"valid", "wrong key", "missing signature", "bad signature", "wrong signed subject", "changed manifest", "changed layer", "wrong document digest", "unsupported schema", "unsupported reader", "canceled"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			payload := append([]byte(nil), body...)
			if name == "unsupported schema" || name == "unsupported reader" {
				var document map[string]any
				if err := json.Unmarshal(payload, &document); err != nil {
					t.Fatal(err)
				}
				metadata := document["metadata"].(map[string]any)
				field := "schemaVersion"
				if name == "unsupported reader" {
					field = "minimumReaderVersion"
				}
				metadata[field] = 2
				payload, err = json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
			}
			config := []byte(`{}`)
			layer := ocispec.Descriptor{MediaType: "application/vnd.astronomer.application-catalog.v1+json", Digest: digest.FromBytes(payload), Size: int64(len(payload))}
			manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, ArtifactType: layer.MediaType, Config: ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: int64(len(config))}, Layers: []ocispec.Descriptor{layer}}
			manifestBytes, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			subject := digest.FromBytes(manifestBytes).String()
			signedSubject := subject
			if name == "wrong signed subject" {
				signedSubject = "sha256:" + strings.Repeat("b", 64)
			}
			claim := []byte(`{"critical":{"type":"cosign container image signature","identity":{"docker-reference":"registry.example.test/catalog/actual-producer"},"image":{"Docker-manifest-digest":"` + signedSubject + `"}},"optional":{}}`)
			signed, err := signer.SignMessage(bytes.NewReader(claim))
			if err != nil {
				t.Fatal(err)
			}
			if name == "bad signature" {
				signed[0] ^= 1
			}
			signatureLayer := ocispec.Descriptor{MediaType: "application/vnd.dev.cosign.simplesigning.v1+json", Digest: digest.FromBytes(claim), Size: int64(len(claim)), Annotations: map[string]string{"dev.cosignproject.cosign/signature": base64.StdEncoding.EncodeToString(signed)}}
			signatureManifest, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: manifest.Config, Layers: []ocispec.Descriptor{signatureLayer}})
			if err != nil {
				t.Fatal(err)
			}
			trust := t.TempDir()
			trusted := public
			if name == "wrong key" {
				trusted = otherPublic
			}
			if err := os.WriteFile(filepath.Join(trust, "qa.pub"), trusted, 0600); err != nil {
				t.Fatal(err)
			}
			requests := 0
			client := &http.Client{Transport: ociRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if err := request.Context().Err(); err != nil {
					return nil, err
				}
				requests++
				if request.URL.Scheme != "https" || request.URL.Host != "registry.example.test" || (request.Method != http.MethodGet && request.Method != http.MethodHead) {
					return nil, errors.New("fixture registry authority escaped")
				}
				var content []byte
				media := ocispec.MediaTypeImageManifest
				switch {
				case strings.HasSuffix(request.URL.Path, "/manifests/"+subject):
					content = manifestBytes
					if name == "changed manifest" {
						content = []byte(`{}`)
					}
				case strings.Contains(request.URL.Path, "/manifests/sha256-") && strings.HasSuffix(request.URL.Path, ".sig"):
					if name == "missing signature" {
						return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
					}
					content = signatureManifest
				case strings.HasSuffix(request.URL.Path, "/blobs/"+layer.Digest.String()):
					content = payload
					media = layer.MediaType
					if name == "changed layer" {
						content = append(append([]byte(nil), payload...), byte(' '))
					}
				case strings.HasSuffix(request.URL.Path, "/blobs/"+signatureLayer.Digest.String()):
					content = claim
					media = signatureLayer.MediaType
				case strings.HasSuffix(request.URL.Path, "/blobs/"+manifest.Config.Digest.String()):
					content = config
					media = manifest.Config.MediaType
				default:
					return nil, errors.New("unowned fixture registry path")
				}
				header := http.Header{"Content-Type": []string{media}, "Docker-Content-Digest": []string{digest.FromBytes(content).String()}}
				return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(bytes.NewReader(content)), ContentLength: int64(len(content)), Request: request}, nil
			})}
			policy := model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}
			expected := digest.FromBytes(payload).String()
			if name == "wrong document digest" {
				expected = "sha256:" + strings.Repeat("a", 64)
			}
			store := &producerCatalogStore{count: int64(len(applications))}
			if name == "canceled" {
				cancel()
			}
			count, err := LoadSource(ctx, store, SourceOptions{URL: "oci://registry.example.test/catalog/actual-producer@" + subject, ExpectedDigest: expected, Clients: SourceClients{Public: client, Mirror: client}, TrustPolicy: &policy, TrustDirectory: trust})
			if name != "valid" {
				if err == nil || count != 0 || store.writes != 0 {
					t.Fatal("invalid producer or signed authority reached persistence")
				}
				if name == "canceled" && requests != 0 {
					t.Fatal("canceled source contacted fixture registry")
				}
				return
			}
			if err != nil || count != len(applications) || store.writes != 1 {
				t.Fatalf("actual producer consumer failed: count=%d writes=%d err=%v", count, store.writes, err)
			}
			p := store.document
			if p.SourceRevision != subject || p.IndexDigest != digest.FromBytes(body).String() || p.VerificationStatus != "verified" || p.VerificationIdentity != "cosign:key:qa" {
				t.Fatal("immutable catalog/signature identity was lost")
			}
			var persisted map[string]any
			if err := json.Unmarshal(p.Document, &persisted); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, persisted) {
				t.Fatal("actual producer presentation, lifecycle or artifact graph was not preserved")
			}
		})
	}
}
