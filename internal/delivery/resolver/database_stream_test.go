package resolver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

type databaseQABytes byte

func (value databaseQABytes) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = byte(value)
	}
	return len(buffer), nil
}

type databaseCountWriter struct{ count int64 }

func (writer *databaseCountWriter) Write(buffer []byte) (int, error) {
	writer.count += int64(len(buffer))
	return len(buffer), nil
}

func TestDatabaseStreamAuthenticatesExactRangesAndCompleteHash(t *testing.T) {
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
	size := databaseRangeBytes + 17
	hash := sha256.New()
	if _, err := io.CopyBuffer(hash, io.LimitReader(databaseQABytes(7), size), make([]byte, 64<<10)); err != nil {
		t.Fatal(err)
	}
	layer := ocispec.Descriptor{MediaType: "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip", Digest: digest.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil))), Size: size}
	config := []byte(`{}`)
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: 2}, Layers: []ocispec.Descriptor{layer}}
	manifestBytes, _ := json.Marshal(manifest)
	subject := digest.FromBytes(manifestBytes).String()
	claim := []byte(`{"critical":{"type":"cosign container image signature","image":{"Docker-manifest-digest":"` + subject + `"}},"optional":{}}`)
	signed, err := signer.SignMessage(bytes.NewReader(claim))
	if err != nil {
		t.Fatal(err)
	}
	signatureManifest, _ := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: manifest.Config, Layers: []ocispec.Descriptor{{MediaType: cosignSimpleSigningMediaType, Digest: digest.FromBytes(claim), Size: int64(len(claim)), Annotations: map[string]string{cosignSignatureAnnotation: base64.StdEncoding.EncodeToString(signed)}}}})
	for _, name := range []string{"valid", "200", "multipart", "misaligned", "wrong-total", "duplicate-range", "truncated", "oversized", "changed-bytes", "untrusted-key", "changed-claim", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			requests := []string{}
			client := &http.Client{Transport: artifactTransport(func(request *http.Request) (*http.Response, error) {
				if strings.HasSuffix(request.URL.Path, "/blobs/"+layer.Digest.String()) {
					requests = append(requests, request.Header.Get("Range"))
					deadline, exists := request.Context().Deadline()
					if !exists || time.Until(deadline) > 15*time.Minute || request.Header.Get("Accept-Encoding") != "identity" {
						t.Fatal("stream lost finite deadline or exact byte encoding")
					}
					start, end := int64(0), int64(0)
					if count, err := fmt.Sscanf(request.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil || count != 2 || start < 0 || end < start || end >= size {
						t.Fatal("invalid range emitted")
					}
					length := end - start + 1
					status := http.StatusPartialContent
					header := http.Header{"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", start, end, size)}}
					contentLength := length
					value := databaseQABytes(7)
					switch name {
					case "200":
						status = http.StatusOK
					case "multipart":
						header.Set("Content-Type", "multipart/byteranges; boundary=qa")
					case "misaligned":
						header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start+1, end, size))
					case "wrong-total":
						header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size+1))
					case "duplicate-range":
						header.Add("Content-Range", header.Get("Content-Range"))
					case "truncated":
						length--
						contentLength = -1
					case "oversized":
						length++
						contentLength = -1
					case "changed-bytes":
						value = databaseQABytes(8)
					}
					return &http.Response{StatusCode: status, Header: header, ContentLength: contentLength, Body: io.NopCloser(io.LimitReader(value, length)), Request: request}, nil
				}
				var body []byte
				media := ocispec.MediaTypeImageManifest
				switch {
				case strings.Contains(request.URL.Path, "/manifests/sha256-"):
					body = signatureManifest
				case strings.HasSuffix(request.URL.Path, "/manifests/"+subject):
					body = manifestBytes
				case strings.HasSuffix(request.URL.Path, "/blobs/"+manifest.Config.Digest.String()):
					body = config
					media = "application/vnd.oci.empty.v1+json"
				case strings.HasSuffix(request.URL.Path, "/blobs/"+digest.FromBytes(claim).String()):
					body = claim
					media = cosignSimpleSigningMediaType
				default:
					t.Fatal("unrelated request emitted")
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {media}, "Docker-Content-Digest": {digest.FromBytes(body).String()}}, ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
			})}
			policy := model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}
			if name == "untrusted-key" {
				policy.KeyRef = "missing"
			}
			expected := layer
			if name == "changed-claim" {
				expected.Size--
			}
			ctx := t.Context()
			if name == "cancelled" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			writer := &databaseCountWriter{}
			verification, err := StreamVerifiedOCIDatabase(ctx, client, "mirror.example.test/db", subject, expected, writer, policy, trust)
			if name == "valid" {
				if err != nil || verification.Status != "verified" || writer.count != size || len(requests) != 2 || requests[0] != fmt.Sprintf("bytes=0-%d", databaseRangeBytes-1) || requests[1] != fmt.Sprintf("bytes=%d-%d", databaseRangeBytes, size-1) {
					t.Fatalf("exact streamed positive failed: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid stream accepted")
			}
			if (name == "untrusted-key" || name == "changed-claim" || name == "cancelled") && (len(requests) != 0 || writer.count != 0) {
				t.Fatal("payload written before signature, captured claim or cancellation validation")
			}
		})
	}
}
