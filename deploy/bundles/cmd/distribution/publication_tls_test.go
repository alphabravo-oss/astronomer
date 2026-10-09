package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestActualSignerUsesOwnedPrivateRegistryCA(t *testing.T) {
	binary := os.Getenv("COSIGN_BIN")
	if binary == "" {
		t.Skip("COSIGN_BIN selects actual qualified signer")
	}
	catalog, charts, assets, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
	output, err := produce(catalog, charts)
	if err != nil {
		t.Fatal(err)
	}
	layout := filepath.Join(t.TempDir(), "layout")
	index, err := buildLayout(output, charts, layout, "registry.example.test/distribution", assets, platforms)
	if err != nil {
		t.Fatal(err)
	}
	child := index.Children[0]
	manifestBytes, err := boundedFile(filepath.Join(layout, "blobs", "sha256", child.Digest[7:]), 1<<20)
	var manifest ocispec.Manifest
	if err != nil || closedJSON(manifestBytes, &manifest) != nil {
		t.Fatal("fixture graph unavailable")
	}
	registry := &publicationRegistry{manifests: map[string][]byte{"owned/chart/" + child.Digest: manifestBytes}, blobs: map[string][]byte{}}
	for _, descriptor := range append([]ocispec.Descriptor{manifest.Config}, manifest.Layers...) {
		data, err := boundedFile(filepath.Join(layout, "blobs", "sha256", descriptor.Digest.Encoded()), 4<<20)
		if err != nil {
			t.Fatal(err)
		}
		registry.blobs[descriptor.Digest.String()] = data
	}
	server := httptest.NewTLSServer(registry)
	defer server.Close()
	directory := t.TempDir()
	caPath := filepath.Join(directory, "approved-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	caSnapshot, cleanup, err := snapshotRegistryCA(caPath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := os.WriteFile(caPath, []byte("changed operator input"), 0600); err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", directory)
	t.Setenv("DOCKER_CONFIG", directory)
	t.Setenv("COSIGN_PASSWORD", hex.EncodeToString(random))
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(name, "")
	}
	if err := exec.Command(binary, "generate-key-pair", "--output-key-prefix", filepath.Join(directory, "qa")).Run(); err != nil {
		t.Fatal("QA key generation failed")
	}
	repository := strings.TrimPrefix(server.URL, "https://") + "/owned/chart"
	options := publicationOptions{CosignBinary: binary, Mode: "qa", QAKeyFile: filepath.Join(directory, "qa.key")}
	if err := signPublishedSubject(t.Context(), options, repository+"@"+child.Digest); err == nil {
		t.Fatal("private TLS CA was implicitly trusted")
	}
	options.RegistryCAFile = caSnapshot
	if err := signPublishedSubject(t.Context(), options, repository+"@"+child.Digest); err != nil {
		t.Fatalf("actual signer ignored approved owned CA: %v", err)
	}
	tls, err := httpclient.TLSForCA(caSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	// Test-only direct TLS transport reaches its owned loopback listener. The
	// production publisher retains DNS-pinned DestinationPolicy enforcement.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tls}}
	defer client.CloseIdleConnections()
	verification, err := resolver.VerifyPinnedOCI(t.Context(), client, repository, child.Digest, model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}, directory)
	if err != nil || verification.Status != "verified" {
		t.Fatalf("actual private TLS signature failed canonical readback: %v", err)
	}
}

func TestSignerPreflightRejectsBeforeAnyRegistryWrite(t *testing.T) {
	for _, binary := range []string{"relative-cosign", filepath.Join(t.TempDir(), "missing"), os.DevNull} {
		if err := preflightSigner(binary); err == nil {
			t.Fatal("invalid signer accepted before writes")
		}
		// An intentionally absent layout proves publisher preflight precedes
		// graph preparation and every ORAS write, rather than checking only
		// after copying the first child.
		options := publicationOptions{Directory: "absent-layout", ExpectedRelease: "v1.2.2", Mode: "qa", CosignBinary: binary, QAKeyFile: "qa-only-placeholder", Client: &http.Client{}, Trust: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}}
		if err := publishLayout(t.Context(), options); err == nil || !strings.Contains(err.Error(), "signer executable required before publication") {
			t.Fatalf("publisher did not reject signer before preparing/writing: %v", err)
		}
	}
	file := filepath.Join(t.TempDir(), "non-executable")
	if err := os.WriteFile(file, []byte("not executable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := preflightSigner(file); err == nil {
		t.Fatal("non-executable signer accepted")
	}
}
