//go:build linux

package upgradecli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	sigstorebundle "github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
)

// The logical ghcr URL is routed only inside this test to one owned HTTP
// listener. This qualifies the real OCI/crypto/content stages, not canonical
// destination admission, TLS, a released registry or full Invocation/Driver.
func TestTargetChartActualLocalOCIAuthenticatesExactArchiveAndRuntimeSources(t *testing.T) {
	fixture := newLocalChartCryptoFixture(t, false)
	bounds, compatibility, err := fixture.chart.Verify(context.Background())
	if err != nil || bounds.Minimum != 66 || bounds.Target != 75 || len(compatibility) == 0 {
		t.Fatal("actual local OCI chart verification failed", err)
	}
	sources, err := fixture.chart.RuntimeSources(context.Background())
	if err != nil || len(sources) != 6 || sources["postgres"] != "fixture.invalid/postgres:exact" {
		t.Fatal("runtime roles did not originate in authenticated chart", err)
	}
	manifest, err := fixture.chart.metadata.Verify(context.Background())
	if err != nil || manifest.Astronomer.Chart.ContentDigest != digest.FromBytes(fixture.archive).String() {
		t.Fatal("manifest/archive cryptographic binding differs", err)
	}
	rootReads, payloadReads := fixture.readCounts()
	if rootReads < 1 || payloadReads < 1 {
		t.Fatal("positive fixture did not exercise actual OCI transport and payload")
	}
}

func TestTargetChartActualLocalOCIDoesNotFetchSubjectBeforeSignatureAcceptance(t *testing.T) {
	fixture := newLocalChartCryptoFixture(t, true)
	if _, _, err := fixture.chart.Verify(context.Background()); err == nil {
		t.Fatal("foreign OCI signer admitted under normal-release publisher")
	}
	rootReads, payloadReads := fixture.readCounts()
	if rootReads != 0 || payloadReads != 0 {
		t.Fatal("artifact manifest or payload fetched before real signature acceptance")
	}
}

func TestTargetChartActualLocalOCIBindsPrivateArchiveAndCanceledContext(t *testing.T) {
	for _, change := range []string{"archive", "canceled"} {
		t.Run(change, func(t *testing.T) {
			fixture := newLocalChartCryptoFixture(t, false)
			if _, _, err := fixture.chart.Verify(context.Background()); err != nil {
				t.Fatal("positive actual chart verification control failed", err)
			}
			ctx := context.Background()
			if change == "archive" {
				fixture.metadata.write(t, filepath.Base(fixture.chart.archivePath), append(append([]byte(nil), fixture.archive...), 0))
			} else {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, _, err := fixture.chart.Verify(ctx)
			if err == nil || change == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("changed original or canceled chart admitted", err)
			}
		})
	}
}

type localChartCryptoFixture struct {
	metadata                metadataCryptoFixture
	chart                   *TargetChartContent
	archive                 []byte
	mu                      sync.Mutex
	rootReads, payloadReads int
}

func (f *localChartCryptoFixture) readCounts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rootReads, f.payloadReads
}

func newLocalChartCryptoFixture(t *testing.T, foreignSigner bool, postgresMajor ...string) *localChartCryptoFixture {
	t.Helper()
	metadata := newMetadataCryptoFixture(t, "v1.2.2")
	manifest, compatibility := chartCompatibilityFixture(t)
	values := runtimeValuesFixture
	if len(postgresMajor) > 1 || len(postgresMajor) == 1 && postgresMajor[0] != "17" {
		t.Fatal("explicit synthetic PostgreSQL 17 fixture only")
	}
	if len(postgresMajor) == 1 {
		values = strings.Replace(values, "repository: fixture.invalid/postgres\n    tag: exact", "repository: fixture.invalid/postgres\n    tag: \"17.11\"", 1)
		for i := range manifest.Astronomer.RuntimeImages {
			if manifest.Astronomer.RuntimeImages[i].SourceReference == "fixture.invalid/postgres:exact" {
				manifest.Astronomer.RuntimeImages[i].SourceReference = "fixture.invalid/postgres:17.11"
			}
		}
	}
	archive := chartArchiveFixture(t, append(normalChartEntries(compatibility), chartArchiveEntry{name: "astronomer/values.yaml", data: []byte(values)}))
	config := []byte(`{}`)
	artifactManifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: int64(len(config))}, Layers: []ocispec.Descriptor{{MediaType: helmChartContentType, Digest: digest.FromBytes(archive), Size: int64(len(archive))}}}
	artifactBytes, err := json.Marshal(artifactManifest)
	if err != nil {
		t.Fatal(err)
	}
	subject := digest.FromBytes(artifactBytes).String()
	repository := "ghcr.io/alphabravo-oss/charts/astronomer"
	reference := repository + "@" + subject
	manifest.Astronomer.Chart.Reference = reference
	manifest.Astronomer.Chart.ContentDigest = digest.FromBytes(archive).String()
	manifest.Astronomer.Chart.Evidence.Signature = "cosign://" + reference
	manifest.Astronomer.Chart.Evidence.SBOM = "cosign-attestation://" + reference + "#spdxjson"
	manifest.Astronomer.Chart.Evidence.Provenance = "cosign-attestation://" + reference + "#slsaprovenance"
	if _, err := manifest.Validate(metadata.target); err != nil {
		t.Fatal("canonical chart fixture is invalid before signing", err)
	}
	metadata.manifest, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	metadata.write(t, "release-manifest.json", metadata.manifest)
	metadata.write(t, "release-manifest.sigstore.json", metadata.sign(t, metadata.manifest, metadata.publisher, metadataIssuer))
	archiveName := "astronomer-1.2.2.tgz"
	metadata.write(t, archiveName, archive)
	signed, err := metadata.construct(context.Background())
	if err != nil {
		t.Fatal("actual signed manifest constructor control failed", err)
	}
	t.Cleanup(signed.Close)
	claim := []byte(`{"critical":{"type":"cosign container image signature","identity":{"docker-reference":"` + repository + `"},"image":{"Docker-manifest-digest":"` + subject + `"}},"optional":{}}`)
	publisher := metadata.publisher
	if foreignSigner {
		publisher = "https://github.com/foreign/repository/.github/workflows/release.yaml@refs/tags/v1.2.2"
	}
	bundleRaw := metadata.sign(t, claim, publisher, metadataIssuer)
	var bundle sigstorebundle.Bundle
	if err := bundle.UnmarshalJSON(bundleRaw); err != nil {
		t.Fatal(err)
	}
	verification, err := bundle.VerificationContent()
	if err != nil || verification.Certificate() == nil {
		t.Fatal("local signature certificate unavailable", err)
	}
	certificate, err := cryptoutils.MarshalCertificateToPEM(verification.Certificate())
	if err != nil {
		t.Fatal(err)
	}
	signature, err := bundle.SignatureContent()
	if err != nil {
		t.Fatal(err)
	}
	signatureManifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: artifactManifest.Config, Layers: []ocispec.Descriptor{{MediaType: "application/vnd.dev.cosign.simplesigning.v1+json", Digest: digest.FromBytes(claim), Size: int64(len(claim)), Annotations: map[string]string{"dev.cosignproject.cosign/signature": base64.StdEncoding.EncodeToString(signature.MessageSignatureContent().Signature()), "dev.sigstore.cosign/certificate": string(certificate), "dev.sigstore.cosign/bundle": string(bundleRaw)}}}}
	signatureBytes, err := json.Marshal(signatureManifest)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/v2/alphabravo-oss/charts/astronomer/"
	signaturePath := prefix + "manifests/sha256-" + strings.TrimPrefix(subject, "sha256:") + ".sig"
	rootPath := prefix + "manifests/" + subject
	payloadPath := prefix + "blobs/" + digest.FromBytes(archive).String()
	bodies := map[string][]byte{signaturePath: signatureBytes, prefix + "blobs/" + digest.FromBytes(claim).String(): claim, rootPath: artifactBytes, payloadPath: archive, prefix + "blobs/" + digest.FromBytes(config).String(): config}
	fixture := &localChartCryptoFixture{metadata: metadata, archive: archive}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		raw, exists := bodies[request.URL.Path]
		if !exists || request.Host != "ghcr.io" || request.URL.RawQuery != "" || request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.Error(w, "outside exact local OCI fixture", http.StatusBadRequest)
			return
		}
		fixture.mu.Lock()
		if request.URL.Path == rootPath {
			fixture.rootReads++
		}
		if request.URL.Path == payloadPath {
			fixture.payloadReads++
		}
		fixture.mu.Unlock()
		media := "application/octet-stream"
		if strings.Contains(request.URL.Path, "/manifests/") {
			media = ocispec.MediaTypeImageManifest
		}
		w.Header().Set("Content-Type", media)
		w.Header().Set("Content-Length", fmt.Sprint(len(raw)))
		w.Header().Set("Docker-Content-Digest", digest.FromBytes(raw).String())
		if request.Method == http.MethodGet {
			_, _ = w.Write(raw)
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(server.Client().CloseIdleConnections)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := server.Client().Transport
	client := &http.Client{Timeout: 10 * time.Second, Transport: chartLocalRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "ghcr.io" || request.URL.User != nil || request.URL.Fragment != "" || request.URL.RawQuery != "" || !strings.HasPrefix(request.URL.Path, prefix) {
			return nil, errors.New("network outside exact logical fixture refused")
		}
		clone := request.Clone(request.Context())
		clone.URL.Scheme, clone.URL.Host = endpoint.Scheme, endpoint.Host
		clone.Host = "ghcr.io"
		return transport.RoundTrip(clone)
	})}
	t.Cleanup(client.CloseIdleConnections)
	input, err := bindMetadataArtifact(context.Background(), filepath.Join(metadata.directory, archiveName), targetChartCompressedMaximum)
	if err != nil {
		t.Fatal(err)
	}
	if input.hash != sha256.Sum256(archive) || !bytes.Equal(archive, bodies[payloadPath]) {
		t.Fatal("initial private archive binding differs")
	}
	fixture.chart = &TargetChartContent{metadata: signed, client: client, input: input, archivePath: filepath.Join(metadata.directory, archiveName)}
	return fixture
}

type chartLocalRoundTripper func(*http.Request) (*http.Response, error)

func (f chartLocalRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
