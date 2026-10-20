//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// Real offline signatures for all seven pinned image subjects and the target
// chart flow through PrepareInvocation and Invocation.Run. A joined synthetic
// gh child accepts ONLY the exact historical attestation argv; this is not
// GitHub attestation provenance. The synthetic kubectl fails at the first
// database read, so no backup, restore, upgrade or official CLI success follows.
func TestInvocationRunAuthenticatesEveryImageBeforeOwnedDatabaseBoundary(t *testing.T) {
	invocation, options, imageReads := invocationVerifiedRunFixture(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := invocation.Run(ctx)
	if result.ExitCode != 94 || result.Result != "failed" || result.FailedStage != upgrade.Backup || result.RollbackAttempted || result.CleanupFailed {
		t.Fatal("actual invocation did not stop at its joined database boundary", result)
	}
	state, err := invocation.store.Load(ctx)
	if err != nil || state == nil || len(state.Completed) != 1 || state.Completed[0] != upgrade.Verify || state.Pending != upgrade.Backup || state.Applied || state.Replaced {
		t.Fatal("complete cryptographic Verify was not acknowledged separately from failed Backup", err)
	}
	reads := imageReads()
	if len(reads) != 7 {
		t.Fatal("all seven image subjects were not verified", reads)
	}
	for role, count := range reads {
		if count < 1 {
			t.Fatal("image signature transport was not exercised", role)
		}
	}
	if invocation.driver.backup.attempted || invocation.driver.backup.complete || invocation.driver.target.database.anchor != nil || invocation.driver.target.database.capturing {
		t.Fatal("unowned database response acknowledged capture or complete backup")
	}
	before, err := os.ReadDir(options.Commands.CaptureDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if replay := invocation.Run(ctx); replay.ExitCode == 0 {
		t.Fatal("partially completed invocation replayed", replay)
	}
	after, err := os.ReadDir(options.Commands.CaptureDirectory)
	if err != nil || len(after) != len(before) {
		t.Fatal("single-use Run launched extra children", err)
	}
	if err := invocation.Close(); err != nil {
		t.Fatal("owned Close failed", err)
	}
	if _, err := os.Stat(filepath.Join(options.CheckpointDirectory, "checkpoint.json")); err != nil {
		t.Fatal("Close removed acknowledged verification checkpoint", err)
	}
}

func TestInvocationRunRejectsOneForeignImageSignerBeforeDatabaseCapture(t *testing.T) {
	invocation, _, imageReads := invocationVerifiedRunFixture(t, "worker")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := invocation.Run(ctx)
	if result.ExitCode == 0 || result.Result != "failed" || result.FailedStage != upgrade.Verify || result.RollbackAttempted {
		t.Fatal("foreign image signature passed concrete Verify", result)
	}
	state, err := invocation.store.Load(ctx)
	if err != nil || state == nil || len(state.Completed) != 0 || state.Pending != upgrade.Verify {
		t.Fatal("foreign signer inherited a Verify acknowledgment", err)
	}
	if imageReads()["worker"] < 1 {
		t.Fatal("negative control did not fetch actual foreign worker signature")
	}
	if invocation.driver.backup.attempted || invocation.driver.target.database.anchor != nil || invocation.driver.target.database.capturing {
		t.Fatal("foreign image signer admitted database capture")
	}
}

func invocationVerifiedRunFixture(t *testing.T, foreignRole string) (*Invocation, InvocationOptions, func() map[string]int) {
	t.Helper()
	address := os.Getenv("ASTRONOMER_HELM_HTTP_PRIVATE_FIXTURE_IP")
	if address == "" {
		t.Skip("owned private canonical-proxy fixture address is not configured")
	}
	ip := net.ParseIP(address)
	if ip == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		t.Fatal("owned proxy address invalid")
	}
	fixture := newLocalChartCryptoFixture(t, false, "17")
	client, reads := invocationImageSignatureClient(t, fixture, foreignRole)
	policy, repositoryOptions := invocationCanonicalProxy(t, ip, client)
	options := invocationActualChildren(t, fixture.metadata, policy, repositoryOptions)
	// Replace before admission, not after a Tool or Commands handle is captured.
	archive := filepath.Join(fixture.metadata.directory, "astronomer-1.2.2.tgz")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nif [ \"$#\" -eq 1 ] && [ \"$1\" = version ]; then printf 'gh version 2.87.0\\nhttps://github.com/cli/cli/releases/tag/v2.87.0\\n'; exit 0; fi\nif [ \"$#\" -eq 5 ] && [ \"$1\" = attestation ] && [ \"$2\" = verify ] && [ \"$3\" = " + quote(archive) + " ] && [ \"$4\" = --repo ] && [ \"$5\" = alphabravo-oss/astronomer ]; then exit 0; fi\nexit 95\n"
	if err := os.WriteFile(options.GH.Path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(script))
	options.GH.SHA256 = "sha256:" + hex.EncodeToString(hash[:])
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	invocation, err := PrepareInvocation(ctx, options)
	if err != nil {
		t.Fatal("actual signed invocation preparation failed", err)
	}
	t.Cleanup(func() {
		if err := invocation.Close(); err != nil {
			t.Error("owned invocation cleanup failed", err)
		}
	})
	return invocation, options, reads
}

func invocationImageSignatureClient(t *testing.T, fixture *localChartCryptoFixture, foreignRole string) (*http.Client, func() map[string]int) {
	t.Helper()
	manifest, err := decodeAuthenticatedManifest(fixture.metadata.manifest, fixture.metadata.target)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Astronomer.Images) != 7 {
		t.Fatal("exact candidate seven-image fixture required")
	}
	bodies := map[string][]byte{}
	signatureRoles := map[string]string{}
	config := []byte(`{}`)
	for _, image := range manifest.Astronomer.Images {
		repository, subject, ok := strings.Cut(image.Reference, "@")
		if !ok || !strings.HasPrefix(repository, "ghcr.io/alphabravo-oss/") {
			t.Fatal("invalid canonical image fixture")
		}
		prefix := "/v2/" + strings.TrimPrefix(repository, "ghcr.io/") + "/"
		claim := []byte(`{"critical":{"type":"cosign container image signature","identity":{"docker-reference":"` + repository + `"},"image":{"Docker-manifest-digest":"` + subject + `"}},"optional":{}}`)
		publisher := fixture.metadata.publisher
		if image.Name == foreignRole {
			publisher = "https://github.com/foreign/repository/.github/workflows/release.yaml@refs/tags/v1.2.2"
		}
		bundleRaw := fixture.metadata.sign(t, claim, publisher, metadataIssuer)
		var bundle sigstorebundle.Bundle
		if err := bundle.UnmarshalJSON(bundleRaw); err != nil {
			t.Fatal(err)
		}
		verification, err := bundle.VerificationContent()
		if err != nil || verification.Certificate() == nil {
			t.Fatal("actual image certificate unavailable", err)
		}
		certificate, err := cryptoutils.MarshalCertificateToPEM(verification.Certificate())
		if err != nil {
			t.Fatal(err)
		}
		signature, err := bundle.SignatureContent()
		if err != nil {
			t.Fatal(err)
		}
		artifact := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: int64(len(config))}, Layers: []ocispec.Descriptor{{MediaType: "application/vnd.dev.cosign.simplesigning.v1+json", Digest: digest.FromBytes(claim), Size: int64(len(claim)), Annotations: map[string]string{"dev.cosignproject.cosign/signature": base64.StdEncoding.EncodeToString(signature.MessageSignatureContent().Signature()), "dev.sigstore.cosign/certificate": string(certificate), "dev.sigstore.cosign/bundle": string(bundleRaw)}}}}
		raw, err := json.Marshal(artifact)
		if err != nil {
			t.Fatal(err)
		}
		path := prefix + "manifests/sha256-" + strings.TrimPrefix(subject, "sha256:") + ".sig"
		bodies[path] = raw
		signatureRoles[path] = image.Name
		bodies[prefix+"blobs/"+digest.FromBytes(claim).String()] = claim
		bodies[prefix+"blobs/"+digest.FromBytes(config).String()] = config
	}
	var mu sync.Mutex
	reads := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bodies[r.URL.Path]
		if !ok || r.Host != "ghcr.io" || r.URL.RawQuery != "" || r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "outside exact image signature fixture", http.StatusBadRequest)
			return
		}
		if role := signatureRoles[r.URL.Path]; role != "" {
			mu.Lock()
			reads[role]++
			mu.Unlock()
		}
		media := "application/octet-stream"
		if strings.Contains(r.URL.Path, "/manifests/") {
			media = ocispec.MediaTypeImageManifest
		}
		w.Header().Set("Content-Type", media)
		w.Header().Set("Content-Length", fmt.Sprint(len(raw)))
		w.Header().Set("Docker-Content-Digest", digest.FromBytes(raw).String())
		if r.Method == http.MethodGet {
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
	client := &http.Client{Timeout: 10 * time.Second, Transport: chartLocalRoundTripper(func(r *http.Request) (*http.Response, error) {
		if _, ok := bodies[r.URL.Path]; !ok {
			return fixture.chart.client.Do(r)
		}
		if r.URL.Scheme != "https" || r.URL.Host != "ghcr.io" || r.URL.User != nil || r.URL.Fragment != "" || r.URL.RawQuery != "" {
			return nil, fmt.Errorf("image request outside exact local signature fixture")
		}
		clone := r.Clone(r.Context())
		clone.URL.Scheme, clone.URL.Host = endpoint.Scheme, endpoint.Host
		clone.Host = "ghcr.io"
		return transport.RoundTrip(clone)
	})}
	t.Cleanup(client.CloseIdleConnections)
	return client, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		copy := map[string]int{}
		for role, n := range reads {
			copy[role] = n
		}
		return copy
	}
}
