package resolver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

func TestSignedCosignClaimsHaveOneInterpretation(t *testing.T) {
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "qa.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	subject := "sha256:" + strings.Repeat("a", 64)
	base := `{"critical":{"type":"cosign container image signature","identity":{"docker-reference":"original.example.test/vendor/app"},"image":{"Docker-manifest-digest":"` + subject + `"}},"optional":{"vendor-annotation":"retained"}}`
	cases := []struct {
		name, payload string
		manifest      func([]byte) []byte
		valid         bool
	}{
		{name: "original-vendor-identity-and-optional", payload: base, valid: true},
		{name: "duplicate-subject-claim", payload: strings.Replace(base, `"Docker-manifest-digest":`, `"Docker-manifest-digest":"sha256:`+strings.Repeat("b", 64)+`","Docker-manifest-digest":`, 1)},
		{name: "duplicate-original-identity", payload: strings.Replace(base, `"docker-reference":`, `"docker-reference":"wrong.example.test/app","docker-reference":`, 1)},
		{name: "duplicate-optional", payload: strings.Replace(base, `"vendor-annotation":"retained"`, `"vendor-annotation":"changed","vendor-annotation":"retained"`, 1)},
		{name: "deep-optional", payload: strings.Replace(base, `"retained"`, strings.Repeat("[", 66)+`"retained"`+strings.Repeat("]", 66), 1)},
		{name: "trailing-document", payload: base + `{}`},
		{name: "duplicate-signature-manifest-layers", payload: base, manifest: func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"layers":`), []byte(`"layers":[],"layers":`), 1)
		}},
		{name: "trailing-signature-manifest", payload: base, manifest: func(data []byte) []byte { return append(data, []byte(`{}`)...) }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			payload := []byte(test.payload)
			signed, err := signer.SignMessage(bytes.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			// Every negative has a genuine valid QA signature over exactly these bytes.
			if err := signer.VerifySignature(bytes.NewReader(signed), bytes.NewReader(payload)); err != nil {
				t.Fatal(err)
			}
			layer := ocispec.Descriptor{MediaType: cosignSimpleSigningMediaType, Digest: digest.FromBytes(payload), Size: int64(len(payload)), Annotations: map[string]string{cosignSignatureAnnotation: base64.StdEncoding.EncodeToString(signed)}}
			manifest, err := json.Marshal(ocispec.Manifest{Layers: []ocispec.Descriptor{layer}})
			if err != nil {
				t.Fatal(err)
			}
			if test.manifest != nil {
				manifest = test.manifest(manifest)
			}
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				body, contentType := payload, cosignSimpleSigningMediaType
				if strings.Contains(request.URL.Path, "/manifests/") {
					body, contentType = manifest, ocispec.MediaTypeImageManifest
				}
				header := make(http.Header)
				header.Set("Docker-Content-Digest", digest.FromBytes(body).String())
				header.Set("Content-Type", contentType)
				return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request}, nil
			})}
			verification, err := VerifyPinnedOCI(t.Context(), client, "mirror.example.test/vendor/app", subject, model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}, directory)
			if test.valid {
				if err != nil || verification.Status != "verified" {
					t.Fatalf("valid vendor signature rejected: %v", err)
				}
			} else if err == nil || !HasCode(err, CodeVerification) {
				t.Fatalf("ambiguous signed evidence accepted or misclassified: %v", err)
			}
		})
	}
}
