package distribution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

// The registry is local test routing only. Production destination policy still
// sees the fixed public fixture origin; no loopback network exception is added.
type databaseUpdateRegistry struct {
	fixture *registryFixture
	mutex   sync.Mutex
	reads   map[string]int
}

func (registry *databaseUpdateRegistry) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	registry.mutex.Lock()
	registry.reads[request.URL.Path]++
	registry.mutex.Unlock()
	registry.fixture.ServeHTTP(writer, request)
}

func (registry *databaseUpdateRegistry) readCount(path string) int {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	return registry.reads[path]
}

func databaseUpdateRegistryFixture(t *testing.T, now time.Time) (*databaseUpdateRegistry, DatabaseUpdateDocument, DatabaseUpdateOptions, *httptest.Server) {
	t.Helper()
	fixture := &registryFixture{manifests: map[string][]byte{}, blobs: map[string][]byte{}}
	fixture.setBlob(contentDigest([]byte("{}")), []byte("{}"))
	document := databaseUpdateFixture(now)
	for index := range document.Databases {
		snapshot := &document.Databases[index]
		repository := "databases/" + snapshot.Role
		payload := []byte("opaque QA database bytes: " + snapshot.Role)
		subject := fixture.addArtifact(repository, snapshot.Layer.MediaType, payload)
		fixture.mutex.Lock()
		manifestBytes := append([]byte(nil), fixture.manifests[repository+"/"+subject]...)
		fixture.mutex.Unlock()
		var manifest ocispec.Manifest
		if json.Unmarshal(manifestBytes, &manifest) != nil {
			t.Fatal("QA manifest invalid")
		}
		snapshot.PublishedSubject = "1.1.1.1/" + repository + "@" + subject
		snapshot.SupplierSubject = "ghcr.io/aquasecurity/" + snapshot.Role + "@" + subject
		snapshot.Manifest = DatabaseDescriptor{Digest: subject, Size: int64(len(manifestBytes)), MediaType: manifest.MediaType}
		snapshot.Layer = DatabaseDescriptor{Digest: manifest.Layers[0].Digest.String(), Size: manifest.Layers[0].Size, MediaType: manifest.Layers[0].MediaType}
	}
	registry := &databaseUpdateRegistry{fixture: fixture, reads: map[string]int{}}
	server := httptest.NewServer(registry)
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: registryRoutingTransport{fixture: fixture, target: parsed.Host, base: server.Client().Transport}, Timeout: time.Minute}
	options := DatabaseUpdateOptions{Repository: "1.1.1.1/updates", Binding: document.Binding, Timing: CadenceDatabaseUpdateTiming(), JavaRequired: true, Policy: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}, TrustDirectory: t.TempDir(), Client: client, Network: httpclient.DestinationPolicy{Restricted: true, Origins: []httpclient.TrustedOrigin{{URL: "https://1.1.1.1", CIDRs: []netip.Prefix{netip.MustParsePrefix("1.1.1.1/32")}}}}}
	return registry, document, options, server
}

func signDatabaseUpdateFixture(t *testing.T, registry *databaseUpdateRegistry, document DatabaseUpdateDocument, options DatabaseUpdateOptions, signer signature.Signer) DatabaseUpdateOptions {
	t.Helper()
	for _, snapshot := range document.Databases {
		repository, subject, _ := strings.Cut(strings.TrimPrefix(snapshot.PublishedSubject, "1.1.1.1/"), "@")
		registry.fixture.sign(t, repository, subject, signer)
	}
	options.Digest = registry.fixture.addArtifact("updates", DatabaseUpdateMediaType, encodeDatabaseUpdateFixture(t, document))
	registry.fixture.sign(t, "updates", options.Digest, signer)
	return options
}

func TestDatabaseUpdateConstructorRejectsUnsignedOrUnboundedIntent(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	_, _, original, _ := databaseUpdateRegistryFixture(t, now)
	original.Digest = contentDigest([]byte("feed"))
	for _, scenario := range []string{"no-client", "unsigned", "invalid-policy", "relative-trust", "mutable-feed", "invalid-digest", "missing-binding", "missing-timing", "empty-restricted-policy"} {
		t.Run(scenario, func(t *testing.T) {
			options := original
			switch scenario {
			case "no-client":
				options.Client = nil
			case "unsigned":
				options.Policy = model.TrustPolicy{AllowUnsigned: true}
			case "invalid-policy":
				options.Policy = model.TrustPolicy{}
			case "relative-trust":
				options.TrustDirectory = "relative"
			case "mutable-feed":
				options.Repository += ":latest"
			case "invalid-digest":
				options.Digest = "sha256:invalid"
			case "missing-binding":
				options.Binding = DatabaseUpdateBinding{}
			case "missing-timing":
				options.Timing = DatabaseUpdateTiming{}
			case "empty-restricted-policy":
				options.Network = httpclient.DestinationPolicy{Restricted: true}
			}
			if loader, err := NewDatabaseUpdateLoader(options); err == nil || loader != nil {
				t.Fatal("incomplete constructor intent accepted")
			}
		})
	}
}

func TestSignedDatabaseUpdateRejectsBeforeProjectionAndNeverReadsLayers(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	registry, document, options, _ := databaseUpdateRegistryFixture(t, now)
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil || os.WriteFile(filepath.Join(options.TrustDirectory, "qa.pub"), public, 0600) != nil {
		t.Fatal("QA public key preparation failed")
	}
	options = signDatabaseUpdateFixture(t, registry, document, options, signer)
	loader, err := NewDatabaseUpdateLoader(options)
	if err != nil {
		t.Fatal(err)
	}
	// Constructor-owned network policy cannot be expanded by caller mutation.
	options.Network.Origins[0].URL = "https://unapproved.example.test"
	update, err := loader.Load(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	digest, captured, ok := update.Snapshot()
	if !ok || digest != options.Digest || len(captured.Databases) != 2 {
		t.Fatal("signed candidate update not captured")
	}
	captured.Databases[0].Layer.Digest = contentDigest([]byte("caller mutation"))
	_, recaptured, _ := update.Snapshot()
	if recaptured.Databases[0].Layer != document.Databases[0].Layer {
		t.Fatal("caller mutation changed authenticated snapshot")
	}
	for _, snapshot := range document.Databases {
		repository, _, _ := strings.Cut(strings.TrimPrefix(snapshot.PublishedSubject, "1.1.1.1/"), "@")
		if registry.readCount("/v2/"+repository+"/blobs/"+snapshot.Layer.Digest) != 0 {
			t.Fatal("descriptor authentication fetched a database payload")
		}
	}
	if _, _, ok := new(AuthenticatedDatabaseUpdate).Snapshot(); ok {
		t.Fatal("zero value forged authentication")
	}
	for _, scenario := range []string{"unsigned-feed", "unsigned-child", "wrong-key", "wrong-base", "expired", "changed-layer-descriptor", "duplicate-json", "child-outside-policy", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := document
			candidate.Databases = append([]DatabaseUpdateSnapshot(nil), document.Databases...)
			selected := loader.options
			var restore func()
			switch scenario {
			case "unsigned-feed":
				selected.Digest = registry.fixture.addArtifact("updates", DatabaseUpdateMediaType, encodeDatabaseUpdateFixture(t, candidate))
				name := "updates/sha256-" + strings.TrimPrefix(selected.Digest, "sha256:") + ".sig"
				saved := registry.fixture.removeManifest(name)
				restore = func() { registry.fixture.setManifest(name, saved) }
			case "unsigned-child":
				child := candidate.Databases[0]
				repository, subject, _ := strings.Cut(strings.TrimPrefix(child.PublishedSubject, "1.1.1.1/"), "@")
				name := repository + "/sha256-" + strings.TrimPrefix(subject, "sha256:") + ".sig"
				saved := registry.fixture.removeManifest(name)
				restore = func() { registry.fixture.setManifest(name, saved) }
			case "wrong-key":
				selected.Policy.KeyRef = "untrusted"
				other, otherKey, err := signature.NewDefaultECDSASignerVerifier()
				if err != nil || other == nil {
					t.Fatal("QA alternate key failed")
				}
				public, err := cryptoutils.MarshalPublicKeyToPEM(otherKey.Public())
				if err != nil || os.WriteFile(filepath.Join(selected.TrustDirectory, "untrusted.pub"), public, 0600) != nil {
					t.Fatal("QA alternate public key failed")
				}
			case "wrong-base":
				candidate.Binding.BaseDistributionDigest = contentDigest([]byte("wrong base"))
			case "expired":
				candidate.ExpiresAt = now
			case "changed-layer-descriptor":
				candidate.Databases[0].Layer.Digest = contentDigest([]byte("wrong layer"))
			case "child-outside-policy":
				candidate.Databases[0].PublishedSubject = strings.Replace(candidate.Databases[0].PublishedSubject, "1.1.1.1", "8.8.8.8", 1)
			}
			if scenario == "wrong-base" || scenario == "expired" || scenario == "changed-layer-descriptor" || scenario == "child-outside-policy" || scenario == "duplicate-json" {
				payload := encodeDatabaseUpdateFixture(t, candidate)
				if scenario == "duplicate-json" {
					payload = append([]byte(`{"generation":1,`), payload[1:]...)
				}
				selected.Digest = registry.fixture.addArtifact("updates", DatabaseUpdateMediaType, payload)
				registry.fixture.sign(t, "updates", selected.Digest, signer)
			}
			if restore != nil {
				defer restore()
			}
			fresh, err := NewDatabaseUpdateLoader(selected)
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if scenario == "canceled" {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			feedPath := "/v2/updates/blobs/" + contentDigest(encodeDatabaseUpdateFixture(t, candidate))
			before := registry.readCount(feedPath)
			if result, err := fresh.Load(ctx, now); err == nil || result != nil || scenario == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("invalid update returned authenticated projection")
			}
			if (scenario == "unsigned-feed" || scenario == "wrong-key" || scenario == "canceled") && registry.readCount(feedPath) != before {
				t.Fatal("authentication failure reached feed payload")
			}
		})
	}
	if registry.fixture.externalCount() != 0 {
		t.Fatal("update verifier contacted an undeclared origin")
	}
}

// Uses the selected real producer, mounted ephemeral QA identity, and canonical
// in-process feed/child consumer. It is not an OIDC publisher or freshness proof.
func TestSelectedCosignDatabaseUpdateToCanonicalLoader(t *testing.T) {
	binary := os.Getenv("COSIGN_BIN")
	if binary == "" {
		t.Skip("COSIGN_BIN must select the qualified producer binary")
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	registry, document, options, server := databaseUpdateRegistryFixture(t, now)
	options.Digest = registry.fixture.addArtifact("updates", DatabaseUpdateMediaType, encodeDatabaseUpdateFixture(t, document))
	var publicRequests atomic.Int64
	trap := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		publicRequests.Add(1)
		writer.WriteHeader(http.StatusForbidden)
	}))
	defer trap.Close()
	// This negative control confirms the proxy trap observes attempted requests.
	proxy, _ := url.Parse(trap.URL)
	control := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: time.Second}
	response, err := control.Get("http://outside.example.test/negative-control")
	if err != nil || response.StatusCode != http.StatusForbidden || publicRequests.Load() != 1 {
		t.Fatal("external request trap negative control failed")
	}
	_ = response.Body.Close()
	control.CloseIdleConnections()
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + options.TrustDirectory, "DOCKER_CONFIG=" + options.TrustDirectory, "COSIGN_PASSWORD=" + hex.EncodeToString(random), "HTTP_PROXY=" + trap.URL, "HTTPS_PROXY=" + trap.URL, "ALL_PROXY=" + trap.URL, "NO_PROXY=127.0.0.1,localhost"}
	run := func(arguments ...string) {
		t.Helper()
		command := exec.CommandContext(t.Context(), binary, arguments...)
		command.Env = environment
		if _, err := command.CombinedOutput(); err != nil {
			t.Fatalf("selected QA producer command failed: %v", err)
		}
	}
	run("generate-key-pair", "--output-key-prefix", filepath.Join(options.TrustDirectory, "qa"))
	for _, name := range registry.fixture.manifestKeys() {
		repository, subject, ok := strings.Cut(name, "/sha256:")
		if !ok {
			t.Fatal("QA subject malformed")
		}
		run("sign", "--yes", "--key", filepath.Join(options.TrustDirectory, "qa.key"), "--new-bundle-format=false", "--registry-referrers-mode=legacy", "--tlog-upload=false", "--use-signing-config=false", "--allow-http-registry", strings.TrimPrefix(server.URL, "http://")+"/"+repository+"@sha256:"+subject)
	}
	loader, err := NewDatabaseUpdateLoader(options)
	if err != nil {
		t.Fatal(err)
	}
	update, err := loader.Load(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	if update.ValidateCurrentMetadata(now) != nil || update.ValidateCurrentMetadata(document.ExpiresAt) == nil {
		t.Fatal("actual authenticated document did not retain finite metadata clocks")
	}
	if _, captured, ok := update.Snapshot(); !ok || len(captured.Databases) != 2 {
		t.Fatal("real producer did not qualify descriptor consumer")
	}
	for _, snapshot := range document.Databases {
		repository, _, _ := strings.Cut(strings.TrimPrefix(snapshot.PublishedSubject, "1.1.1.1/"), "@")
		if registry.readCount("/v2/"+repository+"/blobs/"+snapshot.Layer.Digest) != 0 {
			t.Fatal("selected producer test downloaded unmeasured database payload")
		}
	}
	if publicRequests.Load() != 1 || registry.fixture.externalCount() != 0 {
		t.Fatal("producer or consumer contacted public trust service")
	}
}
